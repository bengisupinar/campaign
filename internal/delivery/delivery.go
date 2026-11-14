// Package delivery sends dispatched campaigns. Preparing a campaign turns its audience into one
// pending message per recipient and channel, each with its own send job. A send job hands its
// message to the channel's provider at a bounded rate, and a receipt job records what the
// provider reports later. Progress is served as snapshots and server-sent events.
//
// The jobs run on River, a job queue in PostgreSQL. The API inserts them; exactly one worker
// process (cmd/worker) works them, because the per-channel rate limits live in that process.
// Like segmentation, delivery reads the customer tables directly.
package delivery

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
)

// Sending limits, the same for each channel but applied to each separately (DECISIONS.md).
const (
	// sendWorkersPerChannel caps a channel's provider calls in flight.
	sendWorkersPerChannel = 20
	// providerRatePerSecond caps the provider calls a channel starts each second, retries
	// included.
	providerRatePerSecond = 100
	// providerTimeout bounds one provider call; running out is a transient failure.
	providerTimeout = 2 * time.Second
	// maxSendAttempts is how often a message is offered to its provider before it fails.
	// River's own limit stays at its default of 25, so that a crash during the last attempt
	// still leaves a run that records the failure.
	maxSendAttempts = 5
	// preparationBatch is how many recipients preparing a campaign reads at a time.
	preparationBatch = 1000
)

// channels lists the channels a message can go out on. Each has its own River queue, named
// after it, its own workers and its own rate limiter, so one channel never holds up another.
func channels() []customer.Channel {
	return []customer.Channel{customer.ChannelEmail, customer.ChannelSMS}
}

// dispatchArgs asks to prepare and send a campaign: now, or at its scheduled time.
type dispatchArgs struct {
	CampaignID int64 `json:"campaign_id"`
}

func (dispatchArgs) Kind() string { return "dispatch_campaign" }

func (dispatchArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// sendArgs asks to send one message. Its job goes into the queue of the message's channel.
type sendArgs struct {
	DeliveryID int64 `json:"delivery_id"`
}

func (sendArgs) Kind() string { return "send_message" }

func (sendArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// receiptArgs is a provider's delivery report, as its webhook would bring it.
type receiptArgs struct {
	ProviderMessageID string `json:"provider_message_id"`
	Delivered         bool   `json:"delivered"`
}

func (receiptArgs) Kind() string { return "delivery_receipt" }

func (receiptArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// Queue queues campaign dispatches. campaign.Service uses it as its dispatcher, inside the
// transaction that records a dispatch. It works no jobs.
type Queue struct {
	client *river.Client[*sql.Tx]
}

// NewQueue returns a queue that inserts jobs through db.
func NewQueue(db *sql.DB, logger *slog.Logger) (*Queue, error) {
	client, err := river.NewClient(riverdatabasesql.New(db), &river.Config{Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("job queue client: %w", err)
	}
	return &Queue{client: client}, nil
}

// EnqueueDispatch queues c's dispatch in tx, to run now or at c's scheduled time. River
// schedules the job itself: nothing polls for campaigns that are due.
func (q *Queue) EnqueueDispatch(ctx context.Context, tx *sql.Tx, c *campaign.Campaign) error {
	args := dispatchArgs{CampaignID: c.ID()}
	if _, err := q.client.InsertTx(ctx, tx, args, &river.InsertOpts{ScheduledAt: c.ScheduledAt()}); err != nil {
		return fmt.Errorf("queue dispatch of campaign %d: %w", c.ID(), err)
	}
	return nil
}

// now is the time in UTC at PostgreSQL's microsecond precision, like campaign.Service's.
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
