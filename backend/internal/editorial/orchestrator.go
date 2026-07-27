package editorial

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// ─── 编排事件 ─────────────────────────────────────────────

// OrchestratorEvent 编排器发出的事件
type OrchestratorEvent struct {
	Type      string      `json:"type"`       // task.status_changed | artifact.produced | artifact.reviewed | decision.required | agent.started | agent.completed | agent.failed
	TaskID    string      `json:"task_id"`
	Payload   interface{} `json:"payload"`
	Timestamp time.Time   `json:"timestamp"`
}

// EventEmitter 事件发射器接口
type EventEmitter interface {
	Emit(evt OrchestratorEvent)
}

// ─── 编排器 ───────────────────────────────────────────────

// Orchestrator 三 Agent 编排器 — 管理 研究→写作→审校 的协作流程
type Orchestrator struct {
	store    *Store
	emitter  EventEmitter
	executors map[AgentRole]AgentExecutorAdapter
}

// AgentExecutorAdapter 适配器 — 将 V2 的 Step/UnifiedAgent 适配为 AgentExecutor
type AgentExecutorAdapter interface {
	Role() AgentRole
	Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error)
}

// NewOrchestrator 创建编排器
func NewOrchestrator(store *Store, emitter EventEmitter) *Orchestrator {
	return &Orchestrator{
		store:     store,
		emitter:   emitter,
		executors: make(map[AgentRole]AgentExecutorAdapter),
	}
}

// RegisterExecutor 注册 Agent 执行器
func (o *Orchestrator) RegisterExecutor(exec AgentExecutorAdapter) {
	o.executors[exec.Role()] = exec
	slog.Info("orchestrator: executor registered", "role", exec.Role())
}

// AdvanceTask 推进任务到下一阶段
//
// 核心编排逻辑：
//   - pending_approval → research: 启动研究 Agent
//   - research → writing: 研究包就绪后启动写作 Agent
//   - writing → review: 初稿就绪后启动审校 Agent
//   - review → pending_publish / writing: 审校通过或驳回
//   - pending_publish → published: 人类编辑发布
func (o *Orchestrator) AdvanceTask(ctx context.Context, taskID string, input AdvanceTaskInput) error {
	task, err := o.store.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}

	if !task.Status.CanTransitionTo(input.TargetStatus) {
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, task.Status, input.TargetStatus)
	}

	// 发射状态变更事件
	o.emit(OrchestratorEvent{
		Type:    "task.status_changed",
		TaskID:  taskID,
		Payload: map[string]interface{}{"from": task.Status, "to": input.TargetStatus},
	})

	// 更新任务状态
	assignee := input.AssigneeType
	if assignee == "" {
		assignee = defaultAssignee(input.TargetStatus)
	}
	if err := o.store.UpdateTaskStatus(ctx, taskID, input.TargetStatus, assignee); err != nil {
		return fmt.Errorf("update task status: %w", err)
	}
	task.Status = input.TargetStatus
	task.AssigneeType = assignee

	// 根据目标状态触发对应 Agent
	switch input.TargetStatus {
	case StatusResearch:
		return o.runResearchAgent(ctx, task)
	case StatusWriting:
		return o.runWritingAgent(ctx, task)
	case StatusReview:
		return o.runReviewAgent(ctx, task)
	default:
		// pending_publish, published, archived 等终态不需要自动执行
		return nil
	}
}

// ─── 研究 Agent ──────────────────────────────────────────

func (o *Orchestrator) runResearchAgent(ctx context.Context, task *Task) error {
	exec, ok := o.executors[RoleResearch]
	if !ok {
		return fmt.Errorf("research agent executor not registered")
	}

	// 构建 Agent 上下文
	ac := NewAgentContext(RoleResearch, task.ID, task.OwnerID)

	// 加载输入交付物（选题卡）
	topicCard, err := o.store.GetLatestApprovedArtifact(ctx, task.ID, ArtifactTopicCard)
	if err == nil && topicCard != nil {
		ac.AddInputArtifact(*topicCard)
	}

	// 启动事件
	o.emit(OrchestratorEvent{
		Type:    "agent.started",
		TaskID:  task.ID,
		Payload: map[string]interface{}{"role": "research_agent"},
	})

	// 异步执行
	go func() {
		execCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		artifact, err := exec.Execute(execCtx, ac, task)
		if err != nil {
			slog.Error("research agent failed", "task_id", task.ID, "error", err)
			o.emit(OrchestratorEvent{
				Type:    "agent.failed",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "research_agent", "error": err.Error()},
			})
			return
		}

		// 存储产出交付物
		if artifact != nil {
			o.emit(OrchestratorEvent{
				Type:    "artifact.produced",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "research_agent", "artifact_type": artifact.Type},
			})

			// 研究完成后自动推进到写作阶段
			o.AdvanceTask(context.Background(), task.ID, AdvanceTaskInput{
				TargetStatus: StatusWriting,
				AssigneeType: AssigneeWritingAgent,
			})
		}

		o.emit(OrchestratorEvent{
			Type:    "agent.completed",
			TaskID:  task.ID,
			Payload: map[string]interface{}{"role": "research_agent"},
		})
	}()

	return nil
}

// ─── 写作 Agent ──────────────────────────────────────────

func (o *Orchestrator) runWritingAgent(ctx context.Context, task *Task) error {
	exec, ok := o.executors[RoleWriting]
	if !ok {
		return fmt.Errorf("writing agent executor not registered")
	}

	// 构建写作 Agent 上下文
	ac := NewAgentContext(RoleWriting, task.ID, task.OwnerID)

	// 加载已批准的研究交付物
	for _, t := range []ArtifactType{ArtifactResearchBrief, ArtifactFactClaims, ArtifactTopicCard} {
		art, err := o.store.GetLatestApprovedArtifact(ctx, task.ID, t)
		if err == nil && art != nil {
			ac.AddInputArtifact(*art)
		}
	}

	// 如果有审查报告（修改场景），也加载
	reviewReport, err := o.store.GetLatestApprovedArtifact(ctx, task.ID, ArtifactReviewReport)
	if err == nil && reviewReport != nil {
		ac.AddInputArtifact(*reviewReport)
	}

	o.emit(OrchestratorEvent{
		Type:    "agent.started",
		TaskID:  task.ID,
		Payload: map[string]interface{}{"role": "writing_agent"},
	})

	go func() {
		execCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		artifact, err := exec.Execute(execCtx, ac, task)
		if err != nil {
			slog.Error("writing agent failed", "task_id", task.ID, "error", err)
			o.emit(OrchestratorEvent{
				Type:    "agent.failed",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "writing_agent", "error": err.Error()},
			})
			return
		}

		if artifact != nil {
			o.emit(OrchestratorEvent{
				Type:    "artifact.produced",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "writing_agent", "artifact_type": artifact.Type},
			})

			// 写作完成后自动推进到审校阶段
			o.AdvanceTask(context.Background(), task.ID, AdvanceTaskInput{
				TargetStatus: StatusReview,
				AssigneeType: AssigneeReviewAgent,
			})
		}

		o.emit(OrchestratorEvent{
			Type:    "agent.completed",
			TaskID:  task.ID,
			Payload: map[string]interface{}{"role": "writing_agent"},
		})
	}()

	return nil
}

// ─── 审校 Agent ──────────────────────────────────────────

func (o *Orchestrator) runReviewAgent(ctx context.Context, task *Task) error {
	exec, ok := o.executors[RoleReview]
	if !ok {
		return fmt.Errorf("review agent executor not registered")
	}

	// 构建审校 Agent 上下文 — 上下文隔离，只看 Artifact
	ac := NewAgentContext(RoleReview, task.ID, task.OwnerID)

	// 审校 Agent 只看初稿和信源包，不看写作过程
	for _, t := range []ArtifactType{ArtifactDraft, ArtifactRevisedDraft, ArtifactSourcePack, ArtifactFactClaims, ArtifactResearchBrief} {
		art, err := o.store.GetLatestApprovedArtifact(ctx, task.ID, t)
		if err == nil && art != nil {
			ac.AddInputArtifact(*art)
		}
	}

	o.emit(OrchestratorEvent{
		Type:    "agent.started",
		TaskID:  task.ID,
		Payload: map[string]interface{}{"role": "review_agent"},
	})

	go func() {
		execCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		artifact, err := exec.Execute(execCtx, ac, task)
		if err != nil {
			slog.Error("review agent failed", "task_id", task.ID, "error", err)
			o.emit(OrchestratorEvent{
				Type:    "agent.failed",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "review_agent", "error": err.Error()},
			})
			return
		}

		if artifact != nil {
			o.emit(OrchestratorEvent{
				Type:    "artifact.produced",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "review_agent", "artifact_type": artifact.Type},
			})

			// 解析审查报告，决定下一步
			o.handleReviewResult(context.Background(), task, artifact)
		}

		o.emit(OrchestratorEvent{
			Type:    "agent.completed",
			TaskID:  task.ID,
			Payload: map[string]interface{}{"role": "review_agent"},
		})
	}()

	return nil
}

// handleReviewResult 根据审查结果决定推进到发布还是退回写作
func (o *Orchestrator) handleReviewResult(ctx context.Context, task *Task, reviewArtifact *Artifact) {
	// 解析审查报告内容
	var report struct {
		Passed   bool `json:"passed"`
		Severity string `json:"severity"` // low | medium | high
	}
	if err := json.Unmarshal([]byte(reviewArtifact.Content), &report); err != nil {
		slog.Warn("failed to parse review report", "error", err)
		// 解析失败，默认退回写作
		o.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
			TargetStatus: StatusWriting,
			AssigneeType: AssigneeWritingAgent,
		})
		return
	}

	if report.Passed {
		// 审查通过 → 推进到待发布
		o.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
			TargetStatus: StatusPendingPublish,
			AssigneeType: AssigneeHuman,
		})
		// 发射需要人类决策事件
		o.emit(OrchestratorEvent{
			Type:    "decision.required",
			TaskID:  task.ID,
			Payload: map[string]interface{}{"type": "publish", "message": "稿件审查通过，等待发布确认"},
		})
	} else if report.Severity == "high" {
		// 严重问题 → 升级到人类
		o.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
			TargetStatus: StatusPendingApproval,
			AssigneeType: AssigneeHuman,
		})
		o.emit(OrchestratorEvent{
			Type:    "decision.required",
			TaskID:  task.ID,
			Payload: map[string]interface{}{"type": "escalate", "message": "审查发现严重问题，需人工裁决"},
		})
	} else {
		// 一般问题 → 退回写作 Agent 修改
		o.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
			TargetStatus: StatusWriting,
			AssigneeType: AssigneeWritingAgent,
			Rationale:    "审查发现可修正问题，退回写作 Agent 修改",
		})
	}
}

// ─── 辅助 ─────────────────────────────────────────────────

func (o *Orchestrator) emit(evt OrchestratorEvent) {
	if o.emitter != nil {
		evt.Timestamp = time.Now()
		o.emitter.Emit(evt)
	}
}

func defaultAssignee(status TaskStatus) AssigneeType {
	switch status {
	case StatusResearch:
		return AssigneeResearchAgent
	case StatusWriting:
		return AssigneeWritingAgent
	case StatusReview:
		return AssigneeReviewAgent
	case StatusPendingApproval, StatusPendingPublish, StatusPublished:
		return AssigneeHuman
	default:
		return AssigneeHuman
	}
}
