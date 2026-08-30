package writingquality

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

type validatorRegistration struct {
	spec      ValidatorSpec
	validator Validator
}

type ValidatorRegistry struct {
	mu      sync.RWMutex
	entries map[string]validatorRegistration
}

func NewValidatorRegistry() *ValidatorRegistry {
	return &ValidatorRegistry{entries: make(map[string]validatorRegistration)}
}

func (r *ValidatorRegistry) Register(spec ValidatorSpec, validator Validator) error {
	if err := validateValidatorSpec(spec, validator); err != nil {
		return err
	}
	copySpec := cloneSpec(spec)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[copySpec.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateValidator, copySpec.ID)
	}
	r.entries[copySpec.ID] = validatorRegistration{spec: copySpec, validator: validator}
	return nil
}

func (r *ValidatorRegistry) Get(id string) (ValidatorSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[id]
	return cloneSpec(entry.spec), ok
}

func (r *ValidatorRegistry) List() []ValidatorSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]ValidatorSpec, 0, len(r.entries))
	for _, entry := range r.entries {
		result = append(result, cloneSpec(entry.spec))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (r *ValidatorRegistry) Run(ctx context.Context, id string, input ValidationInput) (ValidatorExecution, error) {
	r.mu.RLock()
	entry, ok := r.entries[id]
	r.mu.RUnlock()
	if !ok {
		return ValidatorExecution{}, fmt.Errorf("%w: %s", ErrUnknownValidator, id)
	}
	output := entry.validator.Validate(ctx, input)
	if !output.Status.Valid() {
		return ValidatorExecution{}, fmt.Errorf("%w: %s returned status %q", ErrInvalidValidator, id, output.Status)
	}
	findings := append([]writingkernel.QualityFinding(nil), output.Findings...)
	for i := range findings {
		if findings[i].ValidatorID == "" {
			findings[i].ValidatorID = entry.spec.ID
		}
		if findings[i].ValidatorStatus == "" {
			findings[i].ValidatorStatus = output.Status
		}
		if findings[i].RuleVersion == "" {
			findings[i].RuleVersion = entry.spec.Version
		}
	}
	return ValidatorExecution{
		Result: writingkernel.ValidatorResult{ValidatorID: entry.spec.ID, Version: entry.spec.Version,
			Required: entry.spec.Criticality == CriticalityRequired, Status: output.Status,
			Equivalence: writingkernel.ValidatorEquivalencePrimary},
		Findings: findings,
	}, nil
}

func (r *ValidatorRegistry) ResolveDegradation(request DegradationRequest) (DegradationOutcome, error) {
	r.mu.RLock()
	primary, ok := r.entries[request.ValidatorID]
	if !ok {
		r.mu.RUnlock()
		return DegradationOutcome{}, fmt.Errorf("%w: %s", ErrUnknownValidator, request.ValidatorID)
	}
	var fallback validatorRegistration
	if request.FallbackID != "" {
		fallback, ok = r.entries[request.FallbackID]
		if !ok {
			r.mu.RUnlock()
			return DegradationOutcome{}, fmt.Errorf("%w: %s", ErrUnknownValidator, request.FallbackID)
		}
	}
	r.mu.RUnlock()

	if !request.FromAssurance.Valid() {
		return DegradationOutcome{}, fmt.Errorf("%w: invalid assurance", ErrInvalidValidator)
	}
	reason := strings.TrimSpace(request.ReasonCode)
	if reason == "" {
		reason = "validator_unavailable"
	}
	required := primary.spec.Criticality == CriticalityRequired
	base := writingkernel.ValidatorResult{ValidatorID: primary.spec.ID, Version: primary.spec.Version, Required: required,
		Status: writingkernel.ValidatorStatusUnavailable, Equivalence: writingkernel.ValidatorEquivalenceNone}

	if request.FallbackID != "" && request.FallbackStatus == writingkernel.ValidatorStatusPassed &&
		contains(primary.spec.EquivalentFallbacks, request.FallbackID) && compatibleValidatorIO(primary.spec, fallback.spec) {
		base.Status = writingkernel.ValidatorStatusPassed
		base.Equivalence = writingkernel.ValidatorEquivalenceEquivalentFallback
		return DegradationOutcome{Result: base, Assurance: request.FromAssurance,
			Degradation: &writingkernel.QualityDegradation{ValidatorID: primary.spec.ID, Equivalence: "equivalent",
				FromAssurance: request.FromAssurance, ToAssurance: request.FromAssurance, ReasonCode: reason}}, nil
	}

	switch primary.spec.DegradationPolicy {
	case DegradationSkipWithWarning:
		base.Status = writingkernel.ValidatorStatusSkipped
		finding := degradationFinding(primary.spec, writingkernel.FindingSeverityWarning, "VALIDATOR_SKIPPED", "validator skipped under an advisory policy", "review the affected document scope manually", base.Status)
		return DegradationOutcome{Result: base, Finding: &finding, Assurance: request.FromAssurance}, nil
	case DegradationLowerAssurance:
		to := lowerAssurance(request.FromAssurance)
		if request.FallbackID != "" && request.FallbackStatus == writingkernel.ValidatorStatusPassed {
			base.Status = writingkernel.ValidatorStatusPassed
			base.Equivalence = writingkernel.ValidatorEquivalenceNonEquivalentFallback
		}
		finding := degradationFinding(primary.spec, writingkernel.FindingSeverityWarning, "ASSURANCE_DEGRADED", "a non-equivalent validation path lowered achieved assurance", "restore the primary or certified equivalent validator", base.Status)
		return DegradationOutcome{Result: base, Finding: &finding, Assurance: to,
			Degradation: &writingkernel.QualityDegradation{ValidatorID: primary.spec.ID, Equivalence: "non_equivalent",
				FromAssurance: request.FromAssurance, ToAssurance: to, ReasonCode: reason}}, nil
	case DegradationEquivalentFallback, DegradationFailClosed:
		finding := degradationFinding(primary.spec, writingkernel.FindingSeverityBlocker, "REQUIRED_VALIDATOR_UNAVAILABLE", "required validation could not run with an equivalent result", "restore the validator and rerun validation", base.Status)
		return DegradationOutcome{Result: base, Finding: &finding, Assurance: request.FromAssurance}, nil
	default:
		return DegradationOutcome{}, fmt.Errorf("%w: invalid degradation policy", ErrInvalidValidator)
	}
}

func validateValidatorSpec(spec ValidatorSpec, validator Validator) error {
	if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.Version) == "" || !spec.Criticality.valid() || !spec.DegradationPolicy.valid() || validator == nil || len(spec.InputTypes) == 0 || len(spec.OutputTypes) == 0 {
		return fmt.Errorf("%w: incomplete validator specification", ErrInvalidValidator)
	}
	for _, item := range append(append([]string(nil), spec.InputTypes...), spec.OutputTypes...) {
		if strings.TrimSpace(item) == "" {
			return fmt.Errorf("%w: blank input or output type", ErrInvalidValidator)
		}
	}
	if spec.Criticality == CriticalityRequired && spec.DegradationPolicy == DegradationSkipWithWarning {
		return fmt.Errorf("%w: required validator cannot skip with warning", ErrInvalidValidator)
	}
	if spec.DegradationPolicy == DegradationEquivalentFallback && len(spec.EquivalentFallbacks) == 0 {
		return fmt.Errorf("%w: equivalent fallback policy requires a fallback", ErrInvalidValidator)
	}
	if contains(spec.EquivalentFallbacks, spec.ID) {
		return fmt.Errorf("%w: validator cannot fall back to itself", ErrInvalidValidator)
	}
	return nil
}

func cloneSpec(spec ValidatorSpec) ValidatorSpec {
	copy := spec
	copy.InputTypes = append([]string(nil), spec.InputTypes...)
	copy.OutputTypes = append([]string(nil), spec.OutputTypes...)
	copy.EquivalentFallbacks = append([]string(nil), spec.EquivalentFallbacks...)
	return copy
}

func compatibleValidatorIO(primary, fallback ValidatorSpec) bool {
	return equalStringSet(primary.InputTypes, fallback.InputTypes) && equalStringSet(primary.OutputTypes, fallback.OutputTypes)
}

func equalStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func degradationFinding(spec ValidatorSpec, severity writingkernel.FindingSeverity, code, explanation, fixScope string, status writingkernel.ValidatorStatus) writingkernel.QualityFinding {
	return writingkernel.QualityFinding{
		FindingID: "finding_" + stableToken(spec.ID) + "_" + strings.ToLower(code), Severity: severity,
		Category: validatorCategory(spec.ID), Code: code, Message: explanation, ValidatorID: spec.ID,
		ValidatorStatus: status, RuleVersion: spec.Version, Explanation: explanation, FixScope: fixScope,
		Status: writingkernel.FindingStatusOpen,
	}
}

func validatorCategory(id string) writingkernel.FindingCategory {
	switch {
	case strings.Contains(id, "evidence"), strings.Contains(id, "artifact"):
		return writingkernel.FindingCategoryEvidence
	case strings.Contains(id, "semantic"):
		return writingkernel.FindingCategorySemanticPreservation
	case strings.Contains(id, "readability"):
		return writingkernel.FindingCategoryReadability
	case strings.Contains(id, "style"), strings.Contains(id, "voice"):
		return writingkernel.FindingCategoryVoice
	default:
		return writingkernel.FindingCategoryStructure
	}
}

func stableToken(value string) string {
	return strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, value), "_")
}
