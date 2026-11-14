// Package database connects to PostgreSQL through GORM and applies the versioned schema
// migrations. The schema changes only through migrations, never through GORM's AutoMigrate:
// the application's tables through goose, and the job queue's tables through River's own
// versioned migrator.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
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

	// insertBatchSize bounds the rows per INSERT statement, keeping each well below
	// PostgreSQL's limit of 65,535 bind parameters.
	insertBatchSize = 1000
)

// Insert writes rows in batches of up to 1,000 and fills in the IDs the database generates.
// An empty slice is a no-op.
func Insert[T any](ctx context.Context, db *gorm.DB, rows []T) error {
	if len(rows) == 0 {
		return nil
	}
	return db.WithContext(ctx).CreateInBatches(rows, insertBatchSize).Error
}

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

	// River migrates its own tables, so upgrading River never means copying its SQL here.
	migrator, err := queueMigrator(db, logger)
	if err != nil {
		return err
	}
	queueResult, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("migrate job queue: %w", err)
	}
	for _, v := range queueResult.Versions {
		logger.InfoContext(ctx, "job queue migration applied",
			slog.Int("version", v.Version),
			slog.String("name", v.Name),
			slog.String("duration", v.Duration.String()),
		)
	}
	return nil
}

// SQLTx returns the database/sql transaction behind a GORM transaction, so that libraries
// such as River can take part in it. tx must come from (*gorm.DB).Transaction.
func SQLTx(tx *gorm.DB) (*sql.Tx, error) {
	sqlTx, ok := tx.Statement.ConnPool.(*sql.Tx)
	if !ok {
		return nil, fmt.Errorf("not in a database/sql transaction: %T", tx.Statement.ConnPool)
	}
	return sqlTx, nil
}

// CheckQueueSchema fails unless every one of River's migrations has been applied. Processes
// that don't migrate, such as the worker, call it before they start.
func CheckQueueSchema(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
	migrator, err := queueMigrator(db, logger)
	if err != nil {
		return err
	}
	result, err := migrator.Validate(ctx, nil)
	if err != nil {
		return fmt.Errorf("check job queue schema: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("job queue schema is out of date: %s", strings.Join(result.Messages, "; "))
	}
	return nil
}

func queueMigrator(db *sql.DB, logger *slog.Logger) (*rivermigrate.Migrator[*sql.Tx], error) {
	migrator, err := rivermigrate.New(riverdatabasesql.New(db), &rivermigrate.Config{Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("job queue migrator: %w", err)
	}
	return migrator, nil
}
