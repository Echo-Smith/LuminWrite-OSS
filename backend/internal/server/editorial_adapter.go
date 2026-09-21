package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
)

// editorialSSEEmitter 将编辑部编排事件转发到 SSE Hub。
//
// Editorial Transport Migration：编辑部 Workflow/Editorial 的事件通道从
// WebSocket 广播（websocket.Hub.Broadcast，全局无差别）迁移为 SSE 定向推送
// （SSEHub.SendToUser，严格按 task owner 隔离）。DAG 执行语义不变，只换传输。
//
// 事件名映射（保持与旧 WS 消息类型的机械对应，"." → ":"）：
//   - workflow.started  → SSE "workflow:started"
//   - workflow.completed→ SSE "workflow:completed"
//   - node.stream.delta → SSE "node:stream:delta"
//   - 其余编辑部事件（task.* / artifact.* / decision.* / experiment.*）
//     → SSE "editorial:event"，data 为完整 OrchestratorEvent（与旧 WS
//       "editorial.event" 信封一致，前端 editorial-store 无需改造）。
//
// 隔离语义：workflow.*/node.* 事件的 payload 可能携带用户文章内容（如
// node.stream.delta 的流式正文），必须严格定向给 task 的 owner。
// owner 通过 editorial store 按 task_id 反查（owner 不可变，进程内缓存）。
// owner 无法解析的事件一律丢弃并告警 —— 绝不全局广播用户内容。
// experiment.* 事件的 TaskID 字段实为 experiment ID，按 CreatedBy 反查 owner。
type editorialSSEEmitter struct {
	hub   *SSEHub
	store *editorial.Store

	ownerMu    sync.RWMutex
	ownerCache map[string]string // taskID/experimentID → ownerUserID；"!" 表示查无主
}

// newEditorialSSEEmitter 创建 SSE 事件适配器。
func newEditorialSSEEmitter(hub *SSEHub, store *editorial.Store) *editorialSSEEmitter {
	return &editorialSSEEmitter{
		hub:        hub,
		store:      store,
		ownerCache: make(map[string]string),
	}
}

// ownerOf 解析事件归属用户（带进程内缓存；owner 创建后不可变）。
// 返回 "" 表示无法解析（查库失败或对象不存在）。
func (e *editorialSSEEmitter) ownerOf(kind, id string) string {
	if id == "" {
		return ""
	}

	e.ownerMu.RLock()
	cached, ok := e.ownerCache[kind+":"+id]
	e.ownerMu.RUnlock()
	if ok {
		if cached == "!" {
			return ""
		}
		return cached
	}

	owner := ""
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	switch kind {
	case "experiment":
		if exp, err := e.store.GetExperiment(ctx, id); err == nil && exp != nil {
			owner = exp.CreatedBy
		}
	default: // task
		if task, err := e.store.GetTask(ctx, id); err == nil && task != nil {
			owner = task.OwnerID
		}
	}

	e.ownerMu.Lock()
	defer e.ownerMu.Unlock()
	if owner == "" {
		// 负缓存：查无主的对象不会凭空变出 owner（任务在事件发出前已创建），
		// 缓存失败结果避免高频事件反复打库。
		e.ownerCache[kind+":"+id] = "!"
		return ""
	}
	e.ownerCache[kind+":"+id] = owner
	return owner
}

// Emit 实现 editorial.EventEmitter 接口。
func (e *editorialSSEEmitter) Emit(evt editorial.OrchestratorEvent) {
	if e.hub == nil {
		return
	}

	if strings.HasPrefix(evt.Type, "workflow.") || strings.HasPrefix(evt.Type, "node.") {
		e.emitWorkflowEvent(evt)
		return
	}
	e.emitEditorialEvent(evt)
}

// emitWorkflowEvent 发送 workflow.* / node.* 事件：SSE 事件名做 "." → ":" 的
// 机械映射，data 为事件 payload（合并 task_id，方便前端单订阅多任务追踪）。
// 严格 SendToUser(task owner)；owner 不可解析则丢弃 —— 不全局广播用户内容。
func (e *editorialSSEEmitter) emitWorkflowEvent(evt editorial.OrchestratorEvent) {
	owner := e.ownerOf("task", evt.TaskID)
	if owner == "" {
		slog.Warn("editorial SSE: dropping workflow event with unresolvable owner",
			"type", evt.Type, "task_id", evt.TaskID)
		return
	}

	data, err := workflowEventData(evt)
	if err != nil {
		slog.Warn("editorial: failed to marshal event payload", "error", err, "type", evt.Type)
		return
	}

	e.hub.SendToUser(owner, &SSEEvent{
		Event:  strings.ReplaceAll(evt.Type, ".", ":"),
		Data:   data,
		UserID: owner,
	})
}

// workflowEventData 把 payload 平铺为 map 并注入 task_id，保持与旧 WS payload
// 相同的字段结构（前端 workflow-store 直接读顶层字段）。
func workflowEventData(evt editorial.OrchestratorEvent) (map[string]interface{}, error) {
	data := make(map[string]interface{}, 8)
	if evt.Payload != nil {
		raw, err := json.Marshal(evt.Payload)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, err
		}
	}
	if _, exists := data["task_id"]; !exists {
		data["task_id"] = evt.TaskID
	}
	return data, nil
}

// emitEditorialEvent 发送其余编辑部事件（task.* / artifact.* / decision.* /
// experiment.*）：保持旧 WS "editorial.event" 信封（完整 OrchestratorEvent），
// 前端 editorial-store.pushEvent 原样消费。同样按 owner 定向；
// experiment 事件按 CreatedBy 归属。
func (e *editorialSSEEmitter) emitEditorialEvent(evt editorial.OrchestratorEvent) {
	kind := "task"
	if strings.HasPrefix(evt.Type, "experiment.") {
		kind = "experiment"
	}
	owner := e.ownerOf(kind, evt.TaskID)
	if owner == "" {
		slog.Warn("editorial SSE: dropping event with unresolvable owner",
			"type", evt.Type, "task_id", evt.TaskID)
		return
	}

	e.hub.SendToUser(owner, &SSEEvent{
		Event:  "editorial:event",
		Data:   evt,
		UserID: owner,
	})

	slog.Info("editorial event emitted",
		"type", evt.Type, "task_id", evt.TaskID)
}
