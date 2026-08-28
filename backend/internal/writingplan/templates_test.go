package writingplan

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

func TestDefaultTemplateRegistryIsAvailableToCompiler(t *testing.T) {
	registry := DefaultTemplateRegistry()
	if registry == nil {
		t.Fatal("default template registry must be available")
	}
	modes := registry.Modes()
	for _, mode := range []writingkernel.OrchestrationMode{writingkernel.OrchestrationModeFast, writingkernel.OrchestrationModeOutlineFirst, writingkernel.OrchestrationModeSourced, writingkernel.OrchestrationModeStrictResearch} {
		found := false
		for _, got := range modes {
			if got == mode {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("default templates omitted %s", mode)
		}
	}
}

func TestDefaultFastTemplateCompilesToValidatedT1(t *testing.T) {
	request := baseCompileRequest(t)
	rebindContract(&request, contractWithAssurance(t, validContract(t, writingkernel.OrchestrationModeFast), writingkernel.AssuranceLevelStandard, writingkernel.EvidenceLevelStandard))
	request.Registry = boundDefaultCapabilityRegistry(t)
	request.Templates = DefaultTemplateRegistry()
	request.InitialArtifactTypes = []ArtifactType{"contract", "materials"}
	request.AllowedPermissions = []Permission{"model.invoke", "materials.read", "validation.run", "document.revision"}
	request.RequiredFinalArtifact = "revision_set"
	request.Budget = PlanBudget{MaxCostUSD: 20, MaxDurationMS: 1000000, MaxConcurrency: 4, MaxNodes: 20, MaxItems: 20}

	result, err := Compile(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan.TrustLevel != TrustT1 || result.Plan.Status != PlanValidated || !result.Plan.StaticValidation.Valid {
		t.Fatalf("default fast template was not a validated T1 plan: %#v", result.Plan)
	}
	if result.Decision.ApprovalRequired {
		t.Fatal("ordinary bounded T1 plan should execute under conditional approval")
	}
	envelope := WritingPlanEnvelope{SchemaVersion: SchemaVersion, IntentPlan: request.IntentPlan, ExecutablePlan: result.Plan, StrategyDecision: result.Decision}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WritingPlanEnvelope
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("plan envelope did not survive JSON round trip: %v", err)
	}
	dispatchContext := ValidationContext{Registry: request.Registry, InitialArtifactTypes: request.InitialArtifactTypes, AllowedPermissions: request.AllowedPermissions, Budget: request.Budget,
		RequiredValidators: []string{"core.validation.quality"}, RequiredFinalArtifact: "revision_set", ExternalResearchAllowed: request.Contract.MaterialPolicy.AllowExternalResearch}
	if err := decoded.ValidateForDispatch(dispatchContext); err != nil {
		t.Fatalf("validated envelope failed current dispatch checks: %v", err)
	}
	dispatchContext.AllowedPermissions = nil
	if err := decoded.ValidateForDispatch(dispatchContext); err == nil {
		t.Fatal("dispatch must revalidate current permissions instead of trusting wire validation")
	}
}

func TestDefaultCatalogFailsClosedUntilTypedExecutorsAreBound(t *testing.T) {
	request := baseCompileRequest(t)
	rebindContract(&request, contractWithAssurance(t, validContract(t, writingkernel.OrchestrationModeFast), writingkernel.AssuranceLevelStandard, writingkernel.EvidenceLevelStandard))
	request.Registry = DefaultCapabilityRegistry()
	request.Templates = DefaultTemplateRegistry()
	request.InitialArtifactTypes = []ArtifactType{"contract", "materials"}
	request.AllowedPermissions = []Permission{"model.invoke", "materials.read", "validation.run", "document.revision"}
	request.RequiredFinalArtifact = "revision_set"
	request.Budget = PlanBudget{MaxCostUSD: 20, MaxDurationMS: 1000000, MaxConcurrency: 4, MaxNodes: 20, MaxItems: 20}

	result, err := Compile(request)
	if !errors.Is(err, ErrPlanNotExecutable) {
		t.Fatalf("unbound catalog must not compile as executable: %v", err)
	}
	if result.Plan.TrustLevel != TrustT4 || result.Plan.Status != PlanDraft || result.Decision.ApprovalRequired != true {
		t.Fatalf("unbound catalog must fail closed as draft T4: %#v", result)
	}
	if len(result.Plan.Nodes) != 3 || result.Plan.RootNodeID != "node_draft" {
		t.Fatalf("T4 diagnostic plan must preserve the complete intended topology: %#v", result.Plan)
	}
}

func TestStrictAssuranceNeverSilentlyUsesIncompleteFastTemplate(t *testing.T) {
	request := baseCompileRequest(t)
	contract := contractWithAssurance(t, validContract(t, writingkernel.OrchestrationModeFast), writingkernel.AssuranceLevelStrict, writingkernel.EvidenceLevelStrict)
	rebindContract(&request, contract)
	request.Registry = DefaultCapabilityRegistry()
	request.Templates = DefaultTemplateRegistry()
	request.InitialArtifactTypes = []ArtifactType{"contract", "materials"}
	request.AllowedPermissions = []Permission{"model.invoke", "materials.read", "validation.run", "document.revision"}
	request.RequiredFinalArtifact = "revision_set"
	request.Budget = PlanBudget{MaxCostUSD: 20, MaxDurationMS: 1000000, MaxConcurrency: 4, MaxNodes: 20, MaxItems: 20}
	request.SystemRecommendation = writingkernel.OrchestrationModeStrictResearch

	result, err := Compile(request)
	if !errors.Is(err, ErrPlanNotExecutable) {
		t.Fatalf("strict assurance error = %v", err)
	}
	if result.Decision.EffectiveOrchestration != writingkernel.OrchestrationModeFast {
		t.Fatalf("system replaced explicit fast choice: %#v", result.Decision)
	}
	if result.Plan.TrustLevel != TrustT4 || !result.Decision.ApprovalRequired {
		t.Fatalf("missing strict validators must produce non-runnable T4: %#v", result)
	}
}

func contractWithAssurance(t *testing.T, contract writingkernel.WritingContract, assurance writingkernel.AssuranceLevel, evidence writingkernel.EvidenceLevel) writingkernel.WritingContract {
	t.Helper()
	contract.Collaboration.AssuranceLevel = assurance
	contract.EvidencePolicy.Level = evidence
	for i := range contract.SourceAttributions {
		hash, err := contract.FieldValueHash(contract.SourceAttributions[i].FieldPath)
		if err != nil {
			t.Fatal(err)
		}
		contract.SourceAttributions[i].ValueHash = hash
	}
	sealed, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func TestTemplateCandidatesIdentifyTheirSource(t *testing.T) {
	request := templateCompileRequest(t)
	result, err := Compile(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range result.Decision.Candidates {
		if candidate.TemplateID == nil || *candidate.TemplateID == "" {
			t.Errorf("candidate %#v has no template/source identifier", candidate)
		}
	}
}

func TestCompilerMarksExtendedTemplateAsT2(t *testing.T) {
	request := templateCompileRequest(t)
	request.RequiredValidators = []string{"cap_validate"}
	result, err := Compile(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan.TrustLevel != TrustT2 {
		t.Fatalf("template extended with a compiler-inserted validator must be T2, got %s", result.Plan.TrustLevel)
	}
	if len(result.Plan.Nodes) != 2 || result.Plan.Nodes[1].Capability != "cap_validate" {
		t.Fatalf("required validator was not appended: %#v", result.Plan.Nodes)
	}
}

func templateCompileRequest(t *testing.T) CompileRequest {
	t.Helper()
	request := baseCompileRequest(t)
	rebindContract(&request, validContract(t, writingkernel.OrchestrationModeFast))
	templates := NewTemplateRegistry()
	if err := templates.Register(PlanTemplate{
		ID: "tpl_test", Mode: writingkernel.OrchestrationModeFast, TrustLevel: TrustT1, RootNodeID: "node_draft",
		Nodes: []TemplateNode{{NodeID: "node_draft", Kind: NodeAction, CapabilityClass: "writing", InputArtifactTypes: []ArtifactType{"prompt"}, OutputArtifactTypes: []ArtifactType{"draft"}, Bounds: bounded(), FailurePath: FailureFail}},
	}); err != nil {
		t.Fatal(err)
	}
	request.Templates = templates
	request.SystemRecommendation = writingkernel.OrchestrationModeFast
	return request
}

func boundDefaultCapabilityRegistry(t *testing.T) *CapabilityRegistry {
	t.Helper()
	catalog := DefaultCapabilityRegistry()
	registry := NewCapabilityRegistry(catalog.Version())
	for _, declared := range catalog.All() {
		inputs := append(append([]ArtifactType(nil), declared.InputTypes...), declared.OptionalInputTypes...)
		registerTestExecutor(t, registry, declared.Executor, inputs, declared.OutputTypes)
		declared.Available = true
		if err := registry.Register(declared); err != nil {
			t.Fatal(err)
		}
	}
	return registry
}
