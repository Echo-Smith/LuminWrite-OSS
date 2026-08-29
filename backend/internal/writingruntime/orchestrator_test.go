package writingruntime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func TestOrchestratorCompletesReadyNodeAndPersistsCheckpoint(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	out, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != StateCompleted || len(out.CompletedNodes) != 1 || fixture.executor.calls != 1 {
		t.Fatalf("outcome=%#v calls=%d", out, fixture.executor.calls)
	}
	if len(fixture.store.artifacts) != 1 || len(fixture.checkpoints.saved) != 1 {
		t.Fatalf("artifacts=%#v checkpoints=%#v", fixture.store.artifacts, fixture.checkpoints.saved)
	}
}

func TestOrchestratorDoesNotDispatchBeforeRequiredApproval(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, true)
	fixture.store.plan.ApprovalStatus = "pending"
	out, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID)
	if !errors.Is(err, ErrApprovalRequired) || out.State != StateAwaitingApproval || fixture.executor.calls != 0 {
		t.Fatalf("out=%#v err=%v calls=%d", out, err, fixture.executor.calls)
	}
}

func TestOrchestratorRetriesOnlySafeExecutor(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	fixture.executor.failures = 1
	fixture.store.plan.Envelope.ExecutablePlan.Nodes[0].Bounds.MaxAttempts = 2
	fixture.store.plan.Envelope.ExecutablePlan, _ = fixture.store.plan.Envelope.ExecutablePlan.WithComputedHash()
	out, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID)
	if err != nil || out.State != StateCompleted || fixture.executor.calls != 2 {
		t.Fatalf("out=%#v err=%v calls=%d", out, err, fixture.executor.calls)
	}
	if len(fixture.store.attempts) != 2 {
		t.Fatalf("attempts=%#v", fixture.store.attempts)
	}
}

func TestOrchestratorPausesUnsafeAmbiguousRetryForHuman(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencyRequired, false)
	fixture.executor.failures = 1
	fixture.store.plan.Envelope.ExecutablePlan.Nodes[0].Bounds.MaxAttempts = 2
	fixture.store.plan.Envelope.ExecutablePlan, _ = fixture.store.plan.Envelope.ExecutablePlan.WithComputedHash()
	out, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID)
	if !errors.Is(err, ErrHumanRecoveryRequired) || out.State != StatePaused || len(out.HumanRequired) != 1 || fixture.executor.calls != 1 {
		t.Fatalf("out=%#v err=%v calls=%d", out, err, fixture.executor.calls)
	}
}

func TestOrchestratorPauseCancelsActiveExecutorAndCheckpoints(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	fixture.executor.block = make(chan struct{})
	fixture.executor.started = make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID)
		result <- err
	}()
	select {
	case <-fixture.executor.started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	if err := fixture.orchestrator.Pause(context.Background(), fixture.store.run.RunID, "pause_test", writingstore.Actor{Type: writingstore.ActorUser, ID: "user_test"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrRunPaused) {
			t.Fatalf("execute error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("paused execution did not return")
	}
	if fixture.executor.cancelCalls != 1 || fixture.store.run.Status != string(StatePaused) || len(fixture.checkpoints.saved) == 0 {
		t.Fatalf("cancelCalls=%d state=%s checkpoints=%d", fixture.executor.cancelCalls, fixture.store.run.Status, len(fixture.checkpoints.saved))
	}
}

func TestCompositeInitialProviderSuppliesContractAndGovernedMaterials(t *testing.T) {
	contract := InputArtifact{ArtifactID: "art_contract", Version: 1, ArtifactType: "contract", ContentHash: hashForTest("contract"), MediaType: "application/json", ContentRef: "memory://contract"}
	materials := InputArtifact{ArtifactID: "art_materials", Version: 1, ArtifactType: "materials", ContentHash: hashForTest("materials"), MediaType: "application/json", ContentRef: "memory://materials"}
	provider, err := NewCompositeInitialArtifactProvider(fixedInitialProvider{contract}, fixedInitialProvider{materials})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := provider.InitialArtifacts(context.Background(), writingstore.RuntimeRun{RunID: "run_runtime"}, writingstore.PlanRecord{})
	if err != nil || len(artifacts) != 2 || artifacts[0].ArtifactType != "contract" || artifacts[1].ArtifactType != "materials" {
		t.Fatalf("artifacts=%#v error=%v", artifacts, err)
	}
}

func TestOrchestratorPersistsStableExecutorErrorCode(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	fixture.executor.failures = 1
	fixture.executor.failErr = runtimeError(CodeSourceSnapshotFailed, RetrySafe, "snapshot unavailable", errors.New("storage down"))
	_, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID)
	if ErrorCodeOf(err) != CodeSourceSnapshotFailed {
		t.Fatalf("error=%v code=%s", err, ErrorCodeOf(err))
	}
	if len(fixture.store.completions) == 0 || fixture.store.completions[0].ErrorCode != string(CodeSourceSnapshotFailed) || len(fixture.store.artifacts) != 0 {
		t.Fatalf("completions=%#v artifacts=%#v", fixture.store.completions, fixture.store.artifacts)
	}
}

func TestOrchestratorEmitsCanonicalCommitBoundaryMetrics(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	metrics := &metricCapture{}
	fixture.orchestrator.Telemetry = metrics
	if _, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID); err != nil {
		t.Fatal(err)
	}
	if !metrics.has(MetricCanonicalCommit, "started") || !metrics.has(MetricCanonicalCommit, "succeeded") {
		t.Fatalf("metrics=%#v", metrics.metrics)
	}
}

func TestCanonicalCommitFailureIsStableAndProducesNoArtifact(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	fixture.store.completionErr = errors.New("database unavailable")
	metrics := &metricCapture{}
	fixture.orchestrator.Telemetry = metrics
	_, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID)
	if ErrorCodeOf(err) != CodeArtifactCommitFailed || len(fixture.store.artifacts) != 0 {
		t.Fatalf("error=%v code=%s artifacts=%#v", err, ErrorCodeOf(err), fixture.store.artifacts)
	}
	if !metrics.has(MetricCanonicalCommit, "failed") {
		t.Fatalf("metrics=%#v", metrics.metrics)
	}
}

type orchestratorFixture struct {
	orchestrator *Orchestrator
	store        *fakeRuntimeStore
	executor     *fakeGovernedExecutor
	checkpoints  *memoryCheckpoints
}

func newOrchestratorFixture(t *testing.T, idempotency writingplan.IdempotencyClass, approval bool) orchestratorFixture {
	t.Helper()
	now := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	contractHash := hashForTest("contract")
	plan := writingplan.ExecutablePlan{PlanID: "plan_runtime", Status: writingplan.PlanValidated,
		TrustLevel: writingplan.TrustT1, RootNodeID: "node_draft", Nodes: []writingplan.PlanNode{{
			NodeID: "node_draft", Kind: writingplan.NodeAction, Capability: "core.draft.generate", CapabilityVersion: "1.0.0",
			DependsOn: []string{}, InputArtifactTypes: []writingplan.ArtifactType{"contract"}, OutputArtifactTypes: []writingplan.ArtifactType{"full_draft"},
			Bounds: writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 2, TimeoutMS: 5000}, FailurePath: writingplan.FailureFail,
		}}, StaticValidation: writingplan.StaticValidation{Valid: true, CheckedAt: now,
			Errors: []string{}, CapabilityRegistryVersion: "runtime-test", BudgetValid: true,
			PermissionsValid: true, ArtifactFlowValid: true, FailurePathsValid: true}}
	var planErr error
	plan, planErr = plan.WithComputedHash()
	if planErr != nil {
		t.Fatal(planErr)
	}
	decision := writingplan.StrategyDecision{ApprovalRequired: approval}
	store := &fakeRuntimeStore{run: writingstore.RuntimeRun{RunID: "run_runtime", DocumentID: "doc_runtime", ContractID: "ctr_runtime", ContractVersion: 1, ContractHash: contractHash,
		Status: string(StatePlanned), ActivePlanID: plan.PlanID, ActivePlanVersion: 1,
		Budget: writingplan.PlanBudget{MaxCostUSD: 10, MaxDurationMS: 10000, MaxConcurrency: 1, MaxNodes: 2, MaxItems: 1}, Permissions: []writingplan.Permission{"model.invoke", "materials.read"}},
		plan: writingstore.PlanRecord{RunID: "run_runtime", PlanVersion: 1, ApprovalStatus: "not_required", Envelope: writingplan.WritingPlanEnvelope{IntentPlan: writingplan.IntentPlan{ContractRef: writingplan.ObjectRef{ID: "ctr_runtime", Version: 1, Hash: contractHash}}, ExecutablePlan: plan, StrategyDecision: decision}}}
	capabilities := writingplan.NewCapabilityRegistry("runtime-test")
	if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: "engine.step.write", AcceptedInputTypes: []writingplan.ArtifactType{"contract"}, ProducedOutputTypes: []writingplan.ArtifactType{"full_draft"}, Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
		return writingplan.ExecutionResult{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	manifest := writingplan.CapabilityManifest{ID: "core.draft.generate", Class: "writing.draft", Executor: "engine.step.write",
		InputTypes: []writingplan.ArtifactType{"contract"}, OptionalInputTypes: []writingplan.ArtifactType{}, OutputTypes: []writingplan.ArtifactType{"full_draft"}, Permissions: []writingplan.Permission{"model.invoke", "materials.read"},
		EstimatedCostUSD: 1, EstimatedDurationMS: 100, Version: "1.0.0", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction},
		MaxBounds: plan.Nodes[0].Bounds, Idempotency: idempotency, Available: true}
	if err := capabilities.Register(manifest); err != nil {
		t.Fatal(err)
	}
	executor := &fakeGovernedExecutor{descriptor: ExecutorDescriptor{ExecutorID: "engine.step.write", Version: "adapter-1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}, Cancellable: true}}
	executors := NewExecutorRegistry()
	if err := executors.Register(executor); err != nil {
		t.Fatal(err)
	}
	checkpoints := &memoryCheckpoints{}
	orchestrator := &Orchestrator{Store: store, Capabilities: capabilities, Executors: executors,
		State: NewStateMachine(store), Checkpoints: checkpoints,
		Initial: fixedInitialProvider{{ArtifactID: "art_contract", Version: 1, ArtifactType: "contract", ContentHash: contractHash, MediaType: "application/json", ContentRef: "memory://contract"}}, Now: func() time.Time { return now }}
	return orchestratorFixture{orchestrator: orchestrator, store: store, executor: executor, checkpoints: checkpoints}
}

type fixedInitialProvider []InputArtifact

func (provider fixedInitialProvider) InitialArtifacts(context.Context, writingstore.RuntimeRun, writingstore.PlanRecord) ([]InputArtifact, error) {
	return append([]InputArtifact(nil), provider...), nil
}

type fakeRuntimeStore struct {
	mu            sync.Mutex
	run           writingstore.RuntimeRun
	plan          writingstore.PlanRecord
	attempts      []writingstore.NodeAttempt
	artifacts     []writingstore.ArtifactRecord
	completions   []writingstore.AttemptCompletion
	transitions   []TransitionRecord
	completionErr error
}

func (store *fakeRuntimeStore) LoadRuntimeRun(context.Context, string) (writingstore.RuntimeRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.run, nil
}
func (store *fakeRuntimeStore) LoadActivePlan(context.Context, string) (writingstore.PlanRecord, error) {
	return store.plan, nil
}
func (store *fakeRuntimeStore) ListRunAttempts(context.Context, string) ([]writingstore.NodeAttempt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]writingstore.NodeAttempt(nil), store.attempts...), nil
}
func (store *fakeRuntimeStore) ListRunArtifacts(context.Context, string) ([]writingstore.ArtifactRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]writingstore.ArtifactRecord(nil), store.artifacts...), nil
}
func (store *fakeRuntimeStore) StartNodeAttempt(_ context.Context, attempt writingstore.NodeAttempt, _ writingstore.TraceContext) (writingstore.NodeAttempt, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	attempt.Status = "running"
	store.attempts = append(store.attempts, attempt)
	return attempt, true, nil
}
func (store *fakeRuntimeStore) CompleteNodeAttempt(_ context.Context, completion writingstore.AttemptCompletion) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.completionErr != nil {
		return store.completionErr
	}
	for index := range store.attempts {
		if store.attempts[index].NodeID == completion.NodeID && store.attempts[index].Attempt == completion.Attempt {
			store.attempts[index].Status = completion.Status
			store.attempts[index].ActualCostUSD = completion.CostUSD
			store.attempts[index].ActualDurationMS = completion.DurationMS
		}
	}
	store.artifacts = append(store.artifacts, completion.Artifacts...)
	store.completions = append(store.completions, completion)
	return nil
}
func (store *fakeRuntimeStore) RecordTransition(_ context.Context, record TransitionRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.transitions = append(store.transitions, record)
	if record.Accepted {
		store.run.Status = string(record.EffectiveState)
	}
	return nil
}

type memoryCheckpoints struct {
	mu    sync.Mutex
	saved []Checkpoint
}

func (store *memoryCheckpoints) Save(_ context.Context, checkpoint Checkpoint) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.saved = append(store.saved, checkpoint)
	return nil
}
func (store *memoryCheckpoints) LoadLatest(context.Context, string) (Checkpoint, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.saved) == 0 {
		return Checkpoint{}, ErrCheckpointNotFound
	}
	return store.saved[len(store.saved)-1], nil
}

type fakeGovernedExecutor struct {
	mu                           sync.Mutex
	descriptor                   ExecutorDescriptor
	calls, failures, cancelCalls int
	block                        chan struct{}
	started                      chan struct{}
	failErr                      error
	contentRef                   string
}

func (executor *fakeGovernedExecutor) Descriptor() ExecutorDescriptor { return executor.descriptor }
func (executor *fakeGovernedExecutor) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	executor.mu.Lock()
	executor.calls++
	call := executor.calls
	failures := executor.failures
	started := executor.started
	block := executor.block
	executor.mu.Unlock()
	if started != nil && call == 1 {
		close(started)
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ExecutionResult{}, ctx.Err()
		}
	}
	if call <= failures {
		if executor.failErr != nil {
			return ExecutionResult{}, executor.failErr
		}
		return ExecutionResult{}, errors.New("transient")
	}
	parents := make([]writingstore.ArtifactRef, len(request.Inputs))
	hashes := make([]string, len(request.Inputs))
	for index, input := range request.Inputs {
		parents[index] = writingstore.ArtifactRef{ArtifactID: input.ArtifactID, Version: input.Version}
		hashes[index] = input.ContentHash
	}
	contentRef := executor.contentRef
	if contentRef == "" {
		contentRef = "memory://draft"
	}
	now := time.Now().UTC()
	return ExecutionResult{StartedAt: now.Add(-time.Millisecond), CompletedAt: now, Usage: ExecutionUsage{CostUSD: 1, DurationMS: 1}, Artifacts: []OutputArtifactDraft{{OutputKey: "draft", ArtifactType: "full_draft", ContentHash: hashForTest("draft"), MediaType: "text/markdown", ContentRef: contentRef, Parents: parents, Producer: request.Node.Capability, CapabilityVersion: request.Node.CapabilityVersion, InputHashes: hashes, Provenance: map[string]any{"test": true}, SourceRefs: []string{}}}}, nil
}
func (executor *fakeGovernedExecutor) Cancel(context.Context, ExecutionHandle) error {
	executor.mu.Lock()
	executor.cancelCalls++
	executor.mu.Unlock()
	return nil
}

func hashForTest(seed string) string {
	return writingstore.StableID("sha256:", seed) + "00000000000000000000000000000000"
}
