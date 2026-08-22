package editorial

import (
	"log/slog"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// ─── NodeEmitter: DAG 节点级事件桥接器 ──────────────────────
//
// 在 DAG 执行模式下，Agent 执行器内部使用 noopEmitter，
// 导致 LLM 的流式输出（StreamDelta / ReasoningDelta）无法到达前端。
//
// NodeEmitter 将 engine.EventEmitter 的事件桥接为 editorial.EventEmitter 事件，
// 通过 DAGExecutor 的 emit() 方法转发到 WebSocket。
//
// 设计：
//   - 每个 DAG 节点执行时创建一个 NodeEmitter 实例
//   - StreamDelta / ReasoningDelta 被封装为 node.stream 事件
//   - StepStart / StepComplete 被封装为 node.step 事件
//   - 其他事件（Paused / Error / Completed）按需转发

// NodeEmitter 将单个 DAG 节点的 engine.EventEmitter 事件桥接为 editorial 事件。
type NodeEmitter struct {
	taskID   string
	nodeID   string
	agentID  string
	emitter  EventEmitter // editorial EventEmitter (DAGExecutor 的)
}

// NewNodeEmitter 创建节点级事件桥接器
func NewNodeEmitter(taskID, nodeID, agentID string, emitter EventEmitter) *NodeEmitter {
	return &NodeEmitter{
		taskID:  taskID,
		nodeID:  nodeID,
		agentID: agentID,
		emitter: emitter,
	}
}

func (n *NodeEmitter) emit(evtType string, payload map[string]interface{}) {
	if n.emitter == nil {
		return
	}
	if payload == nil {
		payload = make(map[string]interface{})
	}
	payload["node_id"] = n.nodeID
	payload["agent_id"] = n.agentID
	n.emitter.Emit(OrchestratorEvent{
		Type:    evtType,
		TaskID:  n.taskID,
		Payload: payload,
	})
}

// ─── engine.EventEmitter 接口实现 ────────────────────────

func (n *NodeEmitter) StepStart(step engine.StepName, stepIndex int) {
	n.emit("node.step_start", map[string]interface{}{
		"step":       string(step),
		"step_index": stepIndex,
	})
}

func (n *NodeEmitter) StepComplete(step engine.StepName, result interface{}, durationMs int64) {
	n.emit("node.step_complete", map[string]interface{}{
		"step":        string(step),
		"step_index":  0,
		"duration_ms": durationMs,
		"result":      result,
	})
}

func (n *NodeEmitter) StreamDelta(delta string) {
	if delta == "" {
		return
	}
	n.emit("node.stream.delta", map[string]interface{}{
		"delta": delta,
	})
}

func (n *NodeEmitter) StreamReset() {
	n.emit("node.stream.reset", nil)
}

func (n *NodeEmitter) ReasoningDelta(delta string) {
	if delta == "" {
		return
	}
	n.emit("node.reasoning.delta", map[string]interface{}{
		"delta": delta,
	})
}

func (n *NodeEmitter) ArticleTitle(title string) {
	if title == "" {
		return
	}
	n.emit("node.article_title", map[string]interface{}{
		"title": title,
	})
}

func (n *NodeEmitter) StreamDone(fullText string) {
	// DAG 模式下不发送 stream.done（由 node.completed 统一处理）
	// 但记录日志以便调试
	slog.Debug("node emitter: stream done",
		"task_id", n.taskID, "node_id", n.nodeID, "chars", len([]rune(fullText)))
}

func (n *NodeEmitter) AwaitInput(step engine.StepName, data interface{}, options []string, attempt int, maxAttempts int) {
	n.emit("node.await_input", map[string]interface{}{
		"step":         string(step),
		"data":         data,
		"options":      options,
		"attempt":      attempt,
		"max_attempts": maxAttempts,
	})
}

func (n *NodeEmitter) Paused(step engine.StepName, savedState interface{}) {
	n.emit("node.paused", map[string]interface{}{
		"step": string(step),
	})
}

func (n *NodeEmitter) PausedWithReason(step engine.StepName, savedState interface{}, reason string) {
	n.emit("node.paused", map[string]interface{}{
		"step":   string(step),
		"reason": reason,
	})
}

func (n *NodeEmitter) Resumed(step engine.StepName) {
	n.emit("node.resumed", map[string]interface{}{
		"step": string(step),
	})
}

func (n *NodeEmitter) Error(code, message string, step engine.StepName) {
	n.emit("node.error", map[string]interface{}{
		"code":    code,
		"message": message,
		"step":    string(step),
	})
}

func (n *NodeEmitter) Completed(article string, articleTitle string, review interface{}, tokenUsage interface{}) {
	n.emit("node.agent_completed", map[string]interface{}{
		"article":       article,
		"article_title": articleTitle,
		"review":        review,
		"token_usage":   tokenUsage,
	})
}

func (n *NodeEmitter) Cancelled() {
	n.emit("node.cancelled", nil)
}

func (n *NodeEmitter) Compaction(originalMessages, savedTokens int, summaryPreview string, historyVersion uint64, triggerReason string) {
	n.emit("node.compaction", map[string]interface{}{
		"original_messages": originalMessages,
		"saved_tokens":      savedTokens,
		"history_version":   historyVersion,
		"trigger_reason":    triggerReason,
	})
}
