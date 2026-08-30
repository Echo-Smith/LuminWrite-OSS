package writingplan

import (
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

func TestCompileRejectsUnknownExecutor(t *testing.T) {
	req := baseCompileRequest(t)
	req.IntentPlan.ProposedSteps[0].CapabilityHint = "not-registered"
	var hashErr error
	req.IntentPlan, hashErr = req.IntentPlan.WithComputedHash()
	if hashErr != nil {
		t.Fatal(hashErr)
	}
	result, err := Compile(req)
	assertCompileError(t, err, "missing_capability")
	if result.Plan.TrustLevel != TrustT4 {
		t.Fatalf("missing executor/capability should downgrade to T4: %#v", result.Plan)
	}
}

func TestCompileRejectsMissingInput(t *testing.T) {
	req := baseCompileRequest(t)
	req.InitialArtifactTypes = []ArtifactType{"unrelated"}
	_, err := Compile(req)
	assertCompileError(t, err, "missing_input")
}

func TestCompileRejectsBudgetOverflow(t *testing.T) {
	req := baseCompileRequest(t)
	req.Budget = PlanBudget{MaxCostUSD: 0.01, MaxDurationMS: 1000, MaxConcurrency: 1, MaxNodes: 10, MaxItems: 1}
	_, err := Compile(req)
	assertCompileError(t, err, "budget")
}

func TestCompileRejectsUndeclaredPermission(t *testing.T) {
	req := baseCompileRequest(t)
	req.AllowedPermissions = nil
	_, err := Compile(req)
	assertCompileError(t, err, "permission")
}

func TestCompileRejectsMissingFinalArtifact(t *testing.T) {
	req := baseCompileRequest(t)
	req.RequiredFinalArtifact = "published_document"
	_, err := Compile(req)
	assertCompileError(t, err, "final_artifact")
}

func TestCompileRejectsMissingRequiredValidator(t *testing.T) {
	req := baseCompileRequest(t)
	req.RequiredValidators = []string{"citation_check"}
	_, err := Compile(req)
	assertCompileError(t, err, "validator")
}

func TestCompileDoesNotReplaceExplicitUserStrategy(t *testing.T) {
	req := baseCompileRequest(t)
	rebindContract(&req, validContract(t, writingkernel.OrchestrationModeFast))
	req.SystemRecommendation = writingkernel.OrchestrationModeStrictResearch
	result, err := Compile(req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.RequestedOrchestration != writingkernel.OrchestrationModeFast {
		t.Fatalf("decision lost explicit user selection: %#v", result.Decision)
	}
	if result.Decision.EffectiveOrchestration != writingkernel.OrchestrationModeFast {
		t.Fatalf("system recommendation replaced user strategy: %#v", result.Decision)
	}
	if !result.Decision.UserOverride {
		t.Fatal("explicit user strategy should be recorded as an override")
	}
	if result.Decision.SelectionSource != SelectionUser {
		t.Fatalf("explicit strategy selection source = %q", result.Decision.SelectionSource)
	}
}

func TestCompileAutoRecordsStrategyDecisionAndTrustLevels(t *testing.T) {
	req := baseCompileRequest(t)
	rebindContract(&req, validContract(t, writingkernel.OrchestrationModeAuto))
	req.SystemRecommendation = writingkernel.OrchestrationModeAuto
	result, err := Compile(req)
	if err != nil {
		t.Fatal(err)
	}
	decision := result.Decision
	if decision.DecisionID == "" || decision.SelectedPlanHash == "" || decision.Summary == "" || decision.ReasonCode == "" {
		t.Fatalf("incomplete StrategyDecision: %#v", decision)
	}
	if decision.Confidence < 0 || decision.Confidence > 1 {
		t.Fatalf("confidence out of range: %v", decision.Confidence)
	}
	if decision.EffectiveOrchestration == "" || decision.RequestedOrchestration != writingkernel.OrchestrationModeAuto {
		t.Fatalf("auto selection was not recorded: %#v", decision)
	}
	if decision.SelectionSource != SelectionSystem || decision.UserOverride {
		t.Fatalf("auto strategy source/override mismatch: %#v", decision)
	}
	if len(decision.Candidates) == 0 {
		t.Fatal("auto StrategyDecision must include at least one candidate")
	}
	for _, candidate := range decision.Candidates {
		if candidate.TrustLevel != TrustT1 && candidate.TrustLevel != TrustT2 && candidate.TrustLevel != TrustT3 && candidate.TrustLevel != TrustT4 {
			t.Errorf("candidate has invalid trust level %q", candidate.TrustLevel)
		}
	}
	if decision.SelectedPlanHash != result.Plan.PlanHash {
		t.Fatalf("selected plan hash %q does not match executable plan %q", decision.SelectedPlanHash, result.Plan.PlanHash)
	}
}

func TestStrategyDecisionRejectsReplacementOfExplicitUserMode(t *testing.T) {
	req := templateCompileRequest(t)
	result, err := Compile(req)
	if err != nil {
		t.Fatal(err)
	}
	result.Decision.EffectiveOrchestration = writingkernel.OrchestrationModeStrictResearch
	if err := result.Decision.Validate(result.Plan); err == nil || !strings.Contains(err.Error(), "replaced explicit user strategy") {
		t.Fatalf("expected explicit strategy replacement to fail, got %v", err)
	}
}

func TestPlanHashIgnoresValidationObservationTime(t *testing.T) {
	req := templateCompileRequest(t)
	firstCompiler := &Compiler{Now: func() time.Time { return time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC) }}
	secondCompiler := &Compiler{Now: func() time.Time { return time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC) }}
	first, err := firstCompiler.Compile(req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondCompiler.Compile(req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Plan.PlanHash != second.Plan.PlanHash || first.Decision.DecisionID != second.Decision.DecisionID {
		t.Fatalf("observation time changed deterministic plan identity: %s != %s", first.Plan.PlanHash, second.Plan.PlanHash)
	}
	if first.Plan.StaticValidation.CheckedAt == second.Plan.StaticValidation.CheckedAt {
		t.Fatal("test requires distinct validation observation times")
	}
}

func baseCompileRequest(t *testing.T) CompileRequest {
	t.Helper()
	contract := validContract(t, writingkernel.OrchestrationModeAuto)
	intent := IntentPlan{
		IntentPlanID: "iplan_test", ContractRef: ObjectRef{ID: contract.ContractID, Version: contract.Version, Hash: contract.ContractHash}, Summary: "produce a draft", CreatedBy: ActorUser, CreatedAt: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC),
		ProposedSteps: []ProposedStep{{StepID: "draft", Objective: "draft the document", CapabilityHint: "cap_draft"}},
	}
	var err error
	intent, err = intent.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	return CompileRequest{
		IntentPlan: intent,
		Contract:   contract, Registry: testRegistry(t), Templates: NewTemplateRegistry(),
		InitialArtifactTypes: []ArtifactType{"prompt"}, AllowedPermissions: []Permission{"llm.generate", "document.validate"},
		Budget:                PlanBudget{MaxCostUSD: 10, MaxDurationMS: 10000, MaxConcurrency: 4, MaxNodes: 20, MaxItems: 20},
		RequiredFinalArtifact: "draft", SystemRecommendation: writingkernel.OrchestrationModeAuto,
	}
}

func rebindContract(req *CompileRequest, contract writingkernel.WritingContract) {
	req.Contract = contract
	req.IntentPlan.ContractRef = ObjectRef{ID: contract.ContractID, Version: contract.Version, Hash: contract.ContractHash}
	var err error
	req.IntentPlan, err = req.IntentPlan.WithComputedHash()
	if err != nil {
		panic(err)
	}
}

func assertCompileError(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected compile error containing %q", code)
	}
	if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(code)) {
		t.Fatalf("expected compile error containing %q, got %v", code, err)
	}
}
