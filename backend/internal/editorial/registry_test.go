package editorial

import (
	"sync"
	"testing"
)

func TestDynamicAgentRegistry_BuiltinRoles(t *testing.T) {
	r := NewDynamicAgentRegistry()

	// 验证预设角色已注册
	for _, role := range []string{"researcher", "writer", "reviewer"} {
		cfg, ok := r.GetByRole(role)
		if !ok {
			t.Errorf("builtin role %s not found in registry", role)
		}
		if cfg.Name != role {
			t.Errorf("expected name %s, got %s", role, cfg.Name)
		}
		if cfg.IsGenerated {
			t.Errorf("builtin role %s should not be generated", role)
		}
	}
}

func TestDynamicAgentRegistry_ApplyGeneratedAgents(t *testing.T) {
	r := NewDynamicAgentRegistry()

	// 生成 3 个动态角色
	cfgs := []*AgentConfig{
		GenerateAgentConfig("researcher", "宏观研究员", "你是一位宏观经济研究员"),
		GenerateAgentConfig("writer", "深度撰稿人", "你是一位资深撰稿人"),
		GenerateAgentConfig("reviewer", "事实核查编辑", "你是一位严格的编辑"),
	}

	// 注册动态角色
	r.ApplyGeneratedAgents("task-1", cfgs)

	// 验证所有动态角色已注册
	for _, cfg := range cfgs {
		found, ok := r.Get(cfg.ID)
		if !ok {
			t.Errorf("generated agent %s not found in registry", cfg.ID)
		}
		if !found.IsGenerated {
			t.Errorf("agent %s should be marked as generated", cfg.ID)
		}
		if found.BoundTaskID != "task-1" {
			t.Errorf("expected bound task id task-1, got %s", found.BoundTaskID)
		}
	}

	// 清除动态角色
	r.CleanupGeneratedAgents("task-1")

	// 验证动态角色已被清除
	for _, cfg := range cfgs {
		_, ok := r.Get(cfg.ID)
		if ok {
			t.Errorf("generated agent %s should be cleaned up", cfg.ID)
		}
	}

	// 验证预设角色仍然存在
	for _, role := range []string{"researcher", "writer", "reviewer"} {
		_, ok := r.GetByRole(role)
		if !ok {
			t.Errorf("builtin role %s should still exist after cleanup", role)
		}
	}
}

func TestDynamicAgentRegistry_ApplyReplacesPrevious(t *testing.T) {
	r := NewDynamicAgentRegistry()

	// 第一轮生成
	cfgs1 := []*AgentConfig{
		GenerateAgentConfig("researcher", "研究员V1", "你是研究员"),
	}
	r.ApplyGeneratedAgents("task-1", cfgs1)

	// 第二轮生成（同任务）
	cfgs2 := []*AgentConfig{
		GenerateAgentConfig("researcher", "研究员V2", "你是更好的研究员"),
		GenerateAgentConfig("writer", "撰稿人V2", "你是更好的撰稿人"),
	}
	r.ApplyGeneratedAgents("task-1", cfgs2)

	// 第一轮的角色应被清除
	for _, cfg := range cfgs1 {
		_, ok := r.Get(cfg.ID)
		if ok {
			t.Errorf("old generated agent %s should be replaced", cfg.ID)
		}
	}

	// 第二轮的角色应存在
	generated := r.ListGenerated("task-1")
	if len(generated) != 2 {
		t.Errorf("expected 2 generated agents, got %d", len(generated))
	}
}

func TestDynamicAgentRegistry_ConcurrentAccess(t *testing.T) {
	r := NewDynamicAgentRegistry()

	var wg sync.WaitGroup
	// 并发写入
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg := GenerateAgentConfig("writer", "撰稿人", "你是一位撰稿人")
			r.Register(cfg)
		}()
	}
	// 并发读取
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.ListAll()
			r.GetByRole("writer")
		}()
	}
	wg.Wait()
}

func TestApplyRoleOverrides(t *testing.T) {
	base := &AgentConfig{
		ID:           "test-1",
		Name:         "研究员",
		Role:         "researcher",
		Persona:      "原始 persona",
		AllowedTools: []string{"search", "factcheck", "write"},
	}

	// 测试无覆盖
	result := ApplyRoleOverrides(base, nil)
	if result.Persona != "原始 persona" {
		t.Errorf("expected original persona, got %s", result.Persona)
	}

	// 测试覆盖 persona
	newPersona := "新的 persona"
	overrides := &AgentRoleOverrides{
		DeveloperInstructions: &newPersona,
		DisabledTools:         []string{"write"},
	}
	result = ApplyRoleOverrides(base, overrides)
	if result.Persona != "新的 persona" {
		t.Errorf("expected new persona, got %s", result.Persona)
	}
	// 验证工具被禁用
	for _, tool := range result.AllowedTools {
		if tool == "write" {
			t.Error("write tool should be disabled")
		}
	}
	// 验证原始配置未被修改
	if base.Persona != "原始 persona" {
		t.Error("original config should not be modified")
	}
}

func TestGenerateAgentConfig(t *testing.T) {
	cfg := GenerateAgentConfig("researcher", "宏观经济研究员", "你是宏观经济专家")

	if cfg.Name != "宏观经济研究员" {
		t.Errorf("expected name 宏观经济研究员, got %s", cfg.Name)
	}
	if cfg.Role != "researcher" {
		t.Errorf("expected role researcher, got %s", cfg.Role)
	}
	if cfg.Persona != "你是宏观经济专家" {
		t.Errorf("expected custom persona, got %s", cfg.Persona)
	}
	if !cfg.IsGenerated {
		t.Error("should be marked as generated")
	}
	if cfg.BaseRole != "researcher" {
		t.Errorf("expected base role researcher, got %s", cfg.BaseRole)
	}
	// 验证从预设角色继承了工具和能力
	if len(cfg.AllowedTools) == 0 {
		t.Error("should inherit tools from builtin role")
	}
	if len(cfg.CanProduce) == 0 {
		t.Error("should inherit CanProduce from builtin role")
	}
}

func TestGenerateAgentConfig_InvalidRoleFallback(t *testing.T) {
	cfg := GenerateAgentConfig("invalid_role", "测试角色", "你是测试")

	// 应回退到 writer
	if cfg.BaseRole != "writer" {
		t.Errorf("expected fallback to writer, got %s", cfg.BaseRole)
	}
}
