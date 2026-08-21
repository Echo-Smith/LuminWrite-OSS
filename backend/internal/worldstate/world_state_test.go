package worldstate

import (
	"strings"
	"testing"
)

// TestWorldStateDiff_FirstPush 验证首次推送时所有 section 全量渲染。
func TestWorldStateDiff_FirstPush(t *testing.T) {
	ws := NewWorldState()
	ws.Register(NewProfileSection(nil, "chat", false))
	ws.Register(NewDateSection())
	ws.Register(NewTaskInstructionsSection("chat", false))
	ws.Register(NewSecuritySection())

	fragments := ws.UpdateWorldState()

	// 首次推送应该有 4 个 section（Profile + Date + TaskInstructions + Security）
	if len(fragments) != 4 {
		t.Errorf("expected 4 fragments on first push, got %d", len(fragments))
	}

	// 验证每个 fragment 有内容
	for i, frag := range fragments {
		if frag.Body == "" {
			t.Errorf("fragment %d has empty body", i)
		}
	}
}

// TestWorldStateDiff_SecondPushNoChange 验证第二次推送时无变化的 section 不重复推送。
func TestWorldStateDiff_SecondPushNoChange(t *testing.T) {
	ws := NewWorldState()
	ws.Register(NewProfileSection(nil, "chat", false))
	ws.Register(NewDateSection())
	ws.Register(NewTaskInstructionsSection("chat", false))
	ws.Register(NewSecuritySection())

	// 第一次推送 — 全量
	first := ws.UpdateWorldState()
	if len(first) == 0 {
		t.Fatal("first push should have fragments")
	}

	// 第二次推送 — 相同内容，应该全部 diff 为 nil
	second := ws.UpdateWorldState()
	if len(second) != 0 {
		t.Errorf("second push with no changes should have 0 fragments, got %d", len(second))
		for _, frag := range second {
			t.Logf("unexpected fragment: %+v", frag.Body[:min(50, len(frag.Body))])
		}
	}
}

// TestWorldStateDiff_PartialChange 验证只有变化的 section 才推送。
func TestWorldStateDiff_PartialChange(t *testing.T) {
	ws := NewWorldState()

	// 初始 article 为空
	ws.Register(NewProfileSection(nil, "polish", false))
	ws.Register(NewArticleSection(""))
	ws.Register(NewDateSection())
	ws.Register(NewSecuritySection())

	// 第一次推送
	first := ws.UpdateWorldState()
	firstCount := len(first)
	if firstCount == 0 {
		t.Fatal("first push should have fragments")
	}

	// 更新 article（模拟润色场景：文章从无到有）
	ws.Register(NewArticleSection("这是一篇测试文章的内容。"))

	// 第二次推送 — 只有 article section 应该变化
	second := ws.UpdateWorldState()
	if len(second) != 1 {
		t.Errorf("second push should have 1 changed fragment (article), got %d", len(second))
		for _, frag := range second {
			t.Logf("fragment body preview: %s", frag.Body[:min(50, len(frag.Body))])
		}
	} else {
		// 验证变化的是 article
		if !strings.Contains(second[0].Body, "当前文章") {
			t.Errorf("expected fragment to contain article, got: %s", second[0].Body[:min(50, len(second[0].Body))])
		}
	}
}

// TestWorldStateResetBaselines 验证 ResetBaselines 后全量推送。
func TestWorldStateResetBaselines(t *testing.T) {
	ws := NewWorldState()
	ws.Register(NewProfileSection(nil, "chat", false))
	ws.Register(NewSecuritySection())

	// 第一次推送
	ws.UpdateWorldState()

	// 第二次推送 — 无变化
	second := ws.UpdateWorldState()
	if len(second) != 0 {
		t.Errorf("second push should have 0 fragments, got %d", len(second))
	}

	// 重置基线
	ws.ResetBaselines()

	// 第三次推送 — 应该全量推送
	third := ws.UpdateWorldState()
	if len(third) != 2 {
		t.Errorf("after reset, push should have 2 fragments, got %d", len(third))
	}
}

// TestTokenBudget 验证 Token 预算追踪。
func TestTokenBudget(t *testing.T) {
	budget := &TokenBudget{
		ContextWindowID: "test-uuid",
		TotalBudget:     10000,
	}

	// 初始状态
	if budget.Remaining() != 10000 {
		t.Errorf("expected remaining 10000, got %d", budget.Remaining())
	}

	// 消耗 3000
	if !budget.Consume(3000) {
		t.Error("consume 3000 should be within budget")
	}
	if budget.Remaining() != 7000 {
		t.Errorf("expected remaining 7000, got %d", budget.Remaining())
	}

	// 检查是否低于阈值
	if budget.IsLow(5000) {
		t.Error("7000 remaining should not be low with threshold 5000")
	}
	if !budget.IsLow(8000) {
		t.Error("7000 remaining should be low with threshold 8000")
	}

	// 消耗到超出预算
	budget.Consume(8000)
	if budget.Remaining() != 0 {
		t.Errorf("expected remaining 0 when over budget, got %d", budget.Remaining())
	}
}

// TestAutoCompactFallback 验证自动压缩降级逻辑。
func TestAutoCompactFallback(t *testing.T) {
	ac := NewAutoCompactFallback()

	// 预算充足 — 不需要压缩
	budget := &TokenBudget{TotalBudget: 10000, Used: 5000}
	if ac.ShouldCompact(budget) {
		t.Error("should not compact when budget is sufficient")
	}

	// 预算不足 — 需要压缩
	budgetLow := &TokenBudget{TotalBudget: 10000, Used: 8500} // 剩余 1500 < 阈值 2000
	if !ac.ShouldCompact(budgetLow) {
		t.Error("should compact when remaining < threshold")
	}

	// 无限预算 — 不需要压缩
	budgetUnlimited := &TokenBudget{TotalBudget: 0, Used: 999999}
	if ac.ShouldCompact(budgetUnlimited) {
		t.Error("should not compact when budget is unlimited")
	}

	// nil 预算 — 不需要压缩
	if ac.ShouldCompact(nil) {
		t.Error("should not compact when budget is nil")
	}

	// 验证 CompactPrompt 输出
	prompt := ac.CompactPrompt(500)
	if !strings.Contains(prompt, "500") {
		t.Errorf("compact prompt should contain saved tokens, got: %s", prompt)
	}
}

// TestWorldStateVersion 验证版本号递增。
func TestWorldStateVersion(t *testing.T) {
	ws := NewWorldState()

	initialVersion := ws.Version()

	ws.UpdateWorldState()
	if ws.Version() != initialVersion+1 {
		t.Errorf("version should increment after UpdateWorldState, expected %d, got %d", initialVersion+1, ws.Version())
	}

	ws.UpdateWorldState()
	if ws.Version() != initialVersion+2 {
		t.Errorf("version should increment again, expected %d, got %d", initialVersion+2, ws.Version())
	}

	ws.ResetBaselines()
	if ws.Version() != initialVersion+3 {
		t.Errorf("version should increment after ResetBaselines, expected %d, got %d", initialVersion+3, ws.Version())
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
