package delivery

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"golang.org/x/time/rate"
	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/customer"
)

// Worker works the delivery jobs. Exactly one may run: each channel's rate limiter lives in
// its process, so a second worker would double every limit. Multiple active worker replicas
// are not supported.
type Worker struct {
	client *river.Client[*sql.Tx]
}

// NewWorker returns a worker that sends through the mock SMS and email providers.
func NewWorker(db *gorm.DB, sqlDB *sql.DB, logger *slog.Logger) (*Worker, error) {
	receipts, err := river.NewClient(riverdatabasesql.New(sqlDB), &river.Config{Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("receipt queue client: %w", err)
	}
	providers := make(map[customer.Channel]provider)
	for _, ch := range channels() {
		providers[ch] = newMockProvider(ch, db, receipts, now)
	}
	return newWorker(db, sqlDB, providers, logger, now)
}

func newWorker(db *gorm.DB, sqlDB *sql.DB, providers map[customer.Channel]provider, logger *slog.Logger, now func() time.Time) (*Worker, error) {
	queues := map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 10}} // dispatches and receipts
	limiters := make(map[customer.Channel]*rate.Limiter)
	for _, ch := range channels() {
		queues[string(ch)] = river.QueueConfig{MaxWorkers: sendWorkersPerChannel}
		// A burst of 1 spaces calls at least 10 ms apart: no second holds more than 100.
		limiters[ch] = rate.NewLimiter(providerRatePerSecond, 1)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &dispatchWorker{db: db, logger: logger, now: now})
	river.AddWorker(workers, &sendWorker{db: db, providers: providers, limiters: limiters, logger: logger, now: now})
	river.AddWorker(workers, &receiptWorker{db: db, now: now})

	client, err := river.NewClient(riverdatabasesql.New(sqlDB), &river.Config{
		Logger:            logger,
		PollOnly:          true, // the database/sql driver can't LISTEN; polling is enough here
		FetchPollInterval: 200 * time.Millisecond,
		Queues:            queues,
		Workers:           workers,
		// Longer than any job may run (preparation: 5 minutes), so only a crashed worker's jobs
		// are rescued, within minutes.
		RescueStuckJobsAfter: 10 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("job queue client: %w", err)
	}
	return &Worker{client: client}, nil
}

// Start starts working jobs in the background. Cancelling ctx stops abruptly; Stop stops
// gracefully.
func (w *Worker) Start(ctx context.Context) error { return w.client.Start(ctx) }

// Stop stops fetching jobs and waits for the running ones to finish, or for ctx to end.
func (w *Worker) Stop(ctx context.Context) error { return w.client.Stop(ctx) }
