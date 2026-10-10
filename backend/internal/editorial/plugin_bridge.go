package editorial

// 插件桥接（runtime-agility M3）：把热加载的 engine 工具插件适配成
// EditorialTool，注册进编辑部 registry——从此「加工具 = 放一个 JSON
// 文件（或 admin API 一次调用）」，role agent 下一轮运行即可见，无需
// 重编译 Go。
//
// 适配语义：
//   - HTTP 工具永不是信号工具（IsSignal=false，正常执行）；
//   - Roles 来自插件配置（空 = 所有角色）；
//   - MaxCalls 不设限（HTTP 外部调用的频控归 endpoint 服务方）；
//   - args 以 JSON 字符串进出 role 循环，转 map 交给 AgentTool。
//
// SSRF 红线在 engine.BuildPluginFromConfig 注册时已校验（协议白名单 +
// 解析 IP 公网），桥接层不做第二份校验。

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// pluginEditorialTool 适配 engine.AgentTool（HTTPTool）到编辑部接口。
type pluginEditorialTool struct {
	tool      engine.AgentTool
	roles     []string
	pluginTag string
}

func (t *pluginEditorialTool) Name() string        { return t.tool.Name() }
func (t *pluginEditorialTool) Description() string { return t.tool.Description() }
func (t *pluginEditorialTool) Schema() map[string]any {
	if schema := t.tool.Schema(); schema != nil {
		return schema
	}
	return map[string]any{"type": "object"}
}
func (t *pluginEditorialTool) Roles() []string  { return t.roles }
func (t *pluginEditorialTool) MaxCalls() int    { return 0 }
func (t *pluginEditorialTool) IsSignal() bool   { return false }
func (t *pluginEditorialTool) Category() string { return "plugin" }

func (t *pluginEditorialTool) Execute(ctx context.Context, args string, runCtx *ToolRunContext) (string, error) {
	parsed := map[string]any{}
	if len(args) > 0 {
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			return "", fmt.Errorf("插件工具 %s 的参数不是合法 JSON 对象: %w", t.tool.Name(), err)
		}
	}
	var execCtx *engine.ExecutionContext
	var emitter engine.EventEmitter
	if runCtx != nil {
		execCtx = runCtx.ExecCtx
		emitter = runCtx.Emitter
	}
	result, err := t.tool.Execute(ctx, parsed, execCtx, emitter)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", fmt.Errorf("插件工具 %s 返回空结果", t.tool.Name())
	}
	return result.Summary, nil
}

// RegisterPluginTools 把插件内全部工具桥接进编辑部 registry（同名覆盖，
// 幂等——目录 watcher 重扫时安全）。角色的 plugin 工具在 system prompt
// 的工具指引中自动出现（ToolGuideForRole 按注册遍历）。
func RegisterPluginTools(registry *EditorialToolRegistry, plugin *engine.ToolPlugin) {
	if registry == nil || plugin == nil {
		return
	}
	for _, tool := range plugin.Tools {
		registry.Register(&pluginEditorialTool{
			tool:      tool,
			roles:     plugin.ToolRoles[tool.Name()],
			pluginTag: plugin.Name,
		})
	}
}

// UnregisterPluginTools 从编辑部 registry 移除插件注册的全部工具。
func UnregisterPluginTools(registry *EditorialToolRegistry, plugin *engine.ToolPlugin) {
	if registry == nil || plugin == nil {
		return
	}
	for _, tool := range plugin.Tools {
		registry.Remove(tool.Name())
	}
}
