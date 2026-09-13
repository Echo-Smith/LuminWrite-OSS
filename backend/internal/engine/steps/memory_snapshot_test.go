package steps

import (
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
	pkgmem "github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// P0 prompt 快照：AddMemory 对 memoryport.Bundle 的渲染输出
// 必须与旧实现（execCtx.MemoryContext 持有 *memory.MemoryContext +
// FormatMemoryForPrompt + EntityContext.FormattedContext）逐字节一致。

func goldenBundle() *memoryport.Bundle {
	return &memoryport.Bundle{
		Version: memoryport.ContractVersion,
		WriteDirectives: []memoryport.Directive{
			{ID: "m1", Tier: "hard", Category: "style", Value: "标题不超过12个字", Confidence: 1.0, Dismissible: true, Kind: memoryport.KindPreference},
			{ID: "m2", Tier: "pattern", Category: "tone", Value: "偏好口语化表达", Confidence: 0.72, Kind: memoryport.KindPreference},
		},
		ReviewGuard: []memoryport.Directive{
			{ID: "f1", Tier: "feedback", Category: "title", Value: " dislike_long_title", Confidence: 0.55, Kind: memoryport.KindFeedback},
		},
		EntityProfile: "--- 用户画像网络 ---\n[topic] 写作\n",
	}
}

func TestAddMemorySnapshotGolden(t *testing.T) {
	execCtx := &engine.ExecutionContext{}
	execCtx.MemoryContext = goldenBundle()

	got := NewPromptBuilder().AddMemory(execCtx).String()
	want := "\n\n--- 用户写作偏好（请参考但不强制）---\n" +
		"- 标题不超过12个字\n" +
		"- 偏好口语化表达\n" +
		"--- 用户画像网络 ---\n[topic] 写作\n"
	if got != want {
		t.Errorf("AddMemory snapshot drift:\n got=%q\nwant=%q", got, want)
	}
}

// 无记忆时 AddMemory 产生空 section（与旧行为一致：空串不渲染额外内容）。
func TestAddMemoryEmptyGolden(t *testing.T) {
	execCtx := &engine.ExecutionContext{}
	execCtx.MemoryContext = &memoryport.Bundle{Version: memoryport.ContractVersion}
	got := NewPromptBuilder().AddMemory(execCtx).String()
	if got != "" {
		t.Errorf("empty bundle should render empty memory section, got %q", got)
	}
}

// PostReview 的 Tier3 guard 渲染路径（RenderReviewGuard）快照。
func TestReviewGuardRenderGolden(t *testing.T) {
	bundle := goldenBundle()
	want := "\n\n--- 用户历史反馈（审查时请检查）---\n-  dislike_long_title\n"
	if got := memoryport.RenderReviewGuard(bundle); got != want {
		t.Errorf("review guard snapshot drift:\n got=%q\nwant=%q", got, want)
	}
	_ = pkgmem.TierFeedback // 保持 SDK 对齐引用（tier 字符串值契约）
}
