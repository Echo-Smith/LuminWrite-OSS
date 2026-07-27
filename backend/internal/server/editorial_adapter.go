package server

import (
	"encoding/json"
	"log/slog"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/websocket"
)

// editorialWSEmitter 将编辑部编排事件转发到 WebSocket Hub
type editorialWSEmitter struct {
	hub *websocket.Hub
}

// Emit 实现 editorial.EventEmitter 接口
func (e *editorialWSEmitter) Emit(evt editorial.OrchestratorEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		slog.Warn("editorial: failed to marshal event", "error", err)
		return
	}

	// 广播给所有连接的客户端（编辑部事件是全局的）
	e.hub.Broadcast(&websocket.ServerMessage{
		Type:    "editorial.event",
		Payload: json.RawMessage(data),
	})

	slog.Info("editorial event emitted",
		"type", evt.Type, "task_id", evt.TaskID)
}
