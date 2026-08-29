package writingruntime

import (
	"context"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func TestWritingStoreEvidenceAdapterPreservesTask11ExecutionIdentity(t *testing.T) {
	recorder := &evidenceRecorderStub{}
	store := WritingStoreEvidenceStore{Recorder: recorder}
	request := legacyRequest([]byte("contract"))
	evidence := RuntimeEvidence{EvidenceID: "evt_evidence", Kind: "shadow_comparison", Identity: request.Identity(),
		Adapter: OfflineAdapterPolicy(AdapterFamilyHarness), PolicyHash: hashForTest("policy"), PolicyVersion: 1,
		Mode: RolloutShadow, Lane: LaneShadow, Status: "different", RecordedAt: time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC),
		Outputs: []OutputManifest{}, Comparison: &ShadowComparison{BaselineStatus: "succeeded", CandidateStatus: "succeeded"}}
	if err := store.Record(context.Background(), evidence); err != nil {
		t.Fatal(err)
	}
	if recorder.record.RunID != request.RunID || recorder.record.NodeID != request.NodeID || recorder.record.Attempt != request.Attempt || recorder.record.Kind != "shadow_comparison" {
		t.Fatalf("record=%#v", recorder.record)
	}
	identity, ok := recorder.record.Payload["identity"].(map[string]any)
	if !ok || identity["idempotency_key"] != request.IdempotencyKey || recorder.record.Payload["outputs"] == nil {
		t.Fatalf("payload=%#v", recorder.record.Payload)
	}
}

type evidenceRecorderStub struct {
	record writingstore.RuntimeEvidenceRecord
}

func (recorder *evidenceRecorderStub) RecordRuntimeEvidence(_ context.Context, record writingstore.RuntimeEvidenceRecord) error {
	recorder.record = record
	return nil
}
