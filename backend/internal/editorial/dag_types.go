package editorial

import "time"

// ─── DAG 拓扑定义（Beta: 编辑部模式 Phase 2.1）────────────
//
// NodeSpec / WorkflowSpec / Edge 定义 DAG 工作流拓扑。
// 借鉴 OpenMAIC 的 Director Graph 和 LangGraph 的 StateGraph。

// Position 前端节点图坐标
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// ContextForkMode 节点间上下文传递模式（借鉴 Codex SpawnAgentForkMode）
type ContextForkMode int

const (
	// ContextForkFull：子节点继承父节点的完整对话历史
	// 适用于：研究节点 → 写作节点（写作节点需要看到研究的完整过程）
	ContextForkFull ContextForkMode = iota

	// ContextForkLastN：子节点只继承最近 N 轮对话
	// 适用于：并行研究节点之间的交叉引用（只看结论不看过程）
	ContextForkLastN

	// ContextForkSummary：子节点只继承上游节点的 Artifact + 摘要
	// 适用于：审校节点（只需要看最终稿 + 研究简报，不需要看写作过程）
	ContextForkSummary
)

// NodeSpec 定义 DAG 中的一个执行节点
type NodeSpec struct {
	ID             string         `json:"id"`              // "node-1"
	AgentID        string         `json:"agent_id"`        // 引用 AgentConfig.ID
	Label         string         `json:"label"`            // "宏观分析"
	Dependencies   []string       `json:"dependencies"`    // 依赖的 NodeSpec.ID
	InputArtifacts []ArtifactType `json:"input_artifacts"` // 需要哪些交付物作为输入
	OutputArtifact ArtifactType  `json:"output_artifact"`  // 产出什么交付物

	// 上下文传递模式（借鉴 Codex SpawnAgentForkMode）
	ContextFork  ContextForkMode `json:"context_fork,omitempty"`
	ForkNTurns   int             `json:"fork_n_turns,omitempty"` // ContextForkLastN 时生效

	// 前端节点图坐标
	Position     *Position      `json:"position,omitempty"`
}

// Edge DAG 边定义（冗余于 dependencies，供前端直接渲染）
type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"` // "研究简报" — 交付物类型
}

// WorkflowSpec DAG 工作流定义
type WorkflowSpec struct {
	TaskID    string     `json:"task_id"`
	Nodes     []NodeSpec `json:"nodes"`
	Edges     []Edge     `json:"edges"`      // 冗余于 dependencies，供前端直接渲染
	CreatedBy string     `json:"created_by"`
	Source    string     `json:"source"`     // "llm_generated" | "user_modified" | "template"
	CreatedAt time.Time  `json:"created_at"`
}

// NodeStatus 节点执行状态
type NodeStatus string

const (
	NodeStatusPending   NodeStatus = "pending"   // 等待依赖完成
	NodeStatusRunning   NodeStatus = "running"   // 正在执行
	NodeStatusCompleted NodeStatus = "completed" // 执行完成
	NodeStatusFailed    NodeStatus = "failed"    // 执行失败
	NodeStatusSkipped   NodeStatus = "skipped"   // 跳过（依赖失败）
)

// NodeResult 节点执行结果
type NodeResult struct {
	NodeID     string      `json:"node_id"`
	Status     NodeStatus  `json:"status"`
	ArtifactID string      `json:"artifact_id,omitempty"`
	Error      string      `json:"error,omitempty"`
	TokensUsed int64       `json:"tokens_used,omitempty"`
	StartedAt  *time.Time  `json:"started_at,omitempty"`
	FinishedAt *time.Time  `json:"finished_at,omitempty"`
}

// WorkflowStatus 工作流整体状态
type WorkflowStatus string

const (
	WorkflowStatusCreated   WorkflowStatus = "created"
	WorkflowStatusRunning   WorkflowStatus = "running"
	WorkflowStatusCompleted WorkflowStatus = "completed"
	WorkflowStatusFailed    WorkflowStatus = "failed"
	WorkflowStatusPaused    WorkflowStatus = "paused"
)
