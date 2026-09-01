package writingruntime

import (
	"context"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type governanceStoreStub struct {
	health      writingstore.RolloutEvidenceHealth
	approval    writingstore.RolloutApprovalRecord
	approvalErr error
}

func (s *governanceStoreStub) RolloutEvidenceHealth(context.Context, string, time.Time) (writingstore.RolloutEvidenceHealth, error) {
	return s.health, nil
}
func (s *governanceStoreStub) LatestRolloutApproval(context.Context, string, int, string) (writingstore.RolloutApprovalRecord, error) {
	return s.approval, s.approvalErr
}

func allowlistPolicyForGate() AdapterRolloutPolicy {
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, "core.draft.generate", "1.0.0")
	policy.PolicyVersion, policy.Mode, policy.ActivationKey, policy.AllowSubjects = 2, RolloutAllowlist, "change-task13", []string{"user_test"}
	policy, _ = policy.WithComputedHash()
	return policy
}

func TestAllowlistPromotionGateRequiresFreshHealthAndExactApproval(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	policy := allowlistPolicyForGate()
	health := writingstore.RolloutEvidenceHealth{PolicyHash: policy.PolicyHash, ComparisonRecords: 3, TotalRecords: 9, LastRecordedAt: now.Add(-time.Hour), Cutoff: now.Add(-7 * 24 * time.Hour)}
	store := &governanceStoreStub{health: health, approval: writingstore.RolloutApprovalRecord{ApprovalID: "approval_task13", PolicyHash: policy.PolicyHash,
		PolicyVersion: policy.PolicyVersion, ActivationKey: policy.ActivationKey, TargetMode: string(RolloutAllowlist),
		EvidenceLastRecordedAt: health.LastRecordedAt, ExpiresAt: now.Add(time.Hour)}}
	gate := AllowlistPromotionGate{Store: store, Criteria: DefaultPromotionCriteria(), Now: func() time.Time { return now }}
	assessment, err := gate.Assess(context.Background(), policy)
	if err != nil || !assessment.Allowed || assessment.ApprovalID != "approval_task13" {
		t.Fatalf("assessment=%#v err=%v", assessment, err)
	}
	store.health.FailedRecords = 1
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || assessment.Reasons[0] != "evidence_failures_exceeded" {
		t.Fatalf("assessment=%#v err=%v", assessment, err)
	}
}

func TestAllowlistPromotionGateRejectsInactiveAndMismatchedAuthority(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	policy := allowlistPolicyForGate()
	health := writingstore.RolloutEvidenceHealth{PolicyHash: policy.PolicyHash, ComparisonRecords: 3,
		LastRecordedAt: now.Add(-time.Hour), Cutoff: now.Add(-7 * 24 * time.Hour)}
	approval := writingstore.RolloutApprovalRecord{ApprovalID: "approval_task13", PolicyHash: policy.PolicyHash,
		PolicyVersion: policy.PolicyVersion, ActivationKey: policy.ActivationKey, TargetMode: string(RolloutAllowlist),
		EvidenceLastRecordedAt: health.LastRecordedAt, ExpiresAt: now.Add(time.Hour)}
	store := &governanceStoreStub{health: health, approval: approval}
	gate := AllowlistPromotionGate{Store: store, Criteria: DefaultPromotionCriteria(), Now: func() time.Time { return now }}

	inactive := policy
	inactive.KillSwitch = true
	inactive, _ = inactive.WithComputedHash()
	assessment, err := gate.EvidenceAssessment(context.Background(), inactive)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "policy_kill_switch") {
		t.Fatalf("inactive assessment=%#v err=%v", assessment, err)
	}

	store.approval.PolicyHash = hashForTest("wrong-approval-policy")
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "approval_scope_mismatch") {
		t.Fatalf("scope assessment=%#v err=%v", assessment, err)
	}

	percentage := policy
	percentage.Mode, percentage.AllowSubjects, percentage.BasisPoints = RolloutPercentage, []string{}, 100
	percentage, _ = percentage.WithComputedHash()
	assessment, err = gate.EvidenceAssessment(context.Background(), percentage)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "target_mode_not_allowlist") {
		t.Fatalf("percentage assessment=%#v err=%v", assessment, err)
	}
}

func TestGatedProviderFailsClosedAndRecordsDenial(t *testing.T) {
	policy := allowlistPolicyForGate()
	base, _ := NewMutableRolloutPolicyProvider(policy)
	evidence := &MemoryRolloutEvidenceStore{}
	provider := GatedRolloutPolicyProvider{Base: base,
		Gate: AllowlistPromotionGate{Store: &governanceStoreStub{}, Criteria: DefaultPromotionCriteria()}, Evidence: evidence}
	request := legacyRequest([]byte("contract"))
	if _, err := provider.Policy(context.Background(), request.Identity()); ErrorCodeOf(err) != CodeRolloutPromotionDenied {
		t.Fatalf("err=%v", err)
	}
	if records := evidence.Records(); len(records) != 1 || records[0].Status != "promotion_denied" {
		t.Fatalf("records=%#v", records)
	}
}
