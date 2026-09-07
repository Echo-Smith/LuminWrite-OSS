package writingruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// T02 runtime tests (docs/plans/2026-09-07-research-review-integration.md
// §T02): gate arrival atomicity, decision → resume completing exactly once,
// decision durability across restarts, plain resume blocked on pending gates,
// and the budget-boundary clean pause. All store-backed.

const (
	t02ActionCapability = "core.t02.research_read"
	t02GateCapability   = "core.research.gate.evidence"
	t02RunUser          = "00000000-0000-0000-0000-0000000002r1"
)

// t02ExecResult echoes the node's declared outputs as JSON artifacts.
type t02ExecResult struct{}

func (t02ExecResult) Descriptor() ExecutorDescriptor {
	return ExecutorDescriptor{ExecutorID: "engine.step.t02", Version: "1",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}, Cancellable: false}
}

func (t02ExecResult) Cancel(context.Context, ExecutionHandle) error { return nil }

func (t02ExecResult) Execute(_ context.Context, request ExecutionRequest) (ExecutionResult, error) {
	now := time.Now().UTC()
	parents := make([]writingstore.ArtifactRef, 0, len(request.Inputs))
	inputHashes := make([]string, 0, len(request.Inputs))
	for _, input := range request.Inputs {
		parents = append(parents, writingstore.ArtifactRef{ArtifactID: input.ArtifactID, Version: input.Version})
		inputHashes = append(inputHashes, input.ContentHash)
	}
	artifacts := make([]OutputArtifactDraft, 0, len(request.Node.OutputArtifactTypes))
	for index, artifactType := range request.Node.OutputArtifactTypes {
		artifacts = append(artifacts, OutputArtifactDraft{OutputKey: fmt.Sprintf("out_%d", index),
			ArtifactType: artifactType, ContentHash: hashForTest(string(artifactType) + request.Node.NodeID),
			MediaType: "application/json", ContentRef: "memory://t02/" + request.Node.NodeID,
			Parents: parents, Producer: request.Node.Capability,
			CapabilityVersion: request.Node.CapabilityVersion, InputHashes: inputHashes,
			Provenance: map[string]any{"t02": true}, SourceRefs: []string{}})
	}
	return ExecutionResult{StartedAt: now.Add(-time.Millisecond), CompletedAt: now,
		Usage: ExecutionUsage{CostUSD: 0.1, DurationMS: 10}, Artifacts: artifacts}, nil
}

// t02Fixture wires a real store, a two/three-node research plan, and an
// orchestrator with the persistent checkpoint repository.
type t02Fixture struct {
	store        *writingstore.Store
	orchestrator *Orchestrator
	checkpoints  *PersistentCheckpointRepository
	runID        string
	userID       string
	plan         writingplan.ExecutablePlan
	planVersion  int
}

func newT02Fixture(t *testing.T, nodes []writingplan.PlanNode) *t02Fixture {
	t.Helper()
	db, cleanup, err := dbtest.Open(os.Getenv("TEST_DATABASE_URL"), 6, 2)
	if err != nil {
		if errors.Is(err, dbtest.ErrNoDatabaseURL) {
			t.Skip("TEST_DATABASE_URL not set")
		}
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	ctx := context.Background()
	store, err := writingstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := db.QueryRowContext(ctx, `INSERT INTO users (uid, name) VALUES ($1,'t02 runtime') RETURNING id::text`,
		fmt.Sprintf("t02rt_%d", time.Now().UnixNano())).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "lcp", "v1", "fixtures", "writing-contract.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := writingkernel.DecodeWritingContractStrict(payload)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	documentID := writingstore.StableID("doc_", "t02rt", fmt.Sprint(time.Now().UnixNano()))
	if err := store.CreateDocument(ctx, writingstore.DocumentRecord{DocumentID: documentID,
		OwnerUserID: userID, Title: "T02 runtime", Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutContract(ctx, writingstore.ContractRecord{DocumentID: documentID, Contract: sealed,
		Trace: writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
			Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}}); err != nil {
		t.Fatal(err)
	}
	version := deliveryBaseVersion(t, documentID, writingstore.StableID("ver_", documentID, "t02"), "T02 base")
	if _, err := store.CommitDocumentVersion(ctx, writingstore.CommitDocumentVersionParams{
		Version: version, ContractID: sealed.ContractID, ContractVersion: sealed.Version,
		Trace: writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
			Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}}); err != nil {
		t.Fatal(err)
	}
	intentPlan := writingplan.IntentPlan{IntentPlanID: writingstore.StableID("iplan_", documentID, "t02"),
		ContractRef: writingplan.ObjectRef{ID: sealed.ContractID, Version: sealed.Version, Hash: sealed.ContractHash},
		Summary:     "T02 runtime fixture", CreatedBy: writingplan.ActorUser, CreatedAt: time.Now().UTC(),
		ProposedSteps: func() []writingplan.ProposedStep {
			steps := make([]writingplan.ProposedStep, 0, len(nodes))
			for _, node := range nodes {
				steps = append(steps, writingplan.ProposedStep{StepID: node.NodeID,
					Objective: "T02 fixture step", CapabilityHint: node.Capability, DependsOn: node.DependsOn})
			}
			return steps
		}()}
	intentPlan, err = intentPlan.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	plan := writingplan.ExecutablePlan{PlanID: writingstore.StableID("plan_", documentID, "t02"),
		IntentPlanRef: writingplan.ObjectRef{ID: intentPlan.IntentPlanID, Version: 1, Hash: intentPlan.IntentPlanHash},
		Status:        writingplan.PlanValidated, TrustLevel: writingplan.TrustT1, RootNodeID: nodes[0].NodeID, Nodes: nodes,
		StaticValidation: writingplan.StaticValidation{Valid: true, CheckedAt: time.Now().UTC(),
			Errors: []string{}, CapabilityRegistryVersion: "t02", BudgetValid: true,
			PermissionsValid: true, ArtifactFlowValid: true, FailurePathsValid: true}}
	plan, err = plan.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	budget := writingplan.PlanBudget{MaxCostUSD: 100, MaxDurationMS: 3000000, MaxConcurrency: 1, MaxNodes: 10, MaxItems: 4}
	runID := writingstore.StableID("run_", userID, "t02rt", fmt.Sprint(time.Now().UnixNano()))
	trace := writingstore.TraceContext{Provenance: map[string]any{"t02": true}, SourceRefs: []string{},
		Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: userID}}
	run := writingstore.RunRecord{RunID: runID, DocumentID: documentID, ContractID: sealed.ContractID,
		ContractVersion: sealed.Version, ContractHash: sealed.ContractHash, BaseVersionID: version.VersionID,
		Status: "planned", ApprovalMode: sealed.Collaboration.ApprovalMode,
		RequestedAssurance: sealed.Collaboration.AssuranceLevel, Budget: budget,
		Permissions: []writingplan.Permission{"model.invoke", "materials.read"}, Trace: trace}
	planRecord := writingstore.PlanRecord{RunID: runID, PlanVersion: 1, Budget: budget, Permissions: run.Permissions, Trace: trace,
		Envelope: writingplan.WritingPlanEnvelope{SchemaVersion: writingplan.SchemaVersion,
			IntentPlan:     intentPlan,
			ExecutablePlan: plan,
			StrategyDecision: writingplan.StrategyDecision{DecisionID: writingstore.StableID("decision_", plan.PlanHash),
				IntentPlanRef: plan.IntentPlanRef, SelectedPlanHash: plan.PlanHash,
				SelectionSource: writingplan.SelectionUser, UserOverride: true,
				RequestedOrchestration: writingkernel.OrchestrationModeFast, EffectiveOrchestration: writingkernel.OrchestrationModeFast,
				Candidates: []writingplan.StrategyCandidate{{PlanHash: plan.PlanHash, TrustLevel: plan.TrustLevel,
					EstimatedCostUSD: .2, EstimatedDurationMS: 20, EstimatedConfidence: .9}},
				Confidence: .9, ReasonCode: "selected_fast", Summary: "T02 fixture decision",
				DegradationConditions: []string{}, CreatedAt: time.Now().UTC(), ApprovalRequired: false}}}
	if err := store.CreateRunWithPlan(ctx, run, planRecord, "planned"); err != nil {
		t.Fatal(err)
	}

	capabilities := writingplan.NewCapabilityRegistry("t02")
	actionManifest := writingplan.CapabilityManifest{ID: t02ActionCapability, Class: "research.read",
		Executor: "engine.step.t02", InputTypes: []writingplan.ArtifactType{"contract"},
		OptionalInputTypes: []writingplan.ArtifactType{}, OutputTypes: []writingplan.ArtifactType{"source_pack"},
		Permissions:      []writingplan.Permission{"model.invoke", "materials.read"},
		EstimatedCostUSD: .1, EstimatedDurationMS: 10, Version: "1.0.0",
		SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction},
		MaxBounds:          writingplan.Bounds{MaxAttempts: 2, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 2, TimeoutMS: 120000},
		Idempotency:        writingplan.IdempotencySafe, Available: true,
		Context: writingplan.ContextContract{RequiredContext: []writingplan.ContextBlockName{},
			OptionalContext: []writingplan.ContextBlockName{}}}
	if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: "engine.step.t02",
		AcceptedInputTypes:  []writingplan.ArtifactType{"contract", "source_pack"},
		ProducedOutputTypes: []writingplan.ArtifactType{"source_pack"},
		Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
			return writingplan.ExecutionResult{}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	if err := capabilities.Register(actionManifest); err != nil {
		t.Fatal(err)
	}
	executors := NewExecutorRegistry()
	if err := executors.Register(t02ExecResult{}); err != nil {
		t.Fatal(err)
	}
	checkpoints := &PersistentCheckpointRepository{Store: store,
		Trace: writingstore.TraceContext{Provenance: map[string]any{}, SourceRefs: []string{},
			Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime"}}}
	orchestrator := &Orchestrator{Store: store, Capabilities: capabilities, Executors: executors,
		State:       NewStateMachine(WritingStoreTransitionRecorder{Store: store}),
		Checkpoints: checkpoints,
		Initial: fixedInitialProvider{{ArtifactID: writingstore.StableID("art_", runID, "contract"), Version: 1,
			ArtifactType: "contract", ContentHash: sealed.ContractHash, MediaType: "application/json",
			ContentRef: "memory://contract"}},
		Materials: store, Now: func() time.Time { return time.Now().UTC() }}
	return &t02Fixture{store: store, orchestrator: orchestrator, checkpoints: checkpoints,
		runID: runID, userID: userID, plan: plan, planVersion: 1}
}

func t02GateNode() writingplan.PlanNode {
	return writingplan.PlanNode{NodeID: "node_t02_gate", Kind: writingplan.NodeHumanGate,
		Capability: t02GateCapability, CapabilityVersion: "1.0.0", DependsOn: []string{"node_t02_read1"},
		InputArtifactTypes:  []writingplan.ArtifactType{"source_pack"},
		OutputArtifactTypes: []writingplan.ArtifactType{"evidence_approval"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 0, TimeoutMS: 120000},
		FailurePath:         writingplan.FailurePause}
}

func t02ReadNode(nodeID string, deps ...string) writingplan.PlanNode {
	return writingplan.PlanNode{NodeID: nodeID, Kind: writingplan.NodeAction,
		Capability: t02ActionCapability, CapabilityVersion: "1.0.0", DependsOn: deps,
		InputArtifactTypes:  []writingplan.ArtifactType{"contract"},
		OutputArtifactTypes: []writingplan.ArtifactType{"source_pack"},
		Bounds:              writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 2, TimeoutMS: 120000},
		FailurePath:         writingplan.FailureFail}
}

// t02DecisionCommand assembles the decision exactly as the API layer will:
// succeeded gate attempt + approval artifact + cleared checkpoint, all inside
// the store's decision transaction.
func (fixture *t02Fixture) decisionCommand(t *testing.T, node writingplan.PlanNode, input writingstore.ArtifactContentRef, actorID, idempotencyKey string) writingstore.GateDecisionCommand {
	t.Helper()
	run, err := fixture.store.LoadRuntimeRun(context.Background(), fixture.runID)
	if err != nil {
		t.Fatal(err)
	}
	approval := writingstore.ArtifactRecord{ArtifactID: writingstore.StableID("art_", fixture.runID, node.NodeID, "approval"),
		Version: 1, RunID: fixture.runID, PlanID: run.ActivePlanID, PlanVersion: run.ActivePlanVersion,
		NodeID: node.NodeID, Attempt: 1, OutputKey: "evidence_approval", ArtifactType: "evidence_approval",
		Status: "validated", ContentHash: hashForTest("approval-" + node.NodeID), MediaType: "application/json",
		ContentRef: "memory://approval", Parents: []writingstore.ArtifactRef{}, Producer: "kernel.human_gate",
		CapabilityVersion: node.CapabilityVersion, InputHashes: []string{input.ContentHash},
		Trace: DecisionTrace(actorID)}
	return writingstore.GateDecisionCommand{RunID: fixture.runID,
		GateID: writingstore.StableID("gate_", fixture.runID, fixture.plan.PlanID, fmt.Sprint(fixture.planVersion), node.NodeID),
		PlanID: fixture.plan.PlanID, PlanVersion: fixture.planVersion, PlanHash: fixture.plan.PlanHash,
		GateRevision: 1, Input: input, Decision: writingstore.GateDecisionApprove, ActorID: actorID,
		IdempotencyKey: fixture.runID + ":gate:decision:" + actorID + ":" + idempotencyKey,
		RequestHash:    hashForTest("request"),
		Attempt:        gateAttemptFor(run, fixture.planVersion, node),
		AttemptCompletion: writingstore.AttemptCompletion{Artifacts: []writingstore.ArtifactRecord{approval},
			CompletedAt: time.Now().UTC(), Trace: DecisionTrace(actorID)},
		Trace: DecisionTrace(actorID)}
}

// arrival reconstructs the GateArrival from the persisted gate row (the
// same way the API layer will rebuild the decision context after restart).
func (fixture *t02Fixture) arrival(t *testing.T) GateArrival {
	t.Helper()
	run, err := fixture.store.LoadRuntimeRun(context.Background(), fixture.runID)
	if err != nil {
		t.Fatal(err)
	}
	node := fixture.plan.Nodes[len(fixture.plan.Nodes)-1]
	gate, err := fixture.store.GetRunGate(context.Background(), fixture.runID, node.NodeID, fixture.planVersion)
	if err != nil {
		t.Fatalf("load gate: %v", err)
	}
	return GateArrival{Run: run, Plan: fixture.plan, Node: node, PlanVersion: fixture.planVersion,
		GateKind:  gate.GateKind,
		Input:     writingstore.ArtifactContentRef{ArtifactID: gate.InputArtifactID, Version: gate.InputArtifactVersion, ContentHash: gate.InputHash},
		Completed: map[string]int{"node_t02_read1": 1}, Artifacts: []InputArtifact{},
		SpentCostUSD: .1, SpentDurationMS: 10}
}

// TestOrchestratorArrivesAtGateAtomically: one transaction produces the
// pending gate row, the waiting-gate checkpoint (no UnsafeInFlight), the
// paused run, and the gate.pending event.
func TestOrchestratorArrivesAtGateAtomically(t *testing.T) {
	fixture := newT02Fixture(t, []writingplan.PlanNode{t02ReadNode("node_t02_read1"), t02GateNode()})
	out, err := fixture.orchestrator.Execute(context.Background(), fixture.runID)
	if !errors.Is(err, ErrApprovalRequired) || out.State != StatePaused {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	ctx := context.Background()
	gate, err := fixture.store.GetRunGate(ctx, fixture.runID, "node_t02_gate", 1)
	if err != nil || gate.Status != writingstore.GateStatusPending || gate.Revision != 1 {
		t.Fatalf("gate=%#v err=%v", gate, err)
	}
	if gate.InputHash != hashForTest("source_packnode_t02_read1") {
		t.Fatalf("gate input binding lost: %#v", gate)
	}
	// The checkpoint carries the waiting marker and no unsafe node.
	snapshot, err := fixture.store.LoadLatestSnapshot(ctx, fixture.runID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := decodeCheckpointManifest(snapshot.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.WaitingGateID != "node_t02_gate" {
		t.Fatalf("checkpoint waiting_gate_id=%q", checkpoint.WaitingGateID)
	}
	if len(checkpoint.UnsafeInFlight) != 0 {
		t.Fatalf("gate pause must not mark UnsafeInFlight: %#v", checkpoint.UnsafeInFlight)
	}
	events, err := fixture.store.ListRunEvents(ctx, fixture.runID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	for _, event := range events {
		if event.EventType == "gate.pending" {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("gate.pending events=%d want 1", pending)
	}
	// Plain resume cannot bypass the pending gate.
	if _, err := fixture.orchestrator.Resume(ctx, fixture.runID, "resume_blocked", writingstore.Actor{Type: writingstore.ActorUser, ID: fixture.userID}); !errors.Is(err, ErrGateApprovalRequired) {
		t.Fatalf("plain resume err=%v want ErrGateApprovalRequired", err)
	}
	run, err := fixture.store.LoadRuntimeRun(ctx, fixture.runID)
	if err != nil || run.Status != string(StatePaused) {
		t.Fatalf("run=%#v err=%v — blocked resume must leave the run paused", run, err)
	}
}

// TestGateDecisionResumesExactlyOnce: the decision transaction completes the
// gate node; the resume finishes the plan; a second resume is refused; the
// gate node never completes twice.
func TestGateDecisionResumesExactlyOnce(t *testing.T) {
	fixture := newT02Fixture(t, []writingplan.PlanNode{t02ReadNode("node_t02_read1"), t02GateNode()})
	ctx := context.Background()
	if _, err := fixture.orchestrator.Execute(ctx, fixture.runID); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("arrival err=%v", err)
	}
	node := fixture.plan.Nodes[len(fixture.plan.Nodes)-1]
	arrival := fixture.arrival(t)
	command := fixture.decisionCommand(t, node, arrival.Input, fixture.userID, "decide-1")
	command.AttemptCompletion.CompletedAt = time.Now().UTC()
	command.AfterDecision = func(tx *writingstore.Tx) error {
		checkpoint := fixture.orchestrator.DecidedGateCheckpoint(arrival, time.Now().UTC())
		return fixture.checkpoints.CommitWithin(ctx, tx, checkpoint)
	}
	result, err := fixture.store.DecideGate(ctx, command)
	if err != nil || result.Replayed {
		t.Fatalf("decide result=%#v err=%v", result, err)
	}
	// Decision committed but the run is still paused until a resume trigger.
	run, err := fixture.store.LoadRuntimeRun(ctx, fixture.runID)
	if err != nil || run.Status != string(StatePaused) {
		t.Fatalf("run after decide=%#v err=%v", run, err)
	}
	// A fresh orchestrator (simulating a restart) consumes the decision.
	restarted := &Orchestrator{Store: fixture.store, Capabilities: fixture.orchestrator.Capabilities,
		Executors: fixture.orchestrator.Executors, State: fixture.orchestrator.State,
		Checkpoints: fixture.checkpoints, Initial: fixture.orchestrator.Initial, Materials: fixture.orchestrator.Materials,
		Now: fixture.orchestrator.Now}
	out, err := restarted.Resume(ctx, fixture.runID, "resume_after_decide", writingstore.Actor{Type: writingstore.ActorUser, ID: fixture.userID})
	if err != nil || out.State != StateCompleted {
		t.Fatalf("resume out=%#v err=%v", out, err)
	}
	if _, err := restarted.Resume(ctx, fixture.runID, "resume_again", writingstore.Actor{Type: writingstore.ActorUser, ID: fixture.userID}); err == nil {
		t.Fatal("second resume must fail — the run is completed")
	}
	attempts, err := fixture.store.ListRunAttempts(ctx, fixture.runID)
	if err != nil {
		t.Fatal(err)
	}
	gateCompletions := 0
	for _, attempt := range attempts {
		if attempt.NodeID == "node_t02_gate" && attempt.Status == "succeeded" {
			gateCompletions++
		}
	}
	if gateCompletions != 1 {
		t.Fatalf("gate node completed %d times, want exactly 1", gateCompletions)
	}
	events, err := fixture.store.ListRunEvents(ctx, fixture.runID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	decided, completed := 0, 0
	for _, event := range events {
		if event.EventType == "gate.decided" {
			decided++
		}
		if event.EventType == "node.completed" && event.NodeID == "node_t02_gate" {
			completed++
		}
	}
	if decided != 1 || completed != 1 {
		t.Fatalf("gate.decided=%d node.completed(gate)=%d", decided, completed)
	}
	// The waiting marker is gone from the latest checkpoint; the gate node's
	// completion is recovered from the attempt ledger on resume (it lands in
	// the final checkpoint at run completion).
	snapshot, err := fixture.store.LoadLatestSnapshot(ctx, fixture.runID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := decodeCheckpointManifest(snapshot.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.WaitingGateID != "" {
		t.Fatalf("decided checkpoint still waits: %#v", checkpoint)
	}
}

// TestBudgetBoundaryPausesCleanly: the guard pauses the run before a research
// read class node; the pause is resumable and the gate still decisions
// normally afterwards.
func TestBudgetBoundaryPausesCleanly(t *testing.T) {
	fixture := newT02Fixture(t, []writingplan.PlanNode{t02ReadNode("node_t02_read1"),
		t02ReadNode("node_t02_read2", "node_t02_read1"), t02GateNode()})
	ctx := context.Background()
	fixture.orchestrator.BudgetBoundary = boundaryGuardFunc(func(node writingplan.PlanNode) bool {
		return node.NodeID == "node_t02_read2"
	})
	out, err := fixture.orchestrator.Execute(ctx, fixture.runID)
	if !errors.Is(err, ErrRunPaused) || out.State != StatePaused {
		t.Fatalf("budget pause out=%#v err=%v", out, err)
	}
	// The pause is clean: no gate row, no waiting marker.
	if _, err := fixture.store.GetRunGate(ctx, fixture.runID, "node_t02_gate", 1); !errors.Is(err, writingstore.ErrNotFound) {
		t.Fatalf("budget pause must not create a gate row: err=%v", err)
	}
	snapshot, err := fixture.store.LoadLatestSnapshot(ctx, fixture.runID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := decodeCheckpointManifest(snapshot.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.WaitingGateID != "" || len(checkpoint.UnsafeInFlight) != 0 {
		t.Fatalf("budget checkpoint=%#v", checkpoint)
	}
	// Resume with the boundary cleared: the read completes and the gate arrives.
	fixture.orchestrator.BudgetBoundary = nil
	if _, err := fixture.orchestrator.Resume(ctx, fixture.runID, "resume_budget", writingstore.Actor{Type: writingstore.ActorUser, ID: fixture.userID}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("resume to gate err=%v", err)
	}
	gate, err := fixture.store.GetRunGate(ctx, fixture.runID, "node_t02_gate", 1)
	if err != nil || gate.Status != writingstore.GateStatusPending {
		t.Fatalf("gate=%#v err=%v", gate, err)
	}
	node := fixture.plan.Nodes[len(fixture.plan.Nodes)-1]
	arrival := fixture.arrival(t)
	command := fixture.decisionCommand(t, node, arrival.Input, fixture.userID, "decide-budget")
	command.AfterDecision = func(tx *writingstore.Tx) error {
		return fixture.checkpoints.CommitWithin(ctx, tx, fixture.orchestrator.DecidedGateCheckpoint(arrival, time.Now().UTC()))
	}
	if _, err := fixture.store.DecideGate(ctx, command); err != nil {
		t.Fatalf("decide: %v", err)
	}
	final, err := fixture.orchestrator.Resume(ctx, fixture.runID, "resume_final", writingstore.Actor{Type: writingstore.ActorUser, ID: fixture.userID})
	if err != nil || final.State != StateCompleted {
		t.Fatalf("final resume out=%#v err=%v", final, err)
	}
}

type boundaryGuardFunc func(node writingplan.PlanNode) bool

func (fn boundaryGuardFunc) BudgetBoundaryReached(_ context.Context, _ writingstore.RuntimeRun, _ writingplan.ExecutablePlan, node writingplan.PlanNode) (bool, string) {
	if fn(node) {
		return true, "research_budget_boundary"
	}
	return false, ""
}
