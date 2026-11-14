package delivery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/seed"
	"github.com/bengisupinar/campaign/internal/segmentation"
)

func TestConcurrentDispatchesQueueOneJob(t *testing.T) {
	f := newFixture(t, 3)
	id := f.draft(t).ID()

	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := range errs {
		wg.Go(func() { _, errs[i] = f.campaigns.Dispatch(t.Context(), id, nil) })
	}
	wg.Wait()

	var succeeded int
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case !errors.Is(err, campaign.ErrAlreadyDispatched):
			t.Fatalf("Dispatch() error = %v, want nil or ErrAlreadyDispatched", err)
		}
	}
	if succeeded != 1 {
		t.Errorf("%d dispatches succeeded, want 1", succeeded)
	}
	if n := f.count(t, "SELECT count(*) FROM river_job WHERE kind = 'dispatch_campaign'"); n != 1 {
		t.Errorf("%d dispatch jobs, want 1", n)
	}
	if _, err := f.campaigns.Dispatch(t.Context(), id, nil); !errors.Is(err, campaign.ErrAlreadyDispatched) {
		t.Errorf("repeated Dispatch() error = %v, want ErrAlreadyDispatched", err)
	}
}

// failingLister fails on its second batch, as an interrupted preparation would.
type failingLister struct {
	inner recipientLister
	calls int
}

func (l *failingLister) Recipients(ctx context.Context, q segmentation.Query, after int64, limit int) ([]segmentation.Recipient, error) {
	if l.calls++; l.calls == 2 {
		return nil, errors.New("connection lost")
	}
	return l.inner.Recipients(ctx, q, after, limit)
}

func TestPreparationIsAtomic(t *testing.T) {
	f := newFixture(t, 5)
	id := f.dispatch(t)
	prepareWith := func(lister func(tx *gorm.DB) recipientLister) (int, error) {
		var prepared int
		err := f.db.Transaction(func(tx *gorm.DB) error {
			var err error
			prepared, err = prepare(t.Context(), tx, f.queue.client, lister(tx), id, now(), 2) // three batches
			return err
		})
		return prepared, err
	}
	deliveries := func() int { return f.count(t, "SELECT count(*) FROM deliveries WHERE campaign_id = ?", id) }
	sendJobs := func() int { return f.count(t, "SELECT count(*) FROM river_job WHERE kind = 'send_message'") }

	// A failure part-way leaves neither deliveries nor jobs behind.
	_, err := prepareWith(func(tx *gorm.DB) recipientLister { return &failingLister{inner: segmentation.NewService(tx)} })
	if err == nil {
		t.Fatal("prepare() succeeded despite the failing batch")
	}
	if d, j := deliveries(), sendJobs(); d != 0 || j != 0 {
		t.Errorf("after the failure: %d deliveries and %d send jobs, want none", d, j)
	}

	// The retry prepares everything: each delivery with exactly one send job, in its channel's queue.
	prepared, err := prepareWith(func(tx *gorm.DB) recipientLister { return segmentation.NewService(tx) })
	check(t, err)
	if prepared != 10 || deliveries() != 10 {
		t.Errorf("prepared %d, stored %d deliveries; want 10 (5 customers, 2 channels)", prepared, deliveries())
	}
	matched := f.count(t, `
		SELECT count(*) FROM deliveries d
		WHERE d.campaign_id = ? AND (
			SELECT count(*) FROM river_job j
			WHERE j.kind = 'send_message' AND (j.args->>'delivery_id')::bigint = d.id AND j.queue = d.channel) = 1`, id)
	if matched != 10 || sendJobs() != 10 {
		t.Errorf("%d of 10 deliveries have exactly one send job in their channel's queue, out of %d jobs", matched, sendJobs())
	}

	// Preparing again adds nothing.
	prepared, err = prepareWith(func(tx *gorm.DB) recipientLister { return segmentation.NewService(tx) })
	check(t, err)
	if prepared != 0 || deliveries() != 10 || sendJobs() != 10 {
		t.Errorf("preparing again: prepared %d, %d deliveries, %d jobs; want 0, 10, 10", prepared, deliveries(), sendJobs())
	}
}

func TestScheduledDispatchRunsOnceAWorkerStarts(t *testing.T) {
	f := newFixture(t, 3)
	at := now().Add(time.Second)
	c, err := f.campaigns.Dispatch(t.Context(), f.draft(t).ID(), &at)
	check(t, err)
	if c.Status() != campaign.StatusScheduled || !c.ScheduledAt().Equal(at) {
		t.Fatalf("Dispatch() = %s at %v, want scheduled at %v", c.Status(), c.ScheduledAt(), at)
	}
	scheduled := f.count(t, "SELECT count(*) FROM river_job WHERE kind = 'dispatch_campaign' AND state = 'scheduled' AND scheduled_at = ?", at)
	if scheduled != 1 {
		t.Fatalf("%d dispatch jobs scheduled at %v, want 1", scheduled, at)
	}

	time.Sleep(1500 * time.Millisecond) // the time passes with no worker running
	f.startWorker(t, map[customer.Channel]provider{customer.ChannelEmail: &fakeProvider{}, customer.ChannelSMS: &fakeProvider{}})

	waitFor(t, 20*time.Second, "the scheduled campaign to be sent", func() bool { return f.status(t, c.ID()) == campaign.StatusSent })
	if s := f.statuses(t, c.ID()); s[statusAccepted] != 6 {
		t.Errorf("messages = %v, want 6 accepted", s)
	}
}

func TestCampaignFailsWhenItsSegmentNoLongerValidates(t *testing.T) {
	f := newFixture(t, 3)
	check(t, f.db.Exec("INSERT INTO cities (country_code, name) VALUES ('TR', 'Bursa')").Error)
	c, err := f.campaigns.Create(t.Context(), campaign.Draft{
		Name: "Bursa only",
		Segment: campaign.Segment{Version: campaign.SegmentVersion, Match: campaign.MatchAll, Conditions: []campaign.Condition{
			{Type: campaign.ConditionCity, In: []string{"Bursa"}},
		}},
		Content: campaign.Content{SMS: &campaign.SMS{Body: "Merhaba!"}},
	})
	check(t, err)
	at := now().Add(time.Second)
	_, err = f.campaigns.Dispatch(t.Context(), c.ID(), &at)
	check(t, err)
	check(t, f.db.Exec("UPDATE cities SET name = 'Bursa (old)' WHERE name = 'Bursa'").Error)

	f.startWorker(t, map[customer.Channel]provider{customer.ChannelEmail: &fakeProvider{}, customer.ChannelSMS: &fakeProvider{}})

	waitFor(t, 20*time.Second, "the campaign to fail", func() bool { return f.status(t, c.ID()) == campaign.StatusFailed })
	// River records the job's end just after the work returns; retrying can't help, so it is cancelled.
	waitFor(t, 5*time.Second, "the dispatch job to be cancelled", func() bool {
		return f.count(t, "SELECT count(*) FROM river_job WHERE kind = 'dispatch_campaign' AND state = 'cancelled'") == 1
	})
}

// BenchmarkPreparation times the preparation transaction on the seeded dataset: a campaign to
// every customer on both channels. Each iteration rolls back.
//
//	TEST_DATABASE_URL=… go test -run '^$' -bench Preparation ./internal/delivery
func BenchmarkPreparation(b *testing.B) {
	f := newFixture(b, 0)
	if _, err := seed.Run(b.Context(), f.db, seed.DefaultOptions(), now()); err != nil {
		b.Fatalf("seed: %v", err)
	}
	check(b, f.db.Exec("ANALYZE").Error)
	id := f.dispatch(b)
	errRollback := errors.New("roll back")

	var prepared int
	for b.Loop() {
		err := f.db.Transaction(func(tx *gorm.DB) error {
			var err error
			if prepared, err = prepare(b.Context(), tx, f.queue.client, segmentation.NewService(tx), id, now(), preparationBatch); err != nil {
				return err
			}
			return errRollback
		})
		if !errors.Is(err, errRollback) {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(prepared), "messages")
}
