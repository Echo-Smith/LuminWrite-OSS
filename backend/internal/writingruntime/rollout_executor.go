package writingruntime

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type RolloutExecutor struct {
	baseline  Executor
	candidate ExecutorAdapter
	policies  RolloutPolicyProvider
	evidence  RolloutEvidenceStore
	telemetry RuntimeTelemetry
	now       func() time.Time
}

func NewRolloutExecutor(baseline Executor, candidate ExecutorAdapter, policies RolloutPolicyProvider, evidence RolloutEvidenceStore, telemetry RuntimeTelemetry) (*RolloutExecutor, error) {
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
	return &RolloutExecutor{baseline: baseline, candidate: candidate, policies: policies, evidence: evidence,
		telemetry: telemetry, now: func() time.Time { return time.Now().UTC() }}, nil
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
		return executor.executeShadow(ctx, request, policy, decision)
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

type laneResult struct {
	result ExecutionResult
	err    error
}

func (executor *RolloutExecutor) executeShadow(ctx context.Context, request ExecutionRequest, policy AdapterRolloutPolicy, decision RouteDecision) (ExecutionResult, error) {
	baselineCh, shadowCh := make(chan laneResult, 1), make(chan laneResult, 1)
	go func() {
		result, err := executor.executeLane(ctx, LaneBaseline, executor.baseline, request, policy.Mode)
		baselineCh <- laneResult{result: result, err: err}
	}()
	go func() {
		result, err := executor.executeLane(ctx, LaneShadow, executor.candidate, request, policy.Mode)
		shadowCh <- laneResult{result: result, err: err}
	}()
	baseline, shadow := <-baselineCh, <-shadowCh
	baselineEvidenceErr := executor.recordExecution(ctx, request, policy, decision, LaneBaseline, baseline.result, baseline.err)
	shadowEvidenceErr := executor.recordExecution(ctx, request, policy, decision, LaneShadow, shadow.result, shadow.err)
	comparison := compareLaneResults(baseline, shadow)
	comparisonStatus := "different"
	if comparison.ContractMatch && comparison.ContentMatch && comparison.BaselineError == "" && comparison.CandidateError == "" {
		comparisonStatus = "equivalent"
	}
	observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricShadowComparison, Family: policy.Family,
		ExecutorID: policy.ExecutorID, Capability: request.Node.Capability, Mode: policy.Mode,
		Lane: LaneShadow, Status: comparisonStatus, ErrorCode: comparison.CandidateError})
	comparisonEvidenceErr := executor.record(ctx, RuntimeEvidence{Kind: "shadow_comparison", Identity: request.Identity(),
		Adapter: executor.candidate.AdapterPolicy(), PolicyHash: policy.PolicyHash, PolicyVersion: policy.PolicyVersion,
		Mode: policy.Mode, Lane: LaneShadow, Decision: decision, Status: comparisonStatus, Comparison: &comparison})
	if baselineEvidenceErr != nil || shadowEvidenceErr != nil || comparisonEvidenceErr != nil {
		observeRuntime(ctx, executor.telemetry, RuntimeMetric{Kind: MetricShadowComparison, Family: policy.Family,
			ExecutorID: policy.ExecutorID, Capability: request.Node.Capability, Mode: policy.Mode,
			Lane: LaneShadow, Status: "evidence_failed", ErrorCode: CodeRolloutEvidenceFailed})
	}
	return baseline.result, baseline.err
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
