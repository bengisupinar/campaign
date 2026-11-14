// Command worker sends dispatched campaigns: it works the delivery jobs the API queues.
//
// Exactly one worker may run. The per-channel provider rate limits live in its process, so a
// second worker would double them; multiple active replicas are not supported. It applies no
// migrations: the seed and api commands do, and it refuses to start on an outdated schema.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bengisupinar/campaign/internal/delivery"
	"github.com/bengisupinar/campaign/internal/platform/config"
	"github.com/bengisupinar/campaign/internal/platform/database"
)

// stopTimeout bounds a graceful stop. It must stay below the container stop grace period
// (15 s in compose.yaml); jobs still running then are cut short and retried after a restart.
const stopTimeout = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Environ(), os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker: %v\n", err)
		os.Exit(1)
	}
}

// run works jobs until ctx is cancelled, then stops gracefully.
func run(ctx context.Context, environ []string, logOutput io.Writer) error {
	cfg, err := config.Load(environ)
	if err != nil {
		return err
	}
	logger := cfg.NewLogger(logOutput)
	logger.Info("starting worker", slog.Any("config", cfg))

	db, err := database.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("database handle: %w", err)
	}
	defer func() {
		if cerr := sqlDB.Close(); cerr != nil {
			logger.Error("closing database", slog.Any("error", cerr))
		}
	}()
	if err := database.CheckQueueSchema(ctx, sqlDB, logger); err != nil {
		return err
	}

	worker, err := delivery.NewWorker(db, sqlDB, logger)
	if err != nil {
		return err
	}
	// Jobs inherit this context; the worker stops gracefully below rather than by cancellation.
	if err := worker.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("start worker: %w", err)
	}
	logger.Info("worker started")

	<-ctx.Done()
	logger.Info("stopping worker", slog.String("timeout", stopTimeout.String()))
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
	defer cancel()
	if err := worker.Stop(stopCtx); err != nil {
		return fmt.Errorf("stop worker: %w", err)
	}
	logger.Info("worker stopped")
	return nil
}
