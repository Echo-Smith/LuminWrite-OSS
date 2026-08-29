package writingruntime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

func TestDefaultRolloutPolicyIsValidShadow(t *testing.T) {
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, "core.draft.generate", "1.0.0")
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	decision, err := DecideRoute(policy, legacyRequest([]byte("contract")), time.Now().UTC())
	if err != nil || !decision.RunShadow || decision.Lane != LaneBaseline || decision.Reason != "shadow" {
		t.Fatalf("decision=%#v error=%v", decision, err)
	}
}

func TestPercentageDecisionIsDeterministicAndKillSwitchWins(t *testing.T) {
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, "core.draft.generate", "1.0.0")
	policy.Mode, policy.ActivationKey, policy.BasisPoints = RolloutPercentage, "approved-change-42", 5000
	policy, _ = policy.WithComputedHash()
	request := legacyRequest([]byte("contract"))
	first, err := DecideRoute(policy, request, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	second, _ := DecideRoute(policy, request, time.Now().UTC().Add(time.Hour))
	if first.SubjectBucket != second.SubjectBucket || first.Lane != second.Lane {
		t.Fatalf("non-deterministic decisions: %#v %#v", first, second)
	}
	policy.KillSwitch = true
	policy, _ = policy.WithComputedHash()
	killed, err := DecideRoute(policy, request, time.Now().UTC())
	if err != nil || killed.Lane != LaneBaseline || killed.RunShadow || killed.Reason != "kill_switch" {
		t.Fatalf("killed=%#v error=%v", killed, err)
	}
}

func TestRolloutPolicyRejectsUnapprovedAuthoritativeMode(t *testing.T) {
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, "core.draft.generate", "1.0.0")
	policy.Mode = RolloutEnabled
	policy, _ = policy.WithComputedHash()
	if err := policy.Validate(); ErrorCodeOf(err) != CodeRolloutPolicyInvalid {
		t.Fatalf("error=%v code=%s", err, ErrorCodeOf(err))
	}
}

func TestShadowExecutesBothLanesButReturnsOnlyBaseline(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	baseline := &fakeGovernedExecutor{descriptor: ExecutorDescriptor{ExecutorID: "baseline.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}}
	candidateRunner := &fakeLegacyRunner{usage: LegacyUsage{Measured: true, CostUSD: .5, InputTokens: 2, OutputTokens: 3},
		outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", MediaType: "text/markdown", Body: []byte("candidate"), Provenance: map[string]any{}, SourceRefs: []string{"source-a"}}}}
	candidate, err := NewLegacyExecutorAdapter(AdapterFamilyEngine,
		ExecutorDescriptor{ExecutorID: "candidate.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}},
		request.Node.Capability, request.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, &memoryGateway{body: []byte("contract")}, candidateRunner)
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, request.Node.Capability, request.Node.CapabilityVersion)
	provider, _ := NewMutableRolloutPolicyProvider(policy)
	evidence := &MemoryRolloutEvidenceStore{}
	metrics := &metricCapture{}
	executor, err := NewRolloutExecutor(baseline, candidate, provider, evidence, metrics)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), request)
	if err != nil || len(result.Artifacts) != 1 || result.Artifacts[0].ContentHash != hashForTest("draft") {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if baseline.calls != 1 || candidateRunner.calls != 1 {
		t.Fatalf("baseline=%d candidate=%d", baseline.calls, candidateRunner.calls)
	}
	records := evidence.Records()
	if len(records) != 4 || records[3].Kind != "shadow_comparison" || records[3].Comparison == nil || records[3].Comparison.ContentMatch {
		t.Fatalf("evidence=%#v", records)
	}
	if !metrics.has(MetricRouteDecision, "selected") || !metrics.has(MetricShadowComparison, "different") {
		t.Fatalf("metrics=%#v", metrics.metrics)
	}
}

func TestEvidenceFailurePreventsCandidateAuthorityAndFallsBackToBaseline(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	baseline := &fakeGovernedExecutor{descriptor: ExecutorDescriptor{ExecutorID: "baseline.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}}
	candidateRunner := &fakeLegacyRunner{usage: LegacyUsage{Measured: true}, outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", MediaType: "text/markdown", Body: []byte("candidate"), Provenance: map[string]any{}, SourceRefs: []string{}}}}
	candidate, _ := NewLegacyExecutorAdapter(AdapterFamilyEngine,
		ExecutorDescriptor{ExecutorID: "candidate.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}},
		request.Node.Capability, request.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, &memoryGateway{body: []byte("contract")}, candidateRunner)
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, request.Node.Capability, request.Node.CapabilityVersion)
	policy.Mode, policy.ActivationKey = RolloutEnabled, "approved-change-99"
	policy, _ = policy.WithComputedHash()
	provider, _ := NewMutableRolloutPolicyProvider(policy)
	executor, _ := NewRolloutExecutor(baseline, candidate, provider, &MemoryRolloutEvidenceStore{err: errors.New("audit unavailable")}, nil)
	result, err := executor.Execute(context.Background(), request)
	if err != nil || result.Artifacts[0].ContentHash != hashForTest("draft") || baseline.calls != 1 || candidateRunner.calls != 0 {
		t.Fatalf("result=%#v error=%v baseline=%d candidate=%d", result, err, baseline.calls, candidateRunner.calls)
	}
}

func TestShadowCandidateFailureCannotReplaceBaselineResult(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	baseline := &fakeGovernedExecutor{descriptor: ExecutorDescriptor{ExecutorID: "baseline.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}}
	candidateRunner := &fakeLegacyRunner{err: errors.New("candidate unavailable")}
	candidate, _ := NewLegacyExecutorAdapter(AdapterFamilyEngine,
		ExecutorDescriptor{ExecutorID: "candidate.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}},
		request.Node.Capability, request.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, &memoryGateway{body: []byte("contract")}, candidateRunner)
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, request.Node.Capability, request.Node.CapabilityVersion)
	provider, _ := NewMutableRolloutPolicyProvider(policy)
	evidence := &MemoryRolloutEvidenceStore{}
	executor, _ := NewRolloutExecutor(baseline, candidate, provider, evidence, nil)
	result, err := executor.Execute(context.Background(), request)
	if err != nil || result.Artifacts[0].ContentHash != hashForTest("draft") {
		t.Fatalf("baseline result=%#v error=%v", result, err)
	}
	records := evidence.Records()
	comparison := records[len(records)-1].Comparison
	if comparison == nil || comparison.CandidateStatus != "failed" || comparison.CandidateError != CodeExecutionFailed {
		t.Fatalf("comparison=%#v records=%#v", comparison, records)
	}
}

type metricCapture struct {
	mu      sync.Mutex
	metrics []RuntimeMetric
}

func (capture *metricCapture) Observe(_ context.Context, metric RuntimeMetric) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.metrics = append(capture.metrics, metric)
}

func (capture *metricCapture) has(kind MetricKind, status string) bool {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	for _, metric := range capture.metrics {
		if metric.Kind == kind && metric.Status == status {
			return true
		}
	}
	return false
}
