package delivery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/riverqueue/river"
	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/platform/database"
)

// message is one text for one recipient on one channel.
type message struct {
	IdempotencyKey string // the same for every attempt at one message
	Channel        customer.Channel
	To             string // an email address or an E.164 phone number
	Subject        string // email only
	Body           string
}

// provider sends the messages of one channel. A call repeated with the same idempotency key
// must not send the message twice.
type provider interface {
	Send(ctx context.Context, m message) (providerMessageID string, err error)
}

// errRejected marks a provider's permanent refusal: trying again can't help. Every other
// provider error is transient, timeouts included.
var errRejected = errors.New("rejected by the provider")

// errBusy is the mock's transient failure.
var errBusy = errors.New("provider temporarily unavailable")

// behaviour is how the mock answers a message.
type behaviour int

const (
	delivers    behaviour = iota // accepts; the receipt says delivered
	undelivered                  // accepts; the receipt says undelivered
	rejects                      // refuses for good
	busyOnce                     // fails transiently on the first attempt, then accepts
	slowOnce                     // accepts, but answers the first attempt after the caller gave up
)

// behaviourOf picks a key's behaviour from its FNV-1a hash, so a key always behaves the same
// way: 1 % rejected, 1 % slow once, 2 % busy once, 2 % undelivered, the rest delivered.
func behaviourOf(key string) behaviour {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key)) // writing to a hash never fails
	switch b := h.Sum32() % 100; {
	case b == 0:
		return rejects
	case b == 1:
		return slowOnce
	case b <= 3:
		return busyOnce
	case b <= 5:
		return undelivered
	default:
		return delivers
	}
}

// mockProvider stands in for an SMS or email provider. Like a real one, it keeps the first
// answer to each idempotency key in its own storage (mock_provider_messages), so a repeated
// request gets the same answer, even after a restart. When it accepts a message it schedules
// the delivery receipt, in the same transaction, as the job its webhook would trigger. Its
// answers depend only on the key, so every run is reproducible.
type mockProvider struct {
	channel      customer.Channel
	db           *gorm.DB
	receipts     *river.Client[*sql.Tx] // inserts receipt jobs
	latency      time.Duration
	receiptDelay time.Duration
	behaviour    func(key string) behaviour
	now          func() time.Time
}

func newMockProvider(ch customer.Channel, db *gorm.DB, receipts *river.Client[*sql.Tx], now func() time.Time) *mockProvider {
	p := &mockProvider{channel: ch, db: db, receipts: receipts, behaviour: behaviourOf, now: now}
	if ch == customer.ChannelSMS {
		p.latency, p.receiptDelay = 50*time.Millisecond, 2*time.Second
	} else {
		p.latency, p.receiptDelay = 80*time.Millisecond, 3*time.Second
	}
	return p
}

// mockRecord is the mock's stored answer to a key.
type mockRecord struct {
	Attempts          int
	Outcome           *string // accepted, rejected, or nil before the first answer
	ProviderMessageID *string
}

// Send answers m as a provider would.
func (p *mockProvider) Send(ctx context.Context, m message) (string, error) {
	if err := sleep(ctx, p.latency); err != nil {
		return "", err
	}
	b := p.behaviour(m.IdempotencyKey)
	var rec mockRecord
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := p.now()
		err := tx.Raw(`
			INSERT INTO mock_provider_messages (idempotency_key, channel, attempts, created_at, updated_at)
			VALUES (?, ?, 1, ?, ?)
			ON CONFLICT (idempotency_key) DO UPDATE
			SET attempts = mock_provider_messages.attempts + 1, updated_at = excluded.updated_at
			RETURNING attempts, outcome, provider_message_id`,
			m.IdempotencyKey, string(p.channel), now, now).Scan(&rec).Error
		if err != nil {
			return err
		}
		switch {
		case rec.Outcome != nil:
			return nil // answered before: the same answer again
		case b == busyOnce && rec.Attempts == 1:
			return nil // no answer yet
		case b == rejects:
			rec.Outcome = new("rejected")
		default:
			rec.Outcome, rec.ProviderMessageID = new("accepted"), new("mock-"+m.IdempotencyKey)
			if err := p.scheduleReceipt(ctx, tx, *rec.ProviderMessageID, b != undelivered, now); err != nil {
				return err
			}
		}
		return tx.Exec("UPDATE mock_provider_messages SET outcome = ?, provider_message_id = ? WHERE idempotency_key = ?",
			rec.Outcome, rec.ProviderMessageID, m.IdempotencyKey).Error
	})
	if err != nil {
		return "", fmt.Errorf("mock %s provider: %w", p.channel, err)
	}
	switch {
	case rec.Outcome == nil:
		return "", errBusy
	case *rec.Outcome == "rejected":
		return "", errRejected
	case b == slowOnce && rec.Attempts == 1:
		<-ctx.Done() // accepted, but too slow to answer: the caller times out and asks again
		return "", ctx.Err()
	default:
		return *rec.ProviderMessageID, nil
	}
}

func (p *mockProvider) scheduleReceipt(ctx context.Context, tx *gorm.DB, providerMessageID string, delivered bool, now time.Time) error {
	sqlTx, err := database.SQLTx(tx)
	if err != nil {
		return err
	}
	args := receiptArgs{ProviderMessageID: providerMessageID, Delivered: delivered}
	_, err = p.receipts.InsertTx(ctx, sqlTx, args, &river.InsertOpts{ScheduledAt: now.Add(p.receiptDelay)})
	return err
}

// sleep waits for d, or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
