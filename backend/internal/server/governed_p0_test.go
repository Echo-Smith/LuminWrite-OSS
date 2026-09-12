package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"strings"
	"testing"
)

func TestGovernedP0OwnerStylesAndServingTemplates(t *testing.T) {
	t.Setenv("P0_OFFLINE", "1")
	s, _, _ := newGovernedE2EServer(t)
	ctx := context.Background()
	for _, name := range []string{"first", "second"} {
		var owner string
		if err := s.db.QueryRow(`INSERT INTO users(uid,name) VALUES($1,$1) RETURNING id::text`, "p0-"+name).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		p, err := s.userStyleStore.CreateProfile(ctx, owner, "same_slug", name, "")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(profile.StyleProfile{Name: name, KbID: "kb_" + name})
		if _, err = s.userStyleStore.SaveVersion(ctx, p.ID, raw, "test"); err != nil {
			t.Fatal(err)
		}
		resolved, err := (governedStyleResolver{server: s}).ResolveProfile("my_same_slug", owner)
		if err != nil || resolved == nil || resolved.Name != name || resolved.KbID != "kb_"+name {
			t.Fatalf("owner style: %#v %v", resolved, err)
		}
	}
	if got, err := (governedStyleResolver{server: s}).ResolveProfile("my_same_slug", ""); err == nil || got != nil {
		t.Fatal("personal style without owner was accepted")
	}
	api := s.writingAPI.(*persistentWritingAPI)
	for _, mode := range []writingkernel.OrchestrationMode{writingkernel.OrchestrationModeFast, writingkernel.OrchestrationModeSourced, writingkernel.OrchestrationModeStrictResearch} {
		var contract writingkernel.WritingContract
		if err := json.Unmarshal(e2eContractFixture(t), &contract); err != nil {
			t.Fatal(err)
		}
		contract.Collaboration.OrchestrationMode = mode
		contract.Collaboration.AssuranceLevel = writingkernel.AssuranceLevelStandard
		for i := range contract.SourceAttributions {
			hash, err := contract.FieldValueHash(contract.SourceAttributions[i].FieldPath)
			if err != nil {
				t.Fatal(err)
			}
			contract.SourceAttributions[i].ValueHash = hash
		}
		var err error
		contract, err = contract.WithComputedHash()
		if err != nil {
			t.Fatal(err)
		}
		result, err := writingplan.Compile(writingplan.CompileRequest{Contract: contract, IntentPlan: e2eIntentPlan(t, contract), Registry: api.capabilities, InitialArtifactTypes: []writingplan.ArtifactType{"contract", "materials"}, AllowedPermissions: governedWritingPermissions, Budget: writingplan.PlanBudget{MaxCostUSD: 100, MaxDurationMS: 3000000, MaxConcurrency: 2, MaxNodes: 20, MaxItems: 20}, RequiredFinalArtifact: "revision_set"})
		if err != nil || !result.Plan.StaticValidation.Valid {
			t.Fatalf("%s not runnable: %v", mode, err)
		}
		for _, node := range result.Plan.Nodes {
			m, ok := api.capabilities.Get(node.Capability)
			if !ok || !m.Available {
				t.Fatalf("%s unavailable", node.Capability)
			}
		}
	}
	if s.governedTrigger.orchestrator.Telemetry != s.metrics {
		t.Fatal("metrics not mounted")
	}
}
func TestGovernedMetricsHaveNamedLabels(t *testing.T) {
	m := NewMetricsRegistry()
	ctx := context.Background()
	m.Observe(ctx, writingruntime.RuntimeMetric{Kind: writingruntime.MetricContextEnvelope, Capability: "core.draft.generate", Status: "required_missing"})
	m.Observe(ctx, writingruntime.RuntimeMetric{Kind: writingruntime.MetricLifecycle, Status: "run.completed", Reason: "completed", DurationMS: 3000})
	m.Observe(ctx, writingruntime.RuntimeMetric{Kind: writingruntime.MetricExecution, Capability: "core.draft.generate", Status: "succeeded", Lane: writingruntime.LaneBaseline, InputTokens: 12})
	var b bytes.Buffer
	m.Export(&b)
	for _, part := range []string{`governed_context_events_total{kind="context_envelope",capability="core.draft.generate",status="required_missing",reason=""} 1`, `governed_lifecycle_total{event="run.completed",state="completed"} 1`, `governed_run_duration_seconds_count{state="completed"} 1`} {
		if !strings.Contains(b.String(), part) {
			t.Fatalf("missing %s", part)
		}
	}
}

type p0NoopStep struct{}

func (p0NoopStep) Name() engine.StepName { return "noop" }
func (p0NoopStep) CanPause() bool        { return false }
func (p0NoopStep) Execute(context.Context, *engine.ExecutionContext, engine.EventEmitter) error {
	return nil
}
func TestGovernedResearchRejectsSyntheticSources(t *testing.T) {
	step := governedResearchStep{query: p0NoopStep{}, search: p0NoopStep{}, relevance: p0NoopStep{}, compress: p0NoopStep{}}
	exec := &engine.ExecutionContext{SearchResults: []engine.SearchResult{{Title: "fake", Snippet: "generated", URL: "https://example.invalid", IsMock: true}}}
	if err := step.Execute(context.Background(), exec, nil); err == nil {
		t.Fatal("synthetic search accepted")
	}
	exec.UserMaterials = []string{"[material_ref:a source:kb://documents/a title:来源]\n真实材料正文"}
	if err := step.Execute(context.Background(), exec, nil); err != nil {
		t.Fatal(err)
	}
	if len(exec.SearchResults) != 1 || exec.SearchResults[0].URL != "kb://documents/a" {
		t.Fatalf("material provenance lost: %#v", exec.SearchResults)
	}
}
