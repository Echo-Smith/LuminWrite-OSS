package writingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

var ErrInvalidRuntimeEvidence = errors.New("writingruntime: invalid rollout evidence")

type RuntimeEvidenceRecorder interface {
	RecordRuntimeEvidence(context.Context, writingstore.RuntimeEvidenceRecord) error
}

// StoreRolloutEvidence is the only production rollout-evidence adapter. It
// maps into writingstore's append-only RunLedger instead of creating another
// evidence database or fact source.
type StoreRolloutEvidence struct {
	recorder RuntimeEvidenceRecorder
}

// WritingStoreEvidenceStore remains as a source-compatible name for callers
// compiled against the Task12 contract.
type WritingStoreEvidenceStore = StoreRolloutEvidence

func NewStoreRolloutEvidence(recorder RuntimeEvidenceRecorder) (*StoreRolloutEvidence, error) {
	if recorder == nil {
		return nil, ErrRuntimeNotReady
	}
	return &StoreRolloutEvidence{recorder: recorder}, nil
}

func (store *StoreRolloutEvidence) Record(ctx context.Context, evidence RuntimeEvidence) error {
	if store == nil || store.recorder == nil {
		return ErrRuntimeNotReady
	}
	if err := validateRuntimeEvidence(evidence); err != nil {
		return err
	}
	payloadBytes, err := json.Marshal(evidence)
	if err != nil {
		return runtimeError(CodeRolloutEvidenceFailed, RetryNever, "rollout evidence could not be encoded", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return runtimeError(CodeRolloutEvidenceFailed, RetryNever, "rollout evidence payload is not an object", err)
	}
	return store.recorder.RecordRuntimeEvidence(ctx, writingstore.RuntimeEvidenceRecord{
		EvidenceID: evidence.EvidenceID,
		RunID:      evidence.Identity.RunID,
		NodeID:     evidence.Identity.NodeID,
		Attempt:    evidence.Identity.Attempt,
		Kind:       evidence.Kind,
		Payload:    payload,
		OccurredAt: evidence.RecordedAt,
	})
}

func validateRuntimeEvidence(evidence RuntimeEvidence) error {
	if !strings.HasPrefix(evidence.EvidenceID, "evt_") || len(strings.TrimSpace(evidence.EvidenceID)) <= len("evt_") {
		return runtimeError(CodeRolloutEvidenceFailed, RetryNever, "rollout evidence id is required", ErrInvalidRuntimeEvidence)
	}
	if err := evidence.Identity.Validate(); err != nil {
		return runtimeError(CodeRolloutEvidenceFailed, RetryNever, "rollout evidence identity is invalid", err)
	}
	switch evidence.Kind {
	case "route_decision", "execution", "shadow_comparison":
	default:
		return runtimeError(CodeRolloutEvidenceFailed, RetryNever, "unsupported rollout evidence kind", ErrInvalidRuntimeEvidence)
	}
	if evidence.RecordedAt.IsZero() {
		return runtimeError(CodeRolloutEvidenceFailed, RetryNever, "rollout evidence time is required", ErrInvalidRuntimeEvidence)
	}
	return nil
}
