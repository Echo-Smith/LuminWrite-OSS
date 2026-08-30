package writingruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type fakeCanonicalRepository struct {
	records map[string]writingstore.CanonicalContentRecord
}

func (repo *fakeCanonicalRepository) PutCanonicalContent(_ context.Context, record writingstore.CanonicalContentRecord) error {
	if repo.records == nil {
		repo.records = map[string]writingstore.CanonicalContentRecord{}
	}
	if existing, ok := repo.records[record.Key]; ok {
		if existing.ContentHash != record.ContentHash {
			return writingstore.ErrImmutableConflict
		}
		return nil
	}
	repo.records[record.Key] = record
	return nil
}

func (repo *fakeCanonicalRepository) GetCanonicalContent(_ context.Context, key string) ([]byte, error) {
	record, ok := repo.records[key]
	if !ok {
		return nil, writingstore.ErrNotFound
	}
	return append([]byte(nil), record.Body...), nil
}

func TestStoreContentGatewayStagesAndLoads(t *testing.T) {
	gateway, err := NewStoreContentGateway(&fakeCanonicalRepository{})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("baseline draft body")
	ref, hash, err := gateway.Stage(context.Background(), "run_x:node_write:1:full_draft", "text/markdown", body)
	if err != nil {
		t.Fatal(err)
	}
	if ref != writingstore.CanonicalContentRefPrefix+"run_x:node_write:1:full_draft" || hash != contentHash(body) {
		t.Fatalf("ref=%s hash=%s", ref, hash)
	}
	input := InputArtifact{ArtifactID: "art_draft", Version: 1, ArtifactType: "full_draft",
		ContentHash: hash, MediaType: "text/markdown", ContentRef: ref}
	loaded, err := gateway.Load(context.Background(), input)
	if err != nil || string(loaded) != string(body) {
		t.Fatalf("loaded=%s err=%v", loaded, err)
	}
}

// TestStoreContentGatewayEnforcesRecoveryBoundary pins the recovery
// contract: loads verify the declared hash, so a tampered or drifted body
// fails with MATERIAL_INTEGRITY_FAILED instead of feeding stale content
// into downstream nodes after a restart.
func TestStoreContentGatewayEnforcesRecoveryBoundary(t *testing.T) {
	gateway, err := NewStoreContentGateway(&fakeCanonicalRepository{})
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := gateway.Stage(context.Background(), "run_x:node_write:1:full_draft", "text/markdown", []byte("original body"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := InputArtifact{ArtifactID: "art_draft", Version: 1, ArtifactType: "full_draft",
		ContentHash: hash, MediaType: "text/markdown",
		ContentRef: writingstore.CanonicalContentRefPrefix + "run_x:node_write:1:full_draft"}
	repo := gateway.repository.(*fakeCanonicalRepository)
	tamperedRecord := repo.records["run_x:node_write:1:full_draft"]
	tamperedRecord.Body = []byte("tampered body")
	repo.records["run_x:node_write:1:full_draft"] = tamperedRecord
	if _, err := gateway.Load(context.Background(), tampered); !errors.Is(err, ErrLegacyContentIntegrity) {
		t.Fatalf("err=%v", err)
	}
	if _, err := gateway.Load(context.Background(), InputArtifact{ContentRef: "memory://contract"}); err == nil ||
		!strings.Contains(err.Error(), "canonical loads require") {
		t.Fatalf("non-canonical ref err=%v", err)
	}
}
