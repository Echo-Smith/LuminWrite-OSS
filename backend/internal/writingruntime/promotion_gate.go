package writingruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type RolloutGovernanceStore interface {
	RolloutEvidenceHealth(context.Context, string, time.Time) (writingstore.RolloutEvidenceHealth, error)
	LatestRolloutApproval(context.Context, string, int, string) (writingstore.RolloutApprovalRecord, error)
	// LatestRolloutApprovalByActivationKey resolves the ladder check: a
	// percentage promotion requires the same change (activation key) to have
	// already passed the allowlist stage, under whatever policy hash carried it.
	LatestRolloutApprovalByActivationKey(context.Context, string, string) (writingstore.RolloutApprovalRecord, error)
}

type PromotionCriteria struct {
	MinimumComparisons int64
	MaximumFailures    int64
	EvidenceWindow     time.Duration
	Freshness          time.Duration
}

func DefaultPromotionCriteria() PromotionCriteria {
	return PromotionCriteria{MinimumComparisons: 3, MaximumFailures: 0, EvidenceWindow: 7 * 24 * time.Hour, Freshness: 24 * time.Hour}
}

func normalizePromotionCriteria(criteria PromotionCriteria) PromotionCriteria {
	if criteria.MinimumComparisons < 1 || criteria.EvidenceWindow <= 0 || criteria.Freshness <= 0 {
		return DefaultPromotionCriteria()
	}
	return criteria
}

type PromotionAssessment struct {
	Allowed    bool                               `json:"allowed"`
	Reasons    []string                           `json:"reasons"`
	Health     writingstore.RolloutEvidenceHealth `json:"health"`
	ApprovalID string                             `json:"approval_id,omitempty"`
	AssessedAt time.Time                          `json:"assessed_at"`
}

// assessPromotionEvidence runs the shared evidence criteria for one promotion
// target: the policy must carry the expected mode, be active, and show fresh
// failure-free comparison evidence under its own policy hash.
func assessPromotionEvidence(ctx context.Context, store RolloutGovernanceStore, criteria PromotionCriteria, now time.Time, policy AdapterRolloutPolicy, expectedMode RolloutMode) (writingstore.RolloutEvidenceHealth, []string, error) {
	reasons := []string{}
	if store == nil {
		return writingstore.RolloutEvidenceHealth{}, reasons, ErrRuntimeNotReady
	}
	if err := policy.Validate(); err != nil {
		return writingstore.RolloutEvidenceHealth{}, reasons, err
	}
	if policy.Mode != expectedMode {
		reasons = append(reasons, "target_mode_not_"+string(expectedMode))
		return writingstore.RolloutEvidenceHealth{}, reasons, nil
	}
	if policy.KillSwitch {
		reasons = append(reasons, "policy_kill_switch")
	}
	if !policy.EffectiveAt.IsZero() && now.Before(policy.EffectiveAt) {
		reasons = append(reasons, "policy_not_effective")
	}
	if !policy.ExpiresAt.IsZero() && !now.Before(policy.ExpiresAt) {
		reasons = append(reasons, "policy_expired")
	}
	health, err := store.RolloutEvidenceHealth(ctx, policy.PolicyHash, now.Add(-criteria.EvidenceWindow))
	if err != nil {
		return writingstore.RolloutEvidenceHealth{}, reasons, err
	}
	if health.ComparisonRecords < criteria.MinimumComparisons {
		reasons = append(reasons, "insufficient_comparisons")
	}
	if health.FailedRecords > criteria.MaximumFailures {
		reasons = append(reasons, "evidence_failures_exceeded")
	}
	if health.LastRecordedAt.IsZero() || now.Sub(health.LastRecordedAt) > criteria.Freshness {
		reasons = append(reasons, "evidence_stale")
	}
	return health, reasons, nil
}

// bindPromotionApproval completes an evidence assessment with the exact-scope
// approval binding: same hash, version, activation key, target mode, unexpired,
// and not older than the freshest evidence.
func bindPromotionApproval(ctx context.Context, store RolloutGovernanceStore, now time.Time, policy AdapterRolloutPolicy, assessment PromotionAssessment, expectedTargetMode string) (PromotionAssessment, error) {
	approval, err := store.LatestRolloutApproval(ctx, policy.PolicyHash, policy.PolicyVersion, policy.ActivationKey)
	if errors.Is(err, writingstore.ErrNotFound) {
		assessment.Allowed = false
		assessment.Reasons = append(assessment.Reasons, "approval_missing")
		return assessment, nil
	}
	if err != nil {
		return assessment, err
	}
	assessment.ApprovalID = approval.ApprovalID
	if approval.PolicyHash != policy.PolicyHash || approval.PolicyVersion != policy.PolicyVersion || approval.ActivationKey != policy.ActivationKey {
		assessment.Reasons = append(assessment.Reasons, "approval_scope_mismatch")
	}
	if approval.TargetMode != expectedTargetMode {
		assessment.Reasons = append(assessment.Reasons, "approval_mode_mismatch")
	}
	if !approval.ExpiresAt.After(now) {
		assessment.Reasons = append(assessment.Reasons, "approval_expired")
	}
	if approval.EvidenceLastRecordedAt.Before(assessment.Health.LastRecordedAt) {
		assessment.Reasons = append(assessment.Reasons, "approval_evidence_stale")
	}
	assessment.Allowed = len(assessment.Reasons) == 0
	return assessment, nil
}

type AllowlistPromotionGate struct {
	Store    RolloutGovernanceStore
	Criteria PromotionCriteria
	Now      func() time.Time
}

func (gate AllowlistPromotionGate) now() time.Time {
	if gate.Now != nil {
		return gate.Now().UTC()
	}
	return time.Now().UTC()
}

func (gate AllowlistPromotionGate) EvidenceAssessment(ctx context.Context, policy AdapterRolloutPolicy) (PromotionAssessment, error) {
	now := gate.now()
	assessment := PromotionAssessment{Reasons: []string{}, AssessedAt: now}
	health, reasons, err := assessPromotionEvidence(ctx, gate.Store, normalizePromotionCriteria(gate.Criteria), now, policy, RolloutAllowlist)
	assessment.Health = health
	assessment.Reasons = append(assessment.Reasons, reasons...)
	if err != nil {
		return assessment, err
	}
	assessment.Allowed = len(assessment.Reasons) == 0
	return assessment, nil
}

func (gate AllowlistPromotionGate) Assess(ctx context.Context, policy AdapterRolloutPolicy) (PromotionAssessment, error) {
	now := gate.now()
	assessment := PromotionAssessment{Reasons: []string{}, AssessedAt: now}
	health, reasons, err := assessPromotionEvidence(ctx, gate.Store, normalizePromotionCriteria(gate.Criteria), now, policy, RolloutAllowlist)
	assessment.Health = health
	assessment.Reasons = append(assessment.Reasons, reasons...)
	if err != nil {
		return assessment, err
	}
	assessment.Allowed = len(assessment.Reasons) == 0
	if !assessment.Allowed {
		return assessment, nil
	}
	return bindPromotionApproval(ctx, gate.Store, now, policy, assessment, string(RolloutAllowlist))
}

// PercentagePromotionGate governs the allowlist → percentage rung. It applies
// the same evidence criteria as the allowlist gate, requires a
// percentage-target approval bound to the exact policy hash/version/key, and
// enforces the ladder mechanically: the same activation key must already carry
// an allowlist-stage approval. It never switches traffic by itself; it only
// decides whether the gated policy provider may serve a percentage policy.
type PercentagePromotionGate struct {
	Store    RolloutGovernanceStore
	Criteria PromotionCriteria
	Now      func() time.Time
}

func (gate PercentagePromotionGate) now() time.Time {
	if gate.Now != nil {
		return gate.Now().UTC()
	}
	return time.Now().UTC()
}

func (gate PercentagePromotionGate) EvidenceAssessment(ctx context.Context, policy AdapterRolloutPolicy) (PromotionAssessment, error) {
	now := gate.now()
	assessment := PromotionAssessment{Reasons: []string{}, AssessedAt: now}
	health, reasons, err := assessPromotionEvidence(ctx, gate.Store, normalizePromotionCriteria(gate.Criteria), now, policy, RolloutPercentage)
	assessment.Health = health
	assessment.Reasons = append(assessment.Reasons, reasons...)
	if err != nil {
		return assessment, err
	}
	assessment.Allowed = len(assessment.Reasons) == 0
	return assessment, nil
}

func (gate PercentagePromotionGate) Assess(ctx context.Context, policy AdapterRolloutPolicy) (PromotionAssessment, error) {
	now := gate.now()
	assessment := PromotionAssessment{Reasons: []string{}, AssessedAt: now}
	health, reasons, err := assessPromotionEvidence(ctx, gate.Store, normalizePromotionCriteria(gate.Criteria), now, policy, RolloutPercentage)
	assessment.Health = health
	assessment.Reasons = append(assessment.Reasons, reasons...)
	if err != nil {
		return assessment, err
	}
	assessment.Allowed = len(assessment.Reasons) == 0
	if !assessment.Allowed {
		return assessment, nil
	}
	if _, err := gate.Store.LatestRolloutApprovalByActivationKey(ctx, policy.ActivationKey, string(RolloutAllowlist)); errors.Is(err, writingstore.ErrNotFound) {
		assessment.Allowed = false
		assessment.Reasons = append(assessment.Reasons, "allowlist_stage_missing")
		return assessment, nil
	} else if err != nil {
		return assessment, err
	}
	return bindPromotionApproval(ctx, gate.Store, now, policy, assessment, string(RolloutPercentage))
}

type GatedRolloutPolicyProvider struct {
	Base RolloutPolicyProvider
	Gate AllowlistPromotionGate
	// PercentageGate governs percentage policies. A nil gate keeps percentage
	// policies fail-closed even when a percentage approval exists in the store.
	PercentageGate *PercentagePromotionGate
	Evidence       RolloutEvidenceStore
}

func (provider GatedRolloutPolicyProvider) Policy(ctx context.Context, identity ExecutionIdentity) (AdapterRolloutPolicy, error) {
	if provider.Base == nil {
		return AdapterRolloutPolicy{}, ErrRuntimeNotReady
	}
	policy, err := provider.Base.Policy(ctx, identity)
	if err != nil {
		return AdapterRolloutPolicy{}, err
	}
	if policy.Mode == RolloutOff || policy.Mode == RolloutShadow {
		return policy, nil
	}
	var assessment PromotionAssessment
	var gateErr error
	switch policy.Mode {
	case RolloutPercentage:
		if provider.PercentageGate == nil {
			gateErr = fmt.Errorf("percentage promotion gate is not configured")
			assessment = PromotionAssessment{Reasons: []string{"percentage_gate_not_configured"}}
		} else {
			assessment, gateErr = provider.PercentageGate.Assess(ctx, policy)
		}
	default:
		assessment, gateErr = provider.Gate.Assess(ctx, policy)
	}
	if gateErr == nil && assessment.Allowed {
		return policy, nil
	}
	reason := "promotion_denied"
	if len(assessment.Reasons) > 0 {
		reason = strings.Join(assessment.Reasons, ",")
	}
	if provider.Evidence != nil {
		_ = provider.Evidence.Record(ctx, RuntimeEvidence{EvidenceID: writingstore.StableID("evt_", identity.IdempotencyKey, policy.PolicyHash, "promotion-denied"),
			Kind: "route_decision", Identity: identity, PolicyHash: policy.PolicyHash, PolicyVersion: policy.PolicyVersion,
			Mode: policy.Mode, Lane: LaneBaseline, Status: "promotion_denied", ErrorCode: CodeRolloutPromotionDenied,
			RecordedAt: time.Now().UTC(), Decision: RouteDecision{Mode: policy.Mode, Lane: LaneBaseline, Reason: reason, PolicyHash: policy.PolicyHash}})
	}
	if gateErr != nil {
		return AdapterRolloutPolicy{}, runtimeError(CodeRolloutPromotionDenied, RetrySafe, reason, gateErr)
	}
	return AdapterRolloutPolicy{}, runtimeError(CodeRolloutPromotionDenied, RetryNever, reason, fmt.Errorf("rollout promotion gate denied policy"))
}
