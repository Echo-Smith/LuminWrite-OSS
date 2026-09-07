package writingruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

var (
	ErrRuntimeNotReady  = errors.New("writingruntime: orchestrator dependencies are incomplete")
	ErrRunAlreadyActive = errors.New("writingruntime: run already active")
	ErrApprovalRequired = errors.New("writingruntime: approval required")
	ErrPermissionDenied = errors.New("writingruntime: permission denied")
	ErrRuntimeBudget    = errors.New("writingruntime: budget exceeded")
	ErrRunPaused        = errors.New("writingruntime: run paused")
	ErrRunCancelled     = errors.New("writingruntime: run cancelled")
	ErrRunReplanning    = errors.New("writingruntime: run requires replanning")
	ErrNoReadyNode      = errors.New("writingruntime: no ready node")
)

// InitialCaptureNodeID is the synthetic node id the M1.0 delivery protocol's
// initial-artifact capture is recorded under. It is bookkeeping, not a plan
// node: recovery ignores it, and the checkpoint manifest counts only plan
// nodes toward completion.
const InitialCaptureNodeID = "initial"

type RuntimeStore interface {
	LoadRuntimeRun(context.Context, string) (writingstore.RuntimeRun, error)
	LoadActivePlan(context.Context, string) (writingstore.PlanRecord, error)
	ListRunAttempts(context.Context, string) ([]writingstore.NodeAttempt, error)
	ListRunArtifacts(context.Context, string) ([]writingstore.ArtifactRecord, error)
	StartNodeAttempt(context.Context, writingstore.NodeAttempt, writingstore.TraceContext) (writingstore.NodeAttempt, bool, error)
	CompleteNodeAttempt(context.Context, writingstore.AttemptCompletion) error
	// SaveInitialArtifacts persists the run's initial artifacts as real
	// artifact rows on first capture: the lineage edges of every downstream
	// node output reference them, and the artifact FKs require the rows.
	// Idempotent: re-running a capture must not duplicate rows.
	SaveInitialArtifacts(context.Context, []writingstore.ArtifactRecord) error
}

// MaterialSnapshotRepository persists the run-level initial material manifest.
// A run owns exactly one immutable snapshot: captured at first dispatch,
// reused on every resume, so later source edits cannot re-snapshot a paused
// run into a different hash/ref lineage.
type MaterialSnapshotRepository interface {
	SaveInitialMaterialSnapshot(context.Context, string, []writingstore.MaterialSnapshotArtifact) (writingstore.MaterialSnapshotRecord, bool, error)
	LoadInitialMaterialSnapshot(context.Context, string) (writingstore.MaterialSnapshotRecord, error)
}

type InitialArtifactProvider interface {
	InitialArtifacts(context.Context, writingstore.RuntimeRun, writingstore.PlanRecord) ([]InputArtifact, error)
}

type CompositeInitialArtifactProvider struct {
	providers []InitialArtifactProvider
}

func NewCompositeInitialArtifactProvider(providers ...InitialArtifactProvider) (*CompositeInitialArtifactProvider, error) {
	if len(providers) == 0 {
		return nil, ErrRuntimeNotReady
	}
	for _, provider := range providers {
		if provider == nil {
			return nil, ErrRuntimeNotReady
		}
	}
	return &CompositeInitialArtifactProvider{providers: append([]InitialArtifactProvider(nil), providers...)}, nil
}

func (provider *CompositeInitialArtifactProvider) InitialArtifacts(ctx context.Context, run writingstore.RuntimeRun, plan writingstore.PlanRecord) ([]InputArtifact, error) {
	if provider == nil || len(provider.providers) == 0 {
		return nil, ErrRuntimeNotReady
	}
	result := []InputArtifact{}
	seen := map[string]InputArtifact{}
	for _, child := range provider.providers {
		artifacts, err := child.InitialArtifacts(ctx, run, plan)
		if err != nil {
			return nil, err
		}
		for _, artifact := range artifacts {
			if err := validateInitialArtifact(artifact); err != nil {
				return nil, err
			}
			identity := artifactIdentity(artifact.ArtifactID, artifact.Version)
			if prior, duplicate := seen[identity]; duplicate {
				if prior != artifact {
					return nil, runtimeError(CodeMaterialIntegrityFailed, RetryNever, "initial artifact identity has conflicting content", nil)
				}
				continue
			}
			seen[identity] = artifact
			result = append(result, artifact)
		}
	}
	return result, nil
}

func validateInitialArtifact(artifact InputArtifact) error {
	if !hasIDPrefix(artifact.ArtifactID, "art_") || artifact.Version < 1 || strings.TrimSpace(string(artifact.ArtifactType)) == "" || !executionHashPattern.MatchString(artifact.ContentHash) || !validExecutionMediaType(artifact.MediaType) || strings.TrimSpace(artifact.ContentRef) == "" {
		return runtimeError(CodeMaterialIntegrityFailed, RetryNever, "invalid initial artifact", ErrInvalidExecutionRequest)
	}
	return nil
}

type RunMaterialSelectionSource interface {
	MaterialsForRun(context.Context, writingstore.RuntimeRun, writingstore.PlanRecord) (MaterialSnapshotRequest, error)
}

type MaterialArtifactProvider struct {
	Adapter   *MaterialAdapter
	Selection RunMaterialSelectionSource
}

func (provider MaterialArtifactProvider) InitialArtifacts(ctx context.Context, run writingstore.RuntimeRun, plan writingstore.PlanRecord) ([]InputArtifact, error) {
	if provider.Adapter == nil || provider.Selection == nil {
		return nil, ErrRuntimeNotReady
	}
	request, err := provider.Selection.MaterialsForRun(ctx, run, plan)
	if err != nil {
		return nil, err
	}
	if request.RunID != run.RunID {
		return nil, runtimeError(CodeExecutorContractMismatch, RetryNever, "material selection is bound to a different run", nil)
	}
	bundle, err := provider.Adapter.Snapshot(ctx, request)
	if err != nil {
		return nil, err
	}
	return []InputArtifact{bundle.Artifact}, nil
}

type ContractArtifactSource interface {
	GetContract(context.Context, string, int) (writingstore.ContractRecord, error)
}

type ContractArtifactProvider struct{ Source ContractArtifactSource }

func (provider ContractArtifactProvider) InitialArtifacts(ctx context.Context, run writingstore.RuntimeRun, _ writingstore.PlanRecord) ([]InputArtifact, error) {
	if provider.Source == nil {
		return nil, writingstore.ErrNotFound
	}
	record, err := provider.Source.GetContract(ctx, run.ContractID, run.ContractVersion)
	if err != nil {
		return nil, err
	}
	return []InputArtifact{{ArtifactID: writingstore.StableID("art_", run.RunID, "contract"),
		Version: run.ContractVersion, ArtifactType: "contract", ContentHash: record.Contract.ContractHash,
		MediaType: "application/json", ContentRef: fmt.Sprintf("db://writing_contracts/%s/%d", run.ContractID, run.ContractVersion)}}, nil
}

// BudgetBoundaryGuard is the research budget hook (design.md §3/§7): given
// the next dispatchable node, it reports whether the run has reached the
// proactive execution budget and should pause cleanly instead of dispatching.
// The research executors (T05/T06) supply the real node-class policy; the
// orchestrator only owns the mechanics: save a progress checkpoint and
// transition to paused so the run stays resumable and decidable.
type BudgetBoundaryGuard interface {
	BudgetBoundaryReached(ctx context.Context, run writingstore.RuntimeRun, plan writingplan.ExecutablePlan, node writingplan.PlanNode) (bool, string)
}

type Orchestrator struct {
	Store        RuntimeStore
	Capabilities *writingplan.CapabilityRegistry
	Executors    *ExecutorRegistry
	State        *StateMachine
	Checkpoints  CheckpointRepository
	Initial      InitialArtifactProvider
	Materials    MaterialSnapshotRepository
	Telemetry    RuntimeTelemetry
	Now          func() time.Time
	// Delivery drives the M1.0 delivery-commit protocol after a successful
	// node (docs/21 §21.8): draft commits its candidate document version,
	// quality commits the delivery bundle. Nil disables the protocol (mode
	// off / harness keeps its current behavior).
	Delivery *DeliveryProtocol
	// Subject resolves the rollout audience (user/tenant) for allowlist and
	// percentage routing. Nil means executions route by run id.
	Subject func(writingstore.RuntimeRun) string
	// Context compiles per-attempt envelopes from the capability's manifest
	// contract; Envelopes persists them. Nil Context disables the context
	// shadow wiring entirely (M4a: compilation and audit only — required
	// blocks going unsupplied never fail the node).
	Context   ContextSource
	Envelopes ContextEnvelopeSink
	// ContextRuntime consumes envelope lifecycles for the M5 Context
	// Runtime (docs/18 §18.5.6): pressure warn/compress with guarded
	// pre-compression and named recovery paths. Nil disables pressure
	// handling; Execute defaults it whenever Context wiring is present.
	ContextRuntime *ContextRuntime
	// Hooks are the M3 lifecycle observers (docs/27). Nil is the zero
	// posture; Execute also auto-registers the telemetry projection hook
	// whenever Telemetry is present, so the metric stream and the hook bus
	// stay one pipeline.
	Hooks []RuntimeHook
	// BudgetBoundary lets the research path (T05/T06) pause the run at the
	// proactive execution budget boundary instead of dispatching the next
	// research read class node: a clean pause (checkpoint, paused
	// transitions, resumable) rather than a failure (design.md §7). Nil
	// disables the check — current behavior for all non-research plans.
	BudgetBoundary BudgetBoundaryGuard

	initOnce sync.Once
	mu       sync.Mutex
	controls map[string]*runControl
	commands atomic.Uint64
	bus      *hookBus
}

type runControl struct {
	initOnce sync.Once
	mu       sync.Mutex
	intent   string
	cancel   context.CancelFunc
	executor Executor
	handle   ExecutionHandle
}

type RunOutcome struct {
	RunID          string
	State          RunState
	CompletedNodes []string
	Artifacts      []InputArtifact
	SpentCostUSD   float64
	HumanRequired  []string
}

func (orchestrator *Orchestrator) Execute(ctx context.Context, runID string) (RunOutcome, error) {
	if orchestrator == nil || orchestrator.Store == nil || orchestrator.Capabilities == nil || orchestrator.Executors == nil || orchestrator.State == nil || orchestrator.Checkpoints == nil || orchestrator.Initial == nil || orchestrator.Materials == nil {
		return RunOutcome{}, ErrRuntimeNotReady
	}
	orchestrator.initOnce.Do(func() {
		if orchestrator.Now == nil {
			orchestrator.Now = func() time.Time { return time.Now().UTC() }
		}
		if orchestrator.Context != nil && orchestrator.ContextRuntime == nil {
			orchestrator.ContextRuntime = &ContextRuntime{}
		}
		// M3 lifecycle bus (docs/27): the telemetry projection hook rides the
		// bus so metrics and hook observers share one pipeline.
		orchestrator.bus = newHookBus(orchestrator.Hooks...)
		if orchestrator.Telemetry != nil {
			orchestrator.bus.register(lifecycleTelemetryHook{telemetry: orchestrator.Telemetry})
		}
	})
	control, err := orchestrator.acquire(runID)
	if err != nil {
		return RunOutcome{}, err
	}
	defer orchestrator.release(runID, control)

	run, err := orchestrator.Store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		return RunOutcome{}, err
	}
	orchestrator.bus.emit(ctx, LifecycleRunDispatched, LifecycleSnapshot{RunID: runID, DocumentID: run.DocumentID,
		State: RunState(run.Status)})
	planRecord, err := orchestrator.Store.LoadActivePlan(ctx, runID)
	if err != nil {
		return RunOutcome{}, err
	}
	plan := planRecord.Envelope.ExecutablePlan
	if run.ActivePlanID != plan.PlanID || run.ActivePlanVersion != planRecord.PlanVersion {
		return RunOutcome{}, ErrPlanChangedDuringRecovery
	}
	expectedPlanHash, hashErr := plan.ComputeHash()
	if hashErr != nil || expectedPlanHash != plan.PlanHash || !plan.StaticValidation.Valid ||
		(plan.Status != writingplan.PlanValidated && plan.Status != writingplan.PlanApproved && plan.Status != writingplan.PlanLocked) {
		return RunOutcome{}, fmt.Errorf("writingruntime: active plan is not dispatch-valid")
	}
	initial, err := orchestrator.initialArtifacts(ctx, run, planRecord)
	if err != nil {
		return RunOutcome{}, fmt.Errorf("load initial artifacts: %w", err)
	}
	if planRecord.Envelope.StrategyDecision.ApprovalRequired && planRecord.ApprovalStatus != "approved" {
		if RunState(run.Status) == StatePlanned {
			_, _ = orchestrator.transition(ctx, runID, StatePlanned, StateAwaitingApproval, "approval_required")
		}
		return RunOutcome{RunID: runID, State: StateAwaitingApproval}, ErrApprovalRequired
	}
	state := RunState(run.Status)
	switch state {
	case StatePlanned, StateAwaitingApproval:
		if _, err := orchestrator.transition(ctx, runID, state, StateRunning, "dispatch"); err != nil {
			return RunOutcome{}, err
		}
	case StateRunning:
	default:
		return RunOutcome{RunID: runID, State: state}, fmt.Errorf("writingruntime: run state %s is not dispatchable", state)
	}

	attempts, err := orchestrator.Store.ListRunAttempts(ctx, runID)
	if err != nil {
		return RunOutcome{}, err
	}
	// pendingDelivery carries the quality node's delivery payload to the
	// next checkpoint save (M1.0 delivery protocol, docs/21 §21.8).
	var pendingDelivery *QualityDelivery
	manifests := make(map[string]writingplan.CapabilityManifest)
	for _, node := range plan.Nodes {
		if manifest, ok := orchestrator.Capabilities.Get(node.Capability); ok {
			manifests[node.Capability] = manifest
		}
	}
	var checkpoint *Checkpoint
	if saved, loadErr := orchestrator.Checkpoints.LoadLatest(ctx, runID); loadErr == nil {
		checkpoint = &saved
	} else if !errors.Is(loadErr, ErrCheckpointNotFound) {
		return RunOutcome{}, loadErr
	}
	recovery, err := Recover(plan, planRecord.PlanVersion, checkpoint, attempts, manifests)
	if err != nil {
		if errors.Is(err, ErrHumanRecoveryRequired) {
			_, _ = orchestrator.transition(ctx, runID, StateRunning, StatePausing, "unsafe_recovery")
			_, _ = orchestrator.transition(ctx, runID, StatePausing, StatePaused, "unsafe_recovery")
			return RunOutcome{RunID: runID, State: StatePaused, HumanRequired: recovery.HumanRequired}, err
		}
		return RunOutcome{}, err
	}
	artifacts := append([]InputArtifact(nil), initial...)
	persisted, err := orchestrator.Store.ListRunArtifacts(ctx, runID)
	if err != nil {
		return RunOutcome{}, err
	}
	for _, artifact := range persisted {
		artifacts = append(artifacts, InputArtifact{ArtifactID: artifact.ArtifactID, Version: artifact.Version,
			ArtifactType: writingplan.ArtifactType(artifact.ArtifactType), ContentHash: artifact.ContentHash,
			MediaType: artifact.MediaType, ContentRef: artifact.ContentRef})
	}

	completed := recovery.CompletedNodes
	nextAttempts := recovery.NextAttempts
	spentCost, spentDuration := recovery.SpentCostUSD, recovery.SpentDurationMS
	for len(completed) < len(plan.Nodes) {
		// ── Decide: control intents and node readiness ──
		if intent := controlIntent(control); intent != "" {
			return orchestrator.finishControl(ctx, run, plan, completed, artifacts, spentCost, spentDuration, intent)
		}
		node, found := nextReadyNode(plan.Nodes, completed)
		if !found {
			_, _ = orchestrator.transition(ctx, runID, StateRunning, StateFailed, "dependency_deadlock")
			return outcome(runID, StateFailed, completed, artifacts, spentCost), ErrNoReadyNode
		}
		// Proactive execution budget boundary (design.md §7): the research
		// path may pause cleanly before the next dispatch instead of
		// overrunning; the checkpoint carries no unsafe node so the run stays
		// plainly resumable.
		if orchestrator.BudgetBoundary != nil {
			if reached, reason := orchestrator.BudgetBoundary.BudgetBoundaryReached(ctx, run, plan, node); reached {
				return orchestrator.pauseAtBudgetBoundary(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, reason)
			}
		}
		if node.Kind == writingplan.NodeHumanGate {
			// T02 atomic gate arrival (design.md §5.1): the pending gate, the
			// waiting-gate checkpoint, the paused transitions, and the
			// gate.pending event commit in ONE transaction. The gate node
			// never enters UnsafeInFlight — a gate wait is not in-flight
			// execution, so T00's recovery reconciliation does not block it.
			gateKind, input := orchestrator.gateBinding(node, artifacts)
			arrival := GateArrival{Run: run, Plan: plan, Node: node, PlanVersion: planRecord.PlanVersion,
				GateKind: gateKind, Input: input, Completed: completed, Artifacts: artifacts,
				SpentCostUSD: spentCost, SpentDurationMS: spentDuration}
			pauseStore, pauseOK := orchestrator.Store.(GatePauseStore)
			committer, commitOK := orchestrator.Checkpoints.(CheckpointTxCommitter)
			if !pauseOK || !commitOK {
				// Harnesses without the store-backed gate flow keep the
				// legacy three-write pause (behavioral baseline for tests).
				_, _ = orchestrator.transition(ctx, runID, StateRunning, StatePausing, "human_gate")
				_, _ = orchestrator.transition(ctx, runID, StatePausing, StatePaused, "human_gate")
				_ = orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, spentCost, spentDuration, []string{node.NodeID}, nil)
				out := outcome(runID, StatePaused, completed, artifacts, spentCost)
				out.HumanRequired = []string{node.NodeID}
				return out, ErrApprovalRequired
			}
			if _, gateErr := orchestrator.PauseAtGate(ctx, pauseStore, committer, arrival); gateErr != nil {
				return RunOutcome{}, runtimeError(CodeArtifactCommitFailed, RetrySafe, "orchestrator could not commit the gate pause", gateErr)
			}
			out := outcome(runID, StatePaused, completed, artifacts, spentCost)
			out.HumanRequired = []string{node.NodeID}
			return out, ErrApprovalRequired
		}
		// ── Resolve + Authorize: manifest, permissions, budget, executor ──
		manifest, executor, authorizeErr := orchestrator.authorizeNode(run, node, manifests, spentCost, spentDuration)
		if authorizeErr != nil {
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, authorizeErr)
		}
		attemptNumber := nextAttempts[node.NodeID]
		if attemptNumber < 1 {
			attemptNumber = 1
		}
		if attemptNumber > node.Bounds.MaxAttempts {
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, errAttemptExhausted)
		}
		// ── Dispatch setup: inputs, request, context, attempt ledger ──
		request, prepErr := orchestrator.prepareDispatch(ctx, run, planRecord, plan, node, manifest, artifacts, attemptNumber)
		if prepErr != nil {
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, prepErr)
		}
		// M3: before-capability observation (docs/27).
		orchestrator.bus.emit(ctx, LifecycleBeforeCapability, lifecycleSnapshot(run, plan.PlanID, planRecord.PlanVersion, node, attemptNumber, StateRunning, completed, spentCost, spentDuration, nil))
		saved, dispatch, err := orchestrator.Store.StartNodeAttempt(ctx, writingstore.NodeAttempt{RunID: runID, PlanID: plan.PlanID,
			PlanVersion: planRecord.PlanVersion, NodeID: node.NodeID, Attempt: attemptNumber,
			IdempotencyKey: request.IdempotencyKey, NodeKind: node.Kind, CapabilityID: node.Capability,
			CapabilityVersion: node.CapabilityVersion, ExecutorID: manifest.Executor,
			FailurePath: node.FailurePath, Bounds: node.Bounds, InputHash: hashInputs(request.Inputs),
			InputArtifactIDs: artifactIDs(request.Inputs)}, runtimeTrace(node.Capability))
		if err != nil {
			return RunOutcome{}, err
		}
		if !dispatch {
			if saved.Status == "succeeded" {
				completed[node.NodeID] = attemptNumber
				continue
			}
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, fmt.Errorf("attempt is %s", saved.Status))
		}
		// ── Dispatch: the timed executor invocation ──
		result, executeErr := dispatchAttempt(ctx, orchestrator, control, executor, request, node.Bounds.TimeoutMS)
		if intent := controlIntent(control); intent != "" {
			_ = orchestrator.completeAttempt(ctx, manifest.Executor, node.Capability, writingstore.AttemptCompletion{RunID: runID,
				NodeID: node.NodeID, Attempt: attemptNumber, Status: map[string]string{"pause": "paused", "cancel": "cancelled"}[intent],
				ErrorCode: "CONTROL_" + strings.ToUpper(intent), ErrorMessage: intent,
				Trace: runtimeTrace(node.Capability), CompletedAt: orchestrator.Now()})
			return orchestrator.finishControl(ctx, run, plan, completed, artifacts, spentCost, spentDuration, intent)
		}
		// ── Observe: result validation, budget, kernel invariants ──
		if executeErr == nil {
			executeErr = observeResult(result, request, node, spentCost, spentDuration, run)
		}
		if executeErr != nil {
			// T05→T06 sentinel integration: the research read executor's
			// budget-boundary sentinel is a CLEAN pause (design.md §7), not a
			// failure — the completed sub-tasks stay in the ledger and the
			// resume re-dispatch continues the remaining papers. The node's
			// FailurePath is pause; the attempt records the paused outcome
			// with the sentinel code, and the run keeps a plainly resumable
			// checkpoint (no unsafe marker).
			if errors.Is(executeErr, ErrResearchBudgetBoundary) {
				_ = orchestrator.completeAttempt(ctx, manifest.Executor, node.Capability, writingstore.AttemptCompletion{RunID: runID,
					NodeID: node.NodeID, Attempt: attemptNumber, Status: "paused",
					ErrorCode: string(CodeResearchBudgetBoundary), ErrorMessage: executeErr.Error(),
					Trace: runtimeTrace(node.Capability), CompletedAt: orchestrator.Now()})
				nextAttempts[node.NodeID] = attemptNumber + 1
				return orchestrator.pauseAtBudgetBoundary(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, "budget_boundary")
			}
			_ = orchestrator.completeAttempt(ctx, manifest.Executor, node.Capability, writingstore.AttemptCompletion{RunID: runID,
				NodeID: node.NodeID, Attempt: attemptNumber, Status: "failed", ErrorCode: string(ErrorCodeOf(executeErr)),
				ErrorMessage: executeErr.Error(), Trace: runtimeTrace(node.Capability), CompletedAt: orchestrator.Now()})
			nextAttempts[node.NodeID] = attemptNumber + 1
			if manifest.Idempotency == writingplan.IdempotencySafe && attemptNumber < node.Bounds.MaxAttempts {
				continue
			}
			if manifest.Idempotency != writingplan.IdempotencySafe {
				_, _ = orchestrator.transition(ctx, runID, StateRunning, StatePausing, "unsafe_retry")
				_, _ = orchestrator.transition(ctx, runID, StatePausing, StatePaused, "unsafe_retry")
				_ = orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, spentCost, spentDuration, []string{node.NodeID}, nil)
				out := outcome(runID, StatePaused, completed, artifacts, spentCost)
				out.HumanRequired = []string{node.NodeID}
				return out, fmt.Errorf("%w: %s", ErrHumanRecoveryRequired, node.NodeID)
			}
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, executeErr)
		}
		// ── Observe: commit the attempt ledger and artifact lineage ──
		storedArtifacts := make([]writingstore.ArtifactRecord, 0, len(result.Artifacts))
		for _, draft := range result.Artifacts {
			storedArtifacts = append(storedArtifacts, artifactRecord(request, draft, runtimeTrace(node.Capability), orchestrator.Now()))
			artifacts = append(artifacts, InputArtifact{ArtifactID: storedArtifacts[len(storedArtifacts)-1].ArtifactID,
				Version: 1, ArtifactType: draft.ArtifactType, ContentHash: draft.ContentHash,
				MediaType: draft.MediaType, ContentRef: draft.ContentRef})
		}
		if err := orchestrator.completeAttempt(ctx, manifest.Executor, node.Capability, writingstore.AttemptCompletion{RunID: runID,
			NodeID: node.NodeID, Attempt: attemptNumber, Status: "succeeded", Artifacts: storedArtifacts,
			CostUSD: result.Usage.CostUSD, InputTokens: result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens, DurationMS: result.Usage.DurationMS,
			Trace: runtimeTrace(node.Capability), CompletedAt: result.CompletedAt}); err != nil {
			return RunOutcome{}, runtimeError(CodeArtifactCommitFailed, RetrySafe, "orchestrator could not commit node artifacts", err)
		}
		completed[node.NodeID] = attemptNumber
		nextAttempts[node.NodeID] = attemptNumber + 1
		spentCost += result.Usage.CostUSD
		spentDuration += result.Usage.DurationMS
		// M3: artifact-submitted observation (artifacts durable, delivery not
		// yet driven) then after-capability once the node fully commits.
		artifactTypes := make([]string, 0, len(storedArtifacts))
		for _, artifact := range storedArtifacts {
			artifactTypes = append(artifactTypes, artifact.ArtifactType)
		}
		orchestrator.bus.emit(ctx, LifecycleArtifactSubmitted, lifecycleSnapshot(run, plan.PlanID, planRecord.PlanVersion, node, attemptNumber, StateRunning, completed, spentCost, spentDuration, artifactTypes))
		// M1.0 delivery protocol (docs/21 §21.8) via the M2 routing table
		// (docs/26): the delivery protocol owns its class→driver table, so
		// the loop asks "does this class carry delivery?" instead of
		// hard-coding draft/quality branches. A protocol failure is a node
		// failure (evidence gap, fail closed).
		if orchestrator.Delivery != nil {
			delivery, deliveryErr := orchestrator.Delivery.Drive(ctx, manifest.Class, run, node, storedArtifacts)
			if deliveryErr != nil {
				return RunOutcome{}, runtimeError(CodeArtifactCommitFailed, RetryNever, "delivery commit failed", deliveryErr)
			}
			if delivery != nil {
				pendingDelivery = delivery
			}
		}
		if err := orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, spentCost, spentDuration, []string{}, pendingDelivery); err != nil {
			return RunOutcome{}, err
		}
		pendingDelivery = nil
		// M3: after-capability observation (artifacts + delivery + checkpoint
		// all committed — the node is fully done).
		orchestrator.bus.emit(ctx, LifecycleAfterCapability, lifecycleSnapshot(run, plan.PlanID, planRecord.PlanVersion, node, attemptNumber, StateRunning, completed, spentCost, spentDuration, artifactTypes))
	}
	if _, err := orchestrator.transition(ctx, runID, StateRunning, StateCompleted, "plan_completed"); err != nil {
		return RunOutcome{}, err
	}
	// M3: terminal lifecycle observation (docs/27).
	orchestrator.bus.emit(ctx, LifecycleRunCompleted, lifecycleSnapshot(run, plan.PlanID, planRecord.PlanVersion, writingplan.PlanNode{}, 0, StateCompleted, completed, spentCost, spentDuration, nil))
	return outcome(runID, StateCompleted, completed, artifacts, spentCost), nil
}

// initialArtifacts returns the run's immutable initial artifact set. The
// first dispatch captures the provider output into the RunLedger; every later
// dispatch — including resumes after pause — loads that persisted snapshot and
// never re-reads source materials.
func (orchestrator *Orchestrator) initialArtifacts(ctx context.Context, run writingstore.RuntimeRun, planRecord writingstore.PlanRecord) ([]InputArtifact, error) {
	saved, err := orchestrator.Materials.LoadInitialMaterialSnapshot(ctx, run.RunID)
	if err == nil {
		artifacts := make([]InputArtifact, 0, len(saved.Artifacts))
		for _, artifact := range saved.Artifacts {
			candidate := InputArtifact{ArtifactID: artifact.ArtifactID, Version: artifact.Version,
				ArtifactType: writingplan.ArtifactType(artifact.ArtifactType), ContentHash: artifact.ContentHash,
				MediaType: artifact.MediaType, ContentRef: artifact.ContentRef}
			if err := validateInitialArtifact(candidate); err != nil {
				return nil, runtimeError(CodeMaterialIntegrityFailed, RetryNever,
					"persisted material snapshot failed validation", err)
			}
			artifacts = append(artifacts, candidate)
		}
		return artifacts, nil
	}
	if !errors.Is(err, writingstore.ErrNotFound) {
		return nil, err
	}
	artifacts, err := orchestrator.Initial.InitialArtifacts(ctx, run, planRecord)
	if err != nil {
		return nil, err
	}
	manifest := make([]writingstore.MaterialSnapshotArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		manifest = append(manifest, writingstore.MaterialSnapshotArtifact{ArtifactID: artifact.ArtifactID,
			Version: artifact.Version, ArtifactType: string(artifact.ArtifactType), ContentHash: artifact.ContentHash,
			MediaType: artifact.MediaType, ContentRef: artifact.ContentRef})
	}
	_, created, err := orchestrator.Materials.SaveInitialMaterialSnapshot(ctx, run.RunID, manifest)
	if err != nil {
		return nil, runtimeError(CodeSourceSnapshotFailed, RetrySafe, "orchestrator could not persist the material snapshot", err)
	}
	if !created {
		// A concurrent dispatch captured first; adopt its snapshot verbatim.
		return orchestrator.initialArtifacts(ctx, run, planRecord)
	}
	// Persist the initial artifacts as real artifact rows: downstream node
	// outputs record lineage edges to them, and the artifact FKs require the
	// rows to exist. The rows bind to a synthetic succeeded "initial" attempt
	// so the attempt FK resolves. Idempotent at the store layer.
	initialKey, err := writingstore.NodeAttemptKey(run.RunID, "initial", 1)
	if err != nil {
		return nil, runtimeError(CodeArtifactCommitFailed, RetrySafe, "initial attempt key", err)
	}
	initialRows := make([]writingstore.ArtifactRecord, 0, len(artifacts))
	for _, artifact := range artifacts {
		initialRows = append(initialRows, writingstore.ArtifactRecord{ArtifactID: artifact.ArtifactID,
			Version: artifact.Version, RunID: run.RunID, PlanID: planRecord.Envelope.ExecutablePlan.PlanID,
			PlanVersion: planRecord.PlanVersion,
			NodeID:      InitialCaptureNodeID, Attempt: 1, IdempotencyKey: initialKey,
			OutputKey: string(artifact.ArtifactType), ArtifactType: string(artifact.ArtifactType),
			Status: "provisional", ContentHash: artifact.ContentHash, MediaType: artifact.MediaType,
			ContentRef: artifact.ContentRef, Producer: "initial." + string(artifact.ArtifactType),
			CapabilityVersion: "initial", Trace: writingstore.TraceContext{
				Provenance: map[string]any{"initial": true}, SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime.initial"}}})
	}
	if err := orchestrator.Store.SaveInitialArtifacts(ctx, initialRows); err != nil {
		return nil, runtimeError(CodeArtifactCommitFailed, RetrySafe, "orchestrator could not persist initial artifacts", err)
	}
	return artifacts, nil
}

func (orchestrator *Orchestrator) completeAttempt(ctx context.Context, executorID, capability string, completion writingstore.AttemptCompletion) error {
	metric := RuntimeMetric{Kind: MetricCanonicalCommit, ExecutorID: executorID, Capability: capability,
		Lane: LaneBaseline, Status: "started", Reason: completion.Status, DurationMS: completion.DurationMS,
		CostUSD: completion.CostUSD, InputTokens: completion.InputTokens, OutputTokens: completion.OutputTokens}
	observeRuntime(ctx, orchestrator.Telemetry, metric)
	if err := orchestrator.Store.CompleteNodeAttempt(ctx, completion); err != nil {
		metric.Status, metric.ErrorCode = "failed", CodeArtifactCommitFailed
		observeRuntime(ctx, orchestrator.Telemetry, metric)
		return err
	}
	metric.Status = "succeeded"
	observeRuntime(ctx, orchestrator.Telemetry, metric)
	return nil
}

// Resume is the only entry point that advances a paused run. It reloads the
// persisted checkpoint and executes the same active plan version. A run
// paused at an unconfirmed human gate cannot be resumed this way: plain
// resume returns ErrGateApprovalRequired and the run stays paused (design.md
// §5.4). An approved gate consumes its persisted decision transparently —
// the decision transaction already completed the gate node's attempt.
func (orchestrator *Orchestrator) Resume(ctx context.Context, runID, commandID string, actor writingstore.Actor) (RunOutcome, error) {
	run, err := orchestrator.Store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		return RunOutcome{}, err
	}
	if RunState(run.Status) != StatePaused {
		return RunOutcome{}, fmt.Errorf("writingruntime: run is not paused")
	}
	if decisionStore, ok := orchestrator.Store.(GateDecisionStore); ok {
		if _, gateErr := CheckRunGateForResume(ctx, decisionStore, runID); gateErr != nil {
			return RunOutcome{RunID: runID, State: StatePaused}, gateErr
		}
	}
	if _, err := orchestrator.State.Transition(ctx, TransitionRequest{CommandID: commandID, RunID: runID,
		From: StatePaused, To: StateRunning, Cause: "user_resume", Summary: "resume requested", Actor: actor}); err != nil {
		return RunOutcome{}, err
	}
	return orchestrator.Execute(ctx, runID)
}

func (orchestrator *Orchestrator) Pause(ctx context.Context, runID, commandID string, actor writingstore.Actor) error {
	run, err := orchestrator.Store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		return err
	}
	if _, err := orchestrator.State.Transition(ctx, TransitionRequest{CommandID: commandID, RunID: runID,
		From: RunState(run.Status), To: StatePausing, Cause: "user_pause", Summary: "pause requested", Actor: actor}); err != nil {
		return err
	}
	orchestrator.signal(runID, "pause")
	return nil
}

func (orchestrator *Orchestrator) Cancel(ctx context.Context, runID, commandID string, actor writingstore.Actor) error {
	run, err := orchestrator.Store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		return err
	}
	if _, err := orchestrator.State.Transition(ctx, TransitionRequest{CommandID: commandID, RunID: runID,
		From: RunState(run.Status), To: StateCancelling, Cause: "user_cancel", Summary: "cancel requested", Actor: actor}); err != nil {
		return err
	}
	orchestrator.signal(runID, "cancel")
	return nil
}

func (orchestrator *Orchestrator) transition(ctx context.Context, runID string, from, to RunState, cause string) (RunState, error) {
	id := orchestrator.commands.Add(1)
	commandID := writingstore.StableID("command_", runID, string(from), string(to), cause, fmt.Sprint(time.Now().UTC().UnixNano()), fmt.Sprint(id))
	return orchestrator.State.Transition(ctx, TransitionRequest{CommandID: commandID, RunID: runID,
		From: from, To: to, Cause: cause, ReasonCode: cause, Summary: strings.ReplaceAll(cause, "_", " "),
		Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime"}})
}

// pauseAtBudgetBoundary is the clean pause the budget guard triggers: the
// progress checkpoint (no UnsafeInFlight) and the paused transitions commit
// exactly like a user pause, so resume, gate decisions, and cancel all keep
// working from this state.
func (orchestrator *Orchestrator) pauseAtBudgetBoundary(ctx context.Context, run writingstore.RuntimeRun, plan writingplan.ExecutablePlan, node writingplan.PlanNode, completed map[string]int, artifacts []InputArtifact, cost float64, duration int64, reason string) (RunOutcome, error) {
	if reason == "" {
		reason = "budget_boundary"
	}
	_, _ = orchestrator.transition(ctx, run.RunID, StateRunning, StatePausing, reason)
	_, _ = orchestrator.transition(ctx, run.RunID, StatePausing, StatePaused, reason)
	_ = orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, cost, duration, []string{}, nil)
	orchestrator.bus.emit(ctx, LifecycleRunPaused, lifecycleSnapshot(run, plan.PlanID, run.ActivePlanVersion, node, 0, StatePaused, completed, cost, duration, nil))
	return outcome(run.RunID, StatePaused, completed, artifacts, cost), fmt.Errorf("%w: %s", ErrRunPaused, reason)
}

func (orchestrator *Orchestrator) failNode(ctx context.Context, run writingstore.RuntimeRun, plan writingplan.ExecutablePlan, node writingplan.PlanNode, completed map[string]int, artifacts []InputArtifact, cost float64, duration int64, cause error) (RunOutcome, error) {
	if node.FailurePath == writingplan.FailureFallback {
		_, _ = orchestrator.transition(ctx, run.RunID, StateRunning, StateReplanning, "fallback_requested")
		_ = orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, cost, duration, []string{node.NodeID}, nil)
		return outcome(run.RunID, StateReplanning, completed, artifacts, cost), fmt.Errorf("%w: %v", ErrRunReplanning, cause)
	}
	if node.FailurePath == writingplan.FailurePause || node.FailurePath == writingplan.FailurePartial {
		_, _ = orchestrator.transition(ctx, run.RunID, StateRunning, StatePausing, "node_failure")
		_, _ = orchestrator.transition(ctx, run.RunID, StatePausing, StatePaused, "node_failure")
		_ = orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, cost, duration, []string{node.NodeID}, nil)
		orchestrator.bus.emit(ctx, LifecycleRunPaused, lifecycleSnapshot(run, plan.PlanID, run.ActivePlanVersion, node, 0, StatePaused, completed, cost, duration, nil))
		return outcome(run.RunID, StatePaused, completed, artifacts, cost), fmt.Errorf("%w: %v", ErrRunPaused, cause)
	}
	_, _ = orchestrator.transition(ctx, run.RunID, StateRunning, StateFailed, "node_failure")
	orchestrator.bus.emit(ctx, LifecycleRunFailed, lifecycleSnapshot(run, plan.PlanID, run.ActivePlanVersion, node, 0, StateFailed, completed, cost, duration, nil))
	return outcome(run.RunID, StateFailed, completed, artifacts, cost), cause
}

func (orchestrator *Orchestrator) finishControl(ctx context.Context, run writingstore.RuntimeRun, plan writingplan.ExecutablePlan, completed map[string]int, artifacts []InputArtifact, cost float64, duration int64, intent string) (RunOutcome, error) {
	unsafe := []string{}
	_ = orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, cost, duration, unsafe, nil)
	if intent == "cancel" {
		_, _ = orchestrator.transition(ctx, run.RunID, StateCancelling, StateCancelled, "cancelled")
		orchestrator.bus.emit(ctx, LifecycleRunCancelled, lifecycleSnapshot(run, plan.PlanID, run.ActivePlanVersion, writingplan.PlanNode{}, 0, StateCancelled, completed, cost, duration, nil))
		return outcome(run.RunID, StateCancelled, completed, artifacts, cost), ErrRunCancelled
	}
	_, _ = orchestrator.transition(ctx, run.RunID, StatePausing, StatePaused, "paused")
	orchestrator.bus.emit(ctx, LifecycleRunPaused, lifecycleSnapshot(run, plan.PlanID, run.ActivePlanVersion, writingplan.PlanNode{}, 0, StatePaused, completed, cost, duration, nil))
	return outcome(run.RunID, StatePaused, completed, artifacts, cost), ErrRunPaused
}

// gateBinding resolves the gate's identity inputs: the gate kind from the
// capability class (research.gate.evidence / research.gate.outline) and the
// gate's input artifact — the latest artifact of the node's first declared
// input type (research_evidence_pack for the evidence gate, research_outline
// for the outline gate). Falls back to a synthetic reference for harness
// plans without research artifacts.
func (orchestrator *Orchestrator) gateBinding(node writingplan.PlanNode, artifacts []InputArtifact) (string, writingstore.ArtifactContentRef) {
	gateKind := "evidence"
	if strings.Contains(node.Capability, "gate.outline") {
		gateKind = "outline"
	}
	inputType := writingplan.ArtifactType("")
	if len(node.InputArtifactTypes) > 0 {
		inputType = node.InputArtifactTypes[0]
	}
	latest := InputArtifact{}
	found := false
	for _, artifact := range artifacts {
		if artifact.ArtifactType == inputType {
			if !found || artifact.Version > latest.Version {
				latest, found = artifact, true
			}
		}
	}
	if !found {
		// A gate without a matching input artifact cannot bind a decision to
		// content; the empty ref fails the store's pending-gate validation
		// and surfaces as a commit failure rather than a silently empty gate.
		return gateKind, writingstore.ArtifactContentRef{}
	}
	return gateKind, writingstore.ArtifactContentRef{ArtifactID: latest.ArtifactID,
		Version: latest.Version, ContentHash: latest.ContentHash}
}

func (orchestrator *Orchestrator) saveCheckpoint(ctx context.Context, run writingstore.RuntimeRun, plan writingplan.ExecutablePlan, completed map[string]int, artifacts []InputArtifact, cost float64, duration int64, unsafe []string, delivery *QualityDelivery) error {
	// M3: before-checkpoint observation (the BeforeQualityGate/BeforeCommit
	// moments of the reference sequence — quality deliveries ride this save).
	orchestrator.bus.emit(ctx, LifecycleCheckpointBefore, lifecycleSnapshot(run, plan.PlanID, run.ActivePlanVersion, writingplan.PlanNode{}, 0, StateRunning, completed, cost, duration, nil))
	copyCompleted := make(map[string]int, len(completed))
	for key, value := range completed {
		copyCompleted[key] = value
	}
	refs := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		refs = append(refs, artifactIdentity(artifact.ArtifactID, artifact.Version))
	}
	now := orchestrator.Now()
	// append([]string(nil), empty...) yields nil; the checkpoint invariant
	// requires non-nil slices (docs/20 §20.2 M1.0 wiring).
	unsafeInFlight := append([]string(nil), unsafe...)
	if unsafeInFlight == nil {
		unsafeInFlight = []string{}
	}
	return orchestrator.Checkpoints.Save(ctx, Checkpoint{CheckpointID: checkpointID(run.RunID, plan.PlanHash, completed, artifacts, ""),
		RunID: run.RunID, PlanID: plan.PlanID, PlanVersion: run.ActivePlanVersion, PlanHash: plan.PlanHash,
		CompletedNodes: copyCompleted, ArtifactRefs: refs, SpentCostUSD: cost,
		SpentDurationMS: duration, UnsafeInFlight: unsafeInFlight, CreatedAt: now, Delivery: delivery})
}

func (orchestrator *Orchestrator) acquire(runID string) (*runControl, error) {
	orchestrator.mu.Lock()
	defer orchestrator.mu.Unlock()
	if orchestrator.controls == nil {
		orchestrator.controls = map[string]*runControl{}
	}
	if _, exists := orchestrator.controls[runID]; exists {
		return nil, ErrRunAlreadyActive
	}
	control := &runControl{}
	orchestrator.controls[runID] = control
	return control, nil
}
func (orchestrator *Orchestrator) release(runID string, control *runControl) {
	orchestrator.mu.Lock()
	if orchestrator.controls[runID] == control {
		delete(orchestrator.controls, runID)
	}
	orchestrator.mu.Unlock()
}

// NotifyPersistedControl wakes the local worker after a different instance has
// committed pausing/cancelling. The database transition remains authoritative.
func (orchestrator *Orchestrator) NotifyPersistedControl(runID, state string) {
	if state == "pausing" {
		orchestrator.signal(runID, "pause")
	}
	if state == "cancelling" {
		orchestrator.signal(runID, "cancel")
	}
}
func (orchestrator *Orchestrator) signal(runID, intent string) {
	orchestrator.mu.Lock()
	control := orchestrator.controls[runID]
	orchestrator.mu.Unlock()
	if control == nil {
		return
	}
	control.mu.Lock()
	control.intent = intent
	cancel, executor, handle := control.cancel, control.executor, control.handle
	control.mu.Unlock()
	if cancellable, ok := executor.(CancellableExecutor); ok && executor.Descriptor().Cancellable {
		_ = cancellable.Cancel(context.Background(), handle)
	}
	if cancel != nil {
		cancel()
	}
}
func setActiveExecution(control *runControl, executor Executor, handle ExecutionHandle, cancel context.CancelFunc) {
	control.mu.Lock()
	control.executor, control.handle, control.cancel = executor, handle, cancel
	control.mu.Unlock()
}
func clearActiveExecution(control *runControl) {
	control.mu.Lock()
	control.executor, control.cancel = nil, nil
	control.handle = ExecutionHandle{}
	control.mu.Unlock()
}
func controlIntent(control *runControl) string {
	control.mu.Lock()
	defer control.mu.Unlock()
	return control.intent
}

func nextReadyNode(nodes []writingplan.PlanNode, completed map[string]int) (writingplan.PlanNode, bool) {
	for _, node := range nodes {
		if completed[node.NodeID] > 0 {
			continue
		}
		ready := true
		for _, dep := range node.DependsOn {
			if completed[dep] == 0 {
				ready = false
				break
			}
		}
		if ready {
			return node, true
		}
	}
	return writingplan.PlanNode{}, false
}
func selectInputs(node writingplan.PlanNode, artifacts []InputArtifact) ([]InputArtifact, error) {
	latest := map[writingplan.ArtifactType]InputArtifact{}
	for _, artifact := range artifacts {
		if current, ok := latest[artifact.ArtifactType]; !ok || artifact.Version > current.Version {
			latest[artifact.ArtifactType] = artifact
		}
	}
	inputs := make([]InputArtifact, 0, len(node.InputArtifactTypes))
	for _, kind := range node.InputArtifactTypes {
		artifact, ok := latest[kind]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrInvalidExecutionRequest, kind)
		}
		inputs = append(inputs, artifact)
	}
	return inputs, nil
}
func permissionsContain(granted, required []writingplan.Permission) bool {
	set := map[writingplan.Permission]bool{}
	for _, permission := range granted {
		set[permission] = true
	}
	for _, permission := range required {
		if !set[permission] {
			return false
		}
	}
	return true
}
func hashInputs(inputs []InputArtifact) string {
	values := make([]string, 0, len(inputs))
	for _, input := range inputs {
		values = append(values, artifactIdentity(input.ArtifactID, input.Version)+":"+input.ContentHash)
	}
	sort.Strings(values)
	payload, _ := json.Marshal(values)
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func artifactIDs(inputs []InputArtifact) []string {
	values := make([]string, len(inputs))
	for index, input := range inputs {
		values[index] = input.ArtifactID
	}
	return values
}
func runtimeTrace(capability string) writingstore.TraceContext {
	return writingstore.TraceContext{Provenance: map[string]any{"runtime": "governed", "capability": capability}, SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorCapability, ID: capability}}
}
func artifactRecord(request ExecutionRequest, draft OutputArtifactDraft, trace writingstore.TraceContext, createdAt time.Time) writingstore.ArtifactRecord {
	return writingstore.ArtifactRecord{ArtifactID: writingstore.StableID("art_", request.IdempotencyKey, draft.OutputKey, draft.ContentHash), Version: 1, RunID: request.RunID, PlanID: request.PlanID, PlanVersion: request.PlanVersion, NodeID: request.NodeID, Attempt: request.Attempt, IdempotencyKey: request.IdempotencyKey, OutputKey: draft.OutputKey, ArtifactType: string(draft.ArtifactType), Status: "provisional", ContentHash: draft.ContentHash, MediaType: draft.MediaType, ContentRef: draft.ContentRef, Parents: draft.Parents, Producer: draft.Producer, CapabilityVersion: draft.CapabilityVersion, InputHashes: draft.InputHashes, ModelRef: draft.ModelRef, PromptTemplateRef: draft.PromptTemplateRef, Trace: writingstore.TraceContext{Provenance: draft.Provenance, SourceRefs: draft.SourceRefs, Actor: trace.Actor}, CreatedAt: createdAt}
}
func outcome(runID string, state RunState, completed map[string]int, artifacts []InputArtifact, cost float64) RunOutcome {
	nodes := make([]string, 0, len(completed))
	for node := range completed {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	return RunOutcome{RunID: runID, State: state, CompletedNodes: nodes, Artifacts: append([]InputArtifact(nil), artifacts...), SpentCostUSD: cost, HumanRequired: []string{}}
}
