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

type PromotionAssessment struct {
	Allowed    bool                               `json:"allowed"`
	Reasons    []string                           `json:"reasons"`
	Health     writingstore.RolloutEvidenceHealth `json:"health"`
	ApprovalID string                             `json:"approval_id,omitempty"`
	AssessedAt time.Time                          `json:"assessed_at"`
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
	if gate.Store == nil {
		return assessment, ErrRuntimeNotReady
	}
	if err := policy.Validate(); err != nil {
		return assessment, err
	}
	if policy.Mode != RolloutAllowlist {
		assessment.Reasons = append(assessment.Reasons, "target_mode_not_allowlist")
		return assessment, nil
	}
	if policy.KillSwitch {
		assessment.Reasons = append(assessment.Reasons, "policy_kill_switch")
	}
	if !policy.EffectiveAt.IsZero() && now.Before(policy.EffectiveAt) {
		assessment.Reasons = append(assessment.Reasons, "policy_not_effective")
	}
	if !policy.ExpiresAt.IsZero() && !now.Before(policy.ExpiresAt) {
		assessment.Reasons = append(assessment.Reasons, "policy_expired")
	}
	criteria := gate.Criteria
	if criteria.MinimumComparisons < 1 || criteria.EvidenceWindow <= 0 || criteria.Freshness <= 0 {
		criteria = DefaultPromotionCriteria()
	}
	health, err := gate.Store.RolloutEvidenceHealth(ctx, policy.PolicyHash, now.Add(-criteria.EvidenceWindow))
	if err != nil {
		return assessment, err
	}
	assessment.Health = health
	if health.ComparisonRecords < criteria.MinimumComparisons {
		assessment.Reasons = append(assessment.Reasons, "insufficient_comparisons")
	}
	if health.FailedRecords > criteria.MaximumFailures {
		assessment.Reasons = append(assessment.Reasons, "evidence_failures_exceeded")
	}
	if health.LastRecordedAt.IsZero() || now.Sub(health.LastRecordedAt) > criteria.Freshness {
		assessment.Reasons = append(assessment.Reasons, "evidence_stale")
	}
	assessment.Allowed = len(assessment.Reasons) == 0
	return assessment, nil
}

func (gate AllowlistPromotionGate) Assess(ctx context.Context, policy AdapterRolloutPolicy) (PromotionAssessment, error) {
	assessment, err := gate.EvidenceAssessment(ctx, policy)
	if err != nil || !assessment.Allowed {
		return assessment, err
	}
	approval, err := gate.Store.LatestRolloutApproval(ctx, policy.PolicyHash, policy.PolicyVersion, policy.ActivationKey)
	if errors.Is(err, writingstore.ErrNotFound) {
		assessment.Allowed = false
		assessment.Reasons = append(assessment.Reasons, "approval_missing")
		return assessment, nil
	}
	if err != nil {
		return assessment, err
	}
	assessment.ApprovalID = approval.ApprovalID
	now := gate.now()
	if approval.PolicyHash != policy.PolicyHash || approval.PolicyVersion != policy.PolicyVersion || approval.ActivationKey != policy.ActivationKey {
		assessment.Reasons = append(assessment.Reasons, "approval_scope_mismatch")
	}
	if approval.TargetMode != string(RolloutAllowlist) {
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

type GatedRolloutPolicyProvider struct {
	Base     RolloutPolicyProvider
	Gate     AllowlistPromotionGate
	Evidence RolloutEvidenceStore
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
	assessment, gateErr := provider.Gate.Assess(ctx, policy)
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
			RecordedAt: provider.Gate.now(), Decision: RouteDecision{Mode: policy.Mode, Lane: LaneBaseline, Reason: reason, PolicyHash: policy.PolicyHash}})
	}
	if gateErr != nil {
		return AdapterRolloutPolicy{}, runtimeError(CodeRolloutPromotionDenied, RetrySafe, reason, gateErr)
	}
	return AdapterRolloutPolicy{}, runtimeError(CodeRolloutPromotionDenied, RetryNever, reason, fmt.Errorf("allowlist promotion gate denied policy"))
}
