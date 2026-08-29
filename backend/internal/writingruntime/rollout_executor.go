package writingruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type RolloutExecutor struct {
	baseline  Executor
	candidate ExecutorAdapter
	// shadowOnly marks an executor built for shadow rollout: the candidate is
	// provably shadow-isolated and candidate-authoritative lanes are refused
	// at execution time.
	shadowOnly bool
	policies   RolloutPolicyProvider
	evidence   RolloutEvidenceStore
	telemetry  RuntimeTelemetry
	now        func() time.Time

	shadowFailures     atomic.Int64
	shadowCircuitOpen  atomic.Bool
}

// NewRolloutExecutor builds the candidate-authoritative rollout executor.
// The candidate must stage through the canonical gateway: promoting traffic
// to allowlist/percentage/enabled requires this explicit construction.
func NewRolloutExecutor(baseline Executor, candidate ExecutorAdapter, policies RolloutPolicyProvider, evidence RolloutEvidenceStore, telemetry RuntimeTelemetry) (*RolloutExecutor, error) {
	if isolated, ok := candidate.(ShadowIsolatedCandidate); ok && isolated.ShadowGateway() != nil {
		observeRuntime(context.Background(), telemetry, RuntimeMetric{Kind: MetricAuthorityViolation,
			Family: candidate.AdapterPolicy().Family, ExecutorID: candidate.Descriptor().ExecutorID,
			Status: "rejected", ErrorCode: CodeRolloutPolicyInvalid})
		return nil, rolloutPolicyError("candidate-authoritative rollout cannot wrap a shadow-isolated candidate; construct a shadow rollout executor instead")
	}
	return newRolloutExecutor(baseline, candidate, false, policies, evidence, telemetry)
}

// NewShadowRolloutExecutor builds the shadow rollout executor. The candidate
// must prove shadow isolation through ShadowIsolatedCandidate, so a wrongly
// wired canonical gateway fails at construction instead of leaking content
// into the canonical store before the commit guard rejects it.
func NewShadowRolloutExecutor(baseline Executor, candidate ExecutorAdapter, policies RolloutPolicyProvider, evidence RolloutEvidenceStore, telemetry RuntimeTelemetry) (*RolloutExecutor, error) {
	isolated, ok := candidate.(ShadowIsolatedCandidate)
	if !ok || isolated.ShadowGateway() == nil {
		observeRuntime(context.Background(), telemetry, RuntimeMetric{Kind: MetricAuthorityViolation,
			Family: candidateFamily(candidate), ExecutorID: candidateExecutorID(candidate),
			Status: "rejected", ErrorCode: CodeRolloutPolicyInvalid})
		return nil, rolloutPolicyError("shadow rollout requires a shadow-isolated candidate adapter")
	}
	return newRolloutExecutor(baseline, candidate, true, policies, evidence, telemetry)
}

func candidateFamily(candidate ExecutorAdapter) AdapterFamily {
	if candidate == nil {
		return ""
	}
	return candidate.AdapterPolicy().Family
}

func candidateExecutorID(candidate ExecutorAdapter) string {
	if candidate == nil {
		return ""
	}
	return candidate.Descriptor().ExecutorID
}

func newRolloutExecutor(baseline Executor, candidate ExecutorAdapter, shadowOnly bool, policies RolloutPolicyProvider, evidence RolloutEvidenceStore, telemetry RuntimeTelemetry) (*RolloutExecutor, error) {
	if baseline == nil || candidate == nil || policies == nil || evidence == nil {
		return nil, ErrRuntimeNotReady
	}
	if err := baseline.Descriptor().Validate(); err != nil {
		return nil, err
	}
	if err := candidate.Descriptor().Validate(); err != nil {
		return nil, err
	}
	if err := candidate.AdapterPolicy().Validate(); err != nil {
		observeRuntime(context.Background(), telemetry, RuntimeMetric{Kind: MetricAuthorityViolation,
			Family: candidate.AdapterPolicy().Family, ExecutorID: candidate.Descriptor().ExecutorID,
			Status: "rejected", ErrorCode: ErrorCodeOf(err)})
		return nil, err
	}
	if candidate.AdapterPolicy().TrafficMode != AdapterTrafficOffline {
		return nil, rolloutPolicyError("candidate must remain offline behind rollout executor")
	}
	return &RolloutExecutor{baseline: baseline, candidate: candidate, shadowOnly: shadowOnly,
		policies: policies, evidence: evidence, telemetry: telemetry,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

func (executor *RolloutExecutor) Descriptor() ExecutorDescriptor {
	return executor.baseline.Descriptor()
}

func (executor *RolloutExecutor) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	policy, err := executor.policies.Policy(ctx, request.Identity())
	if err != nil {
		observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricRouteDecision,
			Capability: request.Node.Capability, Mode: RolloutOff, Lane: LaneBaseline,
			Status: "policy_failed", Reason: "fail_closed", ErrorCode: ErrorCodeOf(err)})
		return executor.executeLane(ctx, LaneBaseline, executor.baseline, request, RolloutOff)
	}
	if err := bindRolloutPolicy(policy, executor.candidate); err != nil {
		observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricRouteDecision, Family: policy.Family,
			ExecutorID: policy.ExecutorID, Capability: request.Node.Capability, Mode: policy.Mode,
			Lane: LaneBaseline, Status: "rejected", Reason: "binding_mismatch", ErrorCode: ErrorCodeOf(err)})
		return executor.executeLane(ctx, LaneBaseline, executor.baseline, request, RolloutOff)
	}
	decision, err := DecideRoute(policy, request, executor.now())
	if err != nil {
		observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricRouteDecision, Family: policy.Family,
			ExecutorID: policy.ExecutorID, Capability: request.Node.Capability, Mode: policy.Mode,
			Lane: LaneBaseline, Status: "policy_failed", Reason: "fail_closed", ErrorCode: ErrorCodeOf(err)})
		return executor.executeLane(ctx, LaneBaseline, executor.baseline, request, RolloutOff)
	}
	observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricRouteDecision, Family: policy.Family,
		ExecutorID: policy.ExecutorID, Capability: request.Node.Capability, Mode: decision.Mode,
		Lane: decision.Lane, Status: "selected", Reason: decision.Reason})
	if err := executor.record(ctx, RuntimeEvidence{Kind: "route_decision", Identity: request.Identity(),
		Adapter: executor.candidate.AdapterPolicy(), PolicyHash: policy.PolicyHash, PolicyVersion: policy.PolicyVersion,
		Mode: policy.Mode, Lane: decision.Lane, Decision: decision, Status: "selected"}); err != nil {
		observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricRouteDecision, Family: policy.Family,
			ExecutorID: policy.ExecutorID, Capability: request.Node.Capability, Mode: policy.Mode,
			Lane: LaneBaseline, Status: "evidence_failed", Reason: "fail_closed", ErrorCode: CodeRolloutEvidenceFailed})
		return executor.executeLane(ctx, LaneBaseline, executor.baseline, request, RolloutOff)
	}
	if decision.RunShadow {
		if !executor.shadowOnly {
			// Only a shadow rollout executor may run the shadow lane: an
			// authoritative executor's candidate stages through the canonical
			// gateway, so shadow traffic would leak into canonical storage.
			return executor.rejectRoute(ctx, request, policy, decision,
				"shadow_mode_unavailable", CodeExecutorTrafficDisabled)
		}
		gateway := executor.shadowGateway()
		if gateway == nil || gateway.PolicyHash() != policy.PolicyHash {
			// The policy provider can rotate while the isolated gateway is
			// pinned to one policy namespace: refuse to stage into a stale
			// namespace until the executor is rebuilt.
			return executor.rejectRoute(ctx, request, policy, decision,
				"stale_shadow_namespace", CodeRolloutPolicyInvalid)
		}
		if executor.shadowCircuitOpen.Load() {
			observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricShadowComparison,
				Family: policy.Family, ExecutorID: policy.ExecutorID, Capability: request.Node.Capability,
				Mode: policy.Mode, Lane: LaneShadow, Status: "circuit_open"})
			_ = executor.record(ctx, RuntimeEvidence{Kind: "route_decision", Identity: request.Identity(),
				Adapter: executor.candidate.AdapterPolicy(), PolicyHash: policy.PolicyHash,
				PolicyVersion: policy.PolicyVersion, Mode: policy.Mode, Lane: LaneShadow,
				Decision: decision, Status: "shadow_circuit_open", ErrorCode: CodeExecutionFailed})
			return executor.executeLane(ctx, LaneBaseline, executor.baseline, request, policy.Mode)
		}
		return executor.executeShadow(ctx, request, policy, decision)
	}
	if executor.shadowOnly && decision.Lane == LaneCandidate {
		observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricAuthorityViolation,
			Family: policy.Family, ExecutorID: policy.ExecutorID, Capability: request.Node.Capability,
			Mode: policy.Mode, Lane: LaneCandidate, Status: "candidate_lane_blocked",
			ErrorCode: CodeExecutorTrafficDisabled})
		_ = executor.record(ctx, RuntimeEvidence{Kind: "route_decision", Identity: request.Identity(),
			Adapter: executor.candidate.AdapterPolicy(), PolicyHash: policy.PolicyHash,
			PolicyVersion: policy.PolicyVersion, Mode: policy.Mode, Lane: LaneCandidate,
			Decision: decision, Status: "candidate_lane_blocked", ErrorCode: CodeExecutorTrafficDisabled})
		return executor.executeLane(ctx, LaneBaseline, executor.baseline, request, policy.Mode)
	}
	if decision.Lane == LaneCandidate {
		result, executeErr := executor.executeLane(ctx, LaneCandidate, executor.candidate, request, policy.Mode)
		evidenceErr := executor.recordExecution(ctx, request, policy, decision, LaneCandidate, result, executeErr)
		if evidenceErr != nil {
			return ExecutionResult{}, runtimeError(CodeRolloutEvidenceFailed, RetrySafe, "candidate result evidence was not persisted", evidenceErr)
		}
		return result, executeErr
	}
	result, executeErr := executor.executeLane(ctx, LaneBaseline, executor.baseline, request, policy.Mode)
	_ = executor.recordExecution(ctx, request, policy, decision, LaneBaseline, result, executeErr)
	return result, executeErr
}

func (executor *RolloutExecutor) shadowGateway() *ShadowContentGateway {
	if isolated, ok := executor.candidate.(ShadowIsolatedCandidate); ok {
		return isolated.ShadowGateway()
	}
	return nil
}

// rejectRoute records an authority violation and serves the baseline lane:
// route-level policy/engineering mismatches never reach either lane's code.
func (executor *RolloutExecutor) rejectRoute(ctx context.Context, request ExecutionRequest, policy AdapterRolloutPolicy, decision RouteDecision, status string, code ErrorCode) (ExecutionResult, error) {
	observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricAuthorityViolation,
		Family: policy.Family, ExecutorID: policy.ExecutorID, Capability: request.Node.Capability,
		Mode: policy.Mode, Lane: decision.Lane, Status: status, ErrorCode: code})
	_ = executor.record(ctx, RuntimeEvidence{Kind: "route_decision", Identity: request.Identity(),
		Adapter: executor.candidate.AdapterPolicy(), PolicyHash: policy.PolicyHash,
		PolicyVersion: policy.PolicyVersion, Mode: policy.Mode, Lane: decision.Lane,
		Decision: decision, Status: status, ErrorCode: code})
	return executor.executeLane(ctx, LaneBaseline, executor.baseline, request, RolloutOff)
}

type laneResult struct {
	result ExecutionResult
	err    error
}

// The shadow lane runs under a supervisor so a misbehaving candidate can
// never change the baseline business outcome: it gets its own deadline
// (double the node's own timeout), its panics are contained, and repeated
// failures open a circuit that stops dispatching the lane entirely.
const shadowSupervisorMultiplier = 2

const shadowCircuitBreakerThreshold = 3

func (executor *RolloutExecutor) shadowPatience(request ExecutionRequest) time.Duration {
	return time.Duration(request.Node.Bounds.TimeoutMS) * shadowSupervisorMultiplier * time.Millisecond
}

// executeShadow runs the baseline synchronously on the caller's goroutine and
// the shadow lane under supervision. Baseline evidence is recorded on the
// critical path; the shadow result, comparison, and validator summary are
// finalized asynchronously so baseline callers never wait for the shadow lane.
func (executor *RolloutExecutor) executeShadow(ctx context.Context, request ExecutionRequest, policy AdapterRolloutPolicy, decision RouteDecision) (ExecutionResult, error) {
	shadowCh := make(chan laneResult, 1)
	go executor.runSupervisedShadow(ctx, request, policy.Mode, shadowCh)
	baseline, baselineErr := executor.executeLane(ctx, LaneBaseline, executor.baseline, request, policy.Mode)
	baselineEvidenceErr := executor.recordExecution(ctx, request, policy, decision, LaneBaseline, baseline, baselineErr)
	if baselineEvidenceErr != nil {
		observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricShadowComparison,
			Family: policy.Family, ExecutorID: policy.ExecutorID, Capability: request.Node.Capability,
			Mode: policy.Mode, Lane: LaneBaseline, Status: "evidence_failed", ErrorCode: CodeRolloutEvidenceFailed})
	}
	// The orchestrator cancels the execution context as soon as Execute
	// returns; the finalize path must outlive that cancellation.
	finalizeCtx := context.WithoutCancel(ctx)
	go executor.finalizeShadowComparison(finalizeCtx, request, policy, decision,
		laneResult{result: baseline, err: baselineErr}, shadowCh)
	return baseline, baselineErr
}

func (executor *RolloutExecutor) runSupervisedShadow(ctx context.Context, request ExecutionRequest, mode RolloutMode, shadowCh chan<- laneResult) {
	defer func() {
		if recovered := recover(); recovered != nil {
			shadowCh <- laneResult{err: runtimeError(CodeExecutionFailed, RetryNever,
				"shadow candidate panicked", fmt.Errorf("%v", recovered))}
		}
	}()
	shadowCtx, cancel := context.WithTimeout(ctx, executor.shadowPatience(request))
	defer cancel()
	result, err := executor.executeLane(shadowCtx, LaneShadow, executor.candidate, request, mode)
	shadowCh <- laneResult{result: result, err: err}
}

func (executor *RolloutExecutor) finalizeShadowComparison(ctx context.Context, request ExecutionRequest, policy AdapterRolloutPolicy, decision RouteDecision, baseline laneResult, shadowCh <-chan laneResult) {
	timer := time.NewTimer(executor.shadowPatience(request))
	defer timer.Stop()
	var shadow laneResult
	timedOut := false
	select {
	case outcome := <-shadowCh:
		shadow = outcome
	case <-timer.C:
		timedOut = true
		shadow = laneResult{err: runtimeError(CodeExecutionFailed, RetryNever,
			"shadow candidate exceeded supervisor patience", context.DeadlineExceeded)}
	}
	shadowEvidenceErr := executor.recordExecution(ctx, request, policy, decision, LaneShadow, shadow.result, shadow.err)
	comparison := compareLaneResults(baseline, shadow)
	comparison.ValidatorSummary = executor.shadowValidatorSummary(ctx, shadow.result)
	comparisonStatus := "different"
	if timedOut {
		comparisonStatus = "shadow_timeout"
	} else if comparison.ContractMatch && comparison.ContentMatch && comparison.BaselineError == "" && comparison.CandidateError == "" {
		comparisonStatus = "equivalent"
	}
	observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricShadowComparison,
		Family: policy.Family, ExecutorID: policy.ExecutorID, Capability: request.Node.Capability,
		Mode: policy.Mode, Lane: LaneShadow, Status: comparisonStatus, ErrorCode: comparison.CandidateError})
	comparisonEvidenceErr := executor.record(ctx, RuntimeEvidence{Kind: "shadow_comparison",
		Identity: request.Identity(), Adapter: executor.candidate.AdapterPolicy(),
		PolicyHash: policy.PolicyHash, PolicyVersion: policy.PolicyVersion,
		Mode: policy.Mode, Lane: LaneShadow, Decision: decision, Status: comparisonStatus, Comparison: &comparison})
	if shadowEvidenceErr != nil || comparisonEvidenceErr != nil {
		observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricShadowComparison,
			Family: policy.Family, ExecutorID: policy.ExecutorID, Capability: request.Node.Capability,
			Mode: policy.Mode, Lane: LaneShadow, Status: "evidence_failed", ErrorCode: CodeRolloutEvidenceFailed})
		executor.noteShadowFailure()
		return
	}
	if timedOut || shadow.err != nil {
		executor.noteShadowFailure()
		return
	}
	executor.shadowFailures.Store(0)
}

// noteShadowFailure counts consecutive shadow lane failures (crash, timeout,
// or evidence loss) and opens the shadow circuit once the threshold is hit.
// An open circuit stops dispatching the shadow lane until the executor is
// rebuilt; the specification forbids promoting such a policy anyway.
func (executor *RolloutExecutor) noteShadowFailure() {
	if executor.shadowCircuitOpen.Load() {
		return
	}
	if failures := executor.shadowFailures.Add(1); failures >= shadowCircuitBreakerThreshold {
		if executor.shadowCircuitOpen.CompareAndSwap(false, true) {
			observeRuntime(context.Background(), executor.telemetry, RuntimeMetric{Kind: MetricShadowComparison,
				Family: executor.candidate.AdapterPolicy().Family, ExecutorID: executor.candidate.Descriptor().ExecutorID,
				Lane: LaneShadow, Status: "circuit_opened", ErrorCode: CodeExecutionFailed})
		}
	}
}

// shadowValidatorSummary extracts real validator results from quality report
// outputs the shadow candidate staged, so promotion decisions see actual
// validator statuses rather than only content hashes.
func (executor *RolloutExecutor) shadowValidatorSummary(ctx context.Context, result ExecutionResult) []ValidatorSummaryLine {
	gateway := executor.shadowGateway()
	if gateway == nil {
		return nil
	}
	lines := make([]ValidatorSummaryLine, 0)
	for _, artifact := range result.Artifacts {
		if artifact.ArtifactType != "quality_report" {
			continue
		}
		body, err := gateway.LoadShadow(ctx, artifact.ContentRef)
		if err != nil {
			continue
		}
		var report writingkernel.QualityReport
		if json.Unmarshal(body, &report) != nil {
			continue
		}
		for _, validator := range report.Validators {
			lines = append(lines, ValidatorSummaryLine{ValidatorID: validator.ValidatorID,
				Version: validator.Version, Status: string(validator.Status)})
		}
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].ValidatorID < lines[j].ValidatorID })
	return lines
}

func (executor *RolloutExecutor) executeLane(ctx context.Context, lane ExecutionLane, target Executor, request ExecutionRequest, mode RolloutMode) (ExecutionResult, error) {
	started := executor.now()
	metric := RuntimeMetric{Kind: MetricExecution, ExecutorID: target.Descriptor().ExecutorID,
		Capability: request.Node.Capability, Mode: mode, Lane: lane, Status: "started"}
	if adapter, ok := target.(ExecutorAdapter); ok {
		metric.Family = adapter.AdapterPolicy().Family
	}
	observeRuntime(ctx, executor.telemetry, metric)
	result, err := target.Execute(ctx, request)
	duration := executor.now().Sub(started).Milliseconds()
	metric = RuntimeMetric{Kind: MetricExecution, ExecutorID: target.Descriptor().ExecutorID,
		Capability: request.Node.Capability, Mode: mode, Lane: lane, Status: "succeeded", DurationMS: duration,
		CostUSD: result.Usage.CostUSD, InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens}
	if adapter, ok := target.(ExecutorAdapter); ok {
		metric.Family = adapter.AdapterPolicy().Family
	}
	if err != nil {
		metric.Status, metric.ErrorCode = "failed", ErrorCodeOf(err)
	}
	observeRuntime(ctx, executor.telemetry, metric)
	return result, err
}

func (executor *RolloutExecutor) recordExecution(ctx context.Context, request ExecutionRequest, policy AdapterRolloutPolicy, decision RouteDecision, lane ExecutionLane, result ExecutionResult, executeErr error) error {
	status := "succeeded"
	if executeErr != nil {
		status = "failed"
	}
	return executor.record(ctx, RuntimeEvidence{Kind: "execution", Identity: request.Identity(),
		Adapter: executor.candidate.AdapterPolicy(), PolicyHash: policy.PolicyHash, PolicyVersion: policy.PolicyVersion,
		Mode: policy.Mode, Lane: lane, Decision: decision, Status: status, ErrorCode: ErrorCodeOf(executeErr),
		Usage: result.Usage, Outputs: outputManifests(result)})
}

func (executor *RolloutExecutor) record(ctx context.Context, evidence RuntimeEvidence) error {
	evidence.RecordedAt = executor.now()
	evidence.EvidenceID = writingstore.StableID("evt_", evidence.Identity.IdempotencyKey,
		evidence.Kind, string(evidence.Lane), evidence.PolicyHash, evidence.Status)
	return executor.evidence.Record(ctx, evidence)
}

func outputManifests(result ExecutionResult) []OutputManifest {
	outputs := make([]OutputManifest, 0, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		outputs = append(outputs, OutputManifest{OutputKey: artifact.OutputKey, ArtifactType: string(artifact.ArtifactType),
			ContentHash: artifact.ContentHash, MediaType: artifact.MediaType, SourceCount: len(artifact.SourceRefs)})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].OutputKey < outputs[j].OutputKey })
	return outputs
}

func compareLaneResults(baseline, candidate laneResult) ShadowComparison {
	baselineOutputs, candidateOutputs := outputManifests(baseline.result), outputManifests(candidate.result)
	comparison := ShadowComparison{BaselineStatus: "succeeded", CandidateStatus: "succeeded",
		ContractMatch: len(baselineOutputs) == len(candidateOutputs), ContentMatch: len(baselineOutputs) == len(candidateOutputs),
		CostDeltaUSD:    candidate.result.Usage.CostUSD - baseline.result.Usage.CostUSD,
		DurationDeltaMS: candidate.result.Usage.DurationMS - baseline.result.Usage.DurationMS,
		BaselineError:   ErrorCodeOf(baseline.err), CandidateError: ErrorCodeOf(candidate.err)}
	if baseline.err != nil {
		comparison.BaselineStatus = "failed"
	}
	if candidate.err != nil {
		comparison.CandidateStatus = "failed"
	}
	baselineSources, candidateSources := 0, 0
	for index := range baselineOutputs {
		baselineSources += baselineOutputs[index].SourceCount
		if index >= len(candidateOutputs) {
			continue
		}
		candidateSources += candidateOutputs[index].SourceCount
		if baselineOutputs[index].OutputKey != candidateOutputs[index].OutputKey || baselineOutputs[index].ArtifactType != candidateOutputs[index].ArtifactType || baselineOutputs[index].MediaType != candidateOutputs[index].MediaType {
			comparison.ContractMatch = false
		}
		if baselineOutputs[index].ContentHash != candidateOutputs[index].ContentHash {
			comparison.ContentMatch = false
		}
	}
	for index := len(baselineOutputs); index < len(candidateOutputs); index++ {
		candidateSources += candidateOutputs[index].SourceCount
	}
	comparison.SourceDelta = candidateSources - baselineSources
	if baseline.err != nil || candidate.err != nil {
		comparison.ContractMatch, comparison.ContentMatch = false, false
	}
	return comparison
}

func (executor *RolloutExecutor) String() string {
	return fmt.Sprintf("rollout(%s -> %s)", executor.baseline.Descriptor().ExecutorID, executor.candidate.Descriptor().ExecutorID)
}
