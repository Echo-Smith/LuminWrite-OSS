package editorial

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── EditorialTool: 编辑部统一工具接口 ──────────────────────
//
// 参考 dsh 的 "everything is tool" 理念和 engine.AgentTool 的设计，
// 将编辑部模式的工具从硬编码 switch-case 改为注册式管理。
//
// 设计原则：
//   - 一切皆工具：搜索、写作、核查、信号提交都是工具
//   - 工具可注册：通过 EditorialToolRegistry 动态注册，新增工具只需 Register
//   - 工具可分配：每个工具声明它适用于哪些角色（Roles）
//   - 工具有元数据：MaxCalls、Category、Description 供 System Prompt 生成
//   - 信号工具特殊处理：通过 IsSignal 标记，拦截后存入 signalArgs

// EditorialTool 编辑部工具接口
type EditorialTool interface {
	// Name 工具唯一标识
	Name() string
	// Description 供 LLM 理解工具用途
	Description() string
	// Schema OpenAI 兼容的参数 schema
	Schema() map[string]any
	// Roles 此工具适用于哪些角色（空 = 所有角色）
	Roles() []string
	// MaxCalls 每次执行的最大调用次数（0 = 无限）
	MaxCalls() int
	// IsSignal 是否为信号工具（信号工具被拦截后不执行，只存参数）
	IsSignal() bool
	// Category 工具分类（retrieval/writing/review/signal）
	Category() string
	// Execute 执行工具逻辑，返回结果文本
	Execute(ctx context.Context, args string, runCtx *ToolRunContext) (string, error)
}

// ToolRunContext 工具执行时的运行时上下文
type ToolRunContext struct {
	LLM           *tools.LLMClient
	Search        *tools.SearchClient
	KBSearcher    tools.KnowledgeSearcher
	Profile       *profile.StyleProfile
	ExecCtx       *engine.ExecutionContext
	Emitter       engine.EventEmitter
	SearchResults *[]engine.SearchResult
	SearchMu      *sync.Mutex
}

// EditorialToolRegistry 编辑部工具注册中心
type EditorialToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]EditorialTool
}

// NewEditorialToolRegistry 创建工具注册中心
func NewEditorialToolRegistry() *EditorialToolRegistry {
	return &EditorialToolRegistry{tools: make(map[string]EditorialTool)}
}

// Register 注册一个工具
func (r *EditorialToolRegistry) Register(t EditorialTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
	slog.Info("editorial tool registered",
		"name", t.Name(),
		"category", t.Category(),
		"roles", t.Roles(),
		"max_calls", t.MaxCalls(),
		"is_signal", t.IsSignal(),
	)
}

// Get 获取工具
func (r *EditorialToolRegistry) Get(name string) (EditorialTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// ToolsForRole 返回角色可用的所有工具定义（ToolDef 格式）
func (r *EditorialToolRegistry) ToolsForRole(role string, hasSearch, hasKB bool) []tools.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var defs []tools.ToolDef
	for _, t := range r.tools {
		if !r.isToolForRole(t, role) {
			continue
		}
		if t.Name() == "search_web" && !hasSearch {
			continue
		}
		if t.Name() == "search_knowledge" && !hasKB {
			continue
		}

		defs = append(defs, tools.ToolDef{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		})
	}

	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Function.Name < defs[j].Function.Name
	})

	return defs
}

// SignalToolForRole 返回角色的信号工具名称
func (r *EditorialToolRegistry) SignalToolForRole(role string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, t := range r.tools {
		if t.IsSignal() && r.isToolForRole(t, role) {
			return t.Name()
		}
	}
	return ""
}

// MaxCallsMapForRole 返回角色的工具调用次数限制
func (r *EditorialToolRegistry) MaxCallsMapForRole(role string) map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m := make(map[string]int)
	for _, t := range r.tools {
		if t.MaxCalls() > 0 && !t.IsSignal() && r.isToolForRole(t, role) {
			m[t.Name()] = t.MaxCalls()
		}
	}
	return m
}

// ToolGuideForRole 返回角色的工具使用指引文本（供 system prompt）
func (r *EditorialToolRegistry) ToolGuideForRole(role string, hasSearch, hasKB bool) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var sb strings.Builder

	signalTool := ""
	for _, t := range r.tools {
		if t.IsSignal() && r.isToolForRole(t, role) {
			signalTool = t.Name()
			break
		}
	}

	sb.WriteString("你可以使用以下工具完成任务。完成后必须调用信号工具提交结果。\n\n")
	sb.WriteString("可用工具：\n")

	var regular []EditorialTool
	var signals []EditorialTool
	for _, t := range r.tools {
		if !r.isToolForRole(t, role) {
			continue
		}
		if t.Name() == "search_web" && !hasSearch {
			continue
		}
		if t.Name() == "search_knowledge" && !hasKB {
			continue
		}

		if t.IsSignal() {
			signals = append(signals, t)
		} else {
			regular = append(regular, t)
		}
	}

	sort.Slice(regular, func(i, j int) bool {
		return regular[i].Name() < regular[j].Name()
	})

	for _, t := range regular {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", t.Name(), t.Description()))
	}
	for _, t := range signals {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", t.Name(), t.Description()))
	}

	if signalTool != "" {
		sb.WriteString(fmt.Sprintf("\n完成后必须调用 %s 提交结果。\n", signalTool))
	}

	return sb.String()
}

// isToolForRole 检查工具是否适用于指定角色
func (r *EditorialToolRegistry) isToolForRole(t EditorialTool, role string) bool {
	roles := t.Roles()
	if len(roles) == 0 {
		return true // 空 = 所有角色
	}
	for _, r2 := range roles {
		if r2 == role {
			return true
		}
	}
	return false
}
