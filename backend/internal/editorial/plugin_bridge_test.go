package editorial

import (
	"context"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// 插件桥接单测（runtime-agility M3）：插件工具进编辑部 registry 后，
// role agent 的工具表/指引自动纳入，角色过滤与卸载正确。

// stubAgentTool 是 engine.AgentTool 的最小测试替身（不走真 HTTP）。
type stubAgentTool struct {
	name   string
	roles  []string
	result string
}

func (t *stubAgentTool) Name() string                { return t.name }
func (t *stubAgentTool) Description() string         { return "stub " + t.name }
func (t *stubAgentTool) Schema() map[string]any      { return map[string]any{"type": "object"} }
func (t *stubAgentTool) Roles() []string             { return t.roles }
func (t *stubAgentTool) Execute(ctx context.Context, args map[string]any, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) (*engine.ToolResult, error) {
	return &engine.ToolResult{Summary: t.result}, nil
}

func TestRegisterPluginToolsVisibleToRole(t *testing.T) {
	registry := NewEditorialToolRegistry()
	RegisterBuiltinTools(registry)
	before := len(registry.All())

	plugin := &engine.ToolPlugin{
		Name:        "weather-kit",
		Description: "天气工具包",
		Tools: []engine.AgentTool{
			&stubAgentTool{name: "get_weather", result: "stub get_weather"},
			&stubAgentTool{name: "get_market_news", result: "stub get_market_news"},
		},
		ToolRoles: map[string][]string{
			"get_weather": {"writer"},
		},
	}
	RegisterPluginTools(registry, plugin)

	if len(registry.All()) != before+2 {
		t.Fatalf("注册后工具数 = %d, want %d", len(registry.All()), before+2)
	}

	// 角色过滤：get_weather 仅 writer；get_market_news 全角色
	writerDefs := registry.ToolsForRole("writer", false, false)
	researcherDefs := registry.ToolsForRole("researcher", false, false)
	if !roleHasTool(writerDefs, "get_weather") || !roleHasTool(writerDefs, "get_market_news") {
		t.Fatal("writer 应见到 get_weather 与 get_market_news")
	}
	if roleHasTool(researcherDefs, "get_weather") {
		t.Fatal("researcher 不应见到 get_weather（roles=[writer]）")
	}
	if !roleHasTool(researcherDefs, "get_market_news") {
		t.Fatal("researcher 应见到 get_market_news（空 roles = 全角色）")
	}

	// system prompt 指引自动纳入
	guide := registry.ToolGuideForRole("writer", false, false)
	if !strings.Contains(guide, "get_weather") || !strings.Contains(guide, "stub get_weather") {
		t.Fatal("writer 的工具指引应包含插件工具及描述")
	}

	// Execute：参数 JSON 进、Summary 出
	tool, ok := registry.Get("get_weather")
	if !ok {
		t.Fatal("Get(get_weather) 未命中")
	}
	out, err := tool.Execute(context.Background(), `{"city":"北京"}`, &ToolRunContext{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "stub get_weather" {
		t.Fatalf("execute 输出 = %q", out)
	}

	// 非法参数 JSON 明确报错
	if _, err := tool.Execute(context.Background(), `{bad`, &ToolRunContext{}); err == nil {
		t.Fatal("非法 JSON 参数应报错")
	}

	// 卸载：按插件工具名移除
	UnregisterPluginTools(registry, plugin)
	if _, ok := registry.Get("get_weather"); ok {
		t.Fatal("卸载后 get_weather 应消失")
	}
	if len(registry.All()) != before {
		t.Fatalf("卸载后工具数应回到 %d, got %d", before, len(registry.All()))
	}
}

// 插件工具永不是信号工具，不污染角色的信号语义。
func TestPluginToolsAreNeverSignals(t *testing.T) {
	registry := NewEditorialToolRegistry()
	RegisterBuiltinTools(registry)
	plugin := &engine.ToolPlugin{
		Name:  "kit",
		Tools: []engine.AgentTool{&stubAgentTool{name: "kit_tool", roles: nil}},
	}
	RegisterPluginTools(registry, plugin)

	if tool, _ := registry.Get("kit_tool"); tool.IsSignal() {
		t.Fatal("插件工具不得是信号工具")
	}
	if registry.MaxCallsMapForRole("writer")["kit_tool"] != 0 {
		t.Fatal("插件工具不应有调用次数限制（0 = 无限）")
	}
}

func roleHasTool(defs []tools.ToolDef, name string) bool {
	for _, def := range defs {
		if def.Function.Name == name {
			return true
		}
	}
	return false
}
