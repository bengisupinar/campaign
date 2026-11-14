package delivery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"golang.org/x/time/rate"
	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/platform/database"
	"github.com/bengisupinar/campaign/internal/segmentation"
)

// recipientLister lists a segment's recipients in batches: *segmentation.Service.
type recipientLister interface {
	Recipients(ctx context.Context, q segmentation.Query, after int64, limit int) ([]segmentation.Recipient, error)
}

// jobInserter inserts jobs in a transaction: a River client.
type jobInserter interface {
	InsertManyTx(ctx context.Context, tx *sql.Tx, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}

// dispatchWorker prepares a dispatched campaign for sending.
type dispatchWorker struct {
	river.WorkerDefaults[dispatchArgs]
	db     *gorm.DB
	logger *slog.Logger
	now    func() time.Time
}

// Timeout bounds preparation, one transaction that grows with the audience.
func (*dispatchWorker) Timeout(*river.Job[dispatchArgs]) time.Duration { return 5 * time.Minute }

func (w *dispatchWorker) Work(ctx context.Context, job *river.Job[dispatchArgs]) error {
	jobs := river.ClientFromContext[*sql.Tx](ctx)
	start := time.Now()
	var prepared int
	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		prepared, err = prepare(ctx, tx, jobs, segmentation.NewService(tx), job.Args.CampaignID, w.now(), preparationBatch)
		return err
	})
	var invalid *campaign.ValidationError
	if errors.As(err, &invalid) {
		return w.fail(ctx, job.Args.CampaignID, invalid)
	}
	if err != nil {
		return err
	}
	w.logger.InfoContext(ctx, "campaign prepared",
		slog.Int64("campaign_id", job.Args.CampaignID),
		slog.Int("messages", prepared),
		slog.String("duration", time.Since(start).Round(time.Millisecond).String()),
	)
	return nil
}

// fail marks a campaign failed because its segment no longer validates, say a city it names
// was removed, and cancels the job: retrying can't help.
func (w *dispatchWorker) fail(ctx context.Context, campaignID int64, invalid *campaign.ValidationError) error {
	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		campaigns := campaign.NewStore(tx)
		c, err := campaigns.Lock(ctx, campaignID)
		if err != nil {
			return err
		}
		from := c.Status()
		if err := c.Fail(w.now()); err != nil {
			return err
		}
		return campaigns.Transition(ctx, c, from)
	})
	if err != nil {
		return err
	}
	w.logger.WarnContext(ctx, "campaign failed: its segment no longer validates",
		slog.Int64("campaign_id", campaignID), slog.String("problems", invalid.Error()))
	return river.JobCancel(invalid)
}

// prepare turns a dispatched campaign's audience into pending deliveries and their send jobs,
// all in tx: they commit together or not at all, and no send job can run before every
// delivery exists. A scheduled campaign starts first; one that has left scheduled and sending
// is left alone. It returns how many messages it prepared.
func prepare(ctx context.Context, tx *gorm.DB, jobs jobInserter, audience recipientLister, campaignID int64, now time.Time, batch int) (int, error) {
	campaigns := campaign.NewStore(tx)
	c, err := campaigns.Lock(ctx, campaignID)
	if err != nil {
		return 0, err
	}
	switch c.Status() {
	case campaign.StatusScheduled:
		if err := c.Start(now); err != nil {
			return 0, err
		}
		if err := campaigns.Transition(ctx, c, campaign.StatusScheduled); err != nil {
			return 0, err
		}
	case campaign.StatusSending: // dispatched to send now, or a retry
	default:
		return 0, nil
	}
	sqlTx, err := database.SQLTx(tx)
	if err != nil {
		return 0, err
	}

	deliveries := newStore(tx)
	q := segmentation.Query{Segment: c.Draft().Segment, Channels: c.Channels(), AsOf: c.StartedAt()}
	prepared := 0
	for after := int64(0); ; {
		recipients, err := audience.Recipients(ctx, q, after, batch)
		if err != nil {
			return 0, err
		}
		created, err := deliveries.createPending(ctx, c.ID(), recipients, now)
		if err != nil {
			return 0, err
		}
		if len(created) > 0 {
			params := make([]river.InsertManyParams, len(created))
			for i, d := range created {
				params[i] = river.InsertManyParams{Args: sendArgs{DeliveryID: d.ID}, InsertOpts: &river.InsertOpts{Queue: string(d.Channel)}}
			}
			if _, err := jobs.InsertManyTx(ctx, sqlTx, params); err != nil {
				return 0, fmt.Errorf("queue send jobs of campaign %d: %w", c.ID(), err)
			}
		}
		prepared += len(created)
		if len(recipients) < batch {
			break
		}
		after = recipients[len(recipients)-1].CustomerID
	}

	// With nobody to send to, the campaign is sent at once.
	if exists, err := deliveries.hasDeliveries(ctx, c.ID()); err != nil || exists {
		return prepared, err
	}
	if err := c.Finish(now); err != nil {
		return 0, err
	}
	return prepared, campaigns.Transition(ctx, c, campaign.StatusSending)
}

// sendWorker sends one message through its channel's provider.
type sendWorker struct {
	river.WorkerDefaults[sendArgs]
	db        *gorm.DB
	providers map[customer.Channel]provider
	limiters  map[customer.Channel]*rate.Limiter
	logger    *slog.Logger
	now       func() time.Time
}

func (*sendWorker) Timeout(*river.Job[sendArgs]) time.Duration { return 30 * time.Second }

// TODO: Optimize campaign completion, avoid locking the campaign and querying for remaining pending deliveries after every message.
// Work sends a pending message. It holds no lock while the provider works: the outcome is
// recorded afterwards, in a short transaction (completeMessage). The worker owns the retry
// budget, so a job only ends once its message has an outcome: a transient failure on the last
// attempt fails the message, and a run after the last attempt, which only follows a crash or
// panic, fails it without calling the provider.
func (w *sendWorker) Work(ctx context.Context, job *river.Job[sendArgs]) error {
	m, err := newStore(w.db).message(ctx, job.Args.DeliveryID)
	if err != nil {
		return err
	}
	if m.status != statusPending {
		return nil // its outcome was recorded by an earlier run
	}
	out := outcome{status: statusFailed, reason: "provider_unavailable"}
	if job.Attempt <= maxSendAttempts {
		if out, err = w.send(ctx, m, job.Attempt); err != nil {
			return err // transient: River retries with backoff
		}
	}
	return completeMessage(ctx, w.db, m, out, w.now())
}

func (w *sendWorker) send(ctx context.Context, m pendingMessage, attempt int) (outcome, error) {
	msg, ok := m.compose()
	if !ok {
		return outcome{status: statusSkipped, reason: "not_permitted"}, nil
	}
	if m.channel == customer.ChannelSMS && utf8.RuneCountInString(msg.Body) > campaign.MaxSMSLength {
		return outcome{status: statusFailed, reason: "too_long"}, nil
	}
	if err := w.limiters[m.channel].Wait(ctx); err != nil {
		return outcome{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	id, err := w.providers[m.channel].Send(callCtx, msg)
	switch {
	case err == nil:
		return outcome{status: statusAccepted, providerMessageID: id}, nil
	case errors.Is(err, errRejected):
		return outcome{status: statusFailed, reason: "rejected"}, nil
	case attempt >= maxSendAttempts:
		w.logger.WarnContext(ctx, "message failed: provider unavailable",
			slog.Int64("delivery_id", m.id), slog.Int("attempts", attempt), slog.Any("error", err))
		return outcome{status: statusFailed, reason: "provider_unavailable"}, nil
	default:
		return outcome{}, fmt.Errorf("send delivery %d: %w", m.id, err)
	}
}

// compose renders m for its channel. It reports false if the recipient no longer allows the
// channel or has no contact detail for it: permissions are checked again just before sending.
func (m pendingMessage) compose() (message, bool) {
	msg := message{IdempotencyKey: fmt.Sprintf("delivery-%d", m.id), Channel: m.channel}
	switch {
	case !m.permitted:
		return message{}, false
	case m.channel == customer.ChannelEmail && m.email != "" && m.content.Email != nil:
		msg.To = m.email
		msg.Subject = campaign.Fill(m.content.Email.Subject, m.values)
		msg.Body = campaign.Fill(m.content.Email.Body, m.values)
	case m.channel == customer.ChannelSMS && m.phone != nil && m.content.SMS != nil:
		msg.To = *m.phone
		msg.Body = campaign.Fill(m.content.SMS.Body, m.values)
	default:
		return message{}, false
	}
	return msg, true
}

// completeMessage records a pending message's outcome and, if it was its campaign's last
// pending message, marks the campaign sent. One short transaction locks the campaign row
// first, so the completions of a campaign run one after another: each sees every earlier one,
// and the last always finds nothing pending. It never runs during a provider call.
func completeMessage(ctx context.Context, db *gorm.DB, m pendingMessage, out outcome, now time.Time) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		campaigns := campaign.NewStore(tx)
		c, err := campaigns.Lock(ctx, m.campaignID)
		if err != nil {
			return err
		}
		deliveries := newStore(tx)
		recorded, err := deliveries.recordOutcome(ctx, m.id, out, now)
		if err != nil || !recorded {
			return err
		}
		pending, err := deliveries.hasPending(ctx, m.campaignID)
		if err != nil || pending || c.Status() != campaign.StatusSending {
			return err
		}
		if err := c.Finish(now); err != nil {
			return err
		}
		return campaigns.Transition(ctx, c, campaign.StatusSending)
	})
}

// receiptWorker records the delivery reports providers send for accepted messages.
type receiptWorker struct {
	river.WorkerDefaults[receiptArgs]
	db  *gorm.DB
	now func() time.Time
}

// errNotAccepted delays a receipt that came before its message's acceptance was recorded.
var errNotAccepted = errors.New("receipt for a message not yet recorded as accepted")

// Work records a receipt. It never changes the campaign, which is sent once every message has
// an outcome; receipts come later. A receipt for an unknown message is retried a few times,
// then dropped, as a real webhook for an unknown message would be.
func (w *receiptWorker) Work(ctx context.Context, job *river.Job[receiptArgs]) error {
	deliveries := newStore(w.db)
	recorded, err := deliveries.recordReceipt(ctx, job.Args.ProviderMessageID, job.Args.Delivered, w.now())
	if err != nil || recorded {
		return err
	}
	known, err := deliveries.hasProviderMessage(ctx, job.Args.ProviderMessageID)
	if err != nil || known {
		return err // known: a repeated receipt, already recorded
	}
	return errNotAccepted
}
