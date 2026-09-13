package memory

import (
	"testing"
	"time"
)

// P2-2a 验收：多信号召回合并 + 中文 bigram 关键词命中。

func TestUnionRecall(t *testing.T) {
	semantic := []*Memory{
		{ID: "s1", Value: "偏好理性论证"},
		{ID: "s2", Value: "喜欢用数据说话"},
	}
	recent := []*Memory{
		{ID: "s1", Value: "偏好理性论证"},               // 语义已含，去重
		{ID: "n1", Value: "标题不超过12个字", Embedding: nil}, // 缺 embedding → 盲区修复，并入
		{ID: "n2", Value: "避免使用感叹号", Embedding: []float32{0.1}}, // 无关键词命中 → 不并入
		{ID: "n3", Value: "喜欢口语化开头", Embedding: []float32{0.2}}, // 查询含"口语化" → 关键词命中并入
	}

	got := unionRecall(semantic, recent, "用户喜欢口语化的风格吗")

	ids := make([]string, 0, len(got))
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	want := []string{"s1", "s2", "n1", "n3"}
	if len(ids) != len(want) {
		t.Fatalf("union = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("union order = %v, want %v", ids, want)
		}
	}
}

func TestBigramOverlap(t *testing.T) {
	cases := []struct {
		query, value string
		want         bool
	}{
		{"口语化表达", "喜欢口语化的开头", true},
		{"理性论证", "标题简短有力", false},
		{"标题", "标题不超过12个字", true}, // 单 rune 查询走 Contains
		{"", "任意", false},
	}
	for _, tc := range cases {
		if got := bigramOverlap(tc.query, tc.value); got != tc.want {
			t.Errorf("bigramOverlap(%q,%q) = %v, want %v", tc.query, tc.value, got, tc.want)
		}
	}
}

// P2 衰减闭式：与旧逐日近似同量级，但大于半衰期时平滑趋近而非截断在 0.5。
func TestEffectiveConfidenceClosedForm(t *testing.T) {
	m := &Memory{Tier: TierPattern, Confidence: 0.8, LastSeen: time.Now().Add(-30 * 24 * time.Hour)}
	got := m.EffectiveConfidence(30)
	// 恰好一个半衰期：0.8 * 0.5 = 0.4
	if got < 0.39 || got > 0.41 {
		t.Errorf("one half-life decay = %v, want ~0.4", got)
	}
	// 两个半衰期：0.8 * 0.25 = 0.2（旧实现截断在 0.4，闭式更平滑）
	m.LastSeen = time.Now().Add(-60 * 24 * time.Hour)
	got = m.EffectiveConfidence(30)
	if got < 0.19 || got > 0.21 {
		t.Errorf("two half-life decay = %v, want ~0.2", got)
	}
	// 硬偏好不衰减
	hard := &Memory{Tier: TierHard, Confidence: 1.0, LastSeen: time.Now().Add(-365 * 24 * time.Hour)}
	if hard.EffectiveConfidence(30) != 1.0 {
		t.Error("hard tier must not decay")
	}
}
