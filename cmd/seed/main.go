// Command seed fills an empty database with synthetic demo data: 5,000 customers with their
// permissions, baskets and orders, and the product catalogue.
//
// It applies pending migrations first, so it works before the API has ever run, and leaves a
// database that already has customers untouched, so it can run on every start.
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

	"github.com/bengisupinar/campaign/internal/platform/config"
	"github.com/bengisupinar/campaign/internal/platform/database"
	"github.com/bengisupinar/campaign/internal/seed"
	"github.com/bengisupinar/campaign/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Environ(), os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, environ []string, logOutput io.Writer) error {
	cfg, err := config.Load(environ)
	if err != nil {
		return err
	}
	logger := cfg.NewLogger(logOutput)

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

	if err := database.Migrate(ctx, sqlDB, migrations.FS, logger); err != nil {
		return err
	}

	start := time.Now()
	opts := seed.DefaultOptions()
	result, err := seed.Run(ctx, db, opts, start)
	if err != nil {
		return err
	}
	if result.Skipped {
		logger.Info("database already has customers; nothing to seed")
		return nil
	}
	logger.Info("seeded synthetic data",
		slog.Int("customers", result.Customers),
		slog.Int("permissions", result.Permissions),
		slog.Int("products", result.Products),
		slog.Int("orders", result.Orders),
		slog.Int("order_lines", result.OrderLines),
		slog.Int("basket_lines", result.BasketLines),
		slog.Uint64("random_seed", opts.Seed),
		slog.String("duration", time.Since(start).Round(time.Millisecond).String()),
	)
	return nil
}
