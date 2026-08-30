package writingquality

import (
	"context"
	"errors"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

func TestRegistryRejectsInvalidAndDuplicateValidators(t *testing.T) {
	registry := NewValidatorRegistry()
	validator := ValidatorFunc(func(context.Context, ValidationInput) ValidationOutput {
		return ValidationOutput{Status: writingkernel.ValidatorStatusPassed}
	})
	invalid := ValidatorSpec{ID: "required-style", Version: "1", Criticality: CriticalityRequired, DegradationPolicy: DegradationSkipWithWarning, InputTypes: []string{"document_ast"}, OutputTypes: []string{"quality_finding"}}
	if err := registry.Register(invalid, validator); !errors.Is(err, ErrInvalidValidator) {
		t.Fatalf("invalid registration error = %v", err)
	}
	spec := ValidatorSpec{ID: "style", Version: "1", Criticality: CriticalityAdvisory, DegradationPolicy: DegradationSkipWithWarning, InputTypes: []string{"document_ast"}, OutputTypes: []string{"quality_finding"}}
	if err := registry.Register(spec, validator); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(spec, validator); !errors.Is(err, ErrDuplicateValidator) {
		t.Fatalf("duplicate registration error = %v", err)
	}
}

func TestEquivalentFallbackMaintainsAssurance(t *testing.T) {
	registry := NewValidatorRegistry()
	runner := ValidatorFunc(func(context.Context, ValidationInput) ValidationOutput {
		return ValidationOutput{Status: writingkernel.ValidatorStatusPassed}
	})
	primary := ValidatorSpec{ID: "evidence.primary", Version: "2", Criticality: CriticalityRequired, DegradationPolicy: DegradationEquivalentFallback, InputTypes: []string{"claims"}, OutputTypes: []string{"evidence_report"}, EquivalentFallbacks: []string{"evidence.backup"}}
	fallback := primary
	fallback.ID = "evidence.backup"
	fallback.DegradationPolicy = DegradationFailClosed
	fallback.EquivalentFallbacks = nil
	if err := registry.Register(primary, runner); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(fallback, runner); err != nil {
		t.Fatal(err)
	}
	outcome, err := registry.ResolveDegradation(DegradationRequest{ValidatorID: primary.ID, FallbackID: fallback.ID, FallbackStatus: writingkernel.ValidatorStatusPassed, FromAssurance: writingkernel.AssuranceLevelStrict, ReasonCode: "provider_timeout"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Assurance != writingkernel.AssuranceLevelStrict || outcome.Result.Equivalence != writingkernel.ValidatorEquivalenceEquivalentFallback || outcome.Result.Status != writingkernel.ValidatorStatusPassed {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestNonEquivalentEvidenceFallbackLowersAssurance(t *testing.T) {
	registry := NewValidatorRegistry()
	runner := ValidatorFunc(func(context.Context, ValidationInput) ValidationOutput {
		return ValidationOutput{Status: writingkernel.ValidatorStatusPassed}
	})
	primary := ValidatorSpec{ID: "core.evidence", Version: "1", Criticality: CriticalityRequired, DegradationPolicy: DegradationLowerAssurance, InputTypes: []string{"claims"}, OutputTypes: []string{"evidence_report"}}
	fallback := ValidatorSpec{ID: "heuristic.evidence", Version: "1", Criticality: CriticalityAdvisory, DegradationPolicy: DegradationSkipWithWarning, InputTypes: primary.InputTypes, OutputTypes: primary.OutputTypes}
	if err := registry.Register(primary, runner); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(fallback, runner); err != nil {
		t.Fatal(err)
	}
	outcome, err := registry.ResolveDegradation(DegradationRequest{ValidatorID: primary.ID, FallbackID: fallback.ID, FallbackStatus: writingkernel.ValidatorStatusPassed, FromAssurance: writingkernel.AssuranceLevelSourced})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Assurance != writingkernel.AssuranceLevelStandard || outcome.Degradation == nil || outcome.Degradation.Equivalence != "non_equivalent" || outcome.Result.Equivalence != writingkernel.ValidatorEquivalenceNonEquivalentFallback {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestFailClosedAndAdvisorySkipPolicies(t *testing.T) {
	registry := DefaultValidatorRegistry()
	closed, err := registry.ResolveDegradation(DegradationRequest{ValidatorID: ValidatorASTIntegrity, FromAssurance: writingkernel.AssuranceLevelStandard})
	if err != nil {
		t.Fatal(err)
	}
	if closed.Finding == nil || closed.Finding.Severity != writingkernel.FindingSeverityBlocker || closed.Result.Status != writingkernel.ValidatorStatusUnavailable {
		t.Fatalf("fail-closed outcome = %#v", closed)
	}
	skipped, err := registry.ResolveDegradation(DegradationRequest{ValidatorID: ValidatorReadability, FromAssurance: writingkernel.AssuranceLevelStandard})
	if err != nil {
		t.Fatal(err)
	}
	if skipped.Finding == nil || skipped.Finding.Severity != writingkernel.FindingSeverityWarning || skipped.Result.Status != writingkernel.ValidatorStatusSkipped {
		t.Fatalf("skip outcome = %#v", skipped)
	}
}
