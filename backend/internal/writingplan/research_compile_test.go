package writingplan

import (
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

// researchReviewCompileRequest turns the shared fixture contract into a
// research_review compile request. schemaVersion selects lcp/1.0 vs lcp/1.1;
// withResearch controls the ResearchSpec payload.
func researchReviewCompileRequest(t *testing.T, schemaVersion string, withResearch bool) CompileRequest {
	t.Helper()
	contract := validContract(t, writingkernel.OrchestrationModeResearchReview)
	contract.SchemaVersion = schemaVersion
	if withResearch {
		research := writingkernel.DefaultResearchSpec()
		contract.Research = &research
	}
	contract, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	request := baseCompileRequest(t)
	rebindContract(&request, contract)
	request.SystemRecommendation = writingkernel.OrchestrationModeResearchReview
	return request
}

func TestCompileRejectsResearchReviewOnV1Contract(t *testing.T) {
	// T01 fail-closed requirement: research_review + v1.0 must fail compile.
	request := researchReviewCompileRequest(t, writingkernel.SchemaVersionV1, false)
	if _, err := Compile(request); err == nil {
		t.Fatal("expected research_review on a v1.0 contract to fail compilation")
	} else if !strings.Contains(err.Error(), "research_review") {
		t.Fatalf("expected research_review rejection, got %v", err)
	}
}

func TestCompileRejectsResearchReviewWithoutResearchSpec(t *testing.T) {
	request := researchReviewCompileRequest(t, writingkernel.SchemaVersionV11, false)
	if _, err := Compile(request); err == nil {
		t.Fatal("expected research_review without a research spec to fail compilation")
	}
}

func TestCompileAcceptsV11ResearchReviewPastContractGate(t *testing.T) {
	// The contract gate must pass for a proper lcp/1.1 + research contract.
	// Remaining failures (missing research capabilities/template) are T06's
	// concern; here we only assert the protocol gate itself is not the
	// blocker.
	request := researchReviewCompileRequest(t, writingkernel.SchemaVersionV11, true)
	_, err := Compile(request)
	if err != nil && strings.Contains(err.Error(), "RESEARCH_REVIEW_CONTRACT_INVALID") {
		t.Fatalf("valid v1.1 research contract must pass the research gate: %v", err)
	}
}

func TestCompileRejectsSystemRecommendationOfResearchReviewOnV1Contract(t *testing.T) {
	// A system recommendation must not be able to route a v1.0 auto
	// contract into the research path.
	request := baseCompileRequest(t)
	request.SystemRecommendation = writingkernel.OrchestrationModeResearchReview
	result, err := Compile(request)
	if err == nil {
		t.Fatalf("expected system research_review recommendation on v1.0 to fail compile, got %#v", result.Decision)
	}
	if !strings.Contains(err.Error(), "RESEARCH_REVIEW_CONTRACT_INVALID") {
		t.Fatalf("expected RESEARCH_REVIEW_CONTRACT_INVALID, got %v", err)
	}
}

func TestV11ResearchContractResolvesRecommendations(t *testing.T) {
	contract := validContract(t, writingkernel.OrchestrationModeAuto)
	contract.SchemaVersion = writingkernel.SchemaVersionV11
	research := writingkernel.DefaultResearchSpec()
	contract.Research = &research
	contract.Collaboration.TaskMode = writingkernel.TaskModeAuto
	contract.Collaboration.OrchestrationMode = writingkernel.OrchestrationModeAuto
	for i := range contract.SourceAttributions {
		valueHash, hashErr := contract.FieldValueHash(contract.SourceAttributions[i].FieldPath)
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		contract.SourceAttributions[i].ValueHash = valueHash
	}
	contract, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := writingkernel.ResolveExecutionControl(contract, writingkernel.ExecutionRecommendation{
		TaskMode:          writingkernel.TaskModeWriting,
		OrchestrationMode: writingkernel.OrchestrationModeOutlineFirst,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Effective.OrchestrationMode != writingkernel.OrchestrationModeOutlineFirst {
		t.Fatalf("v1.1 contract did not resolve recommendations: %#v", resolved.Effective)
	}
}
