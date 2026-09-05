// Package dbtest gives test binaries a private PostgreSQL database per
// process. Before this helper, every package's integration tests shared one
// TEST_DATABASE_URL: parallel go test packages TRUNCATEd each other's rows,
// deadlocked on ACCESS EXCLUSIVE locks, and collided on fixed fixture ids.
// A per-process database removes the contention at the root — TRUNCATE only
// ever touches rows this process created.
//
// Usage in TestMain:
//
//	db, cleanup, err := dbtest.Open(os.Getenv("TEST_DATABASE_URL"), 5, 2)
//	if errors.Is(err, dbtest.ErrNoDatabaseURL) {
//		os.Exit(m.Run()) // no DB configured: integration tests skip
//	}
//	if err != nil { ...exit... }
//	defer cleanup()
//
// or inside a single test:
//
//	db, cleanup, err := dbtest.Open(databaseURL, 5, 2)
//	if errors.Is(err, dbtest.ErrNoDatabaseURL) { t.Skip(...) }
//	if err != nil { t.Fatal(err) }
//	defer cleanup()
//
// The returned database is fully migrated (migrations provision their own
// extensions, so a fresh template0 database is self-contained) and the
// cleanup drops it WITH (FORCE) after closing the pool.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

// ErrNoDatabaseURL is returned when TEST_DATABASE_URL is empty; callers
// decide whether that means skip (local dev) or fail (CI).
var ErrNoDatabaseURL = errors.New("dbtest: TEST_DATABASE_URL is not set")

// Open creates a uniquely named database, connects, migrates it, and returns
// the handle plus a cleanup that closes the pool and drops the database.
func Open(databaseURL string, maxOpen, maxIdle int) (*database.DB, func(), error) {
	if databaseURL == "" {
		return nil, nil, ErrNoDatabaseURL
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("dbtest: parse database URL: %w", err)
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return nil, nil, fmt.Errorf("dbtest: generate database name: %w", err)
	}
	name := "test_" + hex.EncodeToString(suffix)

	admin, err := database.NewPostgres(databaseURL, 2, 1)
	if err != nil {
		return nil, nil, fmt.Errorf("dbtest: connect admin database: %w", err)
	}
	if _, err := admin.Exec(fmt.Sprintf(`CREATE DATABASE %s TEMPLATE template0`, name)); err != nil {
		admin.Close()
		return nil, nil, fmt.Errorf("dbtest: create %s: %w", name, err)
	}

	isolated := *parsed
	isolated.Path = "/" + name
	db, err := database.NewPostgres(isolated.String(), maxOpen, maxIdle)
	if err != nil {
		admin.Close()
		return nil, nil, fmt.Errorf("dbtest: connect %s: %w", name, err)
	}
	if err := database.Migrate(db); err != nil {
		db.Close()
		admin.Close()
		return nil, nil, fmt.Errorf("dbtest: migrate %s: %w", name, err)
	}
	cleanup := func() {
		db.Close()
		// WITH (FORCE) drops the database even if a pooled connection or a
		// leaked helper handle is still attached (PostgreSQL 13+).
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(dropCtx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
		admin.Close()
	}
	return db, cleanup, nil
}
