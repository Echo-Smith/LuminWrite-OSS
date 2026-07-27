package editorial

import (
	"context"
	"errors"
	"time"
)

// ─── 任务状态 ─────────────────────────────────────────────

// TaskStatus 表示任务在编辑部工作流中的当前阶段
type TaskStatus string

const (
	StatusDraft            TaskStatus = "draft"             // 人类编辑创建选题
	StatusPendingApproval  TaskStatus = "pending_approval"  // 等待立项审批
	StatusResearch         TaskStatus = "research"          // 研究 Agent 工作中
	StatusWriting          TaskStatus = "writing"           // 写作 Agent 工作中
	StatusReview           TaskStatus = "review"            // 审校 Agent 工作中
	StatusPendingPublish   TaskStatus = "pending_publish"   // 等待发布审批
	StatusPublished        TaskStatus = "published"         // 已发布
	StatusArchived         TaskStatus = "archived"          // 已归档
)

// CanTransitionTo 检查状态转换是否合法
func (s TaskStatus) CanTransitionTo(target TaskStatus) bool {
	transitions := map[TaskStatus][]TaskStatus{
		StatusDraft:           {StatusPendingApproval, StatusArchived},
		StatusPendingApproval: {StatusResearch, StatusDraft, StatusArchived},
		StatusResearch:        {StatusWriting, StatusPendingApproval},
		StatusWriting:         {StatusReview, StatusResearch},
		StatusReview:          {StatusPendingPublish, StatusWriting, StatusPendingApproval},
		StatusPendingPublish:  {StatusPublished, StatusReview},
		StatusPublished:       {StatusArchived},
		StatusArchived:        {},
	}
	allowed, ok := transitions[s]
	if !ok {
		return false
	}
	for _, t := range allowed {
		if t == target {
			return true
		}
	}
	return false
}

// ─── 分配类型 ─────────────────────────────────────────────

// AssigneeType 表示任务当前由谁负责
type AssigneeType string

const (
	AssigneeHuman          AssigneeType = "human"
	AssigneeResearchAgent  AssigneeType = "research_agent"
	AssigneeWritingAgent   AssigneeType = "writing_agent"
	AssigneeReviewAgent    AssigneeType = "review_agent"
)

// ─── 任务 ─────────────────────────────────────────────────

// Task 编辑部任务 — 一个选题从立项到发布的完整生命周期
type Task struct {
	ID             string        `json:"id"`
	Title          string        `json:"title"`
	Description    string        `json:"description"`
	OwnerID        string        `json:"owner_id,omitempty"`
	AssigneeType   AssigneeType  `json:"assignee_type"`
	Deadline       *time.Time    `json:"deadline,omitempty"`
	Status         TaskStatus    `json:"status"`
	AcceptCriteria string        `json:"accept_criteria"`
	AllowedTools   []string      `json:"allowed_tools,omitempty"`
	TokenBudget    int           `json:"token_budget"`
	TokenUsed      int           `json:"token_used"`
	Priority       int           `json:"priority"`
	Tags           []string      `json:"tags,omitempty"`
	StyleSlug      string        `json:"style_slug"`
	ConversationID string        `json:"conversation_id,omitempty"`
	CreatedBy      string        `json:"created_by"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

// CreateTaskInput 创建任务的输入
type CreateTaskInput struct {
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Deadline       *time.Time `json:"deadline,omitempty"`
	AcceptCriteria string     `json:"accept_criteria"`
	Priority       int        `json:"priority"`
	Tags           []string   `json:"tags,omitempty"`
	StyleSlug      string     `json:"style_slug"`
	TokenBudget    int        `json:"token_budget"`
}

// AdvanceTaskInput 推进任务的输入
type AdvanceTaskInput struct {
	TargetStatus TaskStatus   `json:"target_status"`
	AssigneeType AssigneeType `json:"assignee_type,omitempty"`
	Rationale    string       `json:"rationale,omitempty"`
	DecidedBy    string       `json:"decided_by,omitempty"` // 决策者 ID（人类 userID 或 "system"）
}

// ─── 交付物类型 ───────────────────────────────────────────

// ArtifactType 交付物类型 — Agent 之间传递的结构化产出
type ArtifactType string

const (
	ArtifactTopicCard     ArtifactType = "topic_card"      // 选题卡
	ArtifactResearchBrief ArtifactType = "research_brief"   // 研究简报
	ArtifactSourcePack    ArtifactType = "source_pack"      // 信源包
	ArtifactFactClaims    ArtifactType = "fact_claims"      // 事实声明表
	ArtifactOutline       ArtifactType = "outline"          // 提纲
	ArtifactDraft         ArtifactType = "draft"            // 初稿
	ArtifactReviewReport  ArtifactType = "review_report"    // 审查报告
	ArtifactRevisedDraft  ArtifactType = "revised_draft"    // 修改稿
)

// ArtifactStatus 交付物状态
type ArtifactStatus string

const (
	ArtifactStatusDraft      ArtifactStatus = "draft"      // 草稿
	ArtifactStatusSubmitted  ArtifactStatus = "submitted"  // 已提交审批
	ArtifactStatusApproved   ArtifactStatus = "approved"   // 已通过
	ArtifactStatusRejected   ArtifactStatus = "rejected"   // 已驳回
	ArtifactStatusSuperseded ArtifactStatus = "superseded" // 已被新版本取代
)

// Artifact 编辑部交付物 — Agent 间传递的可验收产出
type Artifact struct {
	ID          string         `json:"id"`
	TaskID      string         `json:"task_id"`
	Type        ArtifactType   `json:"type"`
	Version     int            `json:"version"`
	Content     string         `json:"content"` // JSON 字符串
	Status      ArtifactStatus `json:"status"`
	ProducedBy  string         `json:"produced_by"`
	ReviewedBy  string         `json:"reviewed_by,omitempty"`
	ReviewNote  string         `json:"review_note,omitempty"`
	ParentID    string         `json:"parent_id,omitempty"`
	TokenCost   int            `json:"token_cost"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// ArtifactRef 交付物引用 — 在 AgentContext 中传递的轻量引用
type ArtifactRef struct {
	ID      string        `json:"id"`
	Type    ArtifactType  `json:"type"`
	Version int           `json:"version"`
}

// SubmitArtifactInput 提交交付物的输入
type SubmitArtifactInput struct {
	Type       ArtifactType `json:"type"`
	Content    string       `json:"content"`
	ProducedBy string       `json:"produced_by"`
	TokenCost  int          `json:"token_cost"`
	ParentID   string       `json:"parent_id,omitempty"`
}

// ReviewArtifactInput 审批交付物的输入
type ReviewArtifactInput struct {
	Status     ArtifactStatus `json:"status"`
	ReviewerID string         `json:"reviewer_id"`
	ReviewNote string         `json:"review_note"`
}

// ProducerFor 返回指定交付物类型的产出者角色
func (t ArtifactType) ProducerFor() string {
	switch t {
	case ArtifactTopicCard:
		return "human"
	case ArtifactResearchBrief, ArtifactSourcePack, ArtifactFactClaims:
		return "research_agent"
	case ArtifactOutline, ArtifactDraft, ArtifactRevisedDraft:
		return "writing_agent"
	case ArtifactReviewReport:
		return "review_agent"
	default:
		return "unknown"
	}
}

// ConsumerFor 返回指定交付物类型的消费者角色
func (t ArtifactType) ConsumerFor() string {
	switch t {
	case ArtifactTopicCard:
		return "research_agent"
	case ArtifactResearchBrief, ArtifactFactClaims:
		return "writing_agent"
	case ArtifactSourcePack, ArtifactDraft, ArtifactRevisedDraft:
		return "review_agent"
	case ArtifactReviewReport:
		return "writing_agent"
	case ArtifactOutline:
		return "human"
	default:
		return "unknown"
	}
}

// ─── Actor 模型 ─────────────────────────────────────────

// ActorType 标识决策主体的类别
type ActorType string

const (
	ActorHuman  ActorType = "human"  // 人类用户
	ActorAgent  ActorType = "agent"  // Agent 角色
	ActorSystem ActorType = "system" // 系统/自动
)

// Actor 表示决策主体 — 替代旧的 decided_by/decided_by_type 组合
// 约束：
//   - human:  必须有 UserID
//   - agent:  必须有 Role
//   - system: 两者都可空
//
// Label 始终填充，用于人类可读的展示。
type Actor struct {
	Type    ActorType `json:"type"`
	UserID  string    `json:"user_id,omitempty"`  // UUID，human 时必填
	Role    string    `json:"role,omitempty"`    // agent 角色标识
	Label   string    `json:"label"`             // 人类可读标签
}

// NewHumanActor 创建一个人类 Actor
func NewHumanActor(userID, label string) Actor {
	return Actor{Type: ActorHuman, UserID: userID, Label: label}
}

// NewAgentActor 创建一个 Agent Actor
func NewAgentActor(role AgentRole, label string) Actor {
	return Actor{Type: ActorAgent, Role: string(role), Label: label}
}

// NewSystemActor 创建一个系统 Actor
func NewSystemActor(label string) Actor {
	return Actor{Type: ActorSystem, Label: label}
}

// ─── 决策类型 ─────────────────────────────────────────────

// DecisionType 决策类型
type DecisionType string

const (
	DecisionApproveTopic     DecisionType = "approve_topic"      // 是否立项
	DecisionSelectAngle      DecisionType = "select_angle"       // 哪个角度更值得写
	DecisionTrustSource      DecisionType = "trust_source"       // 某条信源是否可信
	DecisionAcceptReview     DecisionType = "accept_review"      // 是否接受审校意见
	DecisionAllowRewrite     DecisionType = "allow_rewrite"      // 是否允许重写
	DecisionPublish          DecisionType = "publish"            // 是否达到发布标准
	DecisionEscalate         DecisionType = "escalate"           // 升级到人类裁决
	DecisionResearchComplete DecisionType = "research_complete" // 研究完成，进入写作
	DecisionDraftComplete    DecisionType = "draft_complete"     // 初稿完成，进入审校
)

// DecisionStatus 决策状态
type DecisionStatus string

const (
	DecisionStatusPending    DecisionStatus = "pending"
	DecisionStatusApproved   DecisionStatus = "approved"
	DecisionStatusRejected   DecisionStatus = "rejected"
	DecisionStatusEscalated  DecisionStatus = "escalated"
)

// DecidedByType 决策者类型 (deprecated, use Actor)
type DecidedByType string

const (
	DecidedByHuman          DecidedByType = "human"
	DecidedByResearchAgent  DecidedByType = "research_agent"
	DecidedByWritingAgent   DecidedByType = "writing_agent"
	DecidedByReviewAgent    DecidedByType = "review_agent"
	DecidedBySystem         DecidedByType = "system"
)

// Decision 编辑部决策 — 记录谁、在什么角色下、做什么决策、依据是什么
type Decision struct {
	ID           string         `json:"id"`
	TaskID       string         `json:"task_id"`
	Type         DecisionType   `json:"type"`
	Actor        Actor          `json:"actor"`
	Status       DecisionStatus `json:"status"`
	Rationale    string         `json:"rationale,omitempty"`
	Evidence     string         `json:"evidence,omitempty"`
	ArtifactID   string         `json:"artifact_id,omitempty"`
	// approve_target_status / reject_target_status:
	// 在创建 Decision 时直接保存批准/驳回后的目标状态，
	// ResolveDecision 时直接读取，不需要通过全局 switch 猜测去向。
	ApproveTargetStatus string     `json:"approve_target_status,omitempty"`
	RejectTargetStatus  string     `json:"reject_target_status,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	DecidedAt            *time.Time `json:"decided_at,omitempty"`

	// Legacy fields (deprecated, kept for backward compatibility in API responses)
	DecidedBy     string        `json:"decided_by,omitempty"`
	DecidedByType DecidedByType `json:"decided_by_type"`
}

// CreateDecisionInput 创建决策的输入
type CreateDecisionInput struct {
	Type                DecisionType   `json:"type"`
	Actor               Actor          `json:"actor"`
	Status              DecisionStatus `json:"status"`
	Rationale           string         `json:"rationale"`
	Evidence            string         `json:"evidence,omitempty"`
	ArtifactID          string         `json:"artifact_id,omitempty"`
	ApproveTargetStatus TaskStatus     `json:"approve_target_status,omitempty"`
	RejectTargetStatus  TaskStatus     `json:"reject_target_status,omitempty"`

	// Legacy fields (deprecated, use Actor)
	DecidedBy     string        `json:"decided_by"`
	DecidedByType DecidedByType `json:"decided_by_type"`
}

// DecisionWithTask 带任务信息的决策（用于全局待处理决策列表）
type DecisionWithTask struct {
	Decision        Decision   `json:"decision"`
	TaskTitle       string     `json:"task_title"`
	TaskStatus      TaskStatus `json:"task_status"`
	TaskAssignee    string     `json:"task_assignee"`
	TaskOwnerID     string     `json:"task_owner_id"`
	TaskPriority    int        `json:"task_priority"`
	TaskTokenUsed   int        `json:"task_token_used"`
	TaskTokenBudget int        `json:"task_token_budget"`
}

// ResolveDecisionInput 人类处理待决策的输入
type ResolveDecisionInput struct {
	Status    DecisionStatus `json:"status"`     // approved | rejected
	Rationale string         `json:"rationale"`  // 决策理由
}

// ─── Claim 状态 (P0-5: verified 语义修正) ──────────────

// ClaimStatus 表示事实声明的验证状态
// LLM 判断最多只能标记为 supported/unsupported/conflicted/unknown
// "verified" 应要求可追溯证据或人工确认
// 实验模式需要自动推进时，可配置 experiment_policy=auto_accept_unverified
// 但不能篡改事实状态。
type ClaimStatus string

const (
	ClaimSupported   ClaimStatus = "supported"   // LLM 判断有信源支持
	ClaimUnsupported ClaimStatus = "unsupported" // LLM 判断无信源支持
	ClaimConflicted  ClaimStatus = "conflicted"  // 信源间存在矛盾
	ClaimUnknown     ClaimStatus = "unknown"     // LLM 无法判断
	// ClaimVerified 只有可追溯证据或人工确认后才能标记
	ClaimVerified ClaimStatus = "verified"
)

// Claim 表示研究 Agent 产出的事实声明
// Verified 字段已废弃，使用 Status 字段替代
// Deprecated: 使用 ClaimWithStatus 替代


// ─── Agent 角色 ───────────────────────────────────────────

// AgentRole Agent 角色标识
type AgentRole string

const (
	RoleResearch AgentRole = "research_agent"
	RoleWriting  AgentRole = "writing_agent"
	RoleReview   AgentRole = "review_agent"
)

// AgentDefinition Agent 角色定义
type AgentDefinition struct {
	Role              AgentRole
	Name              string
	Description       string
	CanProduce        []ArtifactType
	CanConsume        []ArtifactType
	CanDecide         []DecisionType
	RequiresIsolation bool
}

// AgentRegistry Agent 角色注册表
var AgentRegistry = map[AgentRole]AgentDefinition{
	RoleResearch: {
		Role:        RoleResearch,
		Name:        "研究 Agent",
		Description: "负责多源检索、信源分级、事实声明与证据绑定、标记矛盾和信息缺口",
		CanProduce:  []ArtifactType{ArtifactResearchBrief, ArtifactSourcePack, ArtifactFactClaims},
		CanConsume:  []ArtifactType{ArtifactTopicCard},
		CanDecide:   []DecisionType{DecisionTrustSource},
	},
	RoleWriting: {
		Role:        RoleWriting,
		Name:        "写作 Agent",
		Description: "基于已批准研究包写作，按风格 Profile 生成提纲和初稿，接受审校意见修改",
		CanProduce:  []ArtifactType{ArtifactOutline, ArtifactDraft, ArtifactRevisedDraft},
		CanConsume:  []ArtifactType{ArtifactResearchBrief, ArtifactFactClaims, ArtifactReviewReport, ArtifactTopicCard},
	},
	RoleReview: {
		Role:              RoleReview,
		Name:              "审校 Agent",
		Description:       "使用独立上下文审查事实、风格、风险，拥有驳回权但不能直接发布",
		CanProduce:        []ArtifactType{ArtifactReviewReport},
		CanConsume:        []ArtifactType{ArtifactSourcePack, ArtifactFactClaims, ArtifactDraft, ArtifactRevisedDraft, ArtifactResearchBrief},
		CanDecide:         []DecisionType{DecisionAcceptReview, DecisionAllowRewrite},
		RequiresIsolation: true,
	},
}

// ─── Agent 上下文 ─────────────────────────────────────────

// AgentContext 每个 Agent 的局部上下文
type AgentContext struct {
	Role           AgentRole
	TaskID         string
	TraceID        string
	UserID         string
	InputArtifacts []Artifact
	OutputArtifact *Artifact
	LocalMemory    interface{}
	TokenUsage     int
	Timeout        time.Duration
	MaxLLMFails    int
}

// NewAgentContext 创建新的 Agent 上下文
func NewAgentContext(role AgentRole, taskID, userID string) *AgentContext {
	return &AgentContext{
		Role:   role,
		TaskID: taskID,
		UserID: userID,
	}
}

// AddInputArtifact 添加输入交付物
func (ac *AgentContext) AddInputArtifact(a Artifact) {
	ac.InputArtifacts = append(ac.InputArtifacts, a)
}

// GetArtifact 按类型获取最近的输入交付物
func (ac *AgentContext) GetArtifact(t ArtifactType) *Artifact {
	for i := len(ac.InputArtifacts) - 1; i >= 0; i-- {
		if ac.InputArtifacts[i].Type == t && ac.InputArtifacts[i].Status == ArtifactStatusApproved {
			return &ac.InputArtifacts[i]
		}
	}
	for i := len(ac.InputArtifacts) - 1; i >= 0; i-- {
		if ac.InputArtifacts[i].Type == t {
			return &ac.InputArtifacts[i]
		}
	}
	return nil
}

// ─── Agent 执行接口 ───────────────────────────────────────

// AgentExecutor Agent 执行器接口
type AgentExecutor interface {
	Role() AgentRole
	Execute(ctx context.Context, ac *AgentContext) (*Artifact, error)
}

// ─── Event / Transition (P0-4: 三层模型) ──────────────
// Event: Agent 完成研究、稿件生成等客观事件
// Decision: 存在选项、风险或权限判断时的选择
// Transition: 状态变化，并引用触发它的 Event 或 Decision

// EventType 事件类型
type EventType string

const (
	EventAgentRunCompleted EventType = "agent_run.completed"
	EventAgentRunFailed    EventType = "agent_run.failed"
	EventArtifactProduced  EventType = "artifact.produced"
)

// AgentRunEvent 记录 Agent 执行事件
// 与 Decision 区分：Agent 完成工作是客观事件，不需要选择
// 只有需要人类裁决或权限判断时才创建 Decision
type AgentRunEvent struct {
	ID        string     `json:"id"`
	TaskID    string     `json:"task_id"`
	Type      EventType  `json:"type"`
	AgentRole AgentRole  `json:"agent_role"`
	Status    string     `json:"status"` // completed | failed
	ArtifactID string    `json:"artifact_id,omitempty"`
	Error     string     `json:"error,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// TransitionCauseType 标识状态变化的触发原因
type TransitionCauseType string

const (
	TransitionCauseEvent    TransitionCauseType = "event"    // 由 Agent 事件触发
	TransitionCauseDecision TransitionCauseType = "decision" // 由人类/系统决策触发
)

// TransitionCommand 是 TransitionTask 的输入参数
// 在单个事务中执行：
//   - SELECT ... FOR UPDATE 锁定任务行
//   - 校验 expected status/version
//   - 创建或处理 Decision（如果有）
//   - 更新 Task 状态
//   - 创建 Agent run lease（如果需要）
//   - 写入 outbox event
//   - commit
// Agent 执行在事务提交后异步触发。
type TransitionCommand struct {
	TaskID           string
	TargetStatus     TaskStatus
	ExpectedStatus   TaskStatus     // 当前期望状态，用于乐观锁校验
	Cause            TransitionCauseType
	CauseEventID     string         // 当 Cause = event 时引用
	CauseDecisionID  string         // 当 Cause = decision 时引用
	Actor            Actor          // 触发转换的主体
	Rationale        string
	AutoStartAgent   bool           // 是否在提交后自动启动对应 Agent
}

// ─── 错误 ─────────────────────────────────────────────────

var (
	ErrTaskNotFound        = errors.New("task not found")
	ErrArtifactNotFound    = errors.New("artifact not found")
	ErrDecisionNotFound    = errors.New("decision not found")
	ErrInvalidTransition   = errors.New("invalid status transition")
	ErrArtifactNotApproved = errors.New("required artifact not approved")
	ErrTokenBudgetExceeded = errors.New("task token budget exceeded")
	ErrLeaseConflict       = errors.New("agent lease already active for this task and role")
	ErrStatusConflict      = errors.New("task status does not match expected status")
	ErrForbidden           = errors.New("access forbidden: resource does not belong to user")
)
