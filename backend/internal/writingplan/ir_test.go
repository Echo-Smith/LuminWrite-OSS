package writingplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

func TestPlanNodeKindsAndBoundsAreRepresented(t *testing.T) {
	kinds := []string{"sequence", "parallel", "map", "reduce", "condition", "retry", "refine", "human_gate", "validate", "fallback", "action"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			plan := baseExecutablePlan()
			plan.Nodes = []PlanNode{{
				NodeID: "node_" + strings.ReplaceAll(kind, "_", "-"), Kind: NodeKind(kind), Capability: "cap_draft",
				CapabilityVersion:  "1.0.0",
				InputArtifactTypes: []ArtifactType{"prompt"}, OutputArtifactTypes: []ArtifactType{"draft"},
				Bounds: bounded(), FailurePath: "fail",
			}}
			plan.RootNodeID = plan.Nodes[0].NodeID
			validation := ValidatePlan(plan, ValidationContext{Registry: testRegistry(t), InitialArtifactTypes: []ArtifactType{"prompt"}, AllowedPermissions: []Permission{"llm.generate", "document.validate"}, RequiredFinalArtifact: "draft"})
			if kind == "action" && !validation.Valid {
				t.Fatalf("ordinary bounded action should validate: %#v", validation.Errors)
			}
		})
	}
}

func TestValidatePlanRejectsUnknownExecutor(t *testing.T) {
	plan := baseExecutablePlan()
	plan.Nodes[0].Capability = "unknown-capability"
	got := ValidatePlan(plan, ValidationContext{Registry: testRegistry(t), InitialArtifactTypes: []ArtifactType{"prompt"}, RequiredFinalArtifact: "draft"})
	assertStaticError(t, got, "unknown_capability")
}

func TestValidatePlanRejectsMissingInputArtifact(t *testing.T) {
	plan := baseExecutablePlan()
	plan.Nodes[0].InputArtifactTypes = []ArtifactType{"research_notes"}
	got := ValidatePlan(plan, ValidationContext{Registry: testRegistry(t), InitialArtifactTypes: []ArtifactType{"prompt"}, RequiredFinalArtifact: "draft"})
	assertStaticError(t, got, "missing_input")
}

func TestValidatePlanRejectsCircularDependency(t *testing.T) {
	plan := baseExecutablePlan()
	plan.Nodes = []PlanNode{
		{NodeID: "node_draft", Kind: NodeAction, Capability: "cap_draft", CapabilityVersion: "1.0.0", DependsOn: []string{"node_polish"}, InputArtifactTypes: []ArtifactType{"prompt"}, OutputArtifactTypes: []ArtifactType{"draft"}, Bounds: bounded(), FailurePath: FailureFail},
		{NodeID: "node_polish", Kind: NodeAction, Capability: "cap_draft", CapabilityVersion: "1.0.0", DependsOn: []string{"node_draft"}, InputArtifactTypes: []ArtifactType{"draft"}, OutputArtifactTypes: []ArtifactType{"draft"}, Bounds: bounded(), FailurePath: FailureFail},
	}
	got := ValidatePlan(plan, ValidationContext{Registry: testRegistry(t), InitialArtifactTypes: []ArtifactType{"prompt"}, RequiredFinalArtifact: "draft"})
	assertStaticError(t, got, "cycle")
}

func TestValidatePlanRejectsUnboundedControlNodes(t *testing.T) {
	for _, kind := range []string{"retry", "refine", "map", "parallel"} {
		t.Run(kind, func(t *testing.T) {
			plan := baseExecutablePlan()
			plan.Nodes[0].Kind = NodeKind(kind)
			plan.Nodes[0].Bounds = Bounds{MaxAttempts: 0, MaxConcurrency: 0, MaxItems: 0, MaxCostUSD: 0, TimeoutMS: 0}
			got := ValidatePlan(plan, ValidationContext{Registry: testRegistry(t), InitialArtifactTypes: []ArtifactType{"prompt"}, RequiredFinalArtifact: "draft"})
			assertStaticError(t, got, "unbounded")
		})
	}
}

func TestValidatePlanAccountsForEveryMapItem(t *testing.T) {
	registry := NewCapabilityRegistry("registry-map")
	registerTestExecutor(t, registry, "map", []ArtifactType{"prompt"}, []ArtifactType{"draft"})
	manifest := capabilityManifest()
	manifest.ID = "cap_map"
	manifest.Executor = "map"
	manifest.SupportedNodeKinds = []NodeKind{NodeMap}
	manifest.EstimatedCostUSD = 1
	manifest.EstimatedDurationMS = 100
	manifest.MaxBounds = Bounds{MaxAttempts: 2, MaxConcurrency: 1, MaxItems: 100, MaxCostUSD: 200, TimeoutMS: 20000}
	if err := registry.Register(manifest); err != nil {
		t.Fatal(err)
	}
	plan := baseExecutablePlan()
	plan.Nodes[0].Kind = NodeMap
	plan.Nodes[0].Capability = manifest.ID
	plan.Nodes[0].Bounds = Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 100, MaxCostUSD: 1, TimeoutMS: 100}
	got := ValidatePlan(plan, ValidationContext{Registry: registry, InitialArtifactTypes: []ArtifactType{"prompt"}, Budget: PlanBudget{MaxCostUSD: 500, MaxDurationMS: 50000, MaxItems: 100}, RequiredFinalArtifact: "draft"})
	assertStaticError(t, got, "node_cost_bound_too_low")
	assertStaticError(t, got, "node_timeout_bound_too_low")
}

func TestValidatePlanRejectsFallbackControlFlowCycle(t *testing.T) {
	registry := NewCapabilityRegistry("registry-fallback")
	registerTestExecutor(t, registry, "fallback", []ArtifactType{"prompt"}, []ArtifactType{"draft"})
	manifest := capabilityManifest()
	manifest.ID = "cap_fallback"
	manifest.Executor = "fallback"
	manifest.SupportedNodeKinds = []NodeKind{NodeFallback}
	if err := registry.Register(manifest); err != nil {
		t.Fatal(err)
	}
	plan := baseExecutablePlan()
	plan.Nodes[0].Kind = NodeFallback
	plan.Nodes[0].Capability = manifest.ID
	plan.Nodes[0].FailurePath = FailureFallback
	plan.Nodes[0].FallbackNodeID = plan.Nodes[0].NodeID
	got := ValidatePlan(plan, ValidationContext{Registry: registry, InitialArtifactTypes: []ArtifactType{"prompt"}, RequiredFinalArtifact: "draft"})
	assertStaticError(t, got, "control_flow_cycle")
}

func TestRequiredValidatorCannotDegradeToPartial(t *testing.T) {
	request := baseCompileRequest(t)
	rebindContract(&request, contractWithAssurance(t, validContract(t, writingkernel.OrchestrationModeFast), writingkernel.AssuranceLevelStandard, writingkernel.EvidenceLevelStandard))
	request.Registry = boundDefaultCapabilityRegistry(t)
	request.Templates = DefaultTemplateRegistry()
	request.InitialArtifactTypes = []ArtifactType{"contract", "materials"}
	request.AllowedPermissions = []Permission{"model.invoke", "materials.read", "validation.run", "document.revision"}
	request.RequiredFinalArtifact = "verified_deliverable"
	request.Budget = PlanBudget{MaxCostUSD: 20, MaxDurationMS: 1000000, MaxConcurrency: 4, MaxNodes: 20, MaxItems: 20}
	compiled, err := Compile(request)
	if err != nil {
		t.Fatal(err)
	}
	plan := compiled.Plan
	plan.PlanHash = ""
	plan.Status = PlanDraft
	plan.StaticValidation = StaticValidation{}
	for i := range plan.Nodes {
		if plan.Nodes[i].Capability == "core.validation.quality" {
			plan.Nodes[i].FailurePath = FailurePartial
			plan.Nodes[i].PartialOutputTypes = []ArtifactType{"quality_report"}
		}
	}
	got := ValidatePlan(plan, ValidationContext{Registry: request.Registry, InitialArtifactTypes: request.InitialArtifactTypes, AllowedPermissions: request.AllowedPermissions, Budget: request.Budget, RequiredValidators: []string{"core.validation.quality"}, RequiredFinalArtifact: "verified_deliverable"})
	assertStaticError(t, got, "required_validator_bypass")
}

func TestValidatePlanRejectsMissingFinalArtifact(t *testing.T) {
	plan := baseExecutablePlan()
	plan.Nodes[0].OutputArtifactTypes = []ArtifactType{"temporary_draft"}
	got := ValidatePlan(plan, ValidationContext{Registry: testRegistry(t), InitialArtifactTypes: []ArtifactType{"prompt"}, RequiredFinalArtifact: "final_document"})
	assertStaticError(t, got, "final_artifact")
}

func TestValidatePlanRejectsMissingRequiredValidator(t *testing.T) {
	plan := baseExecutablePlan()
	got := ValidatePlan(plan, ValidationContext{Registry: testRegistry(t), InitialArtifactTypes: []ArtifactType{"prompt"}, RequiredValidators: []string{"fact_check"}, RequiredFinalArtifact: "draft"})
	assertStaticError(t, got, "validator")
}

func baseExecutablePlan() ExecutablePlan {
	return ExecutablePlan{
		PlanID: "plan_test", IntentPlanRef: ObjectRef{ID: "iplan_test", Version: 1, Hash: testHash()},
		TrustLevel: TrustT3, Status: PlanDraft, RootNodeID: "node_draft",
		Nodes: []PlanNode{{NodeID: "node_draft", Kind: NodeAction, Capability: "cap_draft", CapabilityVersion: "1.0.0", InputArtifactTypes: []ArtifactType{"prompt"}, OutputArtifactTypes: []ArtifactType{"draft"}, Bounds: bounded(), FailurePath: FailureFail}},
	}
}

func bounded() Bounds {
	return Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 1, TimeoutMS: 1000}
}

func testHash() string {
	return "sha256:0000000000000000000000000000000000000000000000000000000000000000"
}

func testRegistry(t *testing.T) *CapabilityRegistry {
	t.Helper()
	registry := NewCapabilityRegistry("registry-test")
	registerTestExecutor(t, registry, "draft", []ArtifactType{"prompt"}, []ArtifactType{"draft"})
	registerTestExecutor(t, registry, "validate", []ArtifactType{"draft"}, []ArtifactType{"validated"})
	if err := registry.Register(CapabilityManifest{ID: "cap_draft", Class: "writing", Executor: "draft", InputTypes: []ArtifactType{"prompt"}, OutputTypes: []ArtifactType{"draft"}, Permissions: []Permission{"llm.generate"}, EstimatedCostUSD: 0.25, EstimatedDurationMS: 100, PreservesVoice: true, Version: "1.0.0", SupportedNodeKinds: []NodeKind{NodeAction}, MaxBounds: bounded(), Idempotency: IdempotencySafe, Available: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(CapabilityManifest{ID: "cap_validate", Class: "validator", Executor: "validate", InputTypes: []ArtifactType{"draft"}, OutputTypes: []ArtifactType{"validated"}, Permissions: []Permission{"document.validate"}, Validator: true, EstimatedCostUSD: 0.1, EstimatedDurationMS: 50, Version: "1.0.0", SupportedNodeKinds: []NodeKind{NodeValidate}, MaxBounds: bounded(), Idempotency: IdempotencySafe, Available: true}); err != nil {
		t.Fatal(err)
	}
	return registry
}

func validContract(t *testing.T, mode writingkernel.OrchestrationMode) writingkernel.WritingContract {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "lcp", "v1", "fixtures", "writing-contract.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := writingkernel.DecodeWritingContractStrict(payload)
	if err != nil {
		t.Fatal(err)
	}
	contract.Collaboration.OrchestrationMode = mode
	for i := range contract.SourceAttributions {
		valueHash, hashErr := contract.FieldValueHash(contract.SourceAttributions[i].FieldPath)
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		contract.SourceAttributions[i].ValueHash = valueHash
	}
	contract, err = contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func assertStaticError(t *testing.T, validation StaticValidation, code string) {
	t.Helper()
	if validation.Valid {
		t.Fatalf("expected static validation to fail for %q", code)
	}
	for _, err := range validation.Errors {
		if strings.Contains(strings.ToLower(err), strings.ToLower(code)) {
			return
		}
	}
	t.Fatalf("expected error code %q, got %#v", code, validation.Errors)
}
