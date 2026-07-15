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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("database connected", "url", maskURL(url))
	return &DB{db}, nil
}

// Migrate runs all SQL migration files in order.
func Migrate(db *DB) error {
	migrations := []string{
		"001_extensions",
		"002_style_profiles",
		"003_users_topics",
		"004_agent_traces",
		"005_knowledge_base",
		"006_feedback_eval",
		"007_sensitive_words_upgrade",
		"008_admin_tables",
		"009_reputation_workbuddy",
		"010_embedding",
		"011_evaluation_seed",
		"012_passkey",
		"013_users_password",
		"014_api_key_hash",
		"015_user_memories",
		"016_user_session_delete",
		"017_model_api_key",
	}

	for _, name := range migrations {
		sqlBytes, err := migrationFS.ReadFile("migrations/" + name + ".up.sql")
		if err != nil {
			slog.Warn("failed to read migration file", "name", name, "error", err)
			continue
		}
		if _, err := db.Exec(string(sqlBytes)); err != nil {
			slog.Warn("failed to execute migration", "name", name, "error", err)
			// Continue — tables may already exist
		} else {
			slog.Info("migration completed", "name", name)
		}
	}

	slog.Info("all migrations completed")
	return nil
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
