package writingquality

import (
	"context"
	"fmt"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

const (
	ValidatorASTIntegrity         = "core.ast.integrity"
	ValidatorRequiredSections     = "core.section.required"
	ValidatorContractConsistency  = "core.contract.consistency"
	ValidatorSemanticPreservation = "core.semantic.preservation"
	ValidatorVersionConsistency   = "core.version.consistency"
	ValidatorArtifactHash         = "core.artifact.hash"
	ValidatorEvidence             = "core.evidence"
	ValidatorReadability          = "core.readability"
	ValidatorStyle                = "core.style"
)

type ArtifactHashCheck struct {
	ArtifactID   string
	ExpectedHash string
	ActualHash   string
}

type ValidationInput struct {
	Candidate               *writingkernel.DocumentVersion
	Base                    *writingkernel.DocumentVersion
	Contract                *writingkernel.WritingContract
	RequiredSections        []string
	CoveredRequiredPoints   map[string]bool
	PresentProhibitedPoints map[string]bool
	ValidatedVersionID      string
	CommittedVersionID      string
	ArtifactHashes          []ArtifactHashCheck
	SemanticChecker         SemanticPreservationChecker
}

type SemanticPreservationResult struct {
	Preserved   bool
	Explanation string
	Locations   []writingkernel.FindingLocation
}

type SemanticPreservationChecker interface {
	Check(context.Context, writingkernel.DocumentVersion, writingkernel.DocumentVersion) (SemanticPreservationResult, error)
}

func DefaultValidatorRegistry() *ValidatorRegistry {
	registry := NewValidatorRegistry()
	mustRegister(registry, ValidatorSpec{ID: ValidatorASTIntegrity, Version: "1.0.0", Criticality: CriticalityRequired, DegradationPolicy: DegradationFailClosed, InputTypes: []string{"document_ast"}, OutputTypes: []string{"quality_finding"}}, astIntegrityValidator{})
	mustRegister(registry, ValidatorSpec{ID: ValidatorRequiredSections, Version: "1.0.0", Criticality: CriticalityRequired, DegradationPolicy: DegradationFailClosed, InputTypes: []string{"document_ast", "section_requirements"}, OutputTypes: []string{"quality_finding"}}, requiredSectionsValidator{})
	mustRegister(registry, ValidatorSpec{ID: ValidatorContractConsistency, Version: "1.0.0", Criticality: CriticalityRequired, DegradationPolicy: DegradationFailClosed, InputTypes: []string{"document_ast", "writing_contract"}, OutputTypes: []string{"quality_finding"}}, contractConsistencyValidator{})
	mustRegister(registry, ValidatorSpec{ID: ValidatorSemanticPreservation, Version: "1.0.0", Criticality: CriticalityRequired, DegradationPolicy: DegradationFailClosed, InputTypes: []string{"base_document_ast", "candidate_document_ast"}, OutputTypes: []string{"quality_finding"}}, semanticPreservationValidator{})
	mustRegister(registry, ValidatorSpec{ID: ValidatorVersionConsistency, Version: "1.0.0", Criticality: CriticalityRequired, DegradationPolicy: DegradationFailClosed, InputTypes: []string{"document_versions"}, OutputTypes: []string{"quality_finding"}}, versionConsistencyValidator{})
	mustRegister(registry, ValidatorSpec{ID: ValidatorArtifactHash, Version: "1.0.0", Criticality: CriticalityRequired, DegradationPolicy: DegradationFailClosed, InputTypes: []string{"artifact_manifest"}, OutputTypes: []string{"quality_finding"}}, artifactHashValidator{})
	mustRegister(registry, ValidatorSpec{ID: ValidatorEvidence, Version: "1.0.0", Criticality: CriticalityRequired, DegradationPolicy: DegradationLowerAssurance, InputTypes: []string{"claim_set", "source_set"}, OutputTypes: []string{"quality_finding"}}, unavailableValidator{})
	mustRegister(registry, ValidatorSpec{ID: ValidatorReadability, Version: "1.0.0", Criticality: CriticalityAdvisory, DegradationPolicy: DegradationSkipWithWarning, InputTypes: []string{"document_ast"}, OutputTypes: []string{"quality_finding"}}, unavailableValidator{})
	mustRegister(registry, ValidatorSpec{ID: ValidatorStyle, Version: "1.0.0", Criticality: CriticalityAdvisory, DegradationPolicy: DegradationSkipWithWarning, InputTypes: []string{"document_ast", "writing_contract"}, OutputTypes: []string{"quality_finding"}}, unavailableValidator{})
	return registry
}

type astIntegrityValidator struct{}

func (astIntegrityValidator) Validate(_ context.Context, input ValidationInput) ValidationOutput {
	if input.Candidate == nil {
		return ValidationOutput{Status: writingkernel.ValidatorStatusUnavailable}
	}
	if err := input.Candidate.Validate(); err != nil {
		return failedOutput(ValidatorASTIntegrity, "AST_INTEGRITY_FAILED", writingkernel.FindingCategoryStructure, err.Error(), "regenerate or repair the complete document AST", nil)
	}
	return ValidationOutput{Status: writingkernel.ValidatorStatusPassed, Findings: []writingkernel.QualityFinding{}}
}

type requiredSectionsValidator struct{}

func (requiredSectionsValidator) Validate(_ context.Context, input ValidationInput) ValidationOutput {
	if input.Candidate == nil || input.Candidate.Root == nil {
		return ValidationOutput{Status: writingkernel.ValidatorStatusUnavailable}
	}
	present := make(map[string]bool)
	collectSectionKeys(input.Candidate.Root, present)
	findings := make([]writingkernel.QualityFinding, 0)
	for _, required := range input.RequiredSections {
		if !present[normalize(required)] {
			location := &writingkernel.FindingLocation{BlockID: input.Candidate.Root.BlockID, ClaimID: "required_section:" + normalize(required)}
			findings = append(findings, validationFinding(ValidatorRequiredSections, "REQUIRED_SECTION_MISSING", writingkernel.FindingSeverityBlocker, writingkernel.FindingCategoryContract, "required section is missing: "+required, "add the required section and its content", location))
		}
	}
	if len(findings) > 0 {
		return ValidationOutput{Status: writingkernel.ValidatorStatusFailed, Findings: findings}
	}
	return ValidationOutput{Status: writingkernel.ValidatorStatusPassed, Findings: []writingkernel.QualityFinding{}}
}

type contractConsistencyValidator struct{}

func (contractConsistencyValidator) Validate(_ context.Context, input ValidationInput) ValidationOutput {
	if input.Contract == nil || input.Candidate == nil {
		return ValidationOutput{Status: writingkernel.ValidatorStatusUnavailable}
	}
	if err := input.Contract.Validate(); err != nil {
		return failedOutput(ValidatorContractConsistency, "CONTRACT_INVALID", writingkernel.FindingCategoryContract, err.Error(), "repair and reconfirm the WritingContract", nil)
	}
	if len(input.Contract.Content.RequiredPoints) > 0 && input.CoveredRequiredPoints == nil {
		return ValidationOutput{Status: writingkernel.ValidatorStatusUnavailable}
	}
	findings := make([]writingkernel.QualityFinding, 0)
	for _, point := range input.Contract.Content.RequiredPoints {
		if !input.CoveredRequiredPoints[normalize(point)] && !input.CoveredRequiredPoints[point] {
			location := &writingkernel.FindingLocation{ClaimID: "required_point:" + normalize(point)}
			findings = append(findings, validationFinding(ValidatorContractConsistency, "CONTRACT_REQUIRED_POINT_MISSING", writingkernel.FindingSeverityBlocker, writingkernel.FindingCategoryContract, "required contract point is not covered: "+point, "revise the document to cover the required point", location))
		}
	}
	for _, point := range input.Contract.Content.ProhibitedPoints {
		if input.PresentProhibitedPoints[normalize(point)] || input.PresentProhibitedPoints[point] {
			location := &writingkernel.FindingLocation{ClaimID: "prohibited_point:" + normalize(point)}
			findings = append(findings, validationFinding(ValidatorContractConsistency, "CONTRACT_PROHIBITED_POINT_PRESENT", writingkernel.FindingSeverityBlocker, writingkernel.FindingCategoryContract, "prohibited contract point is present: "+point, "remove the prohibited point", location))
		}
	}
	if len(findings) > 0 {
		return ValidationOutput{Status: writingkernel.ValidatorStatusFailed, Findings: findings}
	}
	return ValidationOutput{Status: writingkernel.ValidatorStatusPassed, Findings: []writingkernel.QualityFinding{}}
}

type semanticPreservationValidator struct{}

func (semanticPreservationValidator) Validate(ctx context.Context, input ValidationInput) ValidationOutput {
	if input.Base == nil || input.Candidate == nil || input.SemanticChecker == nil {
		return ValidationOutput{Status: writingkernel.ValidatorStatusUnavailable}
	}
	result, err := input.SemanticChecker.Check(ctx, *input.Base, *input.Candidate)
	if err != nil {
		return ValidationOutput{Status: writingkernel.ValidatorStatusUnavailable}
	}
	if result.Preserved {
		return ValidationOutput{Status: writingkernel.ValidatorStatusPassed, Findings: []writingkernel.QualityFinding{}}
	}
	message := strings.TrimSpace(result.Explanation)
	if message == "" {
		message = "the revision changed protected meaning"
	}
	if len(result.Locations) == 0 {
		return failedOutput(ValidatorSemanticPreservation, "SEMANTIC_PRESERVATION_FAILED", writingkernel.FindingCategorySemanticPreservation, message, "restore the protected meaning or amend the contract", nil)
	}
	findings := make([]writingkernel.QualityFinding, 0, len(result.Locations))
	for i := range result.Locations {
		location := result.Locations[i]
		findings = append(findings, validationFinding(ValidatorSemanticPreservation, "SEMANTIC_PRESERVATION_FAILED", writingkernel.FindingSeverityBlocker, writingkernel.FindingCategorySemanticPreservation, message, "restore the protected meaning or amend the contract", &location))
	}
	return ValidationOutput{Status: writingkernel.ValidatorStatusFailed, Findings: findings}
}

type versionConsistencyValidator struct{}

func (versionConsistencyValidator) Validate(_ context.Context, input ValidationInput) ValidationOutput {
	if input.Candidate == nil || input.ValidatedVersionID == "" || input.CommittedVersionID == "" {
		return ValidationOutput{Status: writingkernel.ValidatorStatusUnavailable}
	}
	if input.Candidate.VersionID != input.ValidatedVersionID || input.ValidatedVersionID != input.CommittedVersionID {
		return failedOutput(ValidatorVersionConsistency, "VERSION_MISMATCH", writingkernel.FindingCategoryStructure, "validated, committed, and candidate versions differ", "restore the committed candidate and rerun validation", nil)
	}
	return ValidationOutput{Status: writingkernel.ValidatorStatusPassed, Findings: []writingkernel.QualityFinding{}}
}

type artifactHashValidator struct{}

func (artifactHashValidator) Validate(_ context.Context, input ValidationInput) ValidationOutput {
	if input.ArtifactHashes == nil {
		return ValidationOutput{Status: writingkernel.ValidatorStatusUnavailable}
	}
	findings := make([]writingkernel.QualityFinding, 0)
	for _, artifact := range input.ArtifactHashes {
		if artifact.ArtifactID == "" || artifact.ExpectedHash == "" || artifact.ExpectedHash != artifact.ActualHash {
			location := &writingkernel.FindingLocation{SourceRef: artifact.ArtifactID}
			findings = append(findings, validationFinding(ValidatorArtifactHash, "ARTIFACT_HASH_MISMATCH", writingkernel.FindingSeverityBlocker, writingkernel.FindingCategoryEvidence, "artifact content hash does not match its immutable manifest", "restore the original artifact or regenerate downstream outputs", location))
		}
	}
	if len(findings) > 0 {
		return ValidationOutput{Status: writingkernel.ValidatorStatusFailed, Findings: findings}
	}
	return ValidationOutput{Status: writingkernel.ValidatorStatusPassed, Findings: []writingkernel.QualityFinding{}}
}

type unavailableValidator struct{}

func (unavailableValidator) Validate(context.Context, ValidationInput) ValidationOutput {
	return ValidationOutput{Status: writingkernel.ValidatorStatusUnavailable}
}

func failedOutput(validatorID, code string, category writingkernel.FindingCategory, explanation, fixScope string, location *writingkernel.FindingLocation) ValidationOutput {
	return ValidationOutput{Status: writingkernel.ValidatorStatusFailed, Findings: []writingkernel.QualityFinding{
		validationFinding(validatorID, code, writingkernel.FindingSeverityBlocker, category, explanation, fixScope, location),
	}}
}

func validationFinding(validatorID, code string, severity writingkernel.FindingSeverity, category writingkernel.FindingCategory, explanation, fixScope string, location *writingkernel.FindingLocation) writingkernel.QualityFinding {
	locationToken := "document"
	if location != nil {
		locationToken = location.BlockID + "_" + location.ClaimID + "_" + location.SourceRef
	}
	return writingkernel.QualityFinding{
		FindingID: "finding_" + stableToken(validatorID) + "_" + strings.ToLower(code) + "_" + stableToken(locationToken),
		Severity:  severity, Category: category, Code: code, Message: explanation, ValidatorID: validatorID,
		ValidatorStatus: writingkernel.ValidatorStatusFailed, RuleVersion: "1.0.0", Explanation: explanation,
		FixScope: fixScope, Location: location, Status: writingkernel.FindingStatusOpen,
	}
}

func collectSectionKeys(node *writingkernel.DocumentNode, result map[string]bool) {
	if node == nil {
		return
	}
	if node.Type == writingkernel.NodeTypeSection {
		for _, key := range []string{"section_id", "title"} {
			if value, ok := node.Attrs[key].(string); ok {
				result[normalize(value)] = true
			}
		}
	}
	for _, child := range node.Children {
		collectSectionKeys(child, result)
	}
}

func normalize(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func mustRegister(registry *ValidatorRegistry, spec ValidatorSpec, validator Validator) {
	if err := registry.Register(spec, validator); err != nil {
		panic(fmt.Sprintf("register built-in validator %s: %v", spec.ID, err))
	}
}
