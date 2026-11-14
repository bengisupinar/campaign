package delivery

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"golang.org/x/time/rate"
	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/segmentation"
)

// prepared dispatches a campaign and prepares it without a worker, returning its ID and its
// deliveries' IDs.
func (f *fixture) prepared(t testing.TB) (int64, []int64) {
	t.Helper()
	id := f.dispatch(t)
	check(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := prepare(t.Context(), tx, f.queue.client, segmentation.NewService(tx), id, now(), preparationBatch)
		return err
	}))
	var deliveries []int64
	check(t, f.db.Raw("SELECT id FROM deliveries WHERE campaign_id = ? ORDER BY id", id).Scan(&deliveries).Error)
	return id, deliveries
}

func TestCompletionIsRaceFree(t *testing.T) {
	f := newFixture(t, 100)
	for round := range 20 {
		id, deliveries := f.prepared(t)

		// Every message completes at the same moment, each in its own transaction.
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, len(deliveries))
		for i, d := range deliveries {
			wg.Go(func() {
				<-start
				out := outcome{status: statusAccepted, providerMessageID: fmt.Sprintf("fake-%d", d)}
				errs[i] = completeMessage(t.Context(), f.db, pendingMessage{id: d, campaignID: id}, out, now())
			})
		}
		close(start)
		wg.Wait()

		for _, err := range errs {
			check(t, err)
		}
		c, err := f.campaigns.Get(t.Context(), id)
		check(t, err)
		if c.Status() != campaign.StatusSent || c.FinishedAt().IsZero() {
			t.Fatalf("round %d: campaign is %s with finished_at %v, want sent", round, c.Status(), c.FinishedAt())
		}
		if s := f.statuses(t, id); s[statusAccepted] != 200 {
			t.Fatalf("round %d: messages = %v, want 200 accepted", round, s)
		}
	}
}

func TestRateLimitPerChannel(t *testing.T) {
	f := newFixture(t, 250)
	// Some messages fail their first attempt, so retries go through the limiter too.
	answer := func(_ context.Context, m message, attempt int) (string, error) {
		if attempt == 1 && (strings.HasSuffix(m.IdempotencyKey, "0") || strings.HasSuffix(m.IdempotencyKey, "5")) {
			return "", errBusy
		}
		return "fake-" + m.IdempotencyKey, nil
	}
	sms, email := &fakeProvider{answer: answer}, &fakeProvider{answer: answer}
	f.startWorker(t, map[customer.Channel]provider{customer.ChannelSMS: sms, customer.ChannelEmail: email})

	id := f.dispatch(t)
	waitFor(t, 60*time.Second, "the campaign to be sent", func() bool { return f.status(t, id) == campaign.StatusSent })

	for name, p := range map[string]*fakeProvider{"sms": sms, "email": email} {
		calls := p.callTimes()
		p.mu.Lock()
		messages, retries := len(p.attempts), 0
		for _, n := range p.attempts {
			retries += n - 1
		}
		p.mu.Unlock()
		if messages != 250 || retries == 0 || len(calls) != messages+retries {
			t.Errorf("%s: %d provider calls for %d messages and %d retries, want every retry counted", name, len(calls), messages, retries)
		}
		slices.SortFunc(calls, func(a, b time.Time) int { return a.Compare(b) })
		// 101 calls must span at least a second, or some one-second window held more than 100.
		for i := 0; i+providerRatePerSecond < len(calls); i++ {
			if span := calls[i+providerRatePerSecond].Sub(calls[i]); span < 990*time.Millisecond {
				t.Fatalf("%s: calls %d to %d took %v: over 100 calls in one second", name, i, i+providerRatePerSecond, span)
			}
		}
	}
}

func TestSlowSMSDoesNotHoldEmail(t *testing.T) {
	f := newFixture(t, 30)
	release := make(chan struct{})
	sms := &fakeProvider{answer: func(ctx context.Context, m message, _ int) (string, error) {
		select {
		case <-release:
			return "fake-" + m.IdempotencyKey, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
	f.startWorker(t, map[customer.Channel]provider{customer.ChannelSMS: sms, customer.ChannelEmail: &fakeProvider{}})

	id := f.dispatch(t)
	channelStatus := func(ch customer.Channel, status string) int {
		return f.count(t, "SELECT count(*) FROM deliveries WHERE campaign_id = ? AND channel = ? AND status = ?", id, string(ch), status)
	}
	waitFor(t, 10*time.Second, "every email while SMS is stuck", func() bool { return channelStatus(customer.ChannelEmail, statusAccepted) == 30 })
	if pending := channelStatus(customer.ChannelSMS, statusPending); pending != 30 {
		t.Errorf("%d SMS pending while the SMS provider hangs, want 30", pending)
	}

	close(release)
	waitFor(t, 30*time.Second, "the campaign to be sent", func() bool { return f.status(t, id) == campaign.StatusSent })
}

func TestRetryExhaustionFailsTheMessage(t *testing.T) {
	f := newFixture(t, 1)
	id, deliveries := f.prepared(t) // one email and one SMS
	unavailable := &fakeProvider{answer: func(context.Context, message, int) (string, error) { return "", errBusy }}
	w := &sendWorker{
		db:        f.db,
		providers: map[customer.Channel]provider{customer.ChannelEmail: unavailable, customer.ChannelSMS: unavailable},
		limiters: map[customer.Channel]*rate.Limiter{
			customer.ChannelEmail: rate.NewLimiter(rate.Inf, 1), customer.ChannelSMS: rate.NewLimiter(rate.Inf, 1),
		},
		logger: discard,
		now:    now,
	}
	work := func(deliveryID int64, attempt int) error {
		return w.Work(t.Context(), &river.Job[sendArgs]{JobRow: &rivertype.JobRow{Attempt: attempt}, Args: sendArgs{DeliveryID: deliveryID}})
	}
	status := func(deliveryID int64) (s, reason string) {
		var row struct{ Status, Error string }
		check(t, f.db.Raw("SELECT status, coalesce(error, '') AS error FROM deliveries WHERE id = ?", deliveryID).Scan(&row).Error)
		return row.Status, row.Error
	}

	// Before the last attempt, a transient failure is returned for River to retry.
	if err := work(deliveries[0], maxSendAttempts-1); err == nil {
		t.Fatal("attempt 4: Work() succeeded, want the transient error")
	}
	if s, _ := status(deliveries[0]); s != statusPending {
		t.Fatalf("after attempt 4 the message is %s, want pending", s)
	}
	// On the last attempt, it fails the message instead.
	check(t, work(deliveries[0], maxSendAttempts))
	if s, reason := status(deliveries[0]); s != statusFailed || reason != "provider_unavailable" {
		t.Errorf("after attempt 5 the message is %s (%s), want failed (provider_unavailable)", s, reason)
	}
	// A run after the last attempt, which only follows a crash, fails it without a call.
	calls := len(unavailable.callTimes())
	check(t, work(deliveries[1], maxSendAttempts+1))
	if s, _ := status(deliveries[1]); s != statusFailed || len(unavailable.callTimes()) != calls {
		t.Errorf("attempt 6: the message is %s after %d new calls, want failed after none", s, len(unavailable.callTimes())-calls)
	}
	if got := f.status(t, id); got != campaign.StatusSent {
		t.Errorf("campaign is %s, want sent: both messages have an outcome", got)
	}
}
