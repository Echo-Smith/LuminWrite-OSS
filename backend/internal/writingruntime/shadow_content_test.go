package writingruntime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

type stageCountingGateway struct {
	inner  *memoryGateway
	mu     sync.Mutex
	stages int
}

func (gateway *stageCountingGateway) Load(ctx context.Context, input InputArtifact) ([]byte, error) {
	return gateway.inner.Load(ctx, input)
}

func (gateway *stageCountingGateway) Stage(ctx context.Context, key, mediaType string, body []byte) (string, string, error) {
	gateway.mu.Lock()
	gateway.stages++
	gateway.mu.Unlock()
	return gateway.inner.Stage(ctx, key, mediaType, body)
}

func shadowPolicyForTest() AdapterRolloutPolicy {
	return DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, "core.draft.generate", "1.0.0")
}

func TestShadowContentGatewayStagesIntoIsolatedNamespace(t *testing.T) {
	canonical := &stageCountingGateway{inner: &memoryGateway{body: []byte("contract")}}
	sink := NewMemoryShadowContentSink()
	gateway, err := NewShadowContentGateway(canonical, sink, shadowPolicyForTest())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("shadow draft body")
	ref, hash, err := gateway.Stage(context.Background(), "run_shadow:node_write:1:draft", "text/markdown", body)
	if err != nil {
		t.Fatal(err)
	}
	if !IsShadowContentRef(ref) {
		t.Fatalf("ref=%s is not namespaced", ref)
	}
	if hash != contentHash(body) {
		t.Fatalf("hash=%s", hash)
	}
	policyHash := strings.TrimPrefix(shadowPolicyForTest().PolicyHash, "sha256:")
	if !strings.HasPrefix(ref, ShadowContentScheme+policyHash+"/") {
		t.Fatalf("ref=%s is not bound to policy %s", ref, policyHash)
	}
	keys := sink.Keys()
	if len(keys) != 1 || keys[0] != strings.TrimPrefix(ref, ShadowContentScheme) {
		t.Fatalf("sink keys=%v ref=%s", keys, ref)
	}
	if canonical.stages != 0 {
		t.Fatalf("canonical gateway received %d stage calls", canonical.stages)
	}
	loaded, err := gateway.Load(context.Background(), InputArtifact{})
	if err != nil || string(loaded) != "contract" {
		t.Fatalf("loaded=%s err=%v", loaded, err)
	}
}

func TestShadowContentGatewayRejectsUncomputedPolicy(t *testing.T) {
	policy := shadowPolicyForTest()
	policy.PolicyHash = "sha256:deadbeef"
	if _, err := NewShadowContentGateway(&memoryGateway{}, NewMemoryShadowContentSink(), policy); err == nil {
		t.Fatal("expected policy validation failure")
	}
	if _, err := NewShadowContentGateway(nil, NewMemoryShadowContentSink(), shadowPolicyForTest()); !errors.Is(err, ErrRuntimeNotReady) {
		t.Fatalf("err=%v", err)
	}
}

func TestShadowContentGatewaySanitizesStageKeys(t *testing.T) {
	gateway, err := NewShadowContentGateway(&memoryGateway{}, NewMemoryShadowContentSink(), shadowPolicyForTest())
	if err != nil {
		t.Fatal(err)
	}
	ref, _, err := gateway.Stage(context.Background(), "run_x:n/ode..write:1", "text/markdown", []byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimPrefix(ref, ShadowContentScheme)
	if strings.Contains(path, "..") || strings.Contains(path, "//") || strings.Contains(path, ":") {
		t.Fatalf("unsanitized path %s", path)
	}
}

func TestShadowContentGatewaySweepsExpiredAndPurgesRun(t *testing.T) {
	base := time.Date(2026, 8, 29, 8, 0, 0, 0, time.UTC)
	sink := NewMemoryShadowContentSink()
	gateway, err := NewShadowContentGateway(&memoryGateway{}, sink, shadowPolicyForTest())
	if err != nil {
		t.Fatal(err)
	}
	sink.clock = func() time.Time { return base }
	gateway.now = func() time.Time { return base.Add(8 * 24 * time.Hour) }
	if _, _, err := gateway.Stage(context.Background(), "run_expired:node:1:draft", "text/markdown", []byte("old")); err != nil {
		t.Fatal(err)
	}
	sink.clock = func() time.Time { return base.Add(8 * 24 * time.Hour) }
	if _, _, err := gateway.Stage(context.Background(), "run_fresh:node:1:draft", "text/markdown", []byte("new")); err != nil {
		t.Fatal(err)
	}
	removed, err := gateway.SweepExpired(context.Background())
	if err != nil || removed != 1 || len(sink.Keys()) != 1 {
		t.Fatalf("removed=%d err=%v keys=%v", removed, err, sink.Keys())
	}
	if _, _, err := gateway.Stage(context.Background(), "run_other:node:1:draft", "text/markdown", []byte("keep")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := gateway.Stage(context.Background(), "run_fresh:node:1:draft", "text/markdown", []byte("keep2")); err != nil {
		t.Fatal(err)
	}
	removed, err = gateway.PurgeRun(context.Background(), "run_fresh")
	if err != nil || removed != 2 || len(sink.Keys()) != 1 {
		t.Fatalf("purge removed=%d err=%v keys=%v", removed, err, sink.Keys())
	}
	for _, key := range sink.Keys() {
		if strings.Contains(key, "run_fresh") {
			t.Fatalf("run_fresh survived purge: %s", key)
		}
	}
	if _, err := gateway.PurgeRun(context.Background(), "not-a-run"); err == nil {
		t.Fatal("expected invalid run id rejection")
	}
}

func TestShadowLaneCandidateNeverTouchesCanonicalGateway(t *testing.T) {
	request := legacyRequest([]byte("contract"))
	baseline := &fakeGovernedExecutor{descriptor: ExecutorDescriptor{ExecutorID: "baseline.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}}
	canonical := &stageCountingGateway{inner: &memoryGateway{body: []byte("contract")}}
	sink := NewMemoryShadowContentSink()
	shadowGateway, err := NewShadowContentGateway(canonical, sink, shadowPolicyForTest())
	if err != nil {
		t.Fatal(err)
	}
	candidateRunner := &fakeLegacyRunner{usage: LegacyUsage{Measured: true}, outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", MediaType: "text/markdown", Body: []byte("candidate"), Provenance: map[string]any{}, SourceRefs: []string{}}}}
	candidate, err := NewLegacyExecutorAdapter(AdapterFamilyEngine,
		ExecutorDescriptor{ExecutorID: "candidate.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}},
		request.Node.Capability, request.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, shadowGateway, candidateRunner)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewMutableRolloutPolicyProvider(shadowPolicyForTest())
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewRolloutExecutor(baseline, candidate, provider, &MemoryRolloutEvidenceStore{}, &metricCapture{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifacts[0].ContentRef != "memory://draft" || result.Artifacts[0].ContentHash != hashForTest("draft") {
		t.Fatalf("shadow run returned non-baseline result: %#v", result.Artifacts[0])
	}
	if canonical.stages != 0 {
		t.Fatalf("canonical gateway received %d shadow stage calls", canonical.stages)
	}
	if len(sink.Keys()) == 0 {
		t.Fatal("shadow sink is empty; candidate did not stage into the shadow namespace")
	}
	policyHash := strings.TrimPrefix(shadowPolicyForTest().PolicyHash, "sha256:")
	for _, key := range sink.Keys() {
		if !strings.HasPrefix(key, policyHash+"/run_") {
			t.Fatalf("sink key %s escapes the shadow namespace", key)
		}
	}
}

func TestCanonicalCommitRejectsShadowContentRef(t *testing.T) {
	fixture := newOrchestratorFixture(t, writingplan.IdempotencySafe, false)
	fixture.executor.contentRef = ShadowContentScheme + "policyhash/run_runtime/node/1/hash"
	metrics := &metricCapture{}
	fixture.orchestrator.Telemetry = metrics
	_, err := fixture.orchestrator.Execute(context.Background(), fixture.store.run.RunID)
	if ErrorCodeOf(err) != CodeArtifactCommitFailed {
		t.Fatalf("error=%v code=%s", err, ErrorCodeOf(err))
	}
	if len(fixture.store.artifacts) != 0 || len(fixture.store.completions) == 0 ||
		fixture.store.completions[0].ErrorCode != string(CodeArtifactCommitFailed) {
		t.Fatalf("artifacts=%#v completions=%#v", fixture.store.artifacts, fixture.store.completions)
	}
}

func TestShadowContentRefHelpers(t *testing.T) {
	if IsShadowContentRef("memory://draft") || IsShadowContentRef("") {
		t.Fatal("canonical refs must not be classified as shadow")
	}
	if !IsShadowContentRef(ShadowContentScheme + "x") {
		t.Fatal("shadow refs must be classified")
	}
	artifacts := []OutputArtifactDraft{{ContentRef: "memory://a"}, {ContentRef: ShadowContentScheme + "p/k"}}
	if !containsShadowContentRef(artifacts) {
		t.Fatal("mixed manifest must be detected")
	}
	if containsShadowContentRef([]OutputArtifactDraft{{ContentRef: "memory://a"}}) {
		t.Fatal("clean manifest must pass")
	}
}
