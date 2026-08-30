package writingstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestShadowContentRecordValidationBindsKeyAndBody(t *testing.T) {
	body := []byte("candidate")
	policy := testHash("shadow-policy")
	hash := hashShadowBody(body)
	record := ShadowContentRecord{
		Key:        strings.TrimPrefix(policy, "sha256:") + "/run_shadow-node_write-1-draft/" + strings.TrimPrefix(hash, "sha256:"),
		PolicyHash: policy, RunID: "run_shadow", MediaType: "text/markdown", Body: body,
		ContentHash: hash, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := validateShadowContentRecord(record); err != nil {
		t.Fatal(err)
	}
	record.Body = []byte("different")
	if err := validateShadowContentRecord(record); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("error=%v", err)
	}
}

func TestShadowContentRepositoryRoundTripAndIsolation(t *testing.T) {
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	store, _ := New(integrationDB)
	ctx := context.Background()
	if _, err := integrationDB.ExecContext(ctx, `TRUNCATE writing_shadow_content`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	body := []byte("candidate")
	policy := testHash("shadow-policy")
	hash := hashShadowBody(body)
	record := ShadowContentRecord{
		Key:        strings.TrimPrefix(policy, "sha256:") + "/run_shadow-node_write-1-draft/" + strings.TrimPrefix(hash, "sha256:"),
		PolicyHash: policy, RunID: "run_shadow", MediaType: "text/markdown", Body: body,
		ContentHash: hash, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.PutShadowContent(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := store.PutShadowContent(ctx, record); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	loaded, err := store.GetShadowContent(ctx, record.Key)
	if err != nil || string(loaded) != string(body) {
		t.Fatalf("body=%q err=%v", loaded, err)
	}
	conflict := record
	conflict.Body = []byte("different")
	conflict.ContentHash = hashShadowBody(conflict.Body)
	if err := store.PutShadowContent(ctx, conflict); !errors.Is(err, ErrInvalidRecord) && !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	prefix := strings.TrimPrefix(policy, "sha256:") + "/run_shadow-"
	if removed, err := store.DeleteShadowContentPrefix(ctx, prefix); err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
}
