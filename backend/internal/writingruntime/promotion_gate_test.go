package writingruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type governanceStoreStub struct {
	health      writingstore.RolloutEvidenceHealth
	approval    writingstore.RolloutApprovalRecord
	approvalErr error
	ladder      writingstore.RolloutApprovalRecord
	ladderErr   error
}

func (s *governanceStoreStub) RolloutEvidenceHealth(context.Context, string, time.Time) (writingstore.RolloutEvidenceHealth, error) {
	return s.health, nil
}
func (s *governanceStoreStub) LatestRolloutApproval(context.Context, string, int, string) (writingstore.RolloutApprovalRecord, error) {
	return s.approval, s.approvalErr
}
func (s *governanceStoreStub) LatestRolloutApprovalByActivationKey(context.Context, string, string) (writingstore.RolloutApprovalRecord, error) {
	return s.ladder, s.ladderErr
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

func percentagePolicyForGate() AdapterRolloutPolicy {
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, "core.draft.generate", "1.0.0")
	policy.PolicyVersion, policy.Mode, policy.ActivationKey, policy.BasisPoints, policy.AllowSubjects = 2, RolloutPercentage, "change-task13", 1000, []string{}
	policy, _ = policy.WithComputedHash()
	return policy
}

func TestPercentagePromotionGateRequiresFreshHealthExactApprovalAndLadder(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	policy := percentagePolicyForGate()
	health := writingstore.RolloutEvidenceHealth{PolicyHash: policy.PolicyHash, ComparisonRecords: 3, TotalRecords: 9, LastRecordedAt: now.Add(-time.Hour), Cutoff: now.Add(-7 * 24 * time.Hour)}
	store := &governanceStoreStub{health: health,
		approval: writingstore.RolloutApprovalRecord{ApprovalID: "approval_percentage", PolicyHash: policy.PolicyHash,
			PolicyVersion: policy.PolicyVersion, ActivationKey: policy.ActivationKey, TargetMode: string(RolloutPercentage),
			EvidenceLastRecordedAt: health.LastRecordedAt, ExpiresAt: now.Add(time.Hour)},
		ladder: writingstore.RolloutApprovalRecord{ApprovalID: "approval_allowlist_stage", TargetMode: string(RolloutAllowlist), ExpiresAt: now.Add(time.Hour)}}
	gate := PercentagePromotionGate{Store: store, Criteria: DefaultPromotionCriteria(), Now: func() time.Time { return now }}
	assessment, err := gate.Assess(context.Background(), policy)
	if err != nil || !assessment.Allowed || assessment.ApprovalID != "approval_percentage" {
		t.Fatalf("assessment=%#v err=%v", assessment, err)
	}

	// Missing allowlist stage blocks the percentage rung even when the
	// percentage evidence and approval are perfect.
	store.ladderErr = writingstore.ErrNotFound
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "allowlist_stage_missing") {
		t.Fatalf("ladder assessment=%#v err=%v", assessment, err)
	}
	store.ladderErr = nil

	// A percentage-target approval bound to the wrong hash is rejected.
	store.approval.PolicyHash = hashForTest("wrong-percentage-policy")
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "approval_scope_mismatch") {
		t.Fatalf("scope assessment=%#v err=%v", assessment, err)
	}

	// An allowlist-targeted approval cannot authorize the percentage rung.
	store.approval.PolicyHash = policy.PolicyHash
	store.approval.TargetMode = string(RolloutAllowlist)
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "approval_mode_mismatch") {
		t.Fatalf("mode assessment=%#v err=%v", assessment, err)
	}

	// Stale evidence fails the shared criteria before approval binding.
	store.approval.TargetMode = string(RolloutPercentage)
	store.health.ComparisonRecords = 2
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "insufficient_comparisons") {
		t.Fatalf("health assessment=%#v err=%v", assessment, err)
	}
}

func TestGatedProviderFailsClosedForPercentageWithoutGate(t *testing.T) {
	policy := percentagePolicyForGate()
	base, _ := NewMutableRolloutPolicyProvider(policy)
	evidence := &MemoryRolloutEvidenceStore{}
	// PercentageGate deliberately nil: percentage traffic must fail closed
	// until an operator explicitly wires the percentage gate.
	provider := GatedRolloutPolicyProvider{Base: base,
		Gate: AllowlistPromotionGate{Store: &governanceStoreStub{}, Criteria: DefaultPromotionCriteria()}, Evidence: evidence}
	request := legacyRequest([]byte("contract"))
	_, err := provider.Policy(context.Background(), request.Identity())
	if ErrorCodeOf(err) != CodeRolloutPromotionDenied || !strings.Contains(err.Error(), "percentage_gate_not_configured") {
		t.Fatalf("err=%v", err)
	}

	provider.PercentageGate = &PercentagePromotionGate{Store: &governanceStoreStub{}, Criteria: DefaultPromotionCriteria()}
	if _, err := provider.Policy(context.Background(), request.Identity()); ErrorCodeOf(err) != CodeRolloutPromotionDenied {
		t.Fatalf("err=%v", err)
	}
	if records := evidence.Records(); len(records) != 2 {
		t.Fatalf("records=%d", len(records))
	}
}

func enabledPolicyForGate() AdapterRolloutPolicy {
	policy := DefaultShadowPolicy("candidate.engine", AdapterFamilyEngine, "core.draft.generate", "1.0.0")
	policy.PolicyVersion, policy.Mode, policy.ActivationKey, policy.AllowSubjects = 3, RolloutEnabled, "change-task13", []string{}
	policy, _ = policy.WithComputedHash()
	return policy
}

func TestProductionPromotionGateRequiresPercentageStageFreshEvidenceAndExactApproval(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	policy := enabledPolicyForGate()
	percentageHash := hashForTest("percentage-policy")
	health := writingstore.RolloutEvidenceHealth{PolicyHash: percentageHash, ComparisonRecords: 3, TotalRecords: 9, LastRecordedAt: now.Add(-time.Hour), Cutoff: now.Add(-7 * 24 * time.Hour)}
	store := &governanceStoreStub{health: health,
		approval: writingstore.RolloutApprovalRecord{ApprovalID: "approval_enabled", PolicyHash: policy.PolicyHash,
			PolicyVersion: policy.PolicyVersion, ActivationKey: policy.ActivationKey, TargetMode: string(RolloutEnabled),
			EvidenceLastRecordedAt: health.LastRecordedAt, ExpiresAt: now.Add(time.Hour)},
		ladder: writingstore.RolloutApprovalRecord{ApprovalID: "approval_percentage_stage", PolicyHash: percentageHash,
			TargetMode: string(RolloutPercentage), EvidenceLastRecordedAt: health.LastRecordedAt, ExpiresAt: now.Add(48 * time.Hour)}}
	gate := ProductionPromotionGate{Store: store, Criteria: DefaultPromotionCriteria(), Now: func() time.Time { return now }}
	assessment, err := gate.Assess(context.Background(), policy)
	if err != nil || !assessment.Allowed || assessment.ApprovalID != "approval_enabled" {
		t.Fatalf("assessment=%#v err=%v", assessment, err)
	}
	// The evidence health must be looked up under the percentage policy hash
	// recorded on the stage approval, not under the enabled policy's own hash.
	if assessment.Health.PolicyHash != percentageHash {
		t.Fatalf("health=%#v", assessment.Health)
	}

	// A missing percentage stage blocks production even when everything else
	// is perfect.
	store.ladderErr = writingstore.ErrNotFound
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "percentage_stage_missing") {
		t.Fatalf("ladder assessment=%#v err=%v", assessment, err)
	}
	store.ladderErr = nil

	// An expired percentage stage approval cannot authorize production.
	store.ladder.ExpiresAt = now.Add(-time.Hour)
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "percentage_stage_expired") {
		t.Fatalf("expired stage assessment=%#v err=%v", assessment, err)
	}
	store.ladder.ExpiresAt = now.Add(48 * time.Hour)

	// Stale percentage-stage evidence fails the shared criteria before
	// approval binding.
	store.health.LastRecordedAt = now.Add(-48 * time.Hour)
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "evidence_stale") {
		t.Fatalf("stale assessment=%#v err=%v", assessment, err)
	}
	store.health.LastRecordedAt = now.Add(-time.Hour)

	// Failures on the percentage stage block production.
	store.health.FailedRecords = 1
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "evidence_failures_exceeded") {
		t.Fatalf("failures assessment=%#v err=%v", assessment, err)
	}
	store.health.FailedRecords = 0

	// An allowlist-targeted approval cannot authorize the production rung.
	store.approval.TargetMode = string(RolloutAllowlist)
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "approval_mode_mismatch") {
		t.Fatalf("mode assessment=%#v err=%v", assessment, err)
	}

	// An enabled approval bound to the wrong hash is rejected.
	store.approval.TargetMode = string(RolloutEnabled)
	store.approval.PolicyHash = hashForTest("wrong-enabled-policy")
	assessment, err = gate.Assess(context.Background(), policy)
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "approval_scope_mismatch") {
		t.Fatalf("scope assessment=%#v err=%v", assessment, err)
	}

	// A percentage policy is refused outright by the production gate.
	assessment, err = gate.EvidenceAssessment(context.Background(), percentagePolicyForGate())
	if err != nil || assessment.Allowed || !containsString(assessment.Reasons, "target_mode_not_enabled") {
		t.Fatalf("mode mismatch assessment=%#v err=%v", assessment, err)
	}
}

func TestGatedProviderFailsClosedForEnabledWithoutGate(t *testing.T) {
	policy := enabledPolicyForGate()
	base, _ := NewMutableRolloutPolicyProvider(policy)
	evidence := &MemoryRolloutEvidenceStore{}
	// ProductionGate deliberately nil: enabled traffic must fail closed until
	// an operator explicitly wires the production gate.
	provider := GatedRolloutPolicyProvider{Base: base,
		Gate: AllowlistPromotionGate{Store: &governanceStoreStub{}, Criteria: DefaultPromotionCriteria()}, Evidence: evidence}
	request := legacyRequest([]byte("contract"))
	_, err := provider.Policy(context.Background(), request.Identity())
	if ErrorCodeOf(err) != CodeRolloutPromotionDenied || !strings.Contains(err.Error(), "production_gate_not_configured") {
		t.Fatalf("err=%v", err)
	}

	provider.ProductionGate = &ProductionPromotionGate{Store: &governanceStoreStub{}, Criteria: DefaultPromotionCriteria()}
	if _, err := provider.Policy(context.Background(), request.Identity()); ErrorCodeOf(err) != CodeRolloutPromotionDenied {
		t.Fatalf("err=%v", err)
	}
	if records := evidence.Records(); len(records) != 2 {
		t.Fatalf("records=%d", len(records))
	}
}
