package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/websocket"
)

// ─── Beta: 编辑部模式 DAG 工作流 WebSocket 处理器 ────────────
//
// 处理 workflow.start / workflow.edit / workflow.pause / workflow.resume / workflow.cancel
// 消息，驱动 Planner 生成 DAG + DAGExecutor 执行节点。

// handleWorkflowStart 处理 workflow.start 消息
// 两种模式：
// 1. 有 user_input → 调用 Planner 生成 DAG → 返回 workflow.created
// 2. 有 task_id → 从缓存获取 plan，启动 DAGExecutor 执行
func (s *Server) handleWorkflowStart(client *websocket.Client, payload json.RawMessage, userID, _ string) {
	var p websocket.WorkflowStartPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		slog.Error("workflow.start: failed to parse payload", "error", err)
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "invalid payload"},
		})
		return
	}

	// 模式 1: 有 user_input → 调用 Planner
	if p.UserInput != "" {
		s.handleWorkflowPlan(client, p, userID)
		return
	}

	// 模式 2: 有 task_id → 直接执行 DAG
	if p.TaskID != "" {
		// 注册 client 到 hub，使 DAGExecutor 的 Broadcast 事件能送达前端
		// Bug fix: 如果用户没有先发 agent.start，hub 中没有注册该 client，
		// 导致 workflow.started / node.* 事件通过 Broadcast 发送时被丢弃。
		s.hub.Register(p.TaskID, client)
		s.handleWorkflowExecute(client, p.TaskID, userID)
		return
	}

	client.Send(&websocket.ServerMessage{
		Type:    websocket.MsgWorkflowFailed,
		Payload: map[string]string{"error": "either user_input or task_id is required"},
	})
}

// handleWorkflowPlan 调用 Planner 生成 DAG 工作流，同时将 Task 持久化到 DB
func (s *Server) handleWorkflowPlan(client *websocket.Client, p websocket.WorkflowStartPayload, userID string) {
	if s.planner == nil || s.dagExecutor == nil || s.editorialSvc == nil {
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "planner, dag executor, or editorial service not initialized"},
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// 先用临时 ID 供 Planner 使用，DB Task ID 在 CreateTask 后获得
	tempID := editorial.GenerateNodeID()

	planResult, err := s.planner.Plan(ctx, editorial.PlanInput{
		UserInput:   p.UserInput,
		Title:       p.Title,
		Description: p.Description,
		StyleSlug:   p.StyleSlug,
		Tags:        p.Tags,
		KBEnabled:   p.KBEnabled,
	}, tempID, userID)
	if err != nil {
		slog.Error("workflow: planner failed", "error", err, "user_input", p.UserInput)
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": err.Error()},
		})
		return
	}

	// 构建 Task 标题和描述
	taskTitle := p.Title
	if taskTitle == "" {
		taskTitle = p.UserInput
		if len([]rune(taskTitle)) > 50 {
			taskTitle = string([]rune(taskTitle)[:50]) + "..."
		}
	}
	taskDescription := p.Description
	if taskDescription == "" {
		taskDescription = p.UserInput
		if len([]rune(taskDescription)) > 200 {
			taskDescription = string([]rune(taskDescription)[:200]) + "..."
		}
	}

	// 将 Task 持久化到 DB（CreateTask 同时自动创建选题卡 Artifact 并批准）
	dbTask, err := s.editorialSvc.CreateTask(ctx, editorial.CreateTaskInput{
		Title:       taskTitle,
		Description: taskDescription,
		StyleSlug:   planResult.StyleSlug,
		Tags:        planResult.Tags,
		TokenBudget: 200000,
	}, userID)
	if err != nil {
		slog.Error("workflow: failed to persist task to DB", "error", err)
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "failed to create task: " + err.Error()},
		})
		return
	}

	// 用 DB 返回的真正 Task ID 替换临时 ID
	taskID := dbTask.ID

	// 缓存 plan 结果到 DAGExecutor，同时注册生成的 Agent
	s.dagExecutor.CachePlan(taskID, planResult)

	// 返回 workflow.created 给前端
	client.Send(&websocket.ServerMessage{
		Type: websocket.MsgWorkflowCreated,
		Payload: map[string]interface{}{
			"agents":    planResult.Agents,
			"workflow":  planResult.Workflow,
			"rationale": planResult.Rationale,
			"task_id":  taskID,
		},
	})

	slog.Info("workflow: plan created and task persisted",
		"task_id", taskID, "db_task_id", dbTask.ID,
		"agents", len(planResult.Agents), "nodes", len(planResult.Workflow.Nodes))
}

// handleWorkflowExecute 启动 DAG 执行（从 DB 加载 Task）
func (s *Server) handleWorkflowExecute(client *websocket.Client, taskID, userID string) {
	if s.dagExecutor == nil || s.editorialSvc == nil {
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "dag executor or editorial service not initialized"},
		})
		return
	}

	// 从缓存获取 plan
	plan, ok := s.dagExecutor.GetPlan(taskID)
	if !ok {
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "plan not found for task_id: " + taskID},
		})
		return
	}

	// 从 DB 加载 Task（不再内存构造，确保 task.ID 与 DB 一致）
	task, err := s.editorialSvc.GetTask(context.Background(), taskID)
	if err != nil {
		slog.Error("workflow: failed to load task from DB", "error", err, "task_id", taskID)
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "failed to load task: " + err.Error()},
		})
		return
	}

	// 确保 OwnerID 正确（以防 DB 中为空）
	if task.OwnerID == "" {
		task.OwnerID = userID
	}

	// 异步执行 DAG
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		if err := s.dagExecutor.Execute(ctx, &plan.Workflow, task); err != nil {
			slog.Error("workflow: DAG execution failed", "error", err, "task_id", taskID)
			client.Send(&websocket.ServerMessage{
				Type:    websocket.MsgWorkflowFailed,
				Payload: map[string]string{"error": "DAG execution failed: " + err.Error()},
			})
		}
	}()

	slog.Info("workflow: execution started", "task_id", taskID, "nodes", len(plan.Workflow.Nodes))
}

// handleWorkflowEdit 处理 workflow.edit 消息（用户修改 DAG）
func (s *Server) handleWorkflowEdit(client *websocket.Client, payload json.RawMessage, _, _ string) {
	var p websocket.WorkflowEditPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "invalid payload"},
		})
		return
	}

	// TODO: 实现用户修改 DAG 的逻辑（解析修改后的 WorkflowSpec，重新校验，更新缓存）
	client.Send(&websocket.ServerMessage{
		Type:    websocket.MsgWorkflowFailed,
		Payload: map[string]string{"error": "workflow edit not yet implemented"},
	})
}

// handleWorkflowControl 处理 workflow.pause / resume / cancel
func (s *Server) handleWorkflowControl(client *websocket.Client, payload json.RawMessage, action string) {
	var p websocket.WorkflowControlPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		slog.Warn("workflow.control: failed to parse payload", "action", action, "error", err)
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "invalid payload"},
		})
		return
	}

	if p.TaskID == "" {
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "task_id is required"},
		})
		return
	}

	switch action {
	case "cancel":
		s.handleWorkflowCancel(client, p.TaskID)
	case "pause":
		// TODO: 实现 pause（需要可恢复的 context 暂停机制）
		slog.Info("workflow pause requested", "task_id", p.TaskID)
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowPaused,
			Payload: map[string]string{"task_id": p.TaskID, "status": "not_yet_implemented"},
		})
	case "resume":
		// TODO: 实现 resume
		slog.Info("workflow resume requested", "task_id", p.TaskID)
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowResumed,
			Payload: map[string]string{"task_id": p.TaskID, "status": "not_yet_implemented"},
		})
	default:
		slog.Warn("workflow.control: unknown action", "action", action)
	}
}

// handleWorkflowCancel 处理 workflow.cancel 消息
// 调用 DAGExecutor.Cancel 取消正在执行的 DAG 工作流
func (s *Server) handleWorkflowCancel(client *websocket.Client, taskID string) {
	if s.dagExecutor == nil {
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "dag executor not initialized"},
		})
		return
	}

	cancelled := s.dagExecutor.Cancel(taskID)
	if !cancelled {
		slog.Warn("workflow.cancel: task not found or not running", "task_id", taskID)
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "task not found or not running", "task_id": taskID},
		})
		return
	}

	slog.Info("workflow: cancelled", "task_id", taskID)

	// 通知前端工作流已取消
	client.Send(&websocket.ServerMessage{
		Type: websocket.MsgWorkflowCancelled,
		Payload: map[string]interface{}{
			"task_id": taskID,
			"status":  "cancelled",
		},
	})
}
