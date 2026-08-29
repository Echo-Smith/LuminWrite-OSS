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

type RuntimeStore interface {
	LoadRuntimeRun(context.Context, string) (writingstore.RuntimeRun, error)
	LoadActivePlan(context.Context, string) (writingstore.PlanRecord, error)
	ListRunAttempts(context.Context, string) ([]writingstore.NodeAttempt, error)
	ListRunArtifacts(context.Context, string) ([]writingstore.ArtifactRecord, error)
	StartNodeAttempt(context.Context, writingstore.NodeAttempt, writingstore.TraceContext) (writingstore.NodeAttempt, bool, error)
	CompleteNodeAttempt(context.Context, writingstore.AttemptCompletion) error
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

type Orchestrator struct {
	Store        RuntimeStore
	Capabilities *writingplan.CapabilityRegistry
	Executors    *ExecutorRegistry
	State        *StateMachine
	Checkpoints  CheckpointRepository
	Initial      InitialArtifactProvider
	Telemetry    RuntimeTelemetry
	Now          func() time.Time

	mu       sync.Mutex
	controls map[string]*runControl
	commands atomic.Uint64
}

type runControl struct {
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
	if orchestrator == nil || orchestrator.Store == nil || orchestrator.Capabilities == nil || orchestrator.Executors == nil || orchestrator.State == nil || orchestrator.Checkpoints == nil || orchestrator.Initial == nil {
		return RunOutcome{}, ErrRuntimeNotReady
	}
	if orchestrator.Now == nil {
		orchestrator.Now = func() time.Time { return time.Now().UTC() }
	}
	control, err := orchestrator.acquire(runID)
	if err != nil {
		return RunOutcome{}, err
	}
	defer orchestrator.release(runID, control)

	run, err := orchestrator.Store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		return RunOutcome{}, err
	}
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
	initial, err := orchestrator.Initial.InitialArtifacts(ctx, run, planRecord)
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
		if intent := controlIntent(control); intent != "" {
			return orchestrator.finishControl(ctx, run, plan, completed, artifacts, spentCost, spentDuration, intent)
		}
		node, found := nextReadyNode(plan.Nodes, completed)
		if !found {
			_, _ = orchestrator.transition(ctx, runID, StateRunning, StateFailed, "dependency_deadlock")
			return outcome(runID, StateFailed, completed, artifacts, spentCost), ErrNoReadyNode
		}
		if node.Kind == writingplan.NodeHumanGate {
			_, _ = orchestrator.transition(ctx, runID, StateRunning, StatePausing, "human_gate")
			_, _ = orchestrator.transition(ctx, runID, StatePausing, StatePaused, "human_gate")
			_ = orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, spentCost, spentDuration, []string{node.NodeID})
			return outcome(runID, StatePaused, completed, artifacts, spentCost), ErrApprovalRequired
		}
		manifest, exists := manifests[node.Capability]
		if !exists || !manifest.Available {
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, ErrExecutorNotFound)
		}
		if !permissionsContain(run.Permissions, manifest.Permissions) {
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, ErrPermissionDenied)
		}
		if spentCost+manifest.EstimatedCostUSD > run.Budget.MaxCostUSD || spentDuration+manifest.EstimatedDurationMS > run.Budget.MaxDurationMS {
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, ErrRuntimeBudget)
		}
		executor, err := orchestrator.Executors.Resolve(manifest, node)
		if err != nil {
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, err)
		}
		inputs, err := selectInputs(node, artifacts)
		if err != nil {
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, err)
		}
		attemptNumber := nextAttempts[node.NodeID]
		if attemptNumber < 1 {
			attemptNumber = 1
		}
		if attemptNumber > node.Bounds.MaxAttempts {
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, errors.New("attempt bound exhausted"))
		}
		key, _ := writingstore.NodeAttemptKey(runID, node.NodeID, attemptNumber)
		request := ExecutionRequest{RunID: runID, PlanID: plan.PlanID, PlanVersion: planRecord.PlanVersion,
			NodeID: node.NodeID, Attempt: attemptNumber, IdempotencyKey: key,
			ContractRef: planRecord.Envelope.IntentPlan.ContractRef, Node: node,
			Inputs: inputs, Permissions: append([]writingplan.Permission(nil), run.Permissions...)}
		if err := request.Validate(); err != nil {
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, err)
		}
		attempt := writingstore.NodeAttempt{RunID: runID, PlanID: plan.PlanID,
			PlanVersion: planRecord.PlanVersion, NodeID: node.NodeID, Attempt: attemptNumber,
			IdempotencyKey: key, NodeKind: node.Kind, CapabilityID: node.Capability,
			CapabilityVersion: node.CapabilityVersion, ExecutorID: manifest.Executor,
			FailurePath: node.FailurePath, Bounds: node.Bounds, InputHash: hashInputs(inputs),
			InputArtifactIDs: artifactIDs(inputs)}
		saved, dispatch, err := orchestrator.Store.StartNodeAttempt(ctx, attempt, runtimeTrace(node.Capability))
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

		execCtx, cancel := context.WithTimeout(ctx, time.Duration(node.Bounds.TimeoutMS)*time.Millisecond)
		setActiveExecution(control, executor, request.Handle(), cancel)
		result, executeErr := executor.Execute(execCtx, request)
		cancel()
		clearActiveExecution(control)
		if intent := controlIntent(control); intent != "" {
			_ = orchestrator.completeAttempt(ctx, manifest.Executor, node.Capability, writingstore.AttemptCompletion{RunID: runID,
				NodeID: node.NodeID, Attempt: attemptNumber, Status: map[string]string{"pause": "paused", "cancel": "cancelled"}[intent],
				ErrorCode: "CONTROL_" + strings.ToUpper(intent), ErrorMessage: intent,
				Trace: runtimeTrace(node.Capability), CompletedAt: orchestrator.Now()})
			return orchestrator.finishControl(ctx, run, plan, completed, artifacts, spentCost, spentDuration, intent)
		}
		if executeErr == nil {
			executeErr = result.Validate(request)
		}
		if executeErr == nil && (result.Usage.CostUSD > node.Bounds.MaxCostUSD || spentCost+result.Usage.CostUSD > run.Budget.MaxCostUSD || result.Usage.DurationMS > node.Bounds.TimeoutMS || spentDuration+result.Usage.DurationMS > run.Budget.MaxDurationMS) {
			executeErr = ErrRuntimeBudget
		}
		if executeErr == nil && containsShadowContentRef(result.Artifacts) {
			executeErr = runtimeError(CodeArtifactCommitFailed, RetryNever,
				"canonical artifacts cannot reference shadow content", ErrShadowContentLeak)
		}
		if executeErr != nil {
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
				_ = orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, spentCost, spentDuration, []string{node.NodeID})
				out := outcome(runID, StatePaused, completed, artifacts, spentCost)
				out.HumanRequired = []string{node.NodeID}
				return out, fmt.Errorf("%w: %s", ErrHumanRecoveryRequired, node.NodeID)
			}
			return orchestrator.failNode(ctx, run, plan, node, completed, artifacts, spentCost, spentDuration, executeErr)
		}
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
		if err := orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, spentCost, spentDuration, []string{}); err != nil {
			return RunOutcome{}, err
		}
	}
	if _, err := orchestrator.transition(ctx, runID, StateRunning, StateCompleted, "plan_completed"); err != nil {
		return RunOutcome{}, err
	}
	return outcome(runID, StateCompleted, completed, artifacts, spentCost), nil
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
// persisted checkpoint and executes the same active plan version.
func (orchestrator *Orchestrator) Resume(ctx context.Context, runID, commandID string, actor writingstore.Actor) (RunOutcome, error) {
	run, err := orchestrator.Store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		return RunOutcome{}, err
	}
	if RunState(run.Status) != StatePaused {
		return RunOutcome{}, fmt.Errorf("writingruntime: run is not paused")
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

func (orchestrator *Orchestrator) failNode(ctx context.Context, run writingstore.RuntimeRun, plan writingplan.ExecutablePlan, node writingplan.PlanNode, completed map[string]int, artifacts []InputArtifact, cost float64, duration int64, cause error) (RunOutcome, error) {
	if node.FailurePath == writingplan.FailureFallback {
		_, _ = orchestrator.transition(ctx, run.RunID, StateRunning, StateReplanning, "fallback_requested")
		_ = orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, cost, duration, []string{node.NodeID})
		return outcome(run.RunID, StateReplanning, completed, artifacts, cost), fmt.Errorf("%w: %v", ErrRunReplanning, cause)
	}
	if node.FailurePath == writingplan.FailurePause || node.FailurePath == writingplan.FailurePartial {
		_, _ = orchestrator.transition(ctx, run.RunID, StateRunning, StatePausing, "node_failure")
		_, _ = orchestrator.transition(ctx, run.RunID, StatePausing, StatePaused, "node_failure")
		_ = orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, cost, duration, []string{node.NodeID})
		return outcome(run.RunID, StatePaused, completed, artifacts, cost), fmt.Errorf("%w: %v", ErrRunPaused, cause)
	}
	_, _ = orchestrator.transition(ctx, run.RunID, StateRunning, StateFailed, "node_failure")
	return outcome(run.RunID, StateFailed, completed, artifacts, cost), cause
}

func (orchestrator *Orchestrator) finishControl(ctx context.Context, run writingstore.RuntimeRun, plan writingplan.ExecutablePlan, completed map[string]int, artifacts []InputArtifact, cost float64, duration int64, intent string) (RunOutcome, error) {
	unsafe := []string{}
	_ = orchestrator.saveCheckpoint(ctx, run, plan, completed, artifacts, cost, duration, unsafe)
	if intent == "cancel" {
		_, _ = orchestrator.transition(ctx, run.RunID, StateCancelling, StateCancelled, "cancelled")
		return outcome(run.RunID, StateCancelled, completed, artifacts, cost), ErrRunCancelled
	}
	_, _ = orchestrator.transition(ctx, run.RunID, StatePausing, StatePaused, "paused")
	return outcome(run.RunID, StatePaused, completed, artifacts, cost), ErrRunPaused
}

func (orchestrator *Orchestrator) saveCheckpoint(ctx context.Context, run writingstore.RuntimeRun, plan writingplan.ExecutablePlan, completed map[string]int, artifacts []InputArtifact, cost float64, duration int64, unsafe []string) error {
	copyCompleted := make(map[string]int, len(completed))
	for key, value := range completed {
		copyCompleted[key] = value
	}
	refs := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		refs = append(refs, artifactIdentity(artifact.ArtifactID, artifact.Version))
	}
	now := orchestrator.Now()
	return orchestrator.Checkpoints.Save(ctx, Checkpoint{CheckpointID: checkpointID(run.RunID, plan.PlanHash, completed, artifacts),
		RunID: run.RunID, PlanID: plan.PlanID, PlanVersion: run.ActivePlanVersion, PlanHash: plan.PlanHash,
		CompletedNodes: copyCompleted, ArtifactRefs: refs, SpentCostUSD: cost,
		SpentDurationMS: duration, UnsafeInFlight: append([]string(nil), unsafe...), CreatedAt: now})
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
