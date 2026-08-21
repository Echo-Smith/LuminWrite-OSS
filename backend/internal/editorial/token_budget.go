package editorial

import (
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

// ─── Agent Token 预算追踪（Beta: 编辑部模式 Phase 2.5）────────────
//
// 借鉴 Codex 的 TokenBudgetContext + Context Window ID，
// 为每个 Agent 分配 Token 预算并追踪使用量。

// AgentTokenBudget 单个 Agent 的 Token 预算
type AgentTokenBudget struct {
	AgentID         string `json:"agent_id"`
	ContextWindowID string `json:"context_window_id"` // UUID，标识当前上下文窗口
	TokensLeft      int64  `json:"tokens_left"`
	TotalUsed       int64  `json:"total_used"`
}

// DAGTokenBudget DAG 级别的 Token 预算管理器
type DAGTokenBudget struct {
	mu         sync.Mutex
	budgets    map[string]*AgentTokenBudget // agentID → budget
	totalBudget int64                        // 整个 DAG 的总预算
	totalUsed   atomic.Int64                 // 整个 DAG 已用总量
}

// NewDAGTokenBudget 创建 DAG Token 预算管理器
func NewDAGTokenBudget(totalBudget int64) *DAGTokenBudget {
	return &DAGTokenBudget{
		budgets:     make(map[string]*AgentTokenBudget),
		totalBudget: totalBudget,
	}
}

// AllocateBudget 为指定 Agent 分配 Token 预算
func (d *DAGTokenBudget) AllocateBudget(agentID string, amount int64) *AgentTokenBudget {
	d.mu.Lock()
	defer d.mu.Unlock()
	budget := &AgentTokenBudget{
		AgentID:         agentID,
		ContextWindowID: generateWindowID(),
		TokensLeft:      amount,
	}
	d.budgets[agentID] = budget
	return budget
}

// UpdateUsage 更新 Agent 的 Token 使用量
func (d *DAGTokenBudget) UpdateUsage(agentID string, used int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	budget, ok := d.budgets[agentID]
	if !ok {
		return
	}
	budget.TotalUsed += used
	budget.TokensLeft -= used
	d.totalUsed.Add(used)
}

// GetBudget 获取 Agent 的 Token 预算
func (d *DAGTokenBudget) GetBudget(agentID string) (*AgentTokenBudget, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	budget, ok := d.budgets[agentID]
	return budget, ok
}

// GetTotalUsed 获取整个 DAG 的 Token 使用总量
func (d *DAGTokenBudget) GetTotalUsed() int64 {
	return d.totalUsed.Load()
}

// IsBudgetExceeded 检查整个 DAG 的预算是否已超
func (d *DAGTokenBudget) IsBudgetExceeded() bool {
	if d.totalBudget <= 0 {
		return false
	}
	return d.totalUsed.Load() >= d.totalBudget
}

// IsAgentBudgetLow 检查指定 Agent 的预算是否低于阈值
func (d *DAGTokenBudget) IsAgentBudgetLow(agentID string, threshold int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	budget, ok := d.budgets[agentID]
	if !ok {
		return false
	}
	return budget.TokensLeft < threshold
}

// generateWindowID 生成 Context Window ID
func generateWindowID() string {
	return uuid.New().String()
}
