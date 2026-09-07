package writingkernel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ResearchSpec is the v1.1-only research review contract section
// (specs/research-review/contracts.md §1). It is carried by
// WritingContract.Research with `json:"research,omitempty"`; nil Research
// keeps v1.0 contracts byte-identical (see contract_golden_test.go).
//
// Every field is hashed as part of ComputeHash: year range, counts, and
// evidence scope must never become implicit runtime configuration. Defaults
// are filled at draft time; a confirmed contract has no implicit defaults.
type ResearchSpec struct {
	Version                string   `json:"version"`
	ReviewKind             string   `json:"review_kind"`
	YearFrom               *int     `json:"year_from"`
	YearTo                 *int     `json:"year_to"`
	ExclusionTerms         []string `json:"exclusion_terms"`
	MaxQueries             int      `json:"max_queries"`
	MaxCandidates          int      `json:"max_candidates"`
	MaxPapers              int      `json:"max_papers"`
	MinCitableSources      int      `json:"min_citable_sources"`
	EvidenceRequirement    string   `json:"evidence_requirement"`
	SelectionPolicyVersion string   `json:"selection_policy_version"`
	ReaderPolicyVersion    string   `json:"reader_policy_version"`
	Generator              string   `json:"generator"`
	CitationStyle          string   `json:"citation_style"`
}

const (
	// ResearchSpecVersionV1 is the only accepted ResearchSpec.version.
	ResearchSpecVersionV1 = "research-spec/1"
	// ReviewKindNarrative is the only accepted review_kind for v1.1.
	ReviewKindNarrative = "narrative"
	// Evidence scope requirements.
	EvidenceRequirementAbstractAllowed  = "abstract_allowed"
	EvidenceRequirementFullTextRequired = "full_text_required"
	// ResearchGeneratorLuminWriter is the only v1.1 generator; adding
	// ar012_candidate requires a dedicated protocol version bump.
	ResearchGeneratorLuminWriter = "lumin_writer"
	// CitationStyleNumeric is the only v1.1 citation style.
	CitationStyleNumeric = "numeric"

	// ResearchMaxQueries caps the query plan (1..3 per contracts.md §1).
	ResearchMaxQueries = 3
	// ResearchMaxCandidates caps the merged candidate pool (<= 60).
	ResearchMaxCandidates = 60
	// ResearchMaxPapers caps the reading workset (<= 20). The suggested
	// default for max_papers is 10, but the protocol limit stays 20.
	ResearchMaxPapers = 20
)

func (r ResearchSpec) Validate() error {
	if r.Version != ResearchSpecVersionV1 {
		return fmt.Errorf("research.version must be %q", ResearchSpecVersionV1)
	}
	if r.ReviewKind != ReviewKindNarrative {
		return fmt.Errorf("research.review_kind must be %q", ReviewKindNarrative)
	}
	if (r.YearFrom == nil) != (r.YearTo == nil) {
		return errors.New("research.year_from and research.year_to must be set together or both null")
	}
	if r.YearFrom != nil && r.YearTo != nil && *r.YearFrom > *r.YearTo {
		return fmt.Errorf("research.year_from (%d) must not exceed research.year_to (%d)", *r.YearFrom, *r.YearTo)
	}
	if r.ExclusionTerms == nil {
		return errors.New("research.exclusion_terms must be present")
	}
	for i, term := range r.ExclusionTerms {
		if strings.TrimSpace(term) == "" {
			return fmt.Errorf("research.exclusion_terms[%d] must not be blank", i)
		}
	}
	if r.MaxQueries < 1 || r.MaxQueries > ResearchMaxQueries {
		return fmt.Errorf("research.max_queries must be between 1 and %d", ResearchMaxQueries)
	}
	if r.MinCitableSources < 1 {
		return errors.New("research.min_citable_sources must be at least 1")
	}
	if r.MaxPapers < r.MinCitableSources {
		return fmt.Errorf("research.max_papers (%d) must be at least research.min_citable_sources (%d)", r.MaxPapers, r.MinCitableSources)
	}
	if r.MaxPapers > ResearchMaxPapers {
		return fmt.Errorf("research.max_papers must not exceed %d", ResearchMaxPapers)
	}
	if r.MaxCandidates < r.MaxPapers {
		return fmt.Errorf("research.max_candidates (%d) must be at least research.max_papers (%d)", r.MaxCandidates, r.MaxPapers)
	}
	if r.MaxCandidates > ResearchMaxCandidates {
		return fmt.Errorf("research.max_candidates must not exceed %d", ResearchMaxCandidates)
	}
	if r.EvidenceRequirement != EvidenceRequirementAbstractAllowed && r.EvidenceRequirement != EvidenceRequirementFullTextRequired {
		return fmt.Errorf("research.evidence_requirement must be %q or %q", EvidenceRequirementAbstractAllowed, EvidenceRequirementFullTextRequired)
	}
	if r.Generator != ResearchGeneratorLuminWriter {
		return fmt.Errorf("research.generator must be %q", ResearchGeneratorLuminWriter)
	}
	if r.CitationStyle != CitationStyleNumeric {
		return fmt.Errorf("research.citation_style must be %q", CitationStyleNumeric)
	}
	if strings.TrimSpace(r.SelectionPolicyVersion) == "" {
		return errors.New("research.selection_policy_version must not be blank")
	}
	if strings.TrimSpace(r.ReaderPolicyVersion) == "" {
		return errors.New("research.reader_policy_version must not be blank")
	}
	return nil
}

// DecodeResearchSpecStrict rejects duplicate and unknown fields inside a
// ResearchSpec. It is used by DecodeWritingContractResearchStrict so the
// strictness of the research section matches the rest of the contract.
func DecodeResearchSpecStrict(data []byte) (ResearchSpec, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return ResearchSpec{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var spec ResearchSpec
	if err := decoder.Decode(&spec); err != nil {
		return ResearchSpec{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ResearchSpec{}, err
	}
	return spec, nil
}

// DefaultResearchSpec returns the product defaults from
// specs/research-review/requirements.md §4. They exist only so a draft
// contract can be materialized explicitly; confirmation never applies
// implicit defaults.
func DefaultResearchSpec() ResearchSpec {
	yearFrom := 2020
	yearTo := 2026
	return ResearchSpec{
		Version:                ResearchSpecVersionV1,
		ReviewKind:             ReviewKindNarrative,
		YearFrom:               &yearFrom,
		YearTo:                 &yearTo,
		ExclusionTerms:         []string{},
		MaxQueries:             3,
		MaxCandidates:          60,
		MaxPapers:              10,
		MinCitableSources:      5,
		EvidenceRequirement:    EvidenceRequirementAbstractAllowed,
		SelectionPolicyVersion: "selection/1",
		ReaderPolicyVersion:    "reader/1",
		Generator:              ResearchGeneratorLuminWriter,
		CitationStyle:          CitationStyleNumeric,
	}
}
