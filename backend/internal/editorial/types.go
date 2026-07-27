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

// ─── 决策类型 ─────────────────────────────────────────────

// DecisionType 决策类型
type DecisionType string

const (
	DecisionApproveTopic  DecisionType = "approve_topic"   // 是否立项
	DecisionSelectAngle   DecisionType = "select_angle"     // 哪个角度更值得写
	DecisionTrustSource   DecisionType = "trust_source"     // 某条信源是否可信
	DecisionAcceptReview  DecisionType = "accept_review"    // 是否接受审校意见
	DecisionAllowRewrite  DecisionType = "allow_rewrite"    // 是否允许重写
	DecisionPublish       DecisionType = "publish"          // 是否达到发布标准
	DecisionEscalate      DecisionType = "escalate"         // 升级到人类裁决
)

// DecisionStatus 决策状态
type DecisionStatus string

const (
	DecisionStatusPending    DecisionStatus = "pending"
	DecisionStatusApproved   DecisionStatus = "approved"
	DecisionStatusRejected   DecisionStatus = "rejected"
	DecisionStatusEscalated  DecisionStatus = "escalated"
)

// DecidedByType 决策者类型
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
	ID            string          `json:"id"`
	TaskID        string          `json:"task_id"`
	Type          DecisionType    `json:"type"`
	DecidedBy     string          `json:"decided_by,omitempty"`
	DecidedByType DecidedByType   `json:"decided_by_type"`
	Status        DecisionStatus  `json:"status"`
	Rationale     string          `json:"rationale,omitempty"`
	Evidence      string          `json:"evidence,omitempty"`
	ArtifactID    string          `json:"artifact_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	DecidedAt     *time.Time      `json:"decided_at,omitempty"`
}

// CreateDecisionInput 创建决策的输入
type CreateDecisionInput struct {
	Type          DecisionType    `json:"type"`
	DecidedBy     string          `json:"decided_by"`
	DecidedByType DecidedByType   `json:"decided_by_type"`
	Status        DecisionStatus  `json:"status"`
	Rationale     string          `json:"rationale"`
	Evidence      string          `json:"evidence,omitempty"`
	ArtifactID    string          `json:"artifact_id,omitempty"`
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

// ─── 错误 ─────────────────────────────────────────────────

var (
	ErrTaskNotFound        = errors.New("task not found")
	ErrArtifactNotFound    = errors.New("artifact not found")
	ErrDecisionNotFound    = errors.New("decision not found")
	ErrInvalidTransition   = errors.New("invalid status transition")
	ErrArtifactNotApproved = errors.New("required artifact not approved")
	ErrTokenBudgetExceeded = errors.New("task token budget exceeded")
)
