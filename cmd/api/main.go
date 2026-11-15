// Command api serves the campaign composer HTTP API.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/delivery"
	"github.com/bengisupinar/campaign/internal/platform/config"
	"github.com/bengisupinar/campaign/internal/platform/database"
	"github.com/bengisupinar/campaign/internal/platform/health"
	"github.com/bengisupinar/campaign/internal/platform/httpserver"
	"github.com/bengisupinar/campaign/internal/segmentation"
	"github.com/bengisupinar/campaign/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Environ(), os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "api: %v\n", err)
		os.Exit(1)
	}
}

// run wires the application and blocks until ctx is cancelled and shutdown has finished.
func run(ctx context.Context, environ []string, logOutput io.Writer) error {
	cfg, err := config.Load(environ)
	if err != nil {
		return err
	}
	logger := cfg.NewLogger(logOutput)
	logger.Info("starting api", slog.Any("config", cfg))

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

	probes := health.NewProbes(logger, map[string]health.Check{"postgres": sqlDB.PingContext})
	router := httpserver.NewRouter(logger, health.LivenessPath, health.ReadinessPath)
	router.Get(health.LivenessPath, probes.Live)
	router.Get(health.ReadinessPath, probes.Ready)

	queue, err := delivery.NewQueue(sqlDB, logger)
	if err != nil {
		return err
	}
	segments := segmentation.NewService(db)
	campaigns := campaign.NewService(campaign.NewStore(db), segments, queue, time.Now)
	// Event streams end with ctx: a server shutdown doesn't cancel the requests it waits for.
	router.Mount("/api/campaigns/{id}/delivery", delivery.NewHandler(ctx, campaigns, db, logger))
	router.Mount("/api/campaigns", campaign.NewHandler(campaigns, logger))
	router.Mount("/api/segments", segmentation.NewHandler(segments, time.Now, logger))

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
	}
	logger.Info("http server listening", slog.String("addr", ln.Addr().String()))

	return httpserver.Serve(ctx, logger, ln, router)
}
