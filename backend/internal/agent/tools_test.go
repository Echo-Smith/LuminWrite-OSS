package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

func TestExtractKeywordsSplitsStraightAndChineseQuotes(t *testing.T) {
	got := extractKeywords(`"alpha" 'beta' “gamma”`)
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractKeywords() = %v, want %v", got, want)
	}
}

func TestArticleSignalToolsRepeatNearEndOutputContract(t *testing.T) {
	tests := []struct {
		name string
		run  func() (string, error)
	}{
		{name: "write article", run: func() (string, error) {
			return executeWriteArticle(ToolExecutorConfig{}, `{"topic":"测试"}`)
		}},
		{name: "revise section", run: func() (string, error) {
			return executeReviseSection(ToolExecutorConfig{}, `{"section_hint":"开头","instruction":"更简洁"}`)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.run()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(result, profile.MarkdownArticleOutputReminder) {
				t.Fatalf("tool result lacks near-end reminder: %q", result)
			}
			if strings.Contains(result, "---ARTICLE---") || strings.Contains(result, `{"title"`) {
				t.Fatalf("tool result generates legacy protocol: %q", result)
			}
		})
	}
}

// ─── WP6: 工具调用守卫（MaxCalls）行为 ─────────────────────
//
// 消融 D 变体根因：LLM 规划循环反复调用 retrieve_context 直到预算耗尽，
// 旧守卫返回与工具无关的搜索文案，误导模型继续试错并拖垮执行。
// 现约定：预算耗尽返回 instructive 结果字符串（非 error），重复调用幂等。

// newGuardExecutor 构建最小可注入的执行器：retrieve_context(source="article")
// 在 Session 无文章时立即短路返回，无需外部依赖即可驱动 guard 计数。
func newGuardExecutor(t *testing.T, maxCalls map[string]int) tools.ToolExecutor {
	t.Helper()
	execCtx := &engine.ExecutionContext{}
	execCtx.TraceID = "trace-guard"
	return BuildToolExecutor(ToolExecutorConfig{
		Session:  &WritingSession{},
		ExecCtx:  execCtx,
		MaxCalls: maxCalls,
		Intent:   IntentChat,
	})
}

const guardProbeArgs = `{"query":"测试","source":"article"}`

// (a) 第 N+1 次调用返回 exhausted 结果字符串而非 error，不中断 run。
func TestToolGuardExhaustionDegradesWithoutError(t *testing.T) {
	executor := newGuardExecutor(t, map[string]int{"retrieve_context": 2})

	// 前 N 次正常执行真实逻辑
	for i := 0; i < 2; i++ {
		out, err := executor("retrieve_context", guardProbeArgs)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
		if out == toolBudgetExhaustedResult {
			t.Fatalf("call %d: should run real logic before exhaustion", i+1)
		}
	}

	// 第 N+1 次：优雅降级，返回 exhausted 结果而非 error
	out, err := executor("retrieve_context", guardProbeArgs)
	if err != nil {
		t.Fatalf("exhausted call must degrade gracefully, not return error: %v", err)
	}
	if out != toolBudgetExhaustedResult {
		t.Fatalf("exhausted result = %q, want %q", out, toolBudgetExhaustedResult)
	}
	if !strings.Contains(out, `"exhausted":true`) {
		t.Fatalf("exhausted result lacks exhausted marker: %q", out)
	}
}

// (b) 耗尽后重复调用幂等：稳定返回同一文案，不再执行真实逻辑。
func TestToolGuardExhaustionIsIdempotent(t *testing.T) {
	executor := newGuardExecutor(t, map[string]int{"retrieve_context": 1})

	if _, err := executor("retrieve_context", guardProbeArgs); err != nil {
		t.Fatalf("warm-up call: %v", err)
	}
	first, err := executor("retrieve_context", guardProbeArgs)
	if err != nil {
		t.Fatalf("first exhausted call: %v", err)
	}
	for i := 0; i < 3; i++ {
		again, err := executor("retrieve_context", guardProbeArgs)
		if err != nil {
			t.Fatalf("repeat exhausted call %d: %v", i+1, err)
		}
		if again != first {
			t.Fatalf("repeat exhausted call %d not idempotent: %q != %q", i+1, again, first)
		}
	}
}

// (c) defaultMaxCalls 中 retrieve_context 预算与 WP6 收紧后的 3/3/2 形态一致。
func TestDefaultMaxCallsRetrieveContextBudget(t *testing.T) {
	cases := []struct {
		intent Intent
		want   int
	}{
		{IntentWriting, 3},
		{IntentPolish, 3},
		{IntentShorten, 3},
		{IntentExpand, 3},
		{IntentExtract, 3},
		{IntentChat, 2},
	}
	for _, tc := range cases {
		if got := defaultMaxCalls(tc.intent)["retrieve_context"]; got != tc.want {
			t.Errorf("intent=%s: retrieve_context budget = %d, want %d", tc.intent, got, tc.want)
		}
	}

	// 其余预算保持不变：remember 3/2/3、搜索类工具不动。
	if got := defaultMaxCalls(IntentWriting)["remember"]; got != 3 {
		t.Errorf("writing remember budget = %d, want 3", got)
	}
	if got := defaultMaxCalls(IntentPolish)["remember"]; got != 2 {
		t.Errorf("polish remember budget = %d, want 2", got)
	}
	if got := defaultMaxCalls(IntentChat)["remember"]; got != 3 {
		t.Errorf("chat remember budget = %d, want 3", got)
	}
	if got := defaultMaxCalls(IntentWriting)["search_web"]; got != 5 {
		t.Errorf("writing search_web budget = %d, want 5", got)
	}
	if got := defaultMaxCalls(IntentChat)["search_web"]; got != 2 {
		t.Errorf("chat search_web budget = %d, want 2", got)
	}
}
