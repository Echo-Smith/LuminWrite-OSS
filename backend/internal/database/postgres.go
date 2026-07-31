package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB wraps the sql.DB connection pool.
type DB struct {
	*sql.DB
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
			return &DB{db}, nil
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
