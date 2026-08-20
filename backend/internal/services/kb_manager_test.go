package services

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestHybridSearchReportsUnavailableDatabaseInsteadOfEmptyResult(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://postgres:unused@127.0.0.1:1/writing_agent_v2?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	_, err = NewKbManager(db, nil).HybridSearch(ctx, "", "shadow-e2e", 5)
	if err == nil || !strings.Contains(err.Error(), "local knowledge base unavailable") {
		t.Fatalf("expected an unavailable-KB error, got %v", err)
	}
}
