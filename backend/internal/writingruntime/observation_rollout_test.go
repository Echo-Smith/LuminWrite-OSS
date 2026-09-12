package writingruntime

import (
	"context"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"testing"
	"time"
)

func TestObservationPolicyNeverServesAllowlistedCandidate(t *testing.T) {
	req := legacyRequest([]byte("contract"))
	req.Subject = "owner"
	p := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, req.Node.Capability, req.Node.CapabilityVersion)
	p.Mode = RolloutAllowlist
	p.ActivationKey = "p0"
	p.AllowSubjects = []string{"owner"}
	p, _ = p.WithComputedHash()
	gateway := &stageCountingGateway{inner: &memoryGateway{body: []byte("contract")}}
	sink := NewMemoryShadowContentSink()
	shadowGateway, err := NewShadowContentGateway(gateway, sink, p)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeLegacyRunner{usage: LegacyUsage{Measured: true}, outputs: []LegacyPayload{{OutputKey: "draft", ArtifactType: "full_draft", MediaType: "text/markdown", Body: []byte("candidate"), Provenance: map[string]any{}, SourceRefs: []string{}}}}
	candidate, err := NewShadowIsolatedExecutorAdapter(AdapterFamilyEngine, ExecutorDescriptor{ExecutorID: "candidate.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}, req.Node.Capability, req.Node.CapabilityVersion, []writingplan.Permission{"model.invoke"}, shadowGateway, runner)
	if err != nil {
		t.Fatal(err)
	}
	baseline := &fakeGovernedExecutor{descriptor: ExecutorDescriptor{ExecutorID: "baseline.engine", Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction}}}
	provider, _ := NewMutableRolloutPolicyProvider(p)
	evidence := &MemoryRolloutEvidenceStore{}
	executor, err := NewObservationRolloutExecutor(baseline, candidate, provider, evidence, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, 2*time.Second, "shadow comparison", func() bool {
		for _, v := range evidence.Records() {
			if v.Comparison != nil {
				return true
			}
		}
		return false
	})
	if gateway.stages != 0 || len(sink.Keys()) != 1 {
		t.Fatal("candidate escaped shadow")
	}
	for _, v := range evidence.Records() {
		if v.PolicyHash != p.PolicyHash {
			t.Fatal("evidence hash drift")
		}
		if v.Kind == "route_decision" && v.Lane == LaneCandidate {
			t.Fatal("observation served candidate")
		}
	}
}
