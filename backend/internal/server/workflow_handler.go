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

// handleWorkflowPlan 调用 Planner 生成 DAG 工作流
func (s *Server) handleWorkflowPlan(client *websocket.Client, p websocket.WorkflowStartPayload, userID string) {
	if s.planner == nil || s.dagExecutor == nil {
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "planner or dag executor not initialized"},
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	taskID := editorial.GenerateNodeID() // 临时 task ID

	planResult, err := s.planner.Plan(ctx, editorial.PlanInput{
		UserInput:   p.UserInput,
		Title:       p.Title,
		Description: p.Description,
		StyleSlug:   p.StyleSlug,
		Tags:        p.Tags,
		KBEnabled:   p.KBEnabled,
	}, taskID, userID)
	if err != nil {
		slog.Error("workflow: planner failed", "error", err, "user_input", p.UserInput)
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": err.Error()},
		})
		return
	}

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

	slog.Info("workflow: plan created",
		"task_id", taskID, "agents", len(planResult.Agents),
		"nodes", len(planResult.Workflow.Nodes))
}

// handleWorkflowExecute 启动 DAG 执行
func (s *Server) handleWorkflowExecute(client *websocket.Client, taskID, userID string) {
	if s.dagExecutor == nil {
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "dag executor not initialized"},
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

	// 构建 task 标题和描述
	taskTitle := "编辑部工作流"
	if plan.UserInput != "" {
		taskTitle = plan.UserInput
		if len([]rune(taskTitle)) > 50 {
			taskTitle = string([]rune(taskTitle)[:50]) + "..."
		}
	} else if plan.Rationale != "" {
		taskTitle = plan.Rationale
		if len([]rune(taskTitle)) > 50 {
			taskTitle = string([]rune(taskTitle)[:50]) + "..."
		}
	}
	taskDescription := ""
	if plan.UserInput != "" {
		taskDescription = plan.UserInput
		if len([]rune(taskDescription)) > 200 {
			taskDescription = string([]rune(taskDescription)[:200]) + "..."
		}
	} else {
		for _, a := range plan.Agents {
			if a.Role == "researcher" && a.Persona != "" {
				taskDescription = a.Persona
				if len([]rune(taskDescription)) > 200 {
					taskDescription = string([]rune(taskDescription)[:200]) + "..."
				}
				break
			}
		}
	}

	// 构建 task 对象（含完整上下文，供 Agent 执行器使用）
	task := &editorial.Task{
		ID:           taskID,
		OwnerID:      userID,
		Title:        taskTitle,
		Description:  taskDescription,
		Status:       editorial.StatusResearch,
		TokenBudget:  200000, // 默认 20 万 token 预算
		TokenUsed:    0,
		StyleSlug:    plan.StyleSlug,
		Tags:         plan.Tags,
		KBEnabled:    plan.KBEnabled,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
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
func (s *Server) handleWorkflowControl(client *websocket.Client, _ json.RawMessage, action string) {
	// TODO: 实现工作流暂停/恢复/取消（需要 context cancel 机制）
	slog.Info("workflow control requested", "action", action)
	client.Send(&websocket.ServerMessage{
		Type:    websocket.MsgWorkflowPaused,
		Payload: map[string]string{"action": action, "status": "not_yet_implemented"},
	})
}
