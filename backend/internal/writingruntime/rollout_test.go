package writingruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
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

// TestAllowlistAndPercentageRouteBySubject pins that rollout audiences route
// by the explicit execution subject (user/tenant) when present and fall back
// to the run id otherwise.
func TestAllowlistAndPercentageRouteBySubject(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, request.Node.Capability, request.Node.CapabilityVersion)
	policy.Mode, policy.AllowSubjects, policy.ActivationKey = RolloutAllowlist, []string{"user_alice"}, "approved-subjects-1"
	policy, _ = policy.WithComputedHash()

	request.Subject = "user_alice"
	matched, err := DecideRoute(policy, request, time.Now().UTC())
	if err != nil || matched.Lane != LaneCandidate || matched.Reason != "allowlist_match" {
		t.Fatalf("subject match=%#v error=%v", matched, err)
	}
	request.Subject = "user_bob"
	missed, _ := DecideRoute(policy, request, time.Now().UTC())
	if missed.Lane != LaneBaseline || missed.Reason != "allowlist_miss" {
		t.Fatalf("other subject=%#v", missed)
	}
	request.Subject = ""
	fallbackPolicy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, request.Node.Capability, request.Node.CapabilityVersion)
	fallbackPolicy.Mode, fallbackPolicy.AllowSubjects, fallbackPolicy.ActivationKey = RolloutAllowlist, []string{"run_legacy"}, "approved-subjects-2"
	fallbackPolicy, _ = fallbackPolicy.WithComputedHash()
	byRunID, err := DecideRoute(fallbackPolicy, request, time.Now().UTC())
	if err != nil || byRunID.Lane != LaneCandidate || byRunID.Reason != "allowlist_match" {
		t.Fatalf("run-id subject match=%#v error=%v", byRunID, err)
	}

	policy.Mode, policy.BasisPoints, policy.AllowSubjects = RolloutPercentage, 10000, []string{}
	policy, _ = policy.WithComputedHash()
	request.RunID = "run_legacy"
	request.Subject = "user_carol"
	bucketed, _ := DecideRoute(policy, request, time.Now().UTC())
	if bucketed.SubjectBucket < 0 || bucketed.SubjectBucket > 9999 {
		t.Fatalf("bucket=%d", bucketed.SubjectBucket)
	}
	request.Subject = "user_dave"
	other, _ := DecideRoute(policy, request, time.Now().UTC())
	if other.SubjectBucket == bucketed.SubjectBucket {
		t.Fatal("distinct subjects must bucket independently")
	}
}

// TestExecutionResultRejectsDuplicateOutputType pins contract precision: a
// result may not produce two artifacts of the same declared type.
func TestExecutionResultRejectsDuplicateOutputType(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	now := time.Now().UTC()
	validDraft := OutputArtifactDraft{OutputKey: "draft", ArtifactType: "full_draft", ContentHash: hashForTest("draft"),
		MediaType: "text/markdown", ContentRef: "memory://draft", Parents: []writingstore.ArtifactRef{{ArtifactID: "art_contract", Version: 1}},
		Producer: request.Node.Capability, CapabilityVersion: request.Node.CapabilityVersion,
		InputHashes: []string{request.Inputs[0].ContentHash}, Provenance: map[string]any{}, SourceRefs: []string{}}
	duplicate := validDraft
	duplicate.OutputKey = "draft_alt"
	duplicate.ContentHash = hashForTest("draft-alt")
	result := ExecutionResult{Artifacts: []OutputArtifactDraft{validDraft, duplicate},
		Usage: ExecutionUsage{CostUSD: 1, DurationMS: 1}, StartedAt: now.Add(-time.Millisecond), CompletedAt: now}
	if err := result.Validate(request); ErrorCodeOf(err) != CodeExecutorOutputInvalid || !strings.Contains(err.Error(), "duplicate output artifact type") {
		t.Fatalf("err=%v code=%s", err, ErrorCodeOf(err))
	}
}

// mustShadowCandidate builds a provably shadow-isolated candidate adapter:
// canonical staging goes to a counting gateway, candidate staging to a memory
// shadow sink.
func mustShadowCandidate(t *testing.T, request ExecutionRequest, runner LegacyNodeRunner) (*LegacyExecutor, *stageCountingGateway, *MemoryShadowContentSink) {
	t.Helper()
	canonical := &stageCountingGateway{inner: &memoryGateway{body: []byte("contract")}}
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, request.Node.Capability, request.Node.CapabilityVersion)
	shadowGateway, err := NewShadowContentGateway(canonical, NewMemoryShadowContentSink(), policy)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := NewShadowIsolatedExecutorAdapter(AdapterFamilyEngine,
		ExecutorDescriptor{ExecutorID: "candidate.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}},
		request.Node.Capability, request.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, shadowGateway, runner)
	if err != nil {
		t.Fatal(err)
	}
	return candidate, canonical, shadowGateway.writes.(*MemoryShadowContentSink)
}

func TestShadowExecutesBothLanesButReturnsOnlyBaseline(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	baseline := &fakeGovernedExecutor{descriptor: ExecutorDescriptor{ExecutorID: "baseline.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}}
	candidateRunner := &fakeLegacyRunner{usage: LegacyUsage{Measured: true, CostUSD: .5, InputTokens: 2, OutputTokens: 3},
		outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", MediaType: "text/markdown", Body: []byte("candidate"), Provenance: map[string]any{}, SourceRefs: []string{"source-a"}}}}
	candidate, canonical, sink := mustShadowCandidate(t, request, candidateRunner)
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, request.Node.Capability, request.Node.CapabilityVersion)
	provider, _ := NewMutableRolloutPolicyProvider(policy)
	evidence := &MemoryRolloutEvidenceStore{}
	metrics := &metricCapture{}
	executor, err := NewShadowRolloutExecutor(baseline, candidate, provider, evidence, metrics)
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
	if canonical.stages != 0 || len(sink.Keys()) != 1 {
		t.Fatalf("shadow staging leaked: canonical=%d sink=%v", canonical.stages, sink.Keys())
	}
}

// TestShadowRolloutRequiresProvableIsolation pins the construction contract:
// a canonical-gateway candidate cannot enter a shadow rollout executor, and a
// shadow-isolated candidate cannot enter a candidate-authoritative executor.
func TestShadowRolloutRequiresProvableIsolation(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	baseline := &fakeGovernedExecutor{descriptor: ExecutorDescriptor{ExecutorID: "baseline.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}}
	canonicalCandidate, err := NewLegacyExecutorAdapter(AdapterFamilyEngine,
		ExecutorDescriptor{ExecutorID: "candidate.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}},
		request.Node.Capability, request.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, &memoryGateway{body: []byte("contract")}, &fakeLegacyRunner{})
	if err != nil {
		t.Fatal(err)
	}
	metrics := &metricCapture{}
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, request.Node.Capability, request.Node.CapabilityVersion)
	provider, _ := NewMutableRolloutPolicyProvider(policy)
	if _, err := NewShadowRolloutExecutor(baseline, canonicalCandidate, provider, &MemoryRolloutEvidenceStore{}, metrics); ErrorCodeOf(err) != CodeRolloutPolicyInvalid {
		t.Fatalf("canonical candidate accepted by shadow rollout: %v", err)
	}
	if !metrics.has(MetricAuthorityViolation, "rejected") {
		t.Fatalf("metrics=%#v", metrics.metrics)
	}
	isolatedCandidate, _, _ := mustShadowCandidate(t, request, &fakeLegacyRunner{})
	if _, err := NewRolloutExecutor(baseline, isolatedCandidate, provider, &MemoryRolloutEvidenceStore{}, nil); ErrorCodeOf(err) != CodeRolloutPolicyInvalid {
		t.Fatalf("shadow-isolated candidate accepted by authoritative rollout: %v", err)
	}
}

// TestShadowExecutorNeverDispatchesCandidateLane pins that mode promotion
// inside a shadow executor cannot create candidate traffic: candidate lanes
// fail closed to baseline with an authority-violation record.
func TestShadowExecutorNeverDispatchesCandidateLane(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	baseline := &fakeGovernedExecutor{descriptor: ExecutorDescriptor{ExecutorID: "baseline.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}}
	candidateRunner := &fakeLegacyRunner{usage: LegacyUsage{Measured: true}, outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", MediaType: "text/markdown", Body: []byte("candidate"), Provenance: map[string]any{}, SourceRefs: []string{}}}}
	candidate, _, _ := mustShadowCandidate(t, request, candidateRunner)
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, request.Node.Capability, request.Node.CapabilityVersion)
	policy.Mode, policy.ActivationKey = RolloutEnabled, "approved-change-99"
	policy, _ = policy.WithComputedHash()
	provider, _ := NewMutableRolloutPolicyProvider(policy)
	evidence := &MemoryRolloutEvidenceStore{}
	metrics := &metricCapture{}
	executor, err := NewShadowRolloutExecutor(baseline, candidate, provider, evidence, metrics)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), request)
	if err != nil || len(result.Artifacts) != 1 || result.Artifacts[0].ContentHash != hashForTest("draft") {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if candidateRunner.calls != 0 {
		t.Fatalf("candidate dispatched %d times in a shadow executor", candidateRunner.calls)
	}
	if !metrics.has(MetricAuthorityViolation, "candidate_lane_blocked") {
		t.Fatalf("metrics=%#v", metrics.metrics)
	}
	found := false
	for _, record := range evidence.Records() {
		if record.Status == "candidate_lane_blocked" {
			found = true
		}
	}
	if !found {
		t.Fatal("candidate-lane block was not recorded as evidence")
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
	candidate, _, _ := mustShadowCandidate(t, request, candidateRunner)
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, request.Node.Capability, request.Node.CapabilityVersion)
	provider, _ := NewMutableRolloutPolicyProvider(policy)
	evidence := &MemoryRolloutEvidenceStore{}
	executor, _ := NewShadowRolloutExecutor(baseline, candidate, provider, evidence, nil)
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
