package writingstore

import (
	"context"
	"errors"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

func TestGovernedQueriesAndExactPlanApproval(t *testing.T) {
	store, fixture := newIntegrationFixture(t, false)
	ctx := context.Background()
	version := testDocumentVersion(t, fixture.documentID, "ver_approval", nil, "approval draft")
	if _, err := store.CommitDocumentVersion(ctx, CommitDocumentVersionParams{Version: version, ContractID: fixture.contract.ContractID, ContractVersion: fixture.contract.Version, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	envelope := testPlanEnvelope(t, fixture.contract)
	envelope.StrategyDecision.ApprovalRequired = true
	permissions := []writingplan.Permission{"model.invoke", "validation.run"}
	budget := writingplan.PlanBudget{MaxCostUSD: 10, MaxDurationMS: 10000, MaxConcurrency: 1, MaxNodes: 2, MaxItems: 1}
	run := RunRecord{RunID: "run_approval", DocumentID: fixture.documentID, ContractID: fixture.contract.ContractID, ContractVersion: fixture.contract.Version, ContractHash: fixture.contract.ContractHash, BaseVersionID: version.VersionID, Status: "awaiting_approval", ApprovalMode: writingkernel.ApprovalModeAlways, RequestedAssurance: writingkernel.AssuranceLevelStandard, Budget: budget, Permissions: permissions, Trace: testTrace()}
	plan := PlanRecord{RunID: run.RunID, PlanVersion: 1, Envelope: envelope, Budget: budget, Permissions: permissions, Trace: testTrace()}
	if err := store.CreateRunWithPlan(ctx, run, plan, "awaiting_approval"); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadRuntimeRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BaseVersionID != version.VersionID || loaded.ApprovalStatus != "pending" {
		t.Fatalf("loaded run=%#v", loaded)
	}
	wrong := PlanApprovalCommand{RunID: run.RunID, PlanID: envelope.ExecutablePlan.PlanID, PlanVersion: 1, PlanHash: testHash("wrong-plan"), Permissions: permissions, IdempotencyKey: run.RunID + ":approval:user:wrong", Actor: Actor{Type: ActorUser, ID: "user"}}
	if err := store.ApprovePlan(ctx, wrong); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong plan scope error=%v", err)
	}
	command := wrong
	command.PlanHash = envelope.ExecutablePlan.PlanHash
	command.IdempotencyKey = run.RunID + ":approval:user:approve-1"
	if err := store.ApprovePlan(ctx, command); err != nil {
		t.Fatal(err)
	}
	command.Permissions = []writingplan.Permission{"validation.run", "model.invoke"}
	if err := store.ApprovePlan(ctx, command); err != nil {
		t.Fatalf("same permission set in another order must replay: %v", err)
	}
	loaded, err = store.LoadRuntimeRun(ctx, run.RunID)
	if err != nil || loaded.ApprovalStatus != "approved" {
		t.Fatalf("approved run=%#v err=%v", loaded, err)
	}
	var decisions int
	if err := integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM writing_decisions WHERE run_id=$1 AND decision_type='plan_approval'`, run.RunID).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if decisions != 1 {
		t.Fatalf("approval decisions=%d", decisions)
	}

	committed := appendTestEvent(t, store, RunEvent{RunID: run.RunID, EventType: "document.committed", EntityKind: "document_version", EntityID: version.VersionID, Payload: map[string]any{"document_id": fixture.documentID, "content_hash": version.ContentHash, "quality_state": QualityCandidateDraft}, Trace: testTrace()})
	events, err := store.ListRunEvents(ctx, run.RunID, 0, 20)
	if err != nil || len(events) != 1 || events[0].Sequence != committed.Sequence {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	document, err := store.GetDocument(ctx, fixture.documentID)
	if err != nil || document.CurrentVersionID != version.VersionID {
		t.Fatalf("document=%#v err=%v", document, err)
	}
	versions, err := store.ListDocumentVersions(ctx, fixture.documentID)
	if err != nil || len(versions) != 1 || versions[0].Version.VersionID != version.VersionID {
		t.Fatalf("versions=%#v err=%v", versions, err)
	}
}
