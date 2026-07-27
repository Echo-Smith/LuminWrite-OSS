package editorial

import (
	"context"
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
