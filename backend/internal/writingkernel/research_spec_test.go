package writingkernel

import (
	"encoding/json"
	"strings"
	"testing"
)

// validV11Contract upgrades the shared v1 builder to lcp/1.1 with the
// documented default ResearchSpec.
func validV11Contract(t *testing.T) WritingContract {
	t.Helper()
	contract := validContract(t)
	contract.SchemaVersion = SchemaVersionV11
	research := DefaultResearchSpec()
	contract.Research = &research
	contract = sealContract(t, contract)
	if err := contract.Validate(); err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestV11ContractValidates(t *testing.T) {
	contract := validV11Contract(t)
	if err := contract.Validate(); err != nil {
		t.Fatal(err)
	}
	if contract.Research == nil || contract.Research.MaxPapers != 10 {
		t.Fatal("default research spec not preserved")
	}
}

func TestV10ContractRejectsResearch(t *testing.T) {
	contract := validContract(t)
	research := DefaultResearchSpec()
	contract.Research = &research
	contract = sealContract(t, contract)
	err := contract.Validate()
	if err == nil || !strings.Contains(err.Error(), "research must be nil") {
		t.Fatalf("expected v1.0 contract to reject research, got %v", err)
	}
}

func TestV11ContractRequiresResearch(t *testing.T) {
	contract := validContract(t)
	contract.SchemaVersion = SchemaVersionV11
	contract = sealContract(t, contract)
	err := contract.Validate()
	if err == nil || !strings.Contains(err.Error(), "research spec is required") {
		t.Fatalf("expected v1.1 contract to require research, got %v", err)
	}
}

func TestV11ResearchReviewModeRequiresV11(t *testing.T) {
	contract := validContract(t)
	contract.Collaboration.OrchestrationMode = OrchestrationModeResearchReview
	refreshControlAttributions(t, &contract)
	contract = sealContract(t, contract)
	err := contract.Validate()
	if err == nil || !strings.Contains(err.Error(), "requires schema_version") {
		t.Fatalf("expected research_review mode on v1.0 to fail, got %v", err)
	}
}

func TestV11UnknownSchemaVersionRejected(t *testing.T) {
	contract := validContract(t)
	contract.SchemaVersion = "lcp/1.2"
	contract = sealContract(t, contract)
	if err := contract.Validate(); err == nil {
		t.Fatal("expected unknown schema version to fail")
	}
}

func TestResearchSpecValidateBoundaries(t *testing.T) {
	base := DefaultResearchSpec()
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ResearchSpec)
	}{
		{"bad version", func(r *ResearchSpec) { r.Version = "research-spec/2" }},
		{"bad review kind", func(r *ResearchSpec) { r.ReviewKind = "systematic" }},
		{"year_from above year_to", func(r *ResearchSpec) { from, to := 2027, 2026; r.YearFrom, r.YearTo = &from, &to }},
		{"year only from", func(r *ResearchSpec) { from := 2020; r.YearFrom = &from; r.YearTo = nil }},
		{"nil exclusion terms", func(r *ResearchSpec) { r.ExclusionTerms = nil }},
		{"blank exclusion term", func(r *ResearchSpec) { r.ExclusionTerms = []string{" "} }},
		{"zero queries", func(r *ResearchSpec) { r.MaxQueries = 0 }},
		{"four queries", func(r *ResearchSpec) { r.MaxQueries = 4 }},
		{"min sources zero", func(r *ResearchSpec) { r.MinCitableSources = 0 }},
		{"min sources above papers", func(r *ResearchSpec) { r.MinCitableSources = 11 }},
		{"papers above twenty", func(r *ResearchSpec) { r.MaxPapers = 21; r.MinCitableSources = 5; r.MaxCandidates = 60 }},
		{"candidates below papers", func(r *ResearchSpec) { r.MaxCandidates = 9 }},
		{"candidates above sixty", func(r *ResearchSpec) { r.MaxCandidates = 61 }},
		{"bad evidence requirement", func(r *ResearchSpec) { r.EvidenceRequirement = "full_text_preferred" }},
		{"unknown generator", func(r *ResearchSpec) { r.Generator = "ar012_candidate" }},
		{"bad citation style", func(r *ResearchSpec) { r.CitationStyle = "author_year" }},
		{"blank selection policy", func(r *ResearchSpec) { r.SelectionPolicyVersion = " " }},
		{"blank reader policy", func(r *ResearchSpec) { r.ReaderPolicyVersion = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := DefaultResearchSpec()
			test.mutate(&spec)
			if err := spec.Validate(); err == nil {
				t.Fatalf("expected %q to be rejected", test.name)
			}
		})
	}
	t.Run("max bounds accepted", func(t *testing.T) {
		spec := DefaultResearchSpec()
		spec.MaxPapers = ResearchMaxPapers
		if err := spec.Validate(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("null years accepted", func(t *testing.T) {
		spec := DefaultResearchSpec()
		spec.YearFrom = nil
		spec.YearTo = nil
		if err := spec.Validate(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestResearchSpecStrictDecodeRejectsUnknownAndDuplicate(t *testing.T) {
	spec := DefaultResearchSpec()
	payload, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(string(payload), "{", `{"max_results":10,`, 1)
	if _, err := DecodeResearchSpecStrict([]byte(unknown)); err == nil {
		t.Fatal("expected unknown research field to fail")
	}
	duplicate := `{"version":"research-spec/1","version":"research-spec/1"}`
	if _, err := DecodeResearchSpecStrict([]byte(duplicate)); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("expected duplicate key error, got %v", err)
	}
}

func TestContractResearchStrictDecode(t *testing.T) {
	contract := validV11Contract(t)
	payload, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWritingContractResearchStrict(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatal(err)
	}
	// Unknown top-level field still rejected.
	unknown := strings.Replace(string(payload), "{", `{"research_v2":{},`, 1)
	if _, err := DecodeWritingContractResearchStrict([]byte(unknown)); err == nil {
		t.Fatal("expected unknown top-level field to fail")
	}
	// Unknown nested research field rejected only by the research path.
	researchUnknown := strings.Replace(string(payload), `"research":{`, `"research":{"top_k":5,`, 1)
	if _, err := DecodeWritingContractStrict([]byte(researchUnknown)); err == nil {
		t.Fatal("plain strict decode must not silently accept unknown research fields")
	}
	if _, err := DecodeWritingContractResearchStrict([]byte(researchUnknown)); err == nil {
		t.Fatal("expected unknown nested research field to fail")
	}
	// A v1.0 payload with no research object decodes cleanly on both paths.
	v1Payload, err := json.Marshal(validContract(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWritingContractResearchStrict(v1Payload); err != nil {
		t.Fatalf("v1 payload must decode on research path: %v", err)
	}
}

func TestV11ComputeHashChangesWithResearch(t *testing.T) {
	contract := validV11Contract(t)
	first := contract.ContractHash
	contract.Research.MaxPapers = 15
	second, err := contract.WithComputedHash()
	if err != nil {
		t.Fatal(err)
	}
	if second.ContractHash == first {
		t.Fatal("changing research fields must change the contract hash")
	}
	if err := second.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestV10GoldenHashesUnchangedByV11(t *testing.T) {
	// The v1.0 golden hashes pinned in contract_golden_test.go must remain
	// byte-identical now that the Research field exists. This mirrors the
	// golden assertion directly on the compiled struct to catch silent
	// serialization drift from the new trailing field.
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			contract := goldenContract(tc)
			hash, err := contract.ComputeHash()
			if err != nil {
				t.Fatal(err)
			}
			if hash != goldenHashes[tc.name] {
				t.Fatalf("v1.0 golden hash drifted after v1.1 field addition: %s != %s", hash, goldenHashes[tc.name])
			}
		})
	}
}
