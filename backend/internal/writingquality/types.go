package writingquality

import (
	"context"
	"errors"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

var (
	ErrInvalidReport      = errors.New("invalid quality report")
	ErrGateDenied         = errors.New("quality gate denied")
	ErrInvalidValidator   = errors.New("invalid validator")
	ErrDuplicateValidator = errors.New("duplicate validator")
	ErrUnknownValidator   = errors.New("unknown validator")
)

type GateDecision struct {
	Target                writingkernel.QualityState
	Allowed               bool
	Checks                []writingkernel.QualityGateCheck
	BlockerCount          int
	OpenErrorCount        int
	WaivedErrorCount      int
	RequiredValidatorsMet bool
}

type UserQualitySummary struct {
	QualityState       writingkernel.QualityState
	RequestedAssurance writingkernel.AssuranceLevel
	AchievedAssurance  writingkernel.AssuranceLevel
	AssuranceSatisfied bool
	KeyFindings        []writingkernel.QualityFinding
}

type AuditQualityReport struct {
	Report writingkernel.QualityReport
}

type ValidatorCriticality string

const (
	CriticalityRequired ValidatorCriticality = "required"
	CriticalityAdvisory ValidatorCriticality = "advisory"
)

func (v ValidatorCriticality) valid() bool {
	return v == CriticalityRequired || v == CriticalityAdvisory
}

type DegradationPolicy string

const (
	DegradationFailClosed         DegradationPolicy = "fail_closed"
	DegradationEquivalentFallback DegradationPolicy = "equivalent_fallback"
	DegradationSkipWithWarning    DegradationPolicy = "skip_with_warning"
	DegradationLowerAssurance     DegradationPolicy = "lower_assurance"
)

func (v DegradationPolicy) valid() bool {
	switch v {
	case DegradationFailClosed, DegradationEquivalentFallback, DegradationSkipWithWarning, DegradationLowerAssurance:
		return true
	default:
		return false
	}
}

type ValidatorSpec struct {
	ID                  string
	Version             string
	Criticality         ValidatorCriticality
	DegradationPolicy   DegradationPolicy
	InputTypes          []string
	OutputTypes         []string
	EquivalentFallbacks []string
}

type ValidationOutput struct {
	Status   writingkernel.ValidatorStatus
	Findings []writingkernel.QualityFinding
}

type Validator interface {
	Validate(context.Context, ValidationInput) ValidationOutput
}

type ValidatorFunc func(context.Context, ValidationInput) ValidationOutput

func (f ValidatorFunc) Validate(ctx context.Context, input ValidationInput) ValidationOutput {
	return f(ctx, input)
}

type ValidatorExecution struct {
	Result   writingkernel.ValidatorResult
	Findings []writingkernel.QualityFinding
}

type DegradationRequest struct {
	ValidatorID    string
	FallbackID     string
	FallbackStatus writingkernel.ValidatorStatus
	FromAssurance  writingkernel.AssuranceLevel
	ReasonCode     string
}

type DegradationOutcome struct {
	Result      writingkernel.ValidatorResult
	Degradation *writingkernel.QualityDegradation
	Finding     *writingkernel.QualityFinding
	Assurance   writingkernel.AssuranceLevel
}
