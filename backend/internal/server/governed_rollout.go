package server

import (
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// governedRolloutDependencies is the production composition boundary for
// Task13. Candidate executors are still registered separately and traffic is
// never activated by constructing this bundle.
type governedRolloutDependencies struct {
	evidence writingruntime.RolloutEvidenceStore
	shadow   writingruntime.ShadowContentSink
	gate     writingruntime.AllowlistPromotionGate
}

func newGovernedRolloutDependencies(store *writingstore.Store, telemetry writingruntime.RuntimeTelemetry) *governedRolloutDependencies {
	if store == nil {
		return nil
	}
	_ = telemetry // retained at this composition boundary for executor wiring.
	evidenceStore, _ := writingruntime.NewStoreRolloutEvidence(store)
	return &governedRolloutDependencies{
		evidence: evidenceStore,
		shadow:   writingruntime.WritingStoreShadowContentSink{Store: store},
		gate:     writingruntime.AllowlistPromotionGate{Store: store, Criteria: writingruntime.DefaultPromotionCriteria()},
	}
}
