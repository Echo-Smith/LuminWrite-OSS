package writingplan

import (
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

// resealAttributions recomputes the contract's field value hashes after a
// policy mutation, then reseals the contract hash.
func resealAttributions(t *testing.T, contract *writingkernel.WritingContract) writingkernel.WritingContract {
	t.Helper()
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
	return resealed
}

// TestResearchReviewCompileRejectsNoExternalWithoutMaterials (T09 A02, F2/F5
// updated semantics): a contract whose material policy forbids external
// research COMPILES the user-material branch when the document carries a
// non-empty owner material manifest. Without one there is nothing authorized
// to read — the compile fails closed with an explicit error (never a silent
// empty plan), and the runtime discover executor re-checks it (defense in
// depth).
func TestResearchReviewCompileRejectsNoExternalWithoutMaterials(t *testing.T) {
	request := researchReviewCompileRequest(t, writingkernel.SchemaVersionV11, true)
	contract := request.Contract
	contract.MaterialPolicy.AllowExternalResearch = false
	resealed := resealAttributions(t, &contract)
	rebindContract(&request, resealed)
	request.Registry = researchReviewBoundRegistry(t)
	request.Templates = DefaultTemplateRegistry()
	request.InitialArtifactTypes = []ArtifactType{"contract", "materials"}
	request.AllowedPermissions = []Permission{"model.invoke", "materials.read", "validation.run", "external.research", "document.revision"}
	request.RequiredFinalArtifact = "revision_set"
	request.Budget = PlanBudget{MaxCostUSD: 100, MaxDurationMS: 7200000, MaxConcurrency: 1, MaxNodes: 12, MaxItems: 20}
	request.HasUserMaterials = false
	_, err := Compile(request)
	if err == nil {
		t.Fatal("a no-external-research research plan without an owner material manifest must not compile")
	}
	if !strings.Contains(err.Error(), "RESEARCH_MATERIALS_REQUIRED") {
		t.Fatalf("expected RESEARCH_MATERIALS_REQUIRED, got %v", err)
	}
}

// TestResearchReviewCompilesUserMaterialBranch (F5): with a non-empty owner
// material manifest, the no-external-research contract compiles a VALID plan
// whose discover/read nodes resolve to the tightened material manifests —
// no node carries the external.research permission, so the scholar worker can
// never receive an external call through this plan.
func TestResearchReviewCompilesUserMaterialBranch(t *testing.T) {
	request := researchReviewCompileRequest(t, writingkernel.SchemaVersionV11, true)
	contract := request.Contract
	contract.MaterialPolicy.AllowExternalResearch = false
	resealed := resealAttributions(t, &contract)
	rebindContract(&request, resealed)
	request.Registry = researchReviewBoundRegistry(t)
	request.Templates = DefaultTemplateRegistry()
	request.InitialArtifactTypes = []ArtifactType{"contract", "materials"}
	request.AllowedPermissions = []Permission{"model.invoke", "materials.read", "validation.run", "external.research", "document.revision"}
	request.RequiredFinalArtifact = "revision_set"
	request.Budget = PlanBudget{MaxCostUSD: 100, MaxDurationMS: 7200000, MaxConcurrency: 1, MaxNodes: 12, MaxItems: 20}
	request.HasUserMaterials = true
	result, err := Compile(request)
	if err != nil {
		t.Fatalf("material branch compile: %v", err)
	}
	if !result.Plan.StaticValidation.Valid {
		t.Fatalf("material plan invalid: %v", result.Plan.StaticValidation.Errors)
	}
	capabilityByClass := map[string]string{}
	for _, node := range result.Plan.Nodes {
		capabilityByClass[string(node.Capability)] = node.NodeID
	}
	if _, ok := capabilityByClass[CapabilityResearchDiscoverMaterial]; !ok {
		t.Fatalf("plan lacks the material discover capability (nodes=%v)", capabilityByClass)
	}
	if _, ok := capabilityByClass[CapabilityResearchReadMaterial]; !ok {
		t.Fatalf("plan lacks the material read capability (nodes=%v)", capabilityByClass)
	}
	if _, ok := capabilityByClass[CapabilityResearchDiscover]; ok {
		t.Fatal("the external discover capability leaked into a no-external plan")
	}
	// No manifest reachable from this plan carries external.research, and the
	// dispatch validation passes under the contract's material policy.
	for _, node := range result.Plan.Nodes {
		manifest, ok := request.Registry.Get(node.Capability)
		if !ok {
			t.Fatalf("node %s capability %s missing", node.NodeID, node.Capability)
		}
		for _, permission := range manifest.Permissions {
			if permission == "external.research" {
				t.Fatalf("material node %s carries external.research", node.NodeID)
			}
		}
	}
}
