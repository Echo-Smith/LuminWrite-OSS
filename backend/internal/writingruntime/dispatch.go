// Thin-orchestrator dispatch helpers (V3.0 M2, docs/26): the Execute loop's
// Resolve→Authorize→Dispatch→Observe verbs, extracted verbatim from the
// orchestrator so the main loop reads as the six-verb skeleton the design
// mandates. Nothing here changes behavior — every helper is the same code
// that used to live inline in Execute, now named and unit-shaped. The kernel
// invariants stay in the orchestrator: contract/plan validation, attempt
// ledger, artifact commit, checkpoints, and state transitions are all
// orchestrator-owned and cannot be reached from a capability.
package writingruntime

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// dispatchContext is one iteration's resolved state: everything the six
// verbs need, assembled by authorizeNode before dispatch. Kept minimal —
// the loop still owns completed/artifacts bookkeeping (kernel lineage).
type dispatchContext struct {
	node     writingplan.PlanNode
	manifest writingplan.CapabilityManifest
	executor Executor
}

// authorizeNode is the Resolve+Authorize verb pair: manifest availability,
// run permissions, budget headroom, and executor resolution. Every rejection
// flows through failNode by the caller — the helper only decides.
func (orchestrator *Orchestrator) authorizeNode(run writingstore.RuntimeRun, node writingplan.PlanNode, manifests map[string]writingplan.CapabilityManifest, spentCost float64, spentDuration int64) (writingplan.CapabilityManifest, Executor, error) {
	manifest, exists := manifests[node.Capability]
	if !exists || !manifest.Available {
		return writingplan.CapabilityManifest{}, nil, ErrExecutorNotFound
	}
	if !permissionsContain(run.Permissions, manifest.Permissions) {
		return writingplan.CapabilityManifest{}, nil, ErrPermissionDenied
	}
	if spentCost+manifest.EstimatedCostUSD > run.Budget.MaxCostUSD || spentDuration+manifest.EstimatedDurationMS > run.Budget.MaxDurationMS {
		return writingplan.CapabilityManifest{}, nil, ErrRuntimeBudget
	}
	executor, err := orchestrator.Executors.Resolve(manifest, node)
	if err != nil {
		return writingplan.CapabilityManifest{}, nil, err
	}
	return manifest, executor, nil
}

// prepareDispatch is the Dispatch verb's setup half: input selection,
// attempt numbering, request assembly (with M1.3 style and rollout subject),
// context compilation/injection, and request validation. On success the
// caller starts the attempt row and invokes the executor.
func (orchestrator *Orchestrator) prepareDispatch(ctx context.Context, run writingstore.RuntimeRun, planRecord writingstore.PlanRecord, plan writingplan.ExecutablePlan, node writingplan.PlanNode, manifest writingplan.CapabilityManifest, artifacts []InputArtifact, attemptNumber int) (ExecutionRequest, error) {
	inputs, err := selectInputs(node, artifacts)
	if err != nil {
		return ExecutionRequest{}, err
	}
	key, _ := writingstore.NodeAttemptKey(run.RunID, node.NodeID, attemptNumber)
	request := ExecutionRequest{RunID: run.RunID, PlanID: plan.PlanID, PlanVersion: planRecord.PlanVersion,
		NodeID: node.NodeID, Attempt: attemptNumber, IdempotencyKey: key,
		ContractRef: planRecord.Envelope.IntentPlan.ContractRef, Node: node,
		Inputs: inputs, Permissions: append([]writingplan.Permission(nil), run.Permissions...)}
	// M1.3 per-request style: the run carries the slug the request asked
	// for. User scoping for "my_" slugs is stamped by the composition once
	// it knows the requesting user (M1.4); absent that, resolution degrades
	// to default profile semantics.
	request.StyleSlug = run.StyleSlug
	if orchestrator.Subject != nil {
		request.Subject = orchestrator.Subject(run)
	}
	if err := request.Validate(); err != nil {
		return ExecutionRequest{}, err
	}
	// Context wiring (M4a shadow, M4b activation): compile and persist the
	// envelope, then inject it into the request. Infrastructure failures
	// degrade to a nil envelope; a capability that opted into
	// EnforceRequiredContext fails the node on required-missing (M4b).
	envelope, contextErr := orchestrator.compileNodeContext(ctx, run, node, attemptNumber, manifest)
	if contextErr != nil {
		return ExecutionRequest{}, contextErr
	}
	request.Context = envelope
	return request, nil
}

// observeResult is the Observe verb's validation half: result binding,
// budget accounting, and the shadow-leak kernel invariant. It returns the
// first violation, or nil when the result may be committed.
func observeResult(result ExecutionResult, request ExecutionRequest, node writingplan.PlanNode, spentCost float64, spentDuration int64, run writingstore.RuntimeRun) error {
	if err := result.Validate(request); err != nil {
		return err
	}
	if result.Usage.CostUSD > node.Bounds.MaxCostUSD || spentCost+result.Usage.CostUSD > run.Budget.MaxCostUSD || result.Usage.DurationMS > node.Bounds.TimeoutMS || spentDuration+result.Usage.DurationMS > run.Budget.MaxDurationMS {
		return ErrRuntimeBudget
	}
	if containsShadowContentRef(result.Artifacts) {
		return runtimeError(CodeArtifactCommitFailed, RetryNever,
			"canonical artifacts cannot reference shadow content", ErrShadowContentLeak)
	}
	return nil
}

// dispatchAttempt is the Dispatch verb's execution half: the timed executor
// invocation under the run control, with pause/cancel observation. It never
// interprets the result — the caller owns Observe and Decide.
func dispatchAttempt(ctx context.Context, orchestrator *Orchestrator, control *runControl, executor Executor, request ExecutionRequest, timeoutMS int64) (ExecutionResult, error) {
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	setActiveExecution(control, executor, request.Handle(), cancel)
	result, executeErr := executor.Execute(execCtx, request)
	cancel()
	clearActiveExecution(control)
	return result, executeErr
}

// errAttemptExhausted names the bound violation so the orchestrator's fail
// path stays readable.
var errAttemptExhausted = errors.New("attempt bound exhausted")

// lifecycleSnapshot assembles the M3 hook view for one moment from the
// loop's kernel bookkeeping (M3, docs/27). Artifacts appear as types only —
// hooks observe shape, never canonical bytes or store handles.
func lifecycleSnapshot(run writingstore.RuntimeRun, planID string, planVersion int, node writingplan.PlanNode, attemptNumber int, state RunState, completed map[string]int, cost float64, durationMS int64, artifactTypes []string) LifecycleSnapshot {
	completedNodes := make([]string, 0, len(completed))
	for nodeID, attempt := range completed {
		if attempt > 0 {
			completedNodes = append(completedNodes, nodeID)
		}
	}
	sort.Strings(completedNodes)
	if artifactTypes == nil {
		artifactTypes = []string{}
	}
	return LifecycleSnapshot{RunID: run.RunID, DocumentID: run.DocumentID,
		PlanID: planID, PlanVersion: planVersion, NodeID: node.NodeID,
		Capability: node.Capability, Attempt: attemptNumber, State: state,
		CompletedNodes: completedNodes, CostUSD: cost, DurationMS: durationMS,
		ArtifactTypes: artifactTypes}
}
