package writingplan

import (
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

// T09 acceptance-matrix A02 (docs/plans/2026-09-07-research-review-
// integration.md): a contract whose material policy forbids external research
// must fail plan validation with CONTRACT_FORBIDS_EXTERNAL_RESEARCH — the
// research_review template's discover node requires the external.research
// permission, so the plan can never dispatch and the scholar worker receives
// zero calls.
func TestResearchReviewCompileRejectsContractForbiddingExternalResearch(t *testing.T) {
	request := researchReviewCompileRequest(t, writingkernel.SchemaVersionV11, true)
	contract := request.Contract
	contract.MaterialPolicy.AllowExternalResearch = false
	for i := range contract.SourceAttributions {
		valueHash, hashErr := contract.FieldValueHash(contract.SourceAttributions[i].FieldPath)
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		contract.SourceAttributions[i].ValueHash = valueHash
	}
	resealed, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	rebindContract(&request, resealed)
	request.Registry = researchReviewBoundRegistry(t)
	request.Templates = DefaultTemplateRegistry()
	request.InitialArtifactTypes = []ArtifactType{"contract", "materials"}
	request.AllowedPermissions = []Permission{"model.invoke", "materials.read", "validation.run", "external.research", "document.revision"}
	request.RequiredFinalArtifact = "revision_set"
	request.Budget = PlanBudget{MaxCostUSD: 100, MaxDurationMS: 7200000, MaxConcurrency: 1, MaxNodes: 12, MaxItems: 20}
	result, err := Compile(request)
	if err == nil && result.Plan.StaticValidation.Valid {
		t.Fatalf("a no-external-research research plan compiled valid: %v", result.Plan.StaticValidation.Errors)
	}
	message := err.Error()
	if err == nil {
		message = strings.Join(result.Plan.StaticValidation.Errors, "; ")
	}
	if !strings.Contains(message, "CONTRACT_FORBIDS_EXTERNAL_RESEARCH") {
		t.Fatalf("expected CONTRACT_FORBIDS_EXTERNAL_RESEARCH, got %v", message)
	}
}
