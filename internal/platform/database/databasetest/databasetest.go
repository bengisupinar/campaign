// Package databasetest gives integration tests a throwaway PostgreSQL database with every
// migration applied.
//
// Tests that use it are skipped unless TEST_DATABASE_URL is set, so `make test` needs no
// database; `make db`, then `make test-integration`, runs them.
package databasetest

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/platform/database"
	"github.com/bengisupinar/campaign/migrations"
)

// New creates a database with every migration applied and drops it when the test ends. It
// skips the test unless TEST_DATABASE_URL points at a PostgreSQL server whose user may create
// databases. The development data is never touched.
func New(t testing.TB) *gorm.DB {
	t.Helper()
	serverURL := os.Getenv("TEST_DATABASE_URL")
	if serverURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; run `make db`, then `make test-integration`")
	}
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)

	server := open(t, serverURL, logger)
	// The process ID keeps names unique when several test binaries run at once.
	name := fmt.Sprintf("campaign_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if err := server.WithContext(ctx).Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		// t.Context() is already cancelled when cleanups run.
		drop := server.WithContext(context.Background()).Exec("DROP DATABASE " + name + " WITH (FORCE)")
		if drop.Error != nil {
			t.Errorf("drop database %s: %v", name, drop.Error)
		}
	})

	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal("TEST_DATABASE_URL is not a valid URL") // the error would repeat the password
	}
	u.Path = "/" + name
	db := open(t, u.String(), logger)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("database handle: %v", err)
	}
	if err := database.Migrate(ctx, sqlDB, migrations.FS, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// open connects through the application's own database.Open and closes the pool when the
// test ends.
func open(t testing.TB, dsn string, logger *slog.Logger) *gorm.DB {
	t.Helper()
	db, err := database.Open(t.Context(), dsn, logger)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
