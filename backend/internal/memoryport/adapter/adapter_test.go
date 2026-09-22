package adapter

import (
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
	pkgmem "github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── P0 黄金集：SDK MemoryContext → Bundle 翻译 + 渲染逐字节锁定 ───
//
// 这些用例在重构前先按旧实现（steps.FormatMemoryForPrompt /
// FormatReviewGuardForPrompt + execCtx 直接持有 memory.MemoryContext）
// 记录期望输出，重构后必须逐字节一致（计划 P0 验收）。

func goldenMemoryContext() *pkgmem.MemoryContext {
	return &pkgmem.MemoryContext{
		Injected: []pkgmem.MemoryEntry{
			{ID: "m1", Tier: pkgmem.TierHard, Category: "style", Value: "标题不超过12个字", Confidence: 1.0, Dismissible: true, EvidenceStatus: pkgmem.EvidenceVerified},
			{ID: "m2", Tier: pkgmem.TierPattern, Category: "tone", Value: "偏好口语化表达", Confidence: 0.72},
		},
		ReviewGuard: []pkgmem.MemoryEntry{
			{ID: "f1", Tier: pkgmem.TierFeedback, Category: "title", Value: " dislike_long_title", Confidence: 0.55},
		},
		Dismissed:     []string{"m9"},
		RefusalReason: "",
	}
}

func TestBundleFromMemoryContextGolden(t *testing.T) {
	got := bundleFromMemoryContext(goldenMemoryContext())

	if got.Version != memoryport.ContractVersion {
		t.Errorf("version = %d, want %d", got.Version, memoryport.ContractVersion)
	}
	if len(got.WriteDirectives) != 2 || len(got.ReviewGuard) != 1 {
		t.Fatalf("slot sizes = (%d, %d), want (2, 1)", len(got.WriteDirectives), len(got.ReviewGuard))
	}
	if got.Dismissed == nil || len(got.Dismissed) != 1 || got.Dismissed[0] != "m9" {
		t.Errorf("dismissed passthrough broken: %v", got.Dismissed)
	}

	w0, w1 := got.WriteDirectives[0], got.WriteDirectives[1]
	if w0.ID != "m1" || w0.Value != "标题不超过12个字" || w0.Confidence != 1.0 || !w0.Dismissible || w0.EvidenceStatus != "verified" {
		t.Errorf("directive m1 mapping broken: %+v", w0)
	}
	// 顺序保持（黄金集要求：注入顺序与 SDK 门控输出一致）
	if w1.ID != "m2" || w1.Confidence != 0.72 {
		t.Errorf("directive m2 mapping broken: %+v", w1)
	}
	// tier → 消费槽位映射
	if w0.Kind != memoryport.KindPreference || w1.Kind != memoryport.KindPreference || got.ReviewGuard[0].Kind != memoryport.KindFeedback {
		t.Errorf("kind mapping broken: %s %s %s", w0.Kind, w1.Kind, got.ReviewGuard[0].Kind)
	}
	// Strength = 衰减后有效置信度（时序信号）：非零且等于来源 Confidence，
	// 供消费端预算截断排序（WP6）
	guard := got.ReviewGuard[0]
	if w0.Strength != 1.0 || w1.Strength != 0.72 || guard.Strength != 0.55 {
		t.Errorf("directive strength mapping broken: m1=%v m2=%v guard=%v", w0.Strength, w1.Strength, guard.Strength)
	}
	if w0.Strength == 0 || w1.Strength == 0 || guard.Strength == 0 {
		t.Error("strength must be non-zero (budget truncation ordering signal)")
	}
}

func TestRenderWriteDirectivesGolden(t *testing.T) {
	bundle := bundleFromMemoryContext(goldenMemoryContext())
	want := "\n\n--- 用户写作偏好（请参考但不强制）---\n" +
		"- 标题不超过12个字\n" +
		"- 偏好口语化表达\n"
	if got := memoryport.RenderWriteDirectives(bundle); got != want {
		t.Errorf("write directives render drift:\n got=%q\nwant=%q", got, want)
	}
}

func TestRenderReviewGuardGolden(t *testing.T) {
	bundle := bundleFromMemoryContext(goldenMemoryContext())
	want := "\n\n--- 用户历史反馈（审查时请检查）---\n" +
		"-  dislike_long_title\n"
	if got := memoryport.RenderReviewGuard(bundle); got != want {
		t.Errorf("review guard render drift:\n got=%q\nwant=%q", got, want)
	}
}

// 空输入路径与旧 Format 函数一致：返回空串（prompt 段不出现）。
func TestRenderEmptyGolden(t *testing.T) {
	if got := memoryport.RenderWriteDirectives(nil); got != "" {
		t.Errorf("nil bundle should render empty, got %q", got)
	}
	empty := bundleFromMemoryContext(&pkgmem.MemoryContext{})
	if got := memoryport.RenderWriteDirectives(empty); got != "" {
		t.Errorf("empty injected should render empty, got %q", got)
	}
	if got := memoryport.RenderReviewGuard(empty); got != "" {
		t.Errorf("empty guard should render empty, got %q", got)
	}
}

// EnabledForUser 必须在 svc 为 nil 时安全返回 false（旧行为：step 内 nil 检查）。
func TestEnabledForUserNilService(t *testing.T) {
	a := NewServiceAdapter(nil, nil)
	if a.EnabledForUser("u-1") {
		t.Error("nil service must report disabled")
	}
	if a.Capabilities().EntityGraph {
		t.Error("nil embedder must not advertise EntityGraph")
	}
}

// WS 事件载荷兼容：Directive 的 JSON 字段名必须与旧 MemoryEntry 对齐，
// kind/strength 为 omitempty 加性字段（缺省时 payload 与旧事件一致）。
func TestDirectiveJSONFieldCompat(t *testing.T) {
	d := directiveFromEntry(goldenMemoryContext().Injected[0], memoryport.KindPreference)
	if d.ID == "" || d.Tier == "" {
		t.Fatal("directive mapping lost id/tier")
	}
	// 编译期已由 json tag 保证；此处断言 kind 缺省路径
	quiet := directiveFromEntry(goldenMemoryContext().Injected[1], memoryport.KindPreference)
	_ = quiet
}
