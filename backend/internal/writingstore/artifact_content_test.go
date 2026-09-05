package writingstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func artifactTestHash(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestArtifactContentStore(t *testing.T) {
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, _ := newIntegrationFixture(t, false)
	ctx := context.Background()
	body := []byte("第一章：治理型写作运行时的上下文编译。")
	hash := artifactTestHash(body)

	if err := store.PutArtifactContent(ctx, hash, "text/markdown", body); err != nil {
		t.Fatalf("put: %v", err)
	}
	media, loaded, err := store.GetArtifactContent(ctx, hash)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if media != "text/markdown" || !bytes.Equal(loaded, body) {
		t.Fatalf("round-trip mismatch media=%q body=%q", media, loaded)
	}

	// Re-staging identical bytes is idempotent (ON CONFLICT DO NOTHING).
	if err := store.PutArtifactContent(ctx, hash, "text/markdown", body); err != nil {
		t.Fatalf("idempotent re-stage errored: %v", err)
	}

	// A stored body is immutable: direct UPDATE of the body is rejected.
	if _, err := integrationDB.ExecContext(ctx, `UPDATE writing_artifact_contents SET body=$2 WHERE content_hash=$1`, hash, []byte("tampered")); err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("content body update must be rejected by the immutable trigger, got %v", err)
	}

	// Malformed hash / empty body are refused at the store boundary.
	if err := store.PutArtifactContent(ctx, "not-a-hash", "text/plain", body); err == nil {
		t.Fatal("malformed content hash accepted")
	}
	if err := store.PutArtifactContent(ctx, artifactTestHash([]byte("x")), "text/plain", nil); err == nil {
		t.Fatal("empty body accepted")
	}

	// Missing content is ErrNotFound.
	if _, _, err := store.GetArtifactContent(ctx, artifactTestHash([]byte("absent"))); err == nil {
		t.Fatal("missing content returned no error")
	}
}
