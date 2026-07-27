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
	Type      string      `json:"type"`       // task.status_changed | artifact.produced | artifact.reviewed | decision.required | decision.created | agent.started | agent.completed | agent.failed
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
//
// 三层模型：
//   - Event: Agent 完成工作（客观事实，不涉及选择）
//   - Decision: 需要人类/系统做选择时创建（有 approve/reject 两个方向）
//   - Transition: 状态变化，引用触发它的 Event 或 Decision
//
// Agent 执行流程：
//  1. AcquireLease（数据库级互斥）
//  2. 执行 Agent
//  3. RecordAgentRunEvent（记录客观事件）
//  4. ReleaseLease
//  5. 根据产出质量路由：自动推进用 TransitionTask(event)，需人类审批用 requestHumanDecision
type Orchestrator struct {
	store     *Store
	emitter   EventEmitter
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

// RunResearchAgent 启动研究 Agent（导出方法，供 Service 在事务提交后调用）
func (o *Orchestrator) RunResearchAgent(ctx context.Context, task *Task) error {
	return o.runResearchAgent(ctx, task)
}

// RunWritingAgent 启动写作 Agent（导出方法，供 Service 在事务提交后调用）
func (o *Orchestrator) RunWritingAgent(ctx context.Context, task *Task) error {
	return o.runWritingAgent(ctx, task)
}

// RunReviewAgent 启动审校 Agent（导出方法，供 Service 在事务提交后调用）
func (o *Orchestrator) RunReviewAgent(ctx context.Context, task *Task) error {
	return o.runReviewAgent(ctx, task)
}

// ─── 状态转换 ↔ Decision 映射 ────────────────────────────

// transitionDecision 返回状态转换对应的 DecisionType 和默认 decidedByType
func transitionDecision(from, to TaskStatus) (DecisionType, DecidedByType) {
	switch {
	// draft → pending_approval: 提交审批（人类操作，前向推进）
	case from == StatusDraft && to == StatusPendingApproval:
		return DecisionApproveTopic, DecidedByHuman
	// pending_approval → research: 批准立项（人类决策）
	case from == StatusPendingApproval && to == StatusResearch:
		return DecisionApproveTopic, DecidedByHuman
	// pending_approval → draft: 驳回立项（人类决策）
	case from == StatusPendingApproval && to == StatusDraft:
		return DecisionApproveTopic, DecidedByHuman
	// research → writing: 研究完成，自动推进（系统决策）
	case from == StatusResearch && to == StatusWriting:
		return DecisionResearchComplete, DecidedBySystem
	// writing → review: 写作完成，自动推进（系统决策）
	case from == StatusWriting && to == StatusReview:
		return DecisionDraftComplete, DecidedBySystem
	// review → pending_publish: 审校通过（审校 Agent 决策）
	case from == StatusReview && to == StatusPendingPublish:
		return DecisionAcceptReview, DecidedByReviewAgent
	// review → writing: 审校驳回，退回修改（审校 Agent 决策）
	case from == StatusReview && to == StatusWriting:
		return DecisionAcceptReview, DecidedByReviewAgent
	// review → pending_approval: 严重问题升级（审校 Agent 决策）
	case from == StatusReview && to == StatusPendingApproval:
		return DecisionEscalate, DecidedByReviewAgent
	// pending_publish → published: 发布（人类决策）
	case from == StatusPendingPublish && to == StatusPublished:
		return DecisionPublish, DecidedByHuman
	// pending_publish → review: 驳回发布（人类决策）
	case from == StatusPendingPublish && to == StatusReview:
		return DecisionPublish, DecidedByHuman
	default:
		return DecisionEscalate, DecidedBySystem
	}
}

// decisionStatusForTransition 根据转换方向判断 Decision 是 approved 还是 rejected
func decisionStatusForTransition(from, to TaskStatus) DecisionStatus {
	switch {
	case to == StatusDraft:
		return DecisionStatusRejected
	case from == StatusReview && to == StatusWriting:
		return DecisionStatusRejected
	case from == StatusPendingPublish && to == StatusReview:
		return DecisionStatusRejected
	default:
		return DecisionStatusApproved
	}
}

// AdvanceTask 推进任务到下一阶段 — 用于人类决策驱动的状态转换
//
// 核心编排逻辑：
//   - pending_approval → research: 启动研究 Agent
//   - research → writing: 研究包就绪后启动写作 Agent
//   - writing → review: 初稿就绪后启动审校 Agent
//   - review → pending_publish / writing: 审校通过或驳回
//   - pending_publish → published: 人类编辑发布
//
// 对于 Agent 完成后的自动推进，使用 transitionAfterEvent 而非此方法。
func (o *Orchestrator) AdvanceTask(ctx context.Context, taskID string, input AdvanceTaskInput) error {
	task, err := o.store.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}

	if !task.Status.CanTransitionTo(input.TargetStatus) {
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, task.Status, input.TargetStatus)
	}

	// ── 原子化：先创建 Decision，再更新状态 ──
	decType, decByType := transitionDecision(task.Status, input.TargetStatus)
	decStatus := decisionStatusForTransition(task.Status, input.TargetStatus)

	// 构建 Actor
	actor := Actor{Type: ActorSystem, Label: "system"}
	if decByType == DecidedByHuman {
		actor = NewHumanActor(input.DecidedBy, input.DecidedBy)
	} else if decByType == DecidedByResearchAgent || decByType == DecidedByWritingAgent || decByType == DecidedByReviewAgent {
		actor = Actor{Type: ActorAgent, Role: string(decByType), Label: string(decByType)}
	}

	// 计算 approve/reject target status 并保存在 Decision 上
	approveTarget := input.TargetStatus
	rejectTarget := task.Status
	if decStatus == DecisionStatusRejected {
		approveTarget = task.Status
		rejectTarget = input.TargetStatus
	}

	decision, err := o.store.CreateDecision(ctx, CreateDecisionInput{
		Type:                decType,
		Actor:               actor,
		DecidedByType:       decByType,
		DecidedBy:           input.DecidedBy,
		Status:              decStatus,
		Rationale:           input.Rationale,
		ApproveTargetStatus: approveTarget,
		RejectTargetStatus:  rejectTarget,
	}, taskID)
	if err != nil {
		return fmt.Errorf("create decision: %w", err)
	}

	// 发射决策创建事件
	o.emit(OrchestratorEvent{
		Type:    "decision.created",
		TaskID:  taskID,
		Payload: map[string]interface{}{
			"decision_id": decision.ID,
			"type":        decType,
			"status":      decStatus,
			"by":          decByType,
		},
	})

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
		if input.TargetStatus == StatusPendingPublish || input.TargetStatus == StatusPendingApproval {
			o.emit(OrchestratorEvent{
				Type:    "decision.required",
				TaskID:  taskID,
				Payload: map[string]interface{}{
					"type":    decType,
					"message": humanActionMessage(input.TargetStatus),
				},
			})
		}
		return nil
	}
}

// transitionAfterEvent 在 Agent 完成后用 Event 驱动状态转换
// 不创建 Decision — 因为 Agent 完成工作是客观事件，不涉及选择
func (o *Orchestrator) transitionAfterEvent(ctx context.Context, task *Task, eventID string, targetStatus TaskStatus, rationale string) error {
	if !task.Status.CanTransitionTo(targetStatus) {
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, task.Status, targetStatus)
	}

	// 使用 TransitionTask — 以 Event 为 cause
	updatedTask, err := o.store.TransitionTask(ctx, TransitionCommand{
		TaskID:         task.ID,
		TargetStatus:   targetStatus,
		ExpectedStatus: task.Status,
		Cause:          TransitionCauseEvent,
		CauseEventID:   eventID,
		Actor:          NewSystemActor("system"),
		Rationale:      rationale,
	})
	if err != nil {
		return fmt.Errorf("transition after event: %w", err)
	}

	o.emit(OrchestratorEvent{
		Type:    "task.status_changed",
		TaskID:  task.ID,
		Payload: map[string]interface{}{"from": task.Status, "to": targetStatus, "cause": "event"},
	})

	*task = *updatedTask

	// 根据目标状态触发对应 Agent
	switch targetStatus {
	case StatusWriting:
		return o.runWritingAgent(ctx, task)
	case StatusReview:
		return o.runReviewAgent(ctx, task)
	default:
		return nil
	}
}

func humanActionMessage(status TaskStatus) string {
	switch status {
	case StatusPendingPublish:
		return "稿件审查通过，等待发布确认"
	case StatusPendingApproval:
		return "审校发现严重问题，需人工裁决"
	default:
		return "需要人工介入"
	}
}

// ─── 研究 Agent ──────────────────────────────────────────

func (o *Orchestrator) runResearchAgent(ctx context.Context, task *Task) error {
	exec, ok := o.executors[RoleResearch]
	if !ok {
		return fmt.Errorf("research agent executor not registered")
	}

	// ── G1: AcquireLease — 数据库级互斥 ──
	if err := o.store.AcquireLease(ctx, task.ID, RoleResearch, 10*time.Minute); err != nil {
		return fmt.Errorf("acquire research lease: %w", err)
	}

	// ── 预算检查 ──
	if task.TokenBudget > 0 {
		budgetUsed := float64(task.TokenUsed) / float64(task.TokenBudget)
		if budgetUsed >= 0.95 {
			o.store.ReleaseLease(ctx, task.ID, RoleResearch, "budget_exceeded")
			o.requestHumanDecision(ctx, task, DecisionEscalate,
				fmt.Sprintf("Token 预算已用 %.0f%%，研究 Agent 无法执行，需人工介入", budgetUsed*100))
			return nil
		}
		if budgetUsed >= 0.80 {
			o.emit(OrchestratorEvent{
				Type:    "decision.required",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"type": "budget_warning", "message": fmt.Sprintf("Token 预算已用 %.0f%%，研究 Agent 将以降级模式运行", budgetUsed*100)},
			})
		}
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

	prevStatus := StatusPendingApproval

	// 异步执行
	go func() {
		execCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		// G1: 确保 lease 释放
		defer func() {
			if err := o.store.ReleaseLease(context.Background(), task.ID, RoleResearch, "completed"); err != nil {
				slog.Warn("failed to release research lease", "task_id", task.ID, "error", err)
			}
		}()

		start := time.Now()
		artifact, err := exec.Execute(execCtx, ac, task)
		durationMs := time.Since(start).Milliseconds()

		// 记录 Agent 信誉
		o.recordAgentOutcome(task.ID, RoleResearch, err == nil, artifact, durationMs)

		if err != nil {
			slog.Error("research agent failed", "task_id", task.ID, "error", err)

			// G2: 记录 Event（不是 Decision）
			o.recordEvent(context.Background(), task.ID, EventAgentRunFailed, RoleResearch, "failed", "", err.Error())

			o.emit(OrchestratorEvent{
				Type:    "agent.failed",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "research_agent", "error": err.Error()},
			})
			o.rollbackTask(task.ID, StatusResearch, prevStatus, "research agent failed: "+err.Error())
			return
		}

		// G2: 记录 Event — Agent 完成是客观事件
		var artifactID string
		if artifact != nil {
			artifactID = artifact.ID
		}
		event := o.recordEvent(context.Background(), task.ID, EventAgentRunCompleted, RoleResearch, "completed", artifactID, "")

		if artifact != nil {
			o.emit(OrchestratorEvent{
				Type:    "artifact.produced",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "research_agent", "artifact_type": artifact.Type},
			})

			// ── 动态路由：评估研究质量决定下一步 ──
			o.routeAfterResearch(context.Background(), task, artifact, event)
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

	// ── G1: AcquireLease ──
	if err := o.store.AcquireLease(ctx, task.ID, RoleWriting, 10*time.Minute); err != nil {
		return fmt.Errorf("acquire writing lease: %w", err)
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

	prevStatus := StatusResearch

	// ── 预算检查 ──
	if task.TokenBudget > 0 {
		budgetUsed := float64(task.TokenUsed) / float64(task.TokenBudget)
		if budgetUsed >= 0.95 {
			o.store.ReleaseLease(ctx, task.ID, RoleWriting, "budget_exceeded")
			o.requestHumanDecision(ctx, task, DecisionEscalate,
				fmt.Sprintf("Token 预算已用 %.0f%%，写作 Agent 无法执行，需人工介入", budgetUsed*100))
			return nil
		}
		if budgetUsed >= 0.80 {
			o.emit(OrchestratorEvent{
				Type:    "decision.required",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"type": "budget_warning", "message": fmt.Sprintf("Token 预算已用 %.0f%%，写作 Agent 将以降级模式运行", budgetUsed*100)},
			})
		}
	}
	if reviewReport != nil {
		prevStatus = StatusReview
	}

	o.emit(OrchestratorEvent{
		Type:    "agent.started",
		TaskID:  task.ID,
		Payload: map[string]interface{}{"role": "writing_agent"},
	})

	go func() {
		execCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		// G1: 确保 lease 释放
		defer func() {
			if err := o.store.ReleaseLease(context.Background(), task.ID, RoleWriting, "completed"); err != nil {
				slog.Warn("failed to release writing lease", "task_id", task.ID, "error", err)
			}
		}()

		start := time.Now()
		artifact, err := exec.Execute(execCtx, ac, task)
		durationMs := time.Since(start).Milliseconds()

		// 记录 Agent 信誉
		o.recordAgentOutcome(task.ID, RoleWriting, err == nil, artifact, durationMs)

		if err != nil {
			slog.Error("writing agent failed", "task_id", task.ID, "error", err)

			// G2: 记录 Event
			o.recordEvent(context.Background(), task.ID, EventAgentRunFailed, RoleWriting, "failed", "", err.Error())

			o.emit(OrchestratorEvent{
				Type:    "agent.failed",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "writing_agent", "error": err.Error()},
			})
			o.rollbackTask(task.ID, StatusWriting, prevStatus, "writing agent failed: "+err.Error())
			return
		}

		// G2: 记录 Event
		var artifactID string
		if artifact != nil {
			artifactID = artifact.ID
		}
		event := o.recordEvent(context.Background(), task.ID, EventAgentRunCompleted, RoleWriting, "completed", artifactID, "")

		if artifact != nil {
			o.emit(OrchestratorEvent{
				Type:    "artifact.produced",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "writing_agent", "artifact_type": artifact.Type},
			})

			// ── 动态路由 ──
			o.routeAfterWriting(context.Background(), task, artifact, event)
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

	// ── G1: AcquireLease ──
	if err := o.store.AcquireLease(ctx, task.ID, RoleReview, 10*time.Minute); err != nil {
		return fmt.Errorf("acquire review lease: %w", err)
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

		// G1: 确保 lease 释放
		defer func() {
			if err := o.store.ReleaseLease(context.Background(), task.ID, RoleReview, "completed"); err != nil {
				slog.Warn("failed to release review lease", "task_id", task.ID, "error", err)
			}
		}()

		start := time.Now()
		artifact, err := exec.Execute(execCtx, ac, task)
		durationMs := time.Since(start).Milliseconds()

		// 记录 Agent 信誉
		o.recordAgentOutcome(task.ID, RoleReview, err == nil, artifact, durationMs)

		if err != nil {
			slog.Error("review agent failed", "task_id", task.ID, "error", err)

			// G2: 记录 Event
			o.recordEvent(context.Background(), task.ID, EventAgentRunFailed, RoleReview, "failed", "", err.Error())

			o.emit(OrchestratorEvent{
				Type:    "agent.failed",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "review_agent", "error": err.Error()},
			})
			o.rollbackTask(task.ID, StatusReview, StatusWriting, "review agent failed: "+err.Error())
			return
		}

		// G2: 记录 Event
		var artifactID string
		if artifact != nil {
			artifactID = artifact.ID
		}
		event := o.recordEvent(context.Background(), task.ID, EventAgentRunCompleted, RoleReview, "completed", artifactID, "")

		if artifact != nil {
			o.emit(OrchestratorEvent{
				Type:    "artifact.produced",
				TaskID:  task.ID,
				Payload: map[string]interface{}{"role": "review_agent", "artifact_type": artifact.Type},
			})

			// 解析审查报告，决定下一步
			o.handleReviewResult(context.Background(), task, artifact, event)
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
func (o *Orchestrator) handleReviewResult(ctx context.Context, task *Task, reviewArtifact *Artifact, event *AgentRunEvent) {
	// 解析审查报告内容
	var report struct {
		Passed   bool   `json:"passed"`
		Severity string `json:"severity"` // low | medium | high
	}
	if err := json.Unmarshal([]byte(reviewArtifact.Content), &report); err != nil {
		slog.Warn("failed to parse review report", "error", err)
		// 解析失败，退回写作 — 使用 Decision 因为这是一个判断
		o.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
			TargetStatus: StatusWriting,
			AssigneeType: AssigneeWritingAgent,
			DecidedBy:    "review_agent",
			Rationale:    "审查报告解析失败，退回写作 Agent",
		})
		return
	}

	if report.Passed {
		// 审查通过 → 用 Event 驱动推进到待发布
		o.transitionAfterEvent(ctx, task, event.ID, StatusPendingPublish,
			"审校通过，等待人类发布")
	} else if report.Severity == "high" {
		// 严重问题 → 升级到人类（需要 Decision）
		o.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
			TargetStatus: StatusPendingApproval,
			AssigneeType: AssigneeHuman,
			DecidedBy:    "review_agent",
			Rationale:    "审查发现严重问题（severity=high），升级人工裁决",
		})
	} else {
		// 一般问题 → 退回写作 Agent 修改（需要 Decision — 这是一个驳回判断）
		o.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
			TargetStatus: StatusWriting,
			AssigneeType: AssigneeWritingAgent,
			DecidedBy:    "review_agent",
			Rationale:    "审查发现可修正问题（severity=" + report.Severity + "），退回写作 Agent 修改",
		})
	}
}

// ─── 动态路由 ─────────────────────────────────────────────

// routeAfterResearch 研究完成后评估质量，决定自动推进还是等待人类审批
func (o *Orchestrator) routeAfterResearch(ctx context.Context, task *Task, artifact *Artifact, event *AgentRunEvent) {
	var brief struct {
		Summary string `json:"summary"`
		Sources []struct {
			URL       string `json:"url"`
			Source    string `json:"source"`
			Relevance string `json:"relevance"`
		} `json:"sources"`
		Claims []struct {
			Claim    string      `json:"claim"`
			Status   ClaimStatus `json:"status"`
			Verified bool        `json:"verified,omitempty"`
		} `json:"claims"`
		Gaps []string `json:"gaps"`
	}
	if err := json.Unmarshal([]byte(artifact.Content), &brief); err != nil {
		slog.Warn("failed to parse research brief for routing", "error", err)
		o.requestHumanDecision(ctx, task, DecisionApproveTopic,
			"研究简报格式异常，需人工确认后再进入写作")
		return
	}

	sourceCount := len(brief.Sources)
	supportedClaims := 0
	verifiedClaims := 0
	for _, c := range brief.Claims {
		if c.Verified {
			verifiedClaims++
		}
		switch c.Status {
		case ClaimSupported:
			supportedClaims++
		case ClaimVerified:
			verifiedClaims++
		}
	}
	gapCount := len(brief.Gaps)

	slog.Info("research quality评估",
		"task_id", task.ID,
		"sources", sourceCount,
		"supported_claims", supportedClaims,
		"verified_claims", verifiedClaims,
		"gaps", gapCount)

	switch {
	case sourceCount >= 3 && gapCount == 0:
		// 质量充分 → 用 Event 驱动自动推进到写作
		if err := o.transitionAfterEvent(ctx, task, event.ID, StatusWriting,
			fmt.Sprintf("研究质量充分（%d 信源, %d supported, %d verified），自动推进到写作", sourceCount, supportedClaims, verifiedClaims)); err != nil {
			slog.Error("failed to transition after research event", "task_id", task.ID, "error", err)
		}

	case sourceCount >= 2 && gapCount <= 1:
		// 质量尚可但有小缺口 → 创建 pending decision，人类可快速批准
		o.requestHumanDecision(ctx, task, DecisionSelectAngle,
			fmt.Sprintf("研究有 %d 信源和 %d 个信息缺口，建议人工确认后再进入写作", sourceCount, gapCount))

	default:
		// 质量不足 → 直接重跑研究 Agent
		slog.Warn("research quality insufficient, retrying",
			"task_id", task.ID, "sources", sourceCount, "gaps", gapCount)
		o.emit(OrchestratorEvent{
			Type:    "decision.created",
			TaskID:  task.ID,
			Payload: map[string]interface{}{"type": "allow_rewrite", "status": "rejected", "by": "system", "reason": "research quality insufficient"},
		})
		o.runResearchAgent(ctx, task)
	}
}

// routeAfterWriting 写作完成后评估质量，决定自动推进到审校还是等待人类确认
func (o *Orchestrator) routeAfterWriting(ctx context.Context, task *Task, artifact *Artifact, event *AgentRunEvent) {
	var draft struct {
		Title     string `json:"title"`
		Content   string `json:"content"`
		WordCount int    `json:"word_count"`
		Outline   []struct {
			Section string `json:"section"`
		} `json:"outline"`
	}
	if err := json.Unmarshal([]byte(artifact.Content), &draft); err != nil {
		slog.Warn("failed to parse draft for routing", "error", err)
		// 解析失败，仍然推进到审校 — 用 Event 驱动
		o.transitionAfterEvent(ctx, task, event.ID, StatusReview,
			"初稿格式异常，推进到审校检查")
		return
	}

	slog.Info("draft quality评估",
		"task_id", task.ID,
		"word_count", draft.WordCount,
		"sections", len(draft.Outline))

	switch {
	case draft.WordCount >= 500 && len(draft.Outline) >= 2:
		// 质量充分 → 用 Event 驱动自动推进到审校
		if err := o.transitionAfterEvent(ctx, task, event.ID, StatusReview,
			fmt.Sprintf("初稿质量充分（%d 字, %d 章节），自动推进到审校", draft.WordCount, len(draft.Outline))); err != nil {
			slog.Error("failed to transition after writing event", "task_id", task.ID, "error", err)
		}

	case draft.WordCount > 0:
		// 质量尚可但偏短 → 创建 pending decision
		o.requestHumanDecision(ctx, task, DecisionAllowRewrite,
			fmt.Sprintf("初稿仅 %d 字（建议 500+），建议人工确认是否需要扩充后再审校", draft.WordCount))

	default:
		// 内容为空 → 直接重跑写作 Agent
		slog.Warn("draft content empty, retrying", "task_id", task.ID)
		o.emit(OrchestratorEvent{
			Type:    "decision.created",
			TaskID:  task.ID,
			Payload: map[string]interface{}{"type": "allow_rewrite", "status": "rejected", "by": "system", "reason": "draft content empty"},
		})
		o.runWritingAgent(ctx, task)
	}
}

// requestHumanDecision 创建一个 pending decision 并通知人类
func (o *Orchestrator) requestHumanDecision(ctx context.Context, task *Task, decType DecisionType, message string) {
	approveTarget := nextStatusForDecision(decType)
	rejectTarget := prevStatusForDecision(decType)

	_, err := o.store.CreateDecision(ctx, CreateDecisionInput{
		Type:                decType,
		Actor:               NewSystemActor("system"),
		DecidedByType:       DecidedByHuman,
		Status:              DecisionStatusPending,
		Rationale:           message,
		ApproveTargetStatus: approveTarget,
		RejectTargetStatus:  rejectTarget,
	}, task.ID)
	if err != nil {
		slog.Error("failed to create pending decision", "task_id", task.ID, "error", err)
		o.emit(OrchestratorEvent{
			Type:    "decision.required",
			TaskID:  task.ID,
			Payload: map[string]interface{}{"type": decType, "message": "⚠️ 决策记录创建失败，需人工介入: " + message},
		})
		return
	}

	o.emit(OrchestratorEvent{
		Type:    "decision.required",
		TaskID:  task.ID,
		Payload: map[string]interface{}{
			"type":    decType,
			"message": message,
		},
	})

	slog.Info("human decision requested",
		"task_id", task.ID, "type", decType, "message", message)
}

// ─── 失败回退 ─────────────────────────────────────────────

// rollbackTask 将任务从失败状态回退到上一个状态
func (o *Orchestrator) rollbackTask(taskID string, fromStatus, toStatus TaskStatus, reason string) {
	_, err := o.store.CreateDecision(context.Background(), CreateDecisionInput{
		Type:          DecisionEscalate,
		Actor:         NewSystemActor("system"),
		DecidedBy:     "system",
		DecidedByType: DecidedBySystem,
		Status:        DecisionStatusEscalated,
		Rationale:     reason,
	}, taskID)
	if err != nil {
		slog.Error("failed to create rollback decision", "task_id", taskID, "error", err)
	}

	if err := o.store.UpdateTaskStatus(context.Background(), taskID, toStatus, AssigneeHuman); err != nil {
		slog.Error("failed to rollback task status", "task_id", taskID, "error", err)
		return
	}

	o.emit(OrchestratorEvent{
		Type:    "task.status_changed",
		TaskID:  taskID,
		Payload: map[string]interface{}{"from": fromStatus, "to": toStatus, "reason": "agent_failure"},
	})
	o.emit(OrchestratorEvent{
		Type:    "decision.required",
		TaskID:  taskID,
		Payload: map[string]interface{}{"type": "escalate", "message": "Agent 执行失败，需要人工介入: " + reason},
	})

	slog.Warn("task rolled back due to agent failure",
		"task_id", taskID, "from", fromStatus, "to", toStatus, "reason", reason)
}

// ─── 辅助 ─────────────────────────────────────────────────

// recordEvent 记录 Agent 执行事件到数据库
func (o *Orchestrator) recordEvent(ctx context.Context, taskID string, evtType EventType, role AgentRole, status string, artifactID string, errMsg string) *AgentRunEvent {
	event, err := o.store.RecordAgentRunEvent(ctx, AgentRunEvent{
		TaskID:     taskID,
		Type:       evtType,
		AgentRole:  role,
		Status:     status,
		ArtifactID: artifactID,
		Error:      errMsg,
	})
	if err != nil {
		slog.Error("failed to record agent run event", "task_id", taskID, "error", err)
		return &AgentRunEvent{TaskID: taskID, Type: evtType, AgentRole: role, Status: status}
	}
	return event
}

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

// recordAgentOutcome 记录 Agent 执行结果到信誉系统
func (o *Orchestrator) recordAgentOutcome(taskID string, role AgentRole, success bool, artifact *Artifact, durationMs int64) {
	input := RecordAgentOutcomeInput{
		AgentRole:  string(role),
		TaskID:     taskID,
		Success:    success,
		DurationMs: durationMs,
	}
	if artifact != nil {
		input.TokenCost = artifact.TokenCost
		input.QualityScore = 0.5
		if artifact.Status == ArtifactStatusApproved {
			input.QualityScore = 0.8
		}
	} else if !success {
		input.QualityScore = 0.0
	}

	if err := o.store.RecordAgentOutcome(context.Background(), input); err != nil {
		slog.Warn("failed to record agent outcome", "task_id", taskID, "role", role, "error", err)
	}
}
