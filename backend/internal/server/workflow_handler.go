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
		s.handleWorkflowPlan(client, p.UserInput, userID)
		return
	}

	// 模式 2: 有 task_id → 直接执行 DAG
	if p.TaskID != "" {
		s.handleWorkflowExecute(client, p.TaskID, userID)
		return
	}

	client.Send(&websocket.ServerMessage{
		Type:    websocket.MsgWorkflowFailed,
		Payload: map[string]string{"error": "either user_input or task_id is required"},
	})
}

// handleWorkflowPlan 调用 Planner 生成 DAG 工作流
func (s *Server) handleWorkflowPlan(client *websocket.Client, userInput, userID string) {
	if s.planner == nil || s.dagExecutor == nil {
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "planner or dag executor not initialized"},
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	taskID := editorial.GenerateNodeID() // 临时 task ID
	planResult, err := s.planner.Plan(ctx, userInput, taskID, userID)
	if err != nil {
		slog.Error("workflow: planner failed", "error", err, "user_input", userInput)
		client.Send(&websocket.ServerMessage{
			Type:    websocket.MsgWorkflowFailed,
			Payload: map[string]string{"error": "planner failed: " + err.Error()},
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

	// 构建 task 对象
	task := &editorial.Task{
		ID:      taskID,
		OwnerID: userID,
		Status:  editorial.StatusResearch,
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

	slog.Info("workflow: execution started", "task_id", taskID)
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
