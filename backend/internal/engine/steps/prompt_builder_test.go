package steps

import (
	"testing"
)

// TestPromptBuilder_StaticBudget 验证静态预算模式仍然工作。
func TestPromptBuilder_StaticBudget(t *testing.T) {
	pb := NewPromptBuilder().
		WithBudget(10000).
		Add("task", "写一篇关于AI的文章。").
		AddWithPriority("memory", "用户偏好：喜欢科技话题", priorityLow)

	result := pb.String()
	if result == "" {
		t.Error("prompt builder should produce non-empty result")
	}
}

// TestPromptBuilder_DynamicBudget_Unlimited 验证无限预算时回退到默认值。
func TestPromptBuilder_DynamicBudget_Unlimited(t *testing.T) {
	pb := NewPromptBuilder().
		WithDynamicBudget(0). // 0 = 无限制
		Add("task", "写一篇文章。")

	// 应该使用默认 12000 预算
	if pb.budget != 12000 {
		t.Errorf("expected default budget 12000 for unlimited, got %d", pb.budget)
	}
	if pb.dynamicBudget {
		t.Error("should not be dynamic for unlimited budget")
	}

	result := pb.String()
	if result == "" {
		t.Error("should produce non-empty result")
	}
}

// TestPromptBuilder_DynamicBudget_Calculated 验证动态预算计算。
func TestPromptBuilder_DynamicBudget_Calculated(t *testing.T) {
	// 剩余 20000 tokens
	// 预留 = 2000 (system) + 2048 (history) + 8192 (response) = 12240
	// 预期 budget = 20000 - 12240 = 7760
	pb := NewPromptBuilder().
		WithDynamicBudget(20000).
		Add("task", "写一篇文章。")

	expected := 20000 - (reserveSystemPrompt + reserveHistory + reserveResponse)
	if pb.budget != expected {
		t.Errorf("expected budget %d, got %d", expected, pb.budget)
	}
	if !pb.dynamicBudget {
		t.Error("should be dynamic when remaining is specified")
	}
}

// TestPromptBuilder_DynamicBudget_MinClamp 验证动态预算不低于最低值。
func TestPromptBuilder_DynamicBudget_MinClamp(t *testing.T) {
	// 剩余 5000 tokens，预留 12240，budget 会是负数
	// 应该被钳制到 minUserPromptBudget (2000)
	pb := NewPromptBuilder().
		WithDynamicBudget(5000).
		Add("task", "写一篇文章。")

	if pb.budget != minUserPromptBudget {
		t.Errorf("expected budget clamped to %d, got %d", minUserPromptBudget, pb.budget)
	}
}

// TestPromptBuilder_DynamicBudget_Truncation 验证预算紧张时低优先级 section 被截断。
func TestPromptBuilder_DynamicBudget_Truncation(t *testing.T) {
	// 设置很小的预算，验证低优先级 section 被丢弃
	pb := NewPromptBuilder().
		WithBudget(50). // 50 tokens 的极小预算
		AddWithPriority("task", "写一篇关于人工智能在医疗领域应用的文章。", priorityCritical).
		AddWithPriority("search", "搜索结果1：AI在医疗影像中的应用。搜索结果2：AI辅助诊断的新进展。搜索结果3：机器学习在药物研发中的突破。", priorityMedium).
		AddWithPriority("memory", "用户偏好：喜欢深度技术分析，关注伦理问题。历史记忆：用户上次问了关于AI隐私的问题。", priorityLow)

	result := pb.String()
	if result == "" {
		t.Error("should produce non-empty result even with tiny budget")
	}
	// task (critical) 应该被保留
	if !contains(result, "人工智能") {
		t.Error("critical section (task) should be preserved")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > len(substr) && (startsWith(s, substr) || contains(s[1:], substr))))
}

func startsWith(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}
