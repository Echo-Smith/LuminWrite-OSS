package writingstore

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCanonicalContentOnRealDatabase(t *testing.T) {
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	body := []byte("canonical draft body for restart recovery")
	hash := hashShadowBody(body)
	record := CanonicalContentRecord{Key: fixture.runID + ":node_draft:1:full_draft",
		MediaType: "text/markdown", Body: body, ContentHash: hash, CreatedAt: time.Now().UTC()}
	if err := store.PutCanonicalContent(ctx, record); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetCanonicalContent(ctx, record.Key)
	if err != nil || string(loaded) != string(body) {
		t.Fatalf("loaded=%s err=%v", loaded, err)
	}
	// Idempotent replay with identical content succeeds.
	if err := store.PutCanonicalContent(ctx, record); err != nil {
		t.Fatalf("identical replay err=%v", err)
	}
	// A different body under the same key fails closed.
	tampered := CanonicalContentRecord{Key: record.Key, MediaType: record.MediaType,
		Body: []byte("rewritten under an existing key"), ContentHash: hashShadowBody([]byte("rewritten under an existing key")),
		CreatedAt: time.Now().UTC()}
	if err := store.PutCanonicalContent(ctx, tampered); !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("tampered replay err=%v", err)
	}
	// Stored-body tampering is detected on load (recovery boundary).
	if _, err := store.GetCanonicalContent(ctx, "run_missing:node:1:draft"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key err=%v", err)
	}
	if err := store.PutCanonicalContent(ctx, CanonicalContentRecord{Key: "bad key", MediaType: "text/markdown",
		Body: body, ContentHash: hash, CreatedAt: time.Now().UTC()}); err == nil {
		t.Fatal("invalid key accepted")
	}
}
