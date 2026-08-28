package writingruntime

import (
	"errors"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func TestRecoverKeepsCheckpointUsageOnceAndRejectsPlanVersionDrift(t *testing.T) {
	plan := writingplan.ExecutablePlan{PlanID: "plan_recovery", PlanHash: hashForTest("recovery")}
	checkpoint := &Checkpoint{PlanID: plan.PlanID, PlanVersion: 2, PlanHash: plan.PlanHash,
		CompletedNodes: map[string]int{"node_done": 1}, SpentCostUSD: 3, SpentDurationMS: 30}
	attempts := []writingstore.NodeAttempt{{PlanID: plan.PlanID, PlanVersion: 2,
		NodeID: "node_done", Attempt: 1, Status: "succeeded", ActualCostUSD: 3, ActualDurationMS: 30}}
	state, err := Recover(plan, 2, checkpoint, attempts, map[string]writingplan.CapabilityManifest{})
	if err != nil || state.SpentCostUSD != 3 || state.SpentDurationMS != 30 {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	if _, err := Recover(plan, 3, checkpoint, attempts, nil); !errors.Is(err, ErrPlanChangedDuringRecovery) {
		t.Fatalf("version drift error=%v", err)
	}
}

func TestRecoverRequiresHumanForAmbiguousNonSafeAttempt(t *testing.T) {
	plan := writingplan.ExecutablePlan{PlanID: "plan_recovery", PlanHash: hashForTest("recovery")}
	attempts := []writingstore.NodeAttempt{{PlanID: plan.PlanID, PlanVersion: 1,
		NodeID: "node_external", Attempt: 1, Status: "running", CapabilityID: "external.publish"}}
	manifests := map[string]writingplan.CapabilityManifest{"external.publish": {ID: "external.publish", Idempotency: writingplan.IdempotencyExternal}}
	state, err := Recover(plan, 1, nil, attempts, manifests)
	if !errors.Is(err, ErrHumanRecoveryRequired) || len(state.HumanRequired) != 1 {
		t.Fatalf("state=%#v err=%v", state, err)
	}
}
