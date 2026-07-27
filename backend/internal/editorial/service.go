package editorial

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// Service 编辑部服务 — 对外暴露的统一接口
type Service struct {
	store         *Store
	orchestrator  *Orchestrator
}

// NewService 创建编辑部服务
func NewService(store *Store, emitter EventEmitter) *Service {
	orch := NewOrchestrator(store, emitter)
	return &Service{
		store:        store,
		orchestrator: orch,
	}
}

// Orchestrator 返回编排器（用于注册 Agent 执行器）
func (s *Service) Orchestrator() *Orchestrator {
	return s.orchestrator
}

// Store 返回存储（用于直接 DB 操作）
func (s *Service) Store() *Store {
	return s.store
}

// ─── 任务操作 ─────────────────────────────────────────────

// CreateTask 创建任务
func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput, userID string) (*Task, error) {
	task, err := s.store.CreateTask(ctx, input, userID)
	if err != nil {
		return nil, err
	}
	slog.Info("editorial: task created", "task_id", task.ID, "title", task.Title)

	// 自动创建选题卡 Artifact
	topicCardContent, _ := json.Marshal(map[string]interface{}{
		"title":           task.Title,
		"description":     task.Description,
		"accept_criteria": task.AcceptCriteria,
		"style_slug":      task.StyleSlug,
		"tags":            task.Tags,
		"priority":        task.Priority,
		"created_by":      task.CreatedBy,
	})
	if _, err := s.store.CreateArtifact(ctx, SubmitArtifactInput{
		Type:       ArtifactTopicCard,
		Content:    string(topicCardContent),
		ProducedBy: "human",
		TokenCost:  0,
	}, task.ID); err != nil {
		slog.Warn("editorial: failed to create topic card artifact", "task_id", task.ID, "error", err)
	} else {
		// 自动批准选题卡
		slog.Info("editorial: topic card artifact created", "task_id", task.ID)
	}

	return task, nil
}

// GetTask 获取任务
func (s *Service) GetTask(ctx context.Context, id string) (*Task, error) {
	return s.store.GetTask(ctx, id)
}

// ListTasks 列出任务
func (s *Service) ListTasks(ctx context.Context, status string, limit, offset int) ([]Task, error) {
	return s.store.ListTasks(ctx, status, limit, offset)
}

// AdvanceTask 推进任务
func (s *Service) AdvanceTask(ctx context.Context, taskID string, input AdvanceTaskInput) error {
	return s.orchestrator.AdvanceTask(ctx, taskID, input)
}

// ─── 交付物操作 ───────────────────────────────────────────

// SubmitArtifact 提交交付物
func (s *Service) SubmitArtifact(ctx context.Context, taskID string, input SubmitArtifactInput) (*Artifact, error) {
	art, err := s.store.CreateArtifact(ctx, input, taskID)
	if err != nil {
		return nil, err
	}
	slog.Info("editorial: artifact submitted",
		"task_id", taskID, "type", input.Type, "version", art.Version)
	return art, nil
}

// GetArtifact 获取交付物
func (s *Service) GetArtifact(ctx context.Context, id string) (*Artifact, error) {
	return s.store.GetArtifact(ctx, id)
}

// ListArtifacts 列出任务的所有交付物
func (s *Service) ListArtifacts(ctx context.Context, taskID string) ([]Artifact, error) {
	return s.store.ListArtifacts(ctx, taskID)
}

// ReviewArtifact 审批交付物
func (s *Service) ReviewArtifact(ctx context.Context, id string, input ReviewArtifactInput) (*Artifact, error) {
	art, err := s.store.ReviewArtifact(ctx, id, input)
	if err != nil {
		return nil, err
	}
	slog.Info("editorial: artifact reviewed",
		"artifact_id", id, "status", input.Status)
	return art, nil
}

// ─── 决策操作 ─────────────────────────────────────────────

// CreateDecision 创建决策
func (s *Service) CreateDecision(ctx context.Context, taskID string, input CreateDecisionInput) (*Decision, error) {
	d, err := s.store.CreateDecision(ctx, input, taskID)
	if err != nil {
		return nil, err
	}
	slog.Info("editorial: decision created",
		"task_id", taskID, "type", input.Type, "status", input.Status)
	return d, nil
}

// ListDecisions 列出任务的所有决策
func (s *Service) ListDecisions(ctx context.Context, taskID string) ([]Decision, error) {
	return s.store.ListDecisions(ctx, taskID)
}

// ListPendingDecisions 列出所有待处理决策（跨任务）
func (s *Service) ListPendingDecisions(ctx context.Context, limit int) ([]DecisionWithTask, error) {
	return s.store.ListPendingDecisions(ctx, limit)
}

// ResolveDecision 人类处理待决策 — 更新 Decision 状态后自动推进任务
func (s *Service) ResolveDecision(ctx context.Context, decisionID string, input ResolveDecisionInput, userID string) (*Decision, error) {
	d, err := s.store.UpdateDecisionStatus(ctx, decisionID, input.Status, input.Rationale, userID)
	if err != nil {
		return nil, err
	}

	// 根据 Decision 类型和处理结果决定任务下一步
	if input.Status == DecisionStatusApproved {
		// 批准 → 推进到下一阶段
		nextStatus := nextStatusForDecision(d.Type, d.TaskID)
		if nextStatus != "" {
			s.orchestrator.AdvanceTask(ctx, d.TaskID, AdvanceTaskInput{
				TargetStatus: nextStatus,
				DecidedBy:    userID,
				Rationale:    input.Rationale,
			})
		}
	} else if input.Status == DecisionStatusRejected {
		// 驳回 → 退回上一个阶段
		prevStatus := prevStatusForDecision(d.Type)
		if prevStatus != "" {
			s.orchestrator.AdvanceTask(ctx, d.TaskID, AdvanceTaskInput{
				TargetStatus: prevStatus,
				DecidedBy:    userID,
				Rationale:    input.Rationale,
			})
		}
	}

	slog.Info("editorial: decision resolved",
		"decision_id", decisionID, "status", input.Status, "by", userID)
	return d, nil
}

// ─── 统计 ─────────────────────────────────────────────────

// Stats 编辑部统计
type Stats struct {
	TotalTasks       int            `json:"total_tasks"`
	ByStatus         map[string]int `json:"by_status"`
	TotalArtifacts   int            `json:"total_artifacts"`
	ApprovalRate     float64        `json:"approval_rate"`     // 交付物通过率
	AvgReworkRounds  float64        `json:"avg_rework_rounds"` // 平均返工轮次
	TotalTokenUsed   int            `json:"total_token_used"`
	TotalTokenBudget int            `json:"total_token_budget"`
}

// GetStats 获取编辑部统计
func (s *Service) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{ByStatus: make(map[string]int)}

	// 任务按状态统计
	tasks, err := s.store.ListTasks(ctx, "", 1000, 0)
	if err != nil {
		return nil, fmt.Errorf("list tasks for stats: %w", err)
	}
	stats.TotalTasks = len(tasks)
	for _, t := range tasks {
		stats.ByStatus[string(t.Status)]++
		stats.TotalTokenUsed += t.TokenUsed
		stats.TotalTokenBudget += t.TokenBudget
	}

	return stats, nil
}

// nextStatusForDecision 根据决策类型返回批准后应该推进到的状态
func nextStatusForDecision(decType DecisionType, taskID string) TaskStatus {
	switch decType {
	case DecisionApproveTopic:
		return StatusResearch
	case DecisionPublish:
		return StatusPublished
	case DecisionAcceptReview:
		return StatusPendingPublish
	case DecisionEscalate:
		return StatusResearch // 升级被批准后重新研究
	default:
		return ""
	}
}

// prevStatusForDecision 根据决策类型返回驳回后应该退回的状态
func prevStatusForDecision(decType DecisionType) TaskStatus {
	switch decType {
	case DecisionApproveTopic:
		return StatusDraft
	case DecisionPublish:
		return StatusReview
	case DecisionAcceptReview:
		return StatusWriting
	case DecisionEscalate:
		return StatusPendingApproval
	default:
		return ""
	}
}

// ─── 组织记忆 ─────────────────────────────────────────────

// RecordSourceUsage 记录像源使用情况
func (s *Service) RecordSourceUsage(ctx context.Context, input RecordSourceInput) error {
	return s.store.RecordSourceUsage(ctx, input)
}

// GetSourceCredibility 获取信源可信度
func (s *Service) GetSourceCredibility(ctx context.Context, domain string) (*SourceCredibility, error) {
	return s.store.GetSourceCredibility(ctx, domain)
}

// ListSourceCredibility 列出信源可信度
func (s *Service) ListSourceCredibility(ctx context.Context, limit int) ([]SourceCredibility, error) {
	return s.store.ListSourceCredibility(ctx, limit)
}

// GetColumnPreference 获取栏目偏好
func (s *Service) GetColumnPreference(ctx context.Context, columnTag string) (*ColumnPreference, error) {
	return s.store.GetColumnPreference(ctx, columnTag)
}

// UpsertColumnPreference 创建或更新栏目偏好
func (s *Service) UpsertColumnPreference(ctx context.Context, cp ColumnPreference) (*ColumnPreference, error) {
	return s.store.UpsertColumnPreference(ctx, cp)
}

// ListColumnPreferences 列出所有栏目偏好
func (s *Service) ListColumnPreferences(ctx context.Context) ([]ColumnPreference, error) {
	return s.store.ListColumnPreferences(ctx)
}

// CreateKnowledge 创建编辑部知识
func (s *Service) CreateKnowledge(ctx context.Context, k EditorialKnowledge) (*EditorialKnowledge, error) {
	return s.store.CreateKnowledge(ctx, k)
}

// ListKnowledge 列出编辑部知识
func (s *Service) ListKnowledge(ctx context.Context, category, columnTag string, limit int) ([]EditorialKnowledge, error) {
	return s.store.ListKnowledge(ctx, category, columnTag, limit)
}

// ─── Agent 信誉 ───────────────────────────────────────────

// RecordAgentOutcome 记录 Agent 执行结果
func (s *Service) RecordAgentOutcome(ctx context.Context, input RecordAgentOutcomeInput) error {
	return s.store.RecordAgentOutcome(ctx, input)
}

// ListAgentReputation 列出所有 Agent 信誉
func (s *Service) ListAgentReputation(ctx context.Context) ([]AgentReputation, error) {
	return s.store.ListAgentReputation(ctx)
}
