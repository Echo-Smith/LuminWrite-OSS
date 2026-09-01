package server

import (
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func TestGovernedRolloutProductionCompositionRequiresPersistentStore(t *testing.T) {
	if deps := newGovernedRolloutDependencies(nil, nil); deps != nil {
		t.Fatal("nil store produced rollout dependencies")
	}
	// A non-nil zero Store is sufficient for composition type checks; no query is made.
	deps := newGovernedRolloutDependencies(&writingstore.Store{}, nil)
	if deps == nil {
		t.Fatal("persistent composition was not created")
	}
	if _, ok := deps.evidence.(writingruntime.WritingStoreEvidenceStore); !ok {
		t.Fatalf("evidence=%T", deps.evidence)
	}
	if _, ok := deps.shadow.(writingruntime.WritingStoreShadowContentSink); !ok {
		t.Fatalf("shadow=%T", deps.shadow)
	}
}
