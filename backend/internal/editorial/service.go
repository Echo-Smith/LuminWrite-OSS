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
	topicCard, err := s.store.CreateArtifact(ctx, SubmitArtifactInput{
		Type:       ArtifactTopicCard,
		Content:    string(topicCardContent),
		ProducedBy: "human",
		TokenCost:  0,
	}, task.ID)
	if err != nil {
		slog.Warn("editorial: failed to create topic card artifact", "task_id", task.ID, "error", err)
	} else {
		// 自动批准选题卡（人类创建的选题卡默认可信，研究 Agent 只加载 approved 状态的交付物）
		if _, err := s.store.ReviewArtifact(ctx, topicCard.ID, ReviewArtifactInput{
			Status:     ArtifactStatusApproved,
			ReviewerID: "system",
			ReviewNote: "选题卡自动批准",
		}); err != nil {
			slog.Warn("editorial: failed to auto-approve topic card", "task_id", task.ID, "error", err)
		}
		slog.Info("editorial: topic card artifact created and auto-approved", "task_id", task.ID, "artifact_id", topicCard.ID)
	}

	return task, nil
}

// GetTask 获取任务
func (s *Service) GetTask(ctx context.Context, id string) (*Task, error) {
	return s.store.GetTask(ctx, id)
}

// GetTaskForUser 获取任务并进行所有权检查
func (s *Service) GetTaskForUser(ctx context.Context, id string, userID string, isAdmin bool) (*Task, error) {
	task, err := s.store.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	// Admin can access any task; non-admin can only access their own
	if !isAdmin && task.OwnerID != userID {
		return nil, ErrForbidden
	}
	return task, nil
}

// ListTasks 列出任务（支持用户隔离）
func (s *Service) ListTasks(ctx context.Context, status string, ownerID string, limit, offset int) ([]Task, error) {
	return s.store.ListTasks(ctx, status, ownerID, limit, offset)
}

// AdvanceTask 推进任务
func (s *Service) AdvanceTask(ctx context.Context, taskID string, input AdvanceTaskInput) error {
	return s.orchestrator.AdvanceTask(ctx, taskID, input)
}

// TransitionTask 事务化状态转换（单一入口）
// 使用 SELECT FOR UPDATE 锁定任务行，校验状态，更新状态，并在需要时创建 Agent lease。
// Agent 执行在事务提交后异步触发。
func (s *Service) TransitionTask(ctx context.Context, cmd TransitionCommand) (*Task, error) {
	return s.store.TransitionTask(ctx, cmd)
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

// ListPendingDecisions 列出所有待处理决策（跨任务，支持用户隔离）
func (s *Service) ListPendingDecisions(ctx context.Context, ownerID string, limit int) ([]DecisionWithTask, error) {
	return s.store.ListPendingDecisions(ctx, ownerID, limit)
}

// ResolveDecision 人类处理待决策 — 原子化更新 Decision 状态 + 推进任务状态
// 使用 approve_target_status / reject_target_status 直接从 Decision 读取目标状态，
// 不再依赖全局 switch 猜测去向。
// Agent 执行在事务提交后异步触发。
func (s *Service) ResolveDecision(ctx context.Context, decisionID string, input ResolveDecisionInput, userID string) (*Decision, error) {
	resolved, nextStatus, err := s.store.ResolveDecisionTx(ctx, ResolveDecisionTxParams{
		DecisionID: decisionID,
		Status:     input.Status,
		Rationale:  input.Rationale,
		DecidedBy:  userID,
	})
	if err != nil {
		return nil, err
	}

	// 事务提交后，根据新的任务状态异步触发 Agent
	if nextStatus != "" {
		task, err := s.store.GetTask(ctx, resolved.TaskID)
		if err == nil {
			task.Status = nextStatus
			task.AssigneeType = defaultAssignee(nextStatus)
			switch nextStatus {
			case StatusResearch:
				s.orchestrator.RunResearchAgent(ctx, task)
			case StatusWriting:
				s.orchestrator.RunWritingAgent(ctx, task)
			case StatusReview:
				s.orchestrator.RunReviewAgent(ctx, task)
			}
		}
	}

	slog.Info("editorial: decision resolved",
		"decision_id", decisionID, "status", input.Status, "by", userID, "next_status", nextStatus)
	return resolved, nil
}

// ─── Decision Packet (第二步: 决策包组装) ──────────────────

// BuildDecisionPacket 为人类决策者组装完整的决策上下文包。
// 包含任务摘要、决策选项、证据材料和质量指标。
func (s *Service) BuildDecisionPacket(ctx context.Context, decisionID string) (*DecisionPacket, error) {
	d, err := s.store.GetDecision(ctx, decisionID)
	if err != nil {
		return nil, fmt.Errorf("get decision: %w", err)
	}

	task, err := s.store.GetTask(ctx, d.TaskID)
	if err != nil {
		return nil, fmt.Errorf("get task for packet: %w", err)
	}

	// 获取相关 Artifacts 作为证据
	artifacts, _ := s.store.ListArtifacts(ctx, d.TaskID)

	// 构建 Artifact 摘要列表（取最新的几个）
	var evidence []ArtifactSummary
	for i := len(artifacts) - 1; i >= 0 && len(evidence) < 5; i-- {
		art := artifacts[i]
		snippet := art.Content
		if len(snippet) > 500 {
			snippet = snippet[:500] + "..."
		}
		evidence = append(evidence, ArtifactSummary{
			ID:         art.ID,
			Type:       art.Type,
			Status:     art.Status,
			ProducedBy: art.ProducedBy,
			Snippet:    snippet,
			Version:    art.Version,
			CreatedAt:  art.CreatedAt,
		})
	}

	// 构建决策选项
	options := s.buildDecisionOptions(d)

	// 从 Artifacts 提取质量指标
	metrics := s.extractMetrics(artifacts)

	// 获取关联的 Agent 事件
	events, _ := s.store.ListAgentRunEvents(ctx, d.TaskID)
	var causeEventID string
	if len(events) > 0 {
		causeEventID = events[len(events)-1].ID
	}

	return &DecisionPacket{
		DecisionID:    d.ID,
		TaskID:        d.TaskID,
		Type:          d.Type,
		Status:        d.Status,
		TaskSummary: TaskSummary{
			Title:         task.Title,
			Description:   task.Description,
			CurrentStatus: task.Status,
			Priority:      task.Priority,
			StyleSlug:     task.StyleSlug,
			TokenUsed:     task.TokenUsed,
			TokenBudget:   task.TokenBudget,
			Tags:          task.Tags,
		},
		Options:       options,
		Evidence:      evidence,
		Metrics:       metrics,
		TriggerReason: d.Rationale,
		CauseEventID:  causeEventID,
		CreatedAt:     d.CreatedAt,
	}, nil
}

// buildDecisionOptions 根据决策类型构建选项
func (s *Service) buildDecisionOptions(d *Decision) []DecisionOption {
	approveStatus := TaskStatus(d.ApproveTargetStatus)
	rejectStatus := TaskStatus(d.RejectTargetStatus)

	return []DecisionOption{
		{
			ID:           "approve",
			Label:        "批准",
			Description:  decisionApproveDescription(d.Type),
			TargetStatus: approveStatus,
		},
		{
			ID:           "reject",
			Label:        "驳回",
			Description:  decisionRejectDescription(d.Type),
			TargetStatus: rejectStatus,
		},
	}
}

// extractMetrics 从 Artifacts 中提取质量指标
func (s *Service) extractMetrics(artifacts []Artifact) *DecisionMetrics {
	var metrics DecisionMetrics
	found := false

	for _, art := range artifacts {
		switch art.Type {
		case ArtifactResearchBrief, ArtifactFactClaims:
			var brief struct {
				Sources []struct{} `json:"sources"`
				Claims  []struct {
					Status   ClaimStatus `json:"status"`
					Verified bool        `json:"verified,omitempty"`
				} `json:"claims"`
				Gaps []struct{} `json:"gaps"`
			}
			if json.Unmarshal([]byte(art.Content), &brief) == nil {
				metrics.SourceCount = max(metrics.SourceCount, len(brief.Sources))
				metrics.GapCount = max(metrics.GapCount, len(brief.Gaps))
				for _, c := range brief.Claims {
					if c.Verified {
						metrics.VerifiedClaims++
					}
					switch c.Status {
					case ClaimSupported:
						metrics.SupportedClaims++
					case ClaimVerified:
						metrics.VerifiedClaims++
					case ClaimConflicted:
						metrics.ConflictedClaims++
					}
				}
				found = true
			}
		case ArtifactDraft, ArtifactRevisedDraft:
			var draft struct {
				WordCount int `json:"word_count"`
				Outline   []struct {
					Section string `json:"section"`
				} `json:"outline"`
			}
			if json.Unmarshal([]byte(art.Content), &draft) == nil {
				metrics.WordCount = max(metrics.WordCount, draft.WordCount)
				metrics.SectionCount = max(metrics.SectionCount, len(draft.Outline))
				found = true
			}
		case ArtifactReviewReport:
			var report struct {
				Severity string `json:"severity"`
			}
			if json.Unmarshal([]byte(art.Content), &report) == nil {
				metrics.Severity = report.Severity
				found = true
			}
		}
	}

	if !found {
		return nil
	}
	return &metrics
}

func decisionApproveDescription(decType DecisionType) string {
	switch decType {
	case DecisionApproveTopic:
		return "批准立项，进入研究阶段"
	case DecisionSelectAngle:
		return "确认角度，进入写作阶段"
	case DecisionAllowRewrite:
		return "允许进入审校阶段"
	case DecisionPublish:
		return "批准发布"
	case DecisionAcceptReview:
		return "接受审校结果"
	case DecisionEscalate:
		return "确认无需升级，继续执行"
	default:
		return "批准"
	}
}

func decisionRejectDescription(decType DecisionType) string {
	switch decType {
	case DecisionApproveTopic:
		return "驳回立项，退回草稿"
	case DecisionSelectAngle:
		return "驳回，退回研究"
	case DecisionAllowRewrite:
		return "驳回，退回写作修改"
	case DecisionPublish:
		return "驳回发布，退回审校"
	case DecisionAcceptReview:
		return "驳回，退回写作修改"
	case DecisionEscalate:
		return "升级到人工裁决"
	default:
		return "驳回"
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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
	tasks, err := s.store.ListTasks(ctx, "", "", 1000, 0)
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

// nextStatusForDecision returns the approve target status for a decision type.
// Used when creating pending decisions to pre-compute the target status.
func nextStatusForDecision(decType DecisionType) TaskStatus {
	switch decType {
	case DecisionApproveTopic:
		return StatusResearch
	case DecisionSelectAngle:
		return StatusWriting
	case DecisionAllowRewrite:
		return StatusReview
	case DecisionPublish:
		return StatusPublished
	case DecisionAcceptReview:
		return StatusPendingPublish
	case DecisionEscalate:
		return StatusResearch
	default:
		return ""
	}
}

// prevStatusForDecision returns the reject target status for a decision type.
func prevStatusForDecision(decType DecisionType) TaskStatus {
	switch decType {
	case DecisionApproveTopic:
		return StatusDraft
	case DecisionSelectAngle:
		return StatusResearch
	case DecisionAllowRewrite:
		return StatusWriting
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

// ─── 对照实验 ─────────────────────────────────────────────

// experimentRunner 实验运行器（延迟初始化）
var experimentRunner *ExperimentRunner

// SetExperimentRunner 注入实验运行器（由 server 初始化时调用）
func SetExperimentRunner(r *ExperimentRunner) {
	experimentRunner = r
}

// CreateExperiment 创建对照实验
func (s *Service) CreateExperiment(ctx context.Context, input CreateExperimentInput, userID string) (*Experiment, error) {
	return s.store.CreateExperiment(ctx, input, userID)
}

// GetExperiment 获取实验详情
func (s *Service) GetExperiment(ctx context.Context, id string) (*Experiment, error) {
	return s.store.GetExperiment(ctx, id)
}

// ListExperiments 列出实验
func (s *Service) ListExperiments(ctx context.Context, limit int) ([]Experiment, error) {
	return s.store.ListExperiments(ctx, limit)
}

// RunExperiment 启动实验（异步执行三组对比）
func (s *Service) RunExperiment(ctx context.Context, id string) error {
	if experimentRunner == nil {
		return fmt.Errorf("experiment runner not initialized")
	}
	exp, err := s.store.GetExperiment(ctx, id)
	if err != nil {
		return fmt.Errorf("get experiment: %w", err)
	}
	// 异步执行
	go experimentRunner.RunExperiment(context.Background(), exp)
	return nil
}

// CancelExperiment 取消正在运行的实验
func (s *Service) CancelExperiment(id string) bool {
	if experimentRunner == nil {
		return false
	}
	return experimentRunner.CancelExperiment(id)
}
