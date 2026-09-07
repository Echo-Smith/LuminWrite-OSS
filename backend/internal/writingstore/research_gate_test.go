package writingstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

// T02 store tests (docs/plans/2026-09-07-research-review-integration.md §T02):
// the six gate behaviors — pending / decide / duplicate decide / stale
// decide / unauthorized decide / decision survives a fresh connection — plus
// the research sub-task ledger's lease fencing.

type t02Fixture struct {
	store    *Store
	userID   string
	otherID  string
	runID    string
	planID   string
	nodeID   string
	contract writingkernel.WritingContract
}

// newT02Fixture provisions two users plus one planned run with an active
// plan: the minimum shape a gate decision verifies against.
func newT02Fixture(t *testing.T, name string) t02Fixture {
	t.Helper()
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	if _, err := integrationDB.ExecContext(ctx, `TRUNCATE writing_gate_decisions, writing_research_tasks, writing_documents CASCADE`); err != nil {
		t.Fatalf("reset t02 tables: %v", err)
	}
	// users.id is database-generated; the inserts resolve both owners' ids.
	var userA, userB string
	for _, user := range []struct {
		uid, name string
		target    *string
	}{
		{"t02_user_a_" + name, "t02 owner", &userA},
		{"t02_user_b_" + name, "t02 other", &userB},
	} {
		if err := integrationDB.QueryRowContext(ctx, `
			INSERT INTO users (uid, name) VALUES ($1, $2) RETURNING id::text
		`, user.uid, user.name).Scan(user.target); err != nil {
			t.Fatalf("provision fixture users: %v", err)
		}
	}
	store, err := New(integrationDB)
	if err != nil {
		t.Fatal(err)
	}
	contract := testContract(t)
	documentID := "doc_" + name
	if err := store.CreateDocument(ctx, DocumentRecord{DocumentID: documentID,
		OwnerUserID: userA, Title: "T02 fixture", Actor: testTrace().Actor}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutContract(ctx, ContractRecord{DocumentID: documentID, Contract: contract, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	runID := "run_" + name
	planEnvelope := testPlanEnvelope(t, contract)
	planID := planEnvelope.ExecutablePlan.PlanID
	budget := writingplan.PlanBudget{MaxCostUSD: 10, MaxDurationMS: 10000, MaxConcurrency: 1, MaxNodes: 4, MaxItems: 1}
	if err := store.CreateRun(ctx, RunRecord{RunID: runID, DocumentID: documentID,
		ContractID: contract.ContractID, ContractVersion: contract.Version,
		ContractHash: contract.ContractHash, Status: "planned",
		ApprovalMode:       writingkernel.ApprovalModeAuto,
		RequestedAssurance: writingkernel.AssuranceLevelStandard, Budget: budget,
		Permissions: []writingplan.Permission{"model.invoke"}, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutPlan(ctx, PlanRecord{RunID: runID, PlanVersion: 1, Envelope: planEnvelope,
		Budget: budget, Permissions: []writingplan.Permission{"model.invoke"}, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	if err := store.InTransaction(ctx, func(tx *Tx) error {
		return tx.ActivatePlan(ctx, runID, planID, 1, "running")
	}); err != nil {
		t.Fatal(err)
	}
	return t02Fixture{store: store, userID: userA, otherID: userB, runID: runID,
		planID: planID, nodeID: "node_t02_gate", contract: contract}
}

func (fixture t02Fixture) gate() GateRecord {
	return GateRecord{GateID: StableID("gate_", fixture.runID, fixture.nodeID),
		RunID: fixture.runID, NodeID: fixture.nodeID, PlanID: fixture.planID,
		PlanVersion: 1, PlanHash: testHash("t02-plan"), GateKind: GateKindEvidence,
		InputArtifactID: "art_pack", InputArtifactVersion: 1, InputHash: testHash("t02-pack"),
		Revision: 1, OwnerUserID: fixture.userID}
}

func t02Decision(fixture t02Fixture, key string) GateDecisionCommand {
	return GateDecisionCommand{RunID: fixture.runID, GateID: fixture.gate().GateID,
		PlanID: fixture.planID, PlanVersion: 1, PlanHash: testHash("t02-plan"),
		GateRevision: 1, Decision: GateDecisionApprove, ActorID: fixture.userID,
		Input:          ArtifactContentRef{ArtifactID: "art_pack", Version: 1, ContentHash: testHash("t02-pack")},
		IdempotencyKey: fixture.runID + ":gate:" + fixture.gate().GateID + ":decision:" + fixture.userID + ":" + key,
		RequestHash:    testHash("request-" + key),
		Trace:          testTrace()}
}

// TestResearchTaskLeaseFencing covers the sub-task ledger: claim advances the
// attempt, a succeeded row requires a verified hash, an expired worker's late
// completion is fenced off, outcome_unknown never auto-recycles, and the
// unique identity collapses duplicate inserts.
func TestResearchTaskLeaseFencing(t *testing.T) {
	fixture := newT02Fixture(t, "t02tasks"+fmt.Sprint(time.Now().UnixNano()))
	ctx := context.Background()
	store := fixture.store
	runID, nodeID, owner := fixture.runID, fixture.nodeID, fixture.userID

	var created ResearchTask
	err := store.InTransaction(ctx, func(tx *Tx) error {
		var err error
		created, err = tx.EnsureResearchTask(ctx, CreateResearchTask{OwnerUserID: owner,
			RunID: runID, NodeID: nodeID, TaskKey: "read:paper-1", Phase: "read",
			InputHash: testHash("task-input")}, time.Now().UTC())
		return err
	})
	if err != nil || created.Status != ResearchTaskPending {
		t.Fatalf("ensure task=%#v err=%v", created, err)
	}
	// Duplicate identity collapses onto the same row.
	var replay ResearchTask
	err = store.InTransaction(ctx, func(tx *Tx) error {
		var err error
		replay, err = tx.EnsureResearchTask(ctx, CreateResearchTask{OwnerUserID: owner,
			RunID: runID, NodeID: nodeID, TaskKey: "read:paper-1", Phase: "read",
			InputHash: testHash("task-input")}, time.Now().UTC())
		return err
	})
	if err != nil || replay.ID != created.ID {
		t.Fatalf("replay task=%#v (created %d) err=%v", replay, created.ID, err)
	}

	now := time.Now().UTC()
	var claimed ResearchTask
	err = store.InTransaction(ctx, func(tx *Tx) error {
		var ok bool
		var err error
		claimed, ok, err = tx.ClaimResearchTask(ctx, runID, nodeID, "worker-a", 50*time.Millisecond, now)
		if err != nil || !ok {
			t.Fatalf("claim ok=%v err=%v", ok, err)
		}
		return nil
	})
	if claimed.Status != ResearchTaskRunning || claimed.Attempt != 1 || claimed.LeaseOwner != "worker-a" {
		t.Fatalf("claimed=%#v", claimed)
	}
	// A second worker finds nothing to claim while the lease is live.
	err = store.InTransaction(ctx, func(tx *Tx) error {
		_, ok, err := tx.ClaimResearchTask(ctx, runID, nodeID, "worker-b", time.Minute, now)
		if err != nil || ok {
			t.Fatalf("second claim must not succeed: ok=%v err=%v", ok, err)
		}
		return nil
	})

	// Late owner commits after lease expiry: fenced.
	time.Sleep(60 * time.Millisecond)
	err = store.InTransaction(ctx, func(tx *Tx) error {
		return tx.CompleteResearchTask(ctx, claimed.ID, "worker-a",
			ResearchTaskCompletion{OutputArtifactID: "art_read_1", OutputHash: testHash("read-1")}, time.Now().UTC())
	})
	if err == nil {
		t.Fatal("expired lease completion must be fenced")
	}
	// Failed-task completions require an error code, succeeded require a hash.
	err = store.InTransaction(ctx, func(tx *Tx) error {
		return tx.CompleteResearchTask(ctx, claimed.ID, "worker-b",
			ResearchTaskCompletion{OutputArtifactID: "art_read_1"}, time.Now().UTC())
	})
	if err == nil {
		t.Fatal("succeeded without output_hash must be rejected")
	}
	err = store.InTransaction(ctx, func(tx *Tx) error {
		return tx.FailResearchTask(ctx, claimed.ID, "worker-b", ResearchTaskFailure{}, time.Now().UTC())
	})
	if err == nil {
		t.Fatal("failure without error code must be rejected")
	}
	// The new owner claims the expired task and completes it.
	var reclaimed ResearchTask
	err = store.InTransaction(ctx, func(tx *Tx) error {
		var ok bool
		reclaimed, ok, err = tx.ClaimResearchTask(ctx, runID, nodeID, "worker-b", time.Minute, time.Now().UTC())
		if err != nil || !ok || reclaimed.ID != claimed.ID {
			t.Fatalf("reclaim ok=%v id=%d err=%v", ok, reclaimed.ID, err)
		}
		return nil
	})
	if reclaimed.Attempt != 2 {
		t.Fatalf("reclaimed attempt=%d want 2", reclaimed.Attempt)
	}
	err = store.InTransaction(ctx, func(tx *Tx) error {
		return tx.CompleteResearchTask(ctx, reclaimed.ID, "worker-b",
			ResearchTaskCompletion{OutputArtifactID: "art_read_1", OutputHash: testHash("read-1"),
				Usage: map[string]any{"input_tokens": 11}}, time.Now().UTC())
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	loaded, err := store.GetResearchTask(ctx, reclaimed.ID)
	if err != nil || loaded.Status != ResearchTaskSucceeded || loaded.OutputHash != testHash("read-1") {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	// Completed tasks are never re-claimed.
	err = store.InTransaction(ctx, func(tx *Tx) error {
		_, ok, err := tx.ClaimResearchTask(ctx, runID, nodeID, "worker-c", time.Minute, time.Now().UTC())
		if err != nil || ok {
			t.Fatalf("completed task must not re-claim: ok=%v err=%v", ok, err)
		}
		return nil
	})

	// outcome_unknown parks the task: no claim, explicit state, never recycled.
	var second ResearchTask
	err = store.InTransaction(ctx, func(tx *Tx) error {
		var err error
		second, err = tx.EnsureResearchTask(ctx, CreateResearchTask{OwnerUserID: owner,
			RunID: runID, NodeID: nodeID, TaskKey: "read:paper-2", Phase: "read",
			InputHash: testHash("task-input-2")}, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.InTransaction(ctx, func(tx *Tx) error {
		task, ok, err := tx.ClaimResearchTask(ctx, runID, nodeID, "worker-d", time.Minute, time.Now().UTC())
		if err != nil || !ok || task.ID != second.ID {
			t.Fatalf("claim second ok=%v err=%v", ok, err)
		}
		return tx.FailResearchTask(ctx, second.ID, "worker-d",
			ResearchTaskFailure{OutcomeUnknown: true, ErrorCode: "WORKER_TIMEOUT",
				Usage: map[string]any{"provider": "x"}}, time.Now().UTC())
	})
	if err != nil {
		t.Fatal(err)
	}
	parked, err := store.GetResearchTask(ctx, second.ID)
	if err != nil || parked.Status != ResearchTaskOutcomeUnknwn {
		t.Fatalf("parked=%#v err=%v", parked, err)
	}
	tasks, err := store.ListResearchTasks(ctx, runID, owner)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("tasks=%d err=%v", len(tasks), err)
	}
	// A different owner sees nothing.
	otherTasks, err := store.ListResearchTasks(ctx, runID, fixture.otherID)
	if err != nil || len(otherTasks) != 0 {
		t.Fatalf("owner scoping broken: %d err=%v", len(otherTasks), err)
	}
}

// TestGatePendingAndDecision is the core six-behavior suite:
// pending arrival, first decision, duplicate decision (same key), stale
// revision, unauthorized actor, and decision durability.
func TestGatePendingAndDecision(t *testing.T) {
	fixture := newT02Fixture(t, "t02gate"+fmt.Sprint(time.Now().UnixNano()))
	ctx := context.Background()
	store, gate := fixture.store, fixture.gate()

	// 1. Pending arrival is first-writer-wins and stable across replays.
	saved, created, err := store.PauseAtHumanGate(ctx, HumanGatePause{Gate: gate, Trace: testTrace()})
	if err != nil || !created || saved.Status != GateStatusPending || saved.Revision != 1 {
		t.Fatalf("arrive created=%v gate=%#v err=%v", created, saved, err)
	}
	replayed, createdAgain, err := store.PauseAtHumanGate(ctx, HumanGatePause{Gate: gate, Trace: testTrace()})
	if err != nil || createdAgain || replayed.GateID != gate.GateID {
		t.Fatalf("replay created=%v gate=%#v err=%v", createdAgain, replayed, err)
	}
	byNode, err := store.GetRunGate(ctx, fixture.runID, fixture.nodeID, 1)
	if err != nil || byNode.GateID != gate.GateID {
		t.Fatalf("byNode=%#v err=%v", byNode, err)
	}
	gates, err := store.ListGatesByRun(ctx, fixture.runID, fixture.userID)
	if err != nil || len(gates) != 1 {
		t.Fatalf("gates=%d err=%v", len(gates), err)
	}
	// Owner-scoped listing hides the gate from other users.
	otherGates, err := store.ListGatesByRun(ctx, fixture.runID, fixture.otherID)
	if err != nil || len(otherGates) != 0 {
		t.Fatalf("gate owner scoping broken: %d", len(otherGates))
	}

	// 2. Decide: the decision transaction completes the gate attempt.
	command := t02Decision(fixture, "first")
	command.Attempt = t02GateAttempt(fixture)
	command.AttemptCompletion = t02GateCompletion(fixture, fixture.nodeID, "art_approval_evidence")
	result, err := store.DecideGate(ctx, command)
	if err != nil || result.Replayed || result.Gate.Status != GateStatusApproved {
		t.Fatalf("decide result=%#v err=%v", result, err)
	}
	if result.Gate.DecisionArtifactID == "" || result.Gate.ActorID != fixture.userID {
		t.Fatalf("decision binding=%#v", result.Gate)
	}

	// 3. Duplicate decide with the same key + body: replay of the same decision.
	replay, err := store.DecideGate(ctx, command)
	if err != nil || !replay.Replayed || replay.Gate.GateID != gate.GateID {
		t.Fatalf("replay result=%#v err=%v", replay, err)
	}
	// A second decision under a different key conflicts as already decided.
	second := t02Decision(fixture, "second")
	second.Attempt = t02GateAttempt(fixture)
	second.AttemptCompletion = t02GateCompletion(fixture, fixture.nodeID, "art_approval_evidence")
	if _, err := store.DecideGate(ctx, second); err == nil {
		t.Fatal("second distinct-key decision must fail")
	}

	// 4. Stale revision: a confirm bound to revision 1 after a revision bump.
	revisionGate := fixture.gate()
	revisionGate.GateID = StableID("gate_", revisionGate.GateID, "outline")
	revisionGate.NodeID = "node_t02_gate_outline"
	revisionGate.GateKind = GateKindOutline
	if _, _, err := store.PauseAtHumanGate(ctx, HumanGatePause{Gate: revisionGate, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	revision, err := store.SaveOutlineRevision(ctx, GateOutlineRevision{RunID: fixture.runID,
		GateID: revisionGate.GateID, PlanID: fixture.planID, PlanVersion: 1,
		PlanHash: testHash("t02-plan"), GateRevision: 1,
		Outline:        ArtifactContentRef{ArtifactID: "art_outline_v2", Version: 1, ContentHash: testHash("outline-v2")},
		ActorID:        fixture.userID,
		IdempotencyKey: fixture.runID + ":gate:" + revisionGate.GateID + ":outline_revision:" + fixture.userID + ":rev-1",
		RequestHash:    testHash("outline-request")})
	if err != nil || revision.Gate.Revision != 2 {
		t.Fatalf("revision saved=%#v err=%v", revision, err)
	}
	stale := t02Decision(fixture, "stale")
	stale.GateID = revisionGate.GateID
	stale.GateRevision = 1
	stale.Input = ArtifactContentRef{ArtifactID: "art_outline_v1", Version: 1, ContentHash: testHash("t02-pack")}
	stale.Attempt = t02GateAttempt(fixture)
	stale.Attempt.NodeID = revisionGate.NodeID
	stale.AttemptCompletion = t02GateCompletion(fixture, revisionGate.NodeID, "art_approval_outline_stale")
	stale.AttemptCompletion.NodeID = revisionGate.NodeID
	_, err = store.DecideGate(ctx, stale)
	if err == nil {
		t.Fatal("stale revision decision must fail")
	}

	// 5. Unauthorized actor: the decision is refused before any mutation.
	unauthorized := t02Decision(fixture, "intruder")
	unauthorized.ActorID = fixture.otherID
	unauthorized.IdempotencyKey = fixture.runID + ":gate:" + gate.GateID + ":decision:" + fixture.otherID + ":intruder"
	unauthorized.Attempt = t02GateAttempt(fixture)
	unauthorized.AttemptCompletion = t02GateCompletion(fixture, fixture.nodeID, "art_approval_intruder")
	if _, err := store.DecideGate(ctx, unauthorized); err == nil {
		t.Fatal("unauthorized decision must fail")
	}

	// 6. Durability: a brand-new store handle (fresh connections) still sees
	// every decision — the committed rows are the recovery source of truth.
	reopened, err := New(integrationDB)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := reopened.GetGate(ctx, fixture.runID, gate.GateID)
	if err != nil || durable.Status != GateStatusApproved || durable.Decision != GateDecisionApprove {
		t.Fatalf("durable=%#v err=%v", durable, err)
	}
	durableNodes, err := reopened.ListGatesByRun(ctx, fixture.runID, fixture.userID)
	if err != nil || len(durableNodes) != 2 {
		t.Fatalf("durable gates=%d err=%v", len(durableNodes), err)
	}
	// The gate attempt ledger is also durable.
	attempts, err := reopened.ListRunAttempts(ctx, fixture.runID)
	if err != nil {
		t.Fatal(err)
	}
	succeeded := 0
	for _, attempt := range attempts {
		if attempt.NodeID == fixture.nodeID && attempt.Status == "succeeded" {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("gate attempt completions=%d want 1", succeeded)
	}
	// The scan finds the paused-with-approved-decision runs for recovery.
	// Approve the outline gate first (current revision 2, revised input) so
	// the run has no pending gate left.
	outlineDecision := t02Decision(fixture, "outline-final")
	outlineDecision.GateID = revisionGate.GateID
	outlineDecision.GateRevision = 2
	outlineDecision.Input = ArtifactContentRef{ArtifactID: "art_outline_v2", Version: 1, ContentHash: testHash("outline-v2")}
	outlineDecision.Attempt = t02GateAttempt(fixture)
	outlineDecision.Attempt.NodeID = revisionGate.NodeID
	outlineDecision.Attempt.InputHash = testHash("outline-v2")
	outlineDecision.AttemptCompletion = t02GateCompletion(fixture, revisionGate.NodeID, "art_approval_outline")
	outlineDecision.AttemptCompletion.NodeID = revisionGate.NodeID
	for index := range outlineDecision.AttemptCompletion.Artifacts {
		outlineDecision.AttemptCompletion.Artifacts[index].NodeID = revisionGate.NodeID
	}
	if _, err := store.DecideGate(ctx, outlineDecision); err != nil {
		t.Fatalf("outline decision: %v", err)
	}
	if err := store.InTransaction(ctx, func(tx *Tx) error {
		_, err := tx.RecordRunTransition(ctx, RunTransitionCommand{RunID: fixture.runID,
			IdempotencyKey: fixture.runID + ":transition:" + StableID("cmd_", fixture.runID, "t02-scan"),
			ExpectedFrom:   "running",
			RequestedTo:    "paused", RuleAccepted: true, Cause: "human_gate",
			ReasonCode: "human_gate", Summary: "T02 fixture pause",
			Trace: testTrace()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	resumable, err := reopened.GateResumableRunIDs(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range resumable {
		if id == fixture.runID {
			found = true
		}
	}
	if !found {
		t.Fatalf("approved-decision run not scannable: %v", resumable)
	}
}

func t02GateAttempt(fixture t02Fixture) NodeAttempt {
	return NodeAttempt{RunID: fixture.runID, PlanID: fixture.planID, PlanVersion: 1,
		NodeID: fixture.nodeID, Attempt: 1, NodeKind: writingplan.NodeHumanGate,
		CapabilityID: "core.research.gate.evidence", CapabilityVersion: "1.0.0",
		ExecutorID: "kernel.human_gate", FailurePath: writingplan.FailurePause,
		Bounds:    writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 0, TimeoutMS: 1000},
		InputHash: testHash("t02-pack"), InputArtifactIDs: []string{}}
}

func t02GateCompletion(fixture t02Fixture, nodeID, artifactID string) AttemptCompletion {
	return AttemptCompletion{Status: "succeeded",
		Artifacts: []ArtifactRecord{{ArtifactID: artifactID, Version: 1, RunID: fixture.runID,
			PlanID: fixture.planID, PlanVersion: 1, NodeID: nodeID, Attempt: 1,
			OutputKey: "evidence_approval", ArtifactType: "evidence_approval", Status: "validated",
			ContentHash: testHash(artifactID), MediaType: "application/json",
			ContentRef: "db://writing_artifacts/" + artifactID + "/1", Parents: []ArtifactRef{},
			Producer: "kernel.human_gate", CapabilityVersion: "1.0.0",
			InputHashes: []string{testHash("t02-pack")}, Trace: testTrace()}},
		Trace: testTrace()}
}

// TestOutlineRevisionIdempotencyAndStale pins the outline-revision contract:
// same key + body replays, a changed body under the same key conflicts, and a
// revision submitted against a stale gate_revision fails.
func TestOutlineRevisionIdempotencyAndStale(t *testing.T) {
	fixture := newT02Fixture(t, "t02outline"+fmt.Sprint(time.Now().UnixNano()))
	ctx := context.Background()
	store, gate := fixture.store, fixture.gate()
	gate.GateKind = GateKindOutline
	if _, _, err := store.PauseAtHumanGate(ctx, HumanGatePause{Gate: gate, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	build := func(hash, key string) GateOutlineRevision {
		return GateOutlineRevision{RunID: fixture.runID, GateID: gate.GateID,
			PlanID: fixture.planID, PlanVersion: 1, PlanHash: testHash("t02-plan"),
			GateRevision: 1, Outline: ArtifactContentRef{ArtifactID: "art_outline", Version: 1, ContentHash: hash},
			ActorID:        fixture.userID,
			IdempotencyKey: fixture.runID + ":gate:" + gate.GateID + ":outline_revision:" + fixture.userID + ":" + key,
			RequestHash:    hash}
	}
	first := build(testHash("outline-a"), "rev-a")
	saved, err := store.SaveOutlineRevision(ctx, first)
	if err != nil || saved.Gate.Revision != 2 || saved.Gate.InputHash != testHash("outline-a") {
		t.Fatalf("saved=%#v err=%v", saved, err)
	}
	replay, err := store.SaveOutlineRevision(ctx, first)
	if err != nil || !replay.Replayed {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	conflict := first
	conflict.Outline.ContentHash = testHash("outline-a2")
	conflict.RequestHash = testHash("outline-a2")
	if _, err := store.SaveOutlineRevision(ctx, conflict); err == nil {
		t.Fatal("same key with different body must conflict")
	}
	stale := build(testHash("outline-b"), "rev-b")
	stale.GateRevision = 1 // gate is now at revision 2
	if _, err := store.SaveOutlineRevision(ctx, stale); err == nil {
		t.Fatal("stale gate_revision must fail")
	}
	fresh := build(testHash("outline-b"), "rev-b")
	fresh.GateRevision = 2
	savedAgain, err := store.SaveOutlineRevision(ctx, fresh)
	if err != nil || savedAgain.Gate.Revision != 3 || savedAgain.Gate.InputHash != testHash("outline-b") {
		t.Fatalf("second revision=%#v err=%v", savedAgain, err)
	}
	// Evidence gates never accept outline revisions.
	evidenceGate := fixture.gate()
	evidenceGate.GateID = StableID("gate_", evidenceGate.GateID, "evidence")
	evidenceGate.NodeID = "node_t02_gate_evidence"
	if _, _, err := store.PauseAtHumanGate(ctx, HumanGatePause{Gate: evidenceGate, Trace: testTrace()}); err != nil {
		t.Fatal(err)
	}
	mismatch := fresh
	mismatch.GateID = evidenceGate.GateID
	mismatch.GateRevision = 1
	if _, err := store.SaveOutlineRevision(ctx, mismatch); err == nil {
		t.Fatal("evidence gate must reject outline revisions")
	}
}
