package writingplan

import (
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

// T06 plan-layer tests (docs/plans/2026-09-07-research-review-integration.md
// §T06): the research_review template compiles the ten fixed nodes of
// design.md §3 with the exact dependency edges over a v1.1 research contract;
// the kernel gate exemption is bounded (exact ids + NodeHumanGate + pinned
// I/O) and cannot be forged onto arbitrary manifests; legacy modes compile
// unchanged.

// researchReviewBoundRegistry binds every declared catalog capability to a
// test executor (union I/O per executor id) so the research_review template
// compiles to a validated plan.
func researchReviewBoundRegistry(t *testing.T) *CapabilityRegistry {
	t.Helper()
	return boundDefaultCapabilityRegistry(t)
}

func TestResearchReviewTemplateCompilesTenNodes(t *testing.T) {
	request := researchReviewCompileRequest(t, writingkernel.SchemaVersionV11, true)
	request.Registry = researchReviewBoundRegistry(t)
	request.Templates = DefaultTemplateRegistry()
	request.InitialArtifactTypes = []ArtifactType{"contract", "materials"}
	request.AllowedPermissions = []Permission{"model.invoke", "materials.read", "validation.run", "external.research", "document.revision"}
	request.RequiredFinalArtifact = "revision_set"
	request.Budget = PlanBudget{MaxCostUSD: 100, MaxDurationMS: 7200000, MaxConcurrency: 1, MaxNodes: 12, MaxItems: 20}
	result, err := Compile(request)
	if err != nil {
		t.Fatalf("research_review compile failed: %v", err)
	}
	plan := result.Plan
	if !plan.StaticValidation.Valid {
		t.Fatalf("plan invalid: %v", plan.StaticValidation.Errors)
	}
	if plan.TrustLevel != TrustT1 {
		t.Fatalf("trust level %q, want T1", plan.TrustLevel)
	}
	wantNodes := []string{"node_research_discover", "node_research_read", "node_gate_evidence", "node_research_outline",
		"node_gate_outline", "node_research_draft", "node_research_citations", "node_research_fact", "node_quality", "node_finalize"}
	if len(plan.Nodes) != len(wantNodes) {
		t.Fatalf("plan has %d nodes, want %d: %v", len(plan.Nodes), len(wantNodes), nodeIDs(plan.Nodes))
	}
	got := map[string]PlanNode{}
	for index, want := range wantNodes {
		node := plan.Nodes[index]
		if node.NodeID != want {
			t.Fatalf("node %d is %q, want %q (fixed order)", index, node.NodeID, want)
		}
		got[want] = node
	}
	wantDeps := map[string][]string{
		"node_research_discover":  {},
		"node_research_read":      {"node_research_discover"},
		"node_gate_evidence":      {"node_research_read"},
		"node_research_outline":   {"node_research_read", "node_gate_evidence"},
		"node_gate_outline":       {"node_research_outline"},
		"node_research_draft":     {"node_research_read", "node_gate_evidence", "node_gate_outline"},
		"node_research_citations": {"node_research_read", "node_research_draft"},
		"node_research_fact":      {"node_research_read", "node_research_draft"},
		"node_quality":            {"node_research_draft", "node_research_citations", "node_research_fact"},
		"node_finalize":           {"node_research_draft", "node_quality"},
	}
	for id, deps := range wantDeps {
		node := got[id]
		if len(node.DependsOn) != len(deps) {
			t.Fatalf("node %s deps = %v, want %v", id, node.DependsOn, deps)
		}
		for i, dep := range deps {
			if node.DependsOn[i] != dep {
				t.Fatalf("node %s deps = %v, want %v", id, node.DependsOn, deps)
			}
		}
	}
	// Kinds and bounds.
	if got["node_gate_evidence"].Kind != NodeHumanGate || got["node_gate_outline"].Kind != NodeHumanGate {
		t.Fatal("gate nodes must be NodeHumanGate")
	}
	read := got["node_research_read"]
	if read.Bounds.MaxItems != 20 || read.Bounds.TimeoutMS != 20*60*1000 || read.Bounds.MaxConcurrency != 1 {
		t.Fatalf("research_read bounds = %+v", read.Bounds)
	}
	for _, node := range plan.Nodes {
		if node.Kind != NodeHumanGate && node.FailurePath != FailurePause {
			t.Fatalf("node %s failure path %q, want pause", node.NodeID, node.FailurePath)
		}
	}
	// Required validators carried through.
	for _, validator := range []string{CapabilityResearchCitations, CapabilityResearchFact, "core.validation.quality"} {
		found := false
		for _, node := range plan.Nodes {
			if node.Capability == validator && node.Kind == NodeValidate {
				found = true
			}
		}
		if !found {
			t.Fatalf("required validator %s missing from plan", validator)
		}
	}
	// The draft node declares both outputs: the citation index is a required
	// plan output (contracts.md §2), not just draft-side decoration.
	draftNode := got["node_research_draft"]
	if !containsArtifact(draftNode.OutputArtifactTypes, "research_citation_index") {
		t.Fatal("research_draft must declare research_citation_index output")
	}
}

func nodeIDs(nodes []PlanNode) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.NodeID)
	}
	return ids
}

func TestResearchReviewCompileFailsClosedWithoutResearchCapabilities(t *testing.T) {
	// A bare registry without the research catalog must fail closed (T4 /
	// PLAN_NOT_EXECUTABLE), never silently degrade to a legacy plan.
	request := researchReviewCompileRequest(t, writingkernel.SchemaVersionV11, true)
	request.Registry = testRegistry(t)
	request.Templates = DefaultTemplateRegistry()
	_, err := Compile(request)
	if err == nil {
		t.Fatal("expected compile failure without research capabilities")
	}
	if !strings.Contains(err.Error(), "MISSING_CAPABILITY_CLASS") {
		t.Fatalf("expected missing capability classes, got %v", err)
	}
}

func TestKernelGateExemptionExactShape(t *testing.T) {
	catalog := DefaultCapabilityRegistry()
	for _, id := range []string{CapabilityResearchGateEvidenc, CapabilityResearchGateOutline} {
		manifest, ok := catalog.Get(id)
		if !ok {
			t.Fatalf("catalog lacks gate capability %s", id)
		}
		if !IsKernelHumanGateCapability(manifest) {
			t.Fatalf("gate capability %s does not satisfy its own pinned shape", id)
		}
	}
}

func TestKernelGateExemptionCannotBeForged(t *testing.T) {
	base := func() CapabilityManifest {
		return CapabilityManifest{ID: "cap_fake_gate", Class: ClassResearchGateEvidence,
			Executor: KernelGateExecutor, InputTypes: []ArtifactType{"research_evidence_pack"},
			OutputTypes: []ArtifactType{"evidence_approval"}, Version: "1.0.0",
			SupportedNodeKinds: []NodeKind{NodeHumanGate},
			MaxBounds:          Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, TimeoutMS: 1200000},
			Idempotency:        IdempotencySafe, Available: true}
	}
	cases := map[string]func(*CapabilityManifest){
		"foreign_id":            func(m *CapabilityManifest) { m.ID = "cap_foreign_gate" },
		"foreign_class":         func(m *CapabilityManifest) { m.ID = CapabilityResearchGateEvidenc; m.Class = "validation.quality" },
		"extra_node_kind":       func(m *CapabilityManifest) { m.ID = CapabilityResearchGateEvidenc; m.SupportedNodeKinds = []NodeKind{NodeHumanGate, NodeAction} },
		"swapped_io":            func(m *CapabilityManifest) { m.ID = CapabilityResearchGateOutline; m.InputTypes = []ArtifactType{"research_evidence_pack"} },
		"wrong_version":         func(m *CapabilityManifest) { m.ID = CapabilityResearchGateEvidenc; m.Version = "2.0.0" },
		"carries_permissions":   func(m *CapabilityManifest) { m.ID = CapabilityResearchGateEvidenc; m.Permissions = []Permission{"model.invoke"} },
		"action_kind_mimic":     func(m *CapabilityManifest) { m.ID = CapabilityResearchGateEvidenc; m.SupportedNodeKinds = []NodeKind{NodeAction} },
		"unknown_id_fake_shape": func(m *CapabilityManifest) {},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			manifest := base()
			mutate(&manifest)
			registry := NewCapabilityRegistry("test-1.0.0")
			registerTestExecutor(t, registry, KernelGateExecutor,
				[]ArtifactType{"research_evidence_pack", "research_outline"},
				[]ArtifactType{"evidence_approval", "approved_research_outline"})
			if err := registry.Register(manifest); err == nil {
				t.Fatalf("forged gate manifest was accepted: %#v", manifest)
			}
		})
	}
}

func TestValidatePlanRejectsHumanGateWithoutKernelCapability(t *testing.T) {
	// A NodeHumanGate node backed by a non-kernel capability must fail plan
	// validation at dispatch: the exemption is bound to the pinned ids.
	catalog := DefaultCapabilityRegistry()
	registry := NewCapabilityRegistry(catalog.Version())
	// No executor registered for the gate: the manifest itself is fine but
	// the plan node must not slip through ValidatePlan's checks.
	plan := baseExecutablePlan()
	for i := range plan.Nodes {
		if plan.Nodes[i].Kind != NodeAction {
			continue
		}
		plan.Nodes[i].Kind = NodeHumanGate
		plan.Nodes[i].Capability = "cap_some_random_gate"
		plan.Nodes[i].InputArtifactTypes = []ArtifactType{"prompt"}
		plan.Nodes[i].OutputArtifactTypes = []ArtifactType{"evidence_approval"}
	}
	got := ValidatePlan(plan, ValidationContext{Registry: registry, InitialArtifactTypes: []ArtifactType{"prompt"},
		RequiredFinalArtifact: "draft"})
	assertStaticError(t, got, "UNKNOWN_CAPABILITY")
}

func TestLegacyTemplatesUnaffectedByResearchCatalog(t *testing.T) {
	// The gold-sample guardrails live in the kernel package; here we prove
	// the four legacy templates still compile over the catalog that now also
	// carries the research capabilities.
	for _, mode := range []writingkernel.OrchestrationMode{writingkernel.OrchestrationModeFast,
		writingkernel.OrchestrationModeOutlineFirst, writingkernel.OrchestrationModeSourced,
		writingkernel.OrchestrationModeStrictResearch} {
		// Assurance matched to the template: sourced/strict templates produce
		// source_pack; the fast/outline-first templates are standard.
		assurance, evidence := writingkernel.AssuranceLevelStandard, writingkernel.EvidenceLevelStandard
		if mode == writingkernel.OrchestrationModeSourced {
			assurance, evidence = writingkernel.AssuranceLevelSourced, writingkernel.EvidenceLevelSourced
		}
		if mode == writingkernel.OrchestrationModeStrictResearch {
			assurance, evidence = writingkernel.AssuranceLevelStrict, writingkernel.EvidenceLevelStrict
		}
		request := baseCompileRequest(t)
		rebindContract(&request, contractWithAssurance(t, validContract(t, mode), assurance, evidence))
		request.Registry = researchReviewBoundRegistry(t)
		request.Templates = DefaultTemplateRegistry()
		request.InitialArtifactTypes = []ArtifactType{"contract", "materials"}
		request.AllowedPermissions = []Permission{"model.invoke", "materials.read", "validation.run", "external.research", "document.revision"}
		request.RequiredFinalArtifact = "revision_set"
		request.Budget = PlanBudget{MaxCostUSD: 100, MaxDurationMS: 3000000, MaxConcurrency: 2, MaxNodes: 20, MaxItems: 20}
		result, err := Compile(request)
		if err != nil {
			t.Fatalf("%s compile failed: %v", mode, err)
		}
		if !result.Plan.StaticValidation.Valid {
			t.Fatalf("%s plan invalid: %v", mode, result.Plan.StaticValidation.Errors)
		}
		for _, node := range result.Plan.Nodes {
			if strings.HasPrefix(node.Capability, "core.research.") {
				t.Fatalf("%s plan pulled in research capability %s", mode, node.Capability)
			}
		}
	}
}
