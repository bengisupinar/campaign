// Package database connects to PostgreSQL through GORM and applies the versioned schema
// migrations. The schema changes only through migrations, never through GORM's AutoMigrate.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Connection pool limits. PostgreSQL allows 100 connections by default; these leave
// headroom for migrations, psql sessions and future worker processes.
const (
	maxOpenConns    = 20
	maxIdleConns    = 20
	connMaxLifetime = 30 * time.Minute
	connMaxIdleTime = 5 * time.Minute

	pingTimeout        = 5 * time.Second
	slowQueryThreshold = 200 * time.Millisecond
)

// Open connects to the database at dsn and verifies the connection within pingTimeout.
// The caller owns the pool and closes it through db.DB().
func Open(ctx context.Context, dsn string, logger *slog.Logger) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.NewSlogLogger(logger, gormlogger.Config{
			LogLevel:                  gormlogger.Warn, // errors and slow queries only
			SlowThreshold:             slowQueryThreshold,
			IgnoreRecordNotFoundError: true, // "not found" is a normal outcome, not an error
			ParameterizedQueries:      true, // never log bind values: they may hold personal data
		}),
		TranslateError:       true, // e.g. unique violations become gorm.ErrDuplicatedKey
		DisableAutomaticPing: true, // pinged below, bounded by a timeout
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)
	sqlDB.SetConnMaxIdleTime(connMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

// Migrate applies every pending migration in fsys. A PostgreSQL advisory lock serialises
// concurrent runs, so several instances can start at the same time safely.
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS, logger *slog.Logger) error {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migrate: create session locker: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for _, r := range results {
		logger.InfoContext(ctx, "migration applied",
			slog.Int64("version", r.Source.Version),
			slog.String("file", r.Source.Path),
			slog.String("duration", r.Duration.String()),
		)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("migrate: read schema version: %w", err)
	}
	logger.InfoContext(ctx, "database schema up to date",
		slog.Int64("version", version),
		slog.Int("applied", len(results)),
	)
	return nil
}
