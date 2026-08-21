package editorial

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// ─── 动态 Agent 配置（Beta: 编辑部模式 Phase 1）────────────

// AgentConfig 是动态 Agent 的结构化配置，替代原有的固定 AgentRole 枚举。
// 借鉴 OpenMAIC 的 isGenerated 模式和 Codex 的 Role 覆盖层模式。
//
// 兼容策略：现有 AgentRole（research_agent/writing_agent/review_agent）保留为
// BuiltinRoles 的预设实例，线性模式继续使用固定角色，DAG 模式使用动态配置。
type AgentConfig struct {
	ID            string        `json:"id"`                       // 唯一标识
	Name          string        `json:"name"`                     // "宏观经济分析师"
	Role          string        `json:"role"`                     // "researcher" / "writer" / "reviewer" / 自定义
	Persona       string        `json:"persona,omitempty"`        // 完整 system prompt 覆盖
	AllowedTools  []string      `json:"allowed_tools,omitempty"`  // ["search","write","factcheck"]
	Priority      int           `json:"priority,omitempty"`      // Director 选择优先级
	CanProduce    []ArtifactType `json:"can_produce,omitempty"`
	CanConsume    []ArtifactType `json:"can_consume,omitempty"`

	// 动态生成标记（借鉴 OpenMAIC isGenerated）
	IsGenerated   bool          `json:"is_generated,omitempty"`
	BoundTaskID   string        `json:"bound_task_id,omitempty"`

	// 角色覆盖层（借鉴 Codex apply_role_to_config）
	BaseRole      string        `json:"base_role,omitempty"`      // 基于哪个预设角色
	Model         string        `json:"model,omitempty"`           // 可指定不同模型

	// 元数据
	CreatedAt     time.Time    `json:"created_at"`
}

// NewAgentConfig 创建一个新的 AgentConfig，自动生成 ID 和时间戳。
func NewAgentConfig(name, role string) *AgentConfig {
	return &AgentConfig{
		ID:        uuid.New().String(),
		Name:      name,
		Role:      role,
		CreatedAt: time.Now(),
	}
}

// ─── 角色覆盖层（借鉴 Codex AgentRoleOverrides）─────────────

// AgentRoleOverrides 角色配置覆盖层 — 借鉴 Codex 的 apply_role_to_config 模式。
// 角色作为"配置覆盖层"叠加到基础配置上，可以覆盖 persona、model、禁用工具等。
type AgentRoleOverrides struct {
	DeveloperInstructions *string  `json:"developer_instructions,omitempty"`
	Model                 *string  `json:"model,omitempty"`
	ReasoningEffort       *string  `json:"reasoning_effort,omitempty"`
	DisabledTools         []string `json:"disabled_tools,omitempty"`
}

// ApplyRoleOverrides 将角色覆盖层应用到基础 AgentConfig，返回新的配置。
// 借鉴 Codex 的 apply_role_to_config 模式。
func ApplyRoleOverrides(base *AgentConfig, overrides *AgentRoleOverrides) *AgentConfig {
	if overrides == nil {
		return base
	}
	result := *base // 浅拷贝
	if overrides.DeveloperInstructions != nil {
		result.Persona = *overrides.DeveloperInstructions
	}
	if overrides.Model != nil {
		result.Model = *overrides.Model
	}
	if len(overrides.DisabledTools) > 0 {
		disabled := make(map[string]bool)
		for _, t := range overrides.DisabledTools {
			disabled[t] = true
		}
		var filtered []string
		for _, t := range result.AllowedTools {
			if !disabled[t] {
				filtered = append(filtered, t)
			}
		}
		result.AllowedTools = filtered
	}
	return &result
}

// ─── 动态 Agent 注册表 ─────────────────────────────────────

// DynamicAgentRegistry 动态 Agent 注册表 — 从全局 var 改为实例化的动态注册表。
// 借鉴 OpenMAIC 的 isGenerated 模式：动态角色打标后注册，任务完成后清除。
//
// 并发安全：使用 sync.RWMutex 保护内部 map。
type DynamicAgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]*AgentConfig // ID → Config
}

// NewDynamicAgentRegistry 创建动态注册表，预加载预设角色。
func NewDynamicAgentRegistry() *DynamicAgentRegistry {
	r := &DynamicAgentRegistry{
		agents: make(map[string]*AgentConfig),
	}
	// 预加载预设角色
	for _, role := range BuiltinRoles {
		cfg := role.ToAgentConfig()
		r.agents[cfg.ID] = cfg
	}
	return r
}

// Get 获取 Agent 配置 by ID
func (r *DynamicAgentRegistry) Get(id string) (*AgentConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.agents[id]
	return cfg, ok
}

// GetByRole 获取指定角色的第一个配置（用于线性模式兼容）
func (r *DynamicAgentRegistry) GetByRole(role string) (*AgentConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, cfg := range r.agents {
		if cfg.Role == role && !cfg.IsGenerated {
			return cfg, true
		}
	}
	return nil, false
}

// Register 注册单个 Agent 配置
func (r *DynamicAgentRegistry) Register(cfg *AgentConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[cfg.ID] = cfg
}

// ApplyGeneratedAgents 注册动态生成的 Agent 集合，先清除该任务上轮的生成角色。
// 借鉴 OpenMAIC 的 isGenerated 模式：任务完成后清除。
func (r *DynamicAgentRegistry) ApplyGeneratedAgents(taskID string, configs []*AgentConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 清除上一轮该任务的生成角色
	for id, cfg := range r.agents {
		if cfg.BoundTaskID == taskID {
			delete(r.agents, id)
		}
	}
	// 注册新的生成角色
	for _, cfg := range configs {
		cfg.IsGenerated = true
		cfg.BoundTaskID = taskID
		r.agents[cfg.ID] = cfg
	}
}

// CleanupGeneratedAgents 清除指定任务的所有生成角色
func (r *DynamicAgentRegistry) CleanupGeneratedAgents(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, cfg := range r.agents {
		if cfg.BoundTaskID == taskID {
			delete(r.agents, id)
		}
	}
}

// ListAll 返回所有注册的 Agent 配置
func (r *DynamicAgentRegistry) ListAll() []*AgentConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*AgentConfig, 0, len(r.agents))
	for _, cfg := range r.agents {
		result = append(result, cfg)
	}
	return result
}

// ListGenerated 返回指定任务的生成角色
func (r *DynamicAgentRegistry) ListGenerated(taskID string) []*AgentConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*AgentConfig
	for _, cfg := range r.agents {
		if cfg.BoundTaskID == taskID {
			result = append(result, cfg)
		}
	}
	return result
}
