package server

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/websocket"
)

// editorialWSEmitter 将编辑部编排事件转发到 WebSocket Hub
type editorialWSEmitter struct {
	hub *websocket.Hub
}

// Emit 实现 editorial.EventEmitter 接口
func (e *editorialWSEmitter) Emit(evt editorial.OrchestratorEvent) {
	// DAG 工作流事件（workflow.*/node.*）直接用事件 type 作为 WS 消息 type，
	// 只发送 evt.Payload（而非整个 OrchestratorEvent），避免前端嵌套 payload 问题。
	if strings.HasPrefix(evt.Type, "workflow.") || strings.HasPrefix(evt.Type, "node.") {
		data, err := json.Marshal(evt.Payload)
		if err != nil {
			slog.Warn("editorial: failed to marshal event payload", "error", err, "type", evt.Type)
			return
		}
		e.hub.Broadcast(&websocket.ServerMessage{
			Type:    evt.Type,
			Payload: json.RawMessage(data),
		})
		return
	}

	// 其他编辑部事件保持原有格式（发送完整 OrchestratorEvent）
	data, err := json.Marshal(evt)
	if err != nil {
		slog.Warn("editorial: failed to marshal event", "error", err)
		return
	}
	e.hub.Broadcast(&websocket.ServerMessage{
		Type:    "editorial.event",
		Payload: json.RawMessage(data),
	})

	slog.Info("editorial event emitted",
		"type", evt.Type, "task_id", evt.TaskID)
}
