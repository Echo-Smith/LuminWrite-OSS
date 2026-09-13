package memory

import "testing"

// Layer-0 止血④回归锁：证据升级链路 + 门控默认值。
//
// 背景：evidence_status 此前没有任何写入链路（恒为 none），而
// RequireVerifiedForWriting 默认 true，导致 Tier2/3 在写作场景被
// 静默全拦。本文件锁定三件事：升级方向、门控语义、默认配置。

func TestUpgradedEvidenceStatus(t *testing.T) {
	cases := []struct {
		name    string
		current EvidenceStatus
		grade   ArticleGrade
		qs      QualitySource
		want    EvidenceStatus
	}{
		// 再次观测：none → supported（two-strike 语义）
		{"reinforce none", EvidenceNone, GradeNeutral, QualityNone, EvidenceSupported},
		{"reinforce empty", "", GradeNeutral, QualityNone, EvidenceSupported},
		// 正面反馈：至少 supported
		{"positive none", EvidenceNone, GradePositive, QualityNone, EvidenceSupported},
		{"positive supported", EvidenceSupported, GradePositive, QualityNone, EvidenceSupported},
		// 人工显式确认：直接 verified，且不受当前状态拖累
		{"workbuddy", EvidenceNone, GradeNeutral, QualityWorkbuddy, EvidenceVerified},
		{"manual approve", EvidenceSupported, GradeNeutral, QualityManualApprove, EvidenceVerified},
		// 不洗白：conflicted 不因重复观测升级，维持现状待人工裁决
		{"conflicted stays", EvidenceConflicted, GradeNeutral, QualityNone, EvidenceConflicted},
		{"verified stays", EvidenceVerified, GradeNeutral, QualityNone, EvidenceVerified},
	}
	for _, tc := range cases {
		if got := upgradedEvidenceStatus(tc.current, tc.grade, tc.qs); got != tc.want {
			t.Errorf("%s: upgradedEvidenceStatus(%v,%v,%v) = %v, want %v", tc.name, tc.current, tc.grade, tc.qs, got, tc.want)
		}
	}
}

func TestIsSafeForInjectionSemantics(t *testing.T) {
	// 宽松门（当前写作/聊天默认）：非 conflicted/unknown 即可注入
	if !EvidenceNone.IsSafeForInjection(false) {
		t.Error("loose gate: none must pass")
	}
	if EvidenceConflicted.IsSafeForInjection(false) {
		t.Error("loose gate: conflicted must not pass")
	}
	if EvidenceUnknown.IsSafeForInjection(false) {
		t.Error("loose gate: unknown must not pass")
	}
	// 严格门（升级链路就位后可重新启用）：要求 verified/supported
	if EvidenceNone.IsSafeForInjection(true) {
		t.Error("strict gate: none must not pass")
	}
	if !EvidenceSupported.IsSafeForInjection(true) {
		t.Error("strict gate: supported must pass")
	}
}

// 默认配置回归锁：RequireVerifiedForWriting 必须为 false。
// 若有人要改回 true，必须先确认升级链路（conflict.go）+ 回填迁移
// （115/112）已覆盖存量，否则写作场景 Tier2/3 将再次被静默全拦。
func TestDefaultSafetyConfigUnlocksEvidence(t *testing.T) {
	cfg := DefaultSafetyConfig()
	if !cfg.Enabled || !cfg.PIIFilterEnabled {
		t.Error("safety/PII must stay on by default")
	}
	if cfg.RequireVerifiedForWriting {
		t.Error("RequireVerifiedForWriting must default to false — strict gate deadlocks Tier2/3 (see Layer-0)")
	}
}
