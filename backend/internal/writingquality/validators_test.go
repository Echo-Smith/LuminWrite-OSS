package writingquality

import (
	"context"
	"errors"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

func TestDefaultRegistryDeclaresGovernedValidatorPolicies(t *testing.T) {
	registry := DefaultValidatorRegistry()
	for _, id := range []string{ValidatorASTIntegrity, ValidatorRequiredSections, ValidatorContractConsistency, ValidatorSemanticPreservation, ValidatorVersionConsistency, ValidatorArtifactHash} {
		spec, ok := registry.Get(id)
		if !ok || spec.Criticality != CriticalityRequired || spec.DegradationPolicy != DegradationFailClosed {
			t.Fatalf("%s spec = %#v, present=%v", id, spec, ok)
		}
	}
	for _, id := range []string{ValidatorReadability, ValidatorStyle} {
		spec, ok := registry.Get(id)
		if !ok || spec.Criticality != CriticalityAdvisory || spec.DegradationPolicy != DegradationSkipWithWarning {
			t.Fatalf("%s spec = %#v, present=%v", id, spec, ok)
		}
	}
	if spec, ok := registry.Get(ValidatorEvidence); !ok || spec.DegradationPolicy != DegradationLowerAssurance {
		t.Fatalf("evidence spec = %#v, present=%v", spec, ok)
	}
}

func TestASTAndRequiredSectionValidators(t *testing.T) {
	registry := DefaultValidatorRegistry()
	document := qualityTestDocument(t)
	execution, err := registry.Run(context.Background(), ValidatorASTIntegrity, ValidationInput{Candidate: &document})
	if err != nil || execution.Result.Status != writingkernel.ValidatorStatusPassed {
		t.Fatalf("AST execution = %#v, err=%v", execution, err)
	}
	execution, err = registry.Run(context.Background(), ValidatorRequiredSections, ValidationInput{Candidate: &document, RequiredSections: []string{"Introduction", "Conclusion", "Sources"}})
	if err != nil {
		t.Fatal(err)
	}
	if execution.Result.Status != writingkernel.ValidatorStatusFailed || len(execution.Findings) != 2 || execution.Findings[0].FindingID == execution.Findings[1].FindingID || execution.Findings[0].Code != "REQUIRED_SECTION_MISSING" {
		t.Fatalf("required section execution = %#v", execution)
	}
}

func TestArtifactAndVersionMismatchAreBlockers(t *testing.T) {
	registry := DefaultValidatorRegistry()
	document := qualityTestDocument(t)
	version, err := registry.Run(context.Background(), ValidatorVersionConsistency, ValidationInput{Candidate: &document, ValidatedVersionID: document.VersionID, CommittedVersionID: "ver_other"})
	if err != nil || version.Result.Status != writingkernel.ValidatorStatusFailed || version.Findings[0].Severity != writingkernel.FindingSeverityBlocker {
		t.Fatalf("version execution = %#v, err=%v", version, err)
	}
	artifact, err := registry.Run(context.Background(), ValidatorArtifactHash, ValidationInput{ArtifactHashes: []ArtifactHashCheck{{ArtifactID: "art_1", ExpectedHash: "sha256:expected", ActualHash: "sha256:actual"}}})
	if err != nil || artifact.Result.Status != writingkernel.ValidatorStatusFailed || artifact.Findings[0].Code != "ARTIFACT_HASH_MISMATCH" {
		t.Fatalf("artifact execution = %#v, err=%v", artifact, err)
	}
}

func TestSemanticPreservationUsesPluggableChecker(t *testing.T) {
	registry := DefaultValidatorRegistry()
	base := qualityTestDocument(t)
	candidate := base.Clone()
	candidate.VersionID = "ver_candidate_2"
	checker := semanticCheckerFunc(func(context.Context, writingkernel.DocumentVersion, writingkernel.DocumentVersion) (SemanticPreservationResult, error) {
		return SemanticPreservationResult{Preserved: false, Explanation: "locked claim changed", Locations: []writingkernel.FindingLocation{{BlockID: "blk_intro"}}}, nil
	})
	execution, err := registry.Run(context.Background(), ValidatorSemanticPreservation, ValidationInput{Base: &base, Candidate: &candidate, SemanticChecker: checker})
	if err != nil {
		t.Fatal(err)
	}
	if execution.Result.Status != writingkernel.ValidatorStatusFailed || len(execution.Findings) != 1 || execution.Findings[0].Location == nil || execution.Findings[0].Location.BlockID != "blk_intro" {
		t.Fatalf("semantic execution = %#v", execution)
	}

	checker = semanticCheckerFunc(func(context.Context, writingkernel.DocumentVersion, writingkernel.DocumentVersion) (SemanticPreservationResult, error) {
		return SemanticPreservationResult{}, errors.New("provider unavailable")
	})
	execution, err = registry.Run(context.Background(), ValidatorSemanticPreservation, ValidationInput{Base: &base, Candidate: &candidate, SemanticChecker: checker})
	if err != nil || execution.Result.Status != writingkernel.ValidatorStatusUnavailable {
		t.Fatalf("unavailable execution = %#v, err=%v", execution, err)
	}
}

type semanticCheckerFunc func(context.Context, writingkernel.DocumentVersion, writingkernel.DocumentVersion) (SemanticPreservationResult, error)

func (f semanticCheckerFunc) Check(ctx context.Context, base, candidate writingkernel.DocumentVersion) (SemanticPreservationResult, error) {
	return f(ctx, base, candidate)
}

func qualityTestDocument(t *testing.T) writingkernel.DocumentVersion {
	t.Helper()
	document := writingkernel.DocumentVersion{SchemaVersion: writingkernel.SchemaVersionV1, DocumentID: "doc_quality", VersionID: "ver_quality", Root: &writingkernel.DocumentNode{
		Type: writingkernel.NodeTypeDocument, Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{{
			Type: writingkernel.NodeTypeSection, Attrs: map[string]any{"section_id": "introduction", "title": "Introduction"}, Children: []*writingkernel.DocumentNode{{
				Type: writingkernel.NodeTypeParagraph, Attrs: map[string]any{}, Children: []*writingkernel.DocumentNode{{Type: writingkernel.NodeTypeText, Text: "Quality governed writing", Attrs: map[string]any{}}},
			}},
		}},
	}}
	stampQualityOrigin(document.Root)
	sealed, err := document.WithComputedHashes()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func stampQualityOrigin(node *writingkernel.DocumentNode) {
	if node == nil {
		return
	}
	node.Origin = writingkernel.Origin{Kind: writingkernel.OriginSystem, Ref: "quality/test"}
	for _, child := range node.Children {
		stampQualityOrigin(child)
	}
}
