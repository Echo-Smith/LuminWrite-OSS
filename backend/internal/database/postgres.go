package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DBMetrics holds lightweight counters for database operations.
// These are read by the server's metrics exporter and converted to Prometheus metrics.
type DBMetrics struct {
	Queries atomic.Int64
	Errors  atomic.Int64
}

// globalDBMetrics is a package-level singleton for DB metrics.
// It is read by the server's /metrics endpoint.
var globalDBMetrics = &DBMetrics{}

// GetDBMetrics returns the global DB metrics counters.
func GetDBMetrics() *DBMetrics {
	return globalDBMetrics
}

// DB wraps the sql.DB connection pool.
type DB struct {
	*sql.DB
	metrics *DBMetrics
}

// NewPostgres creates a new PostgreSQL connection pool.
func NewPostgres(url string, maxOpen, maxIdle int) (*DB, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Retry connection — handles Docker startup race condition where
	// the backend starts before PostgreSQL is fully ready.
	maxRetries := 5
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := db.PingContext(ctx)
		cancel()
		if err == nil {
			slog.Info("database connected", "url", maskURL(url), "attempt", attempt)
			return &DB{DB: db, metrics: globalDBMetrics}, nil
		}
		lastErr = err
		slog.Warn("database connection retry", "attempt", attempt, "max", maxRetries, "error", err)
		if attempt < maxRetries {
			time.Sleep(2 * time.Second)
		}
	}

	db.Close()
	return nil, fmt.Errorf("failed to ping database after %d attempts: %w", maxRetries, lastErr)
}

// Migrate runs all pending SQL migrations using the new migration engine.
// Migrations are auto-discovered from the embedded migrations/ directory,
// tracked in a schema_migrations table, and each runs in its own transaction.
func Migrate(db *DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return MigrateDB(ctx, db, migrationFS)
}

// IsAvailable checks if the database is reachable.
func (db *DB) IsAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return db.PingContext(ctx) == nil
}

// ExecContext wraps sql.DB.ExecContext with metrics instrumentation.
func (db *DB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if db.metrics != nil {
		db.metrics.Queries.Add(1)
	}
	res, err := db.DB.ExecContext(ctx, query, args...)
	if err != nil && db.metrics != nil {
		db.metrics.Errors.Add(1)
	}
	return res, err
}

// QueryContext wraps sql.DB.QueryContext with metrics instrumentation.
func (db *DB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	if db.metrics != nil {
		db.metrics.Queries.Add(1)
	}
	rows, err := db.DB.QueryContext(ctx, query, args...)
	if err != nil && db.metrics != nil {
		db.metrics.Errors.Add(1)
	}
	return rows, err
}

// QueryRowContext wraps sql.DB.QueryRowContext with metrics instrumentation.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	if db.metrics != nil {
		db.metrics.Queries.Add(1)
	}
	// Note: QueryRowContext doesn't return an error directly — it's deferred to Scan().
	// We still count the query; errors will be caught at the Scan() call site.
	return db.DB.QueryRowContext(ctx, query, args...)
}

// maskURL hides credentials in the database URL for logging.
func maskURL(url string) string {
	// Just show the host part
	for i := 0; i < len(url); i++ {
		if url[i] == '@' {
			// Find the host part after @
			rest := url[i+1:]
			return "postgres://***@" + rest
		}
	}
	return "postgres://***"
}
