package websocket

import "encoding/json"

// ClientMessage types (client → server)
const (
	MsgAgentStart      = "agent.start"
	MsgAgentPause      = "agent.pause"
	MsgAgentResume     = "agent.resume"
	MsgAgentCancel     = "agent.cancel"
	MsgAgentConfirm    = "agent.confirm"
	MsgAgentEdit       = "agent.edit"
	MsgFeedback        = "feedback.submit"
	MsgSessionResume   = "session.resume"

	// Beta: 编辑部模式 DAG 工作流消息（client → server）
	MsgWorkflowStart = "workflow.start"   // 用户确认 DAG 后启动
	MsgWorkflowEdit   = "workflow.edit"    // 用户修改 DAG（增删节点/改依赖）
	MsgWorkflowPause  = "workflow.pause"   // 暂停工作流
	MsgWorkflowResume = "workflow.resume"  // 恢复工作流
	MsgWorkflowCancel = "workflow.cancel"  // 取消工作流
)

// ServerMessage types (server → client)
const (
	MsgAgentCreated      = "agent.created"
	MsgAgentStepStart    = "agent.step.start"
	MsgAgentStepComplete = "agent.step.complete"
	MsgAgentStream       = "agent.stream"
	MsgAgentStreamReset  = "agent.stream.reset"
	MsgAgentReasoning    = "agent.reasoning"
	MsgAgentStreamDone   = "agent.stream.done"
	MsgAgentArticleTitle = "agent.article_title"
	MsgAgentPaused       = "agent.paused"
	MsgAgentResumed      = "agent.resumed"
	MsgAgentAwaitInput   = "agent.await_input"
	MsgAgentCompleted    = "agent.completed"
	MsgAgentError        = "agent.error"
	MsgAgentCancelled    = "agent.cancelled"
	MsgAgentEdited       = "agent.edited"
	MsgSessionResumed    = "session.resumed"
	MsgMemoryUsed        = "memory.used"
	MsgMemoryDismiss     = "memory.dismiss"
	MsgAgentCompaction   = "agent.compaction"
	MsgTaskNameUpdated   = "task_name.updated" // LLM 异步提取的 task_name 就绪，推送前端实时更新

	// Beta: 编辑部模式 DAG 工作流消息（server → client）
	MsgWorkflowCreated   = "workflow.created"    // Planner 返回角色集 + DAG
	MsgWorkflowStarted   = "workflow.started"     // DAG 开始执行
	MsgNodeStarted       = "node.started"         // 节点开始执行
	MsgNodeStreamDelta   = "node.stream.delta"     // 节点流式输出
	MsgNodeCompleted     = "node.completed"       // 节点完成
	MsgNodeFailed        = "node.failed"           // 节点失败
	MsgWorkflowCompleted = "workflow.completed"   // 整个 DAG 完成
	MsgWorkflowFailed    = "workflow.failed"      // 整个 DAG 失败
	MsgWorkflowPaused    = "workflow.paused"      // DAG 已暂停
	MsgWorkflowResumed   = "workflow.resumed"      // DAG 已恢复
	MsgWorkflowCancelled = "workflow.cancelled"   // DAG 已取消
)

// ClientMessage is a message from the client.
type ClientMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ServerMessage is a message to the client.
type ServerMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// AgentStartPayload is the payload for agent.start.
type AgentStartPayload struct {
	Message      string   `json:"message"`
	Style        string   `json:"style,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	Model        string   `json:"model,omitempty"`
	AgentMode    string   `json:"agent_mode,omitempty"` // "pipeline" | "unified" — overrides server default
	SessionID    string   `json:"session_id,omitempty"`
	UserMaterials []string `json:"user_materials,omitempty"`
	WordLimit    int      `json:"word_limit,omitempty"`
	TopicURL     string   `json:"topic_url,omitempty"` // 热搜选题原始链接，用于抓取事件背景
	KBEnabled    *bool    `json:"kb_enabled,omitempty"` // 知识库搜索开关（nil=默认开启）
}

// AgentControlPayload is the payload for pause/resume/cancel.
type AgentControlPayload struct {
	TraceID string `json:"trace_id"`
}

// AgentConfirmPayload is the payload for agent.confirm.
type AgentConfirmPayload struct {
	TraceID string                 `json:"trace_id"`
	Step    string                 `json:"step"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

// AgentEditPayload is the payload for agent.edit — allows the user to edit
// the article text (or a segment) while the agent is paused or after completion.
type AgentEditPayload struct {
	TraceID string `json:"trace_id"`
	Field   string `json:"field"`           // "article" | "title" | "paragraph"
	Index   int    `json:"index,omitempty"` // for paragraph edits
	Value   string `json:"value"`
	Reason  string `json:"reason,omitempty"` // why the user edited
}

// AgentCreatedPayload
type AgentCreatedPayload struct {
	TraceID string `json:"trace_id"`
	Style   string `json:"style"`
	Mode    string `json:"mode"`
}

// StepStartPayload
type StepStartPayload struct {
	TraceID    string `json:"trace_id"`
	Step       string `json:"step"`
	StepIndex  int    `json:"step_index"`
}

// StepCompletePayload
type StepCompletePayload struct {
	TraceID    string      `json:"trace_id"`
	Step       string      `json:"step"`
	Result     interface{} `json:"result"`
	DurationMs int64       `json:"duration_ms"`
}

// StreamPayload
type StreamPayload struct {
	TraceID string `json:"trace_id"`
	Delta   string `json:"delta"`
}

// StreamResetPayload instructs the client to discard all streamed text
// content for the current step. Sent when the agent loop detects that
// optimistically-streamed content belongs to an intermediate tool-call
// round rather than the final answer.
type StreamResetPayload struct {
	TraceID string `json:"trace_id"`
}

// ReasoningPayload carries a reasoning (thinking) delta to the client.
// Sent during thinking-mode streaming to visualize the model's chain-of-thought.
type ReasoningPayload struct {
	TraceID string `json:"trace_id"`
	Delta   string `json:"delta"`
}

// ArticleTitlePayload carries the extracted article title to the client.
// Sent once after the title is resolved from the LLM's JSON prefix (or fallback).
type ArticleTitlePayload struct {
	TraceID string `json:"trace_id"`
	Title   string `json:"title"`
}

// StreamDonePayload
type StreamDonePayload struct {
	TraceID  string `json:"trace_id"`
	FullText string `json:"full_text"`
}

// AwaitInputPayload
type AwaitInputPayload struct {
	TraceID    string      `json:"trace_id"`
	Step       string      `json:"step"`
	Data       interface{} `json:"data"`
	Options    []string    `json:"options"`
	Attempt    int         `json:"attempt,omitempty"`
	MaxAttempts int        `json:"max_attempts,omitempty"`
}

// CompletedPayload
type CompletedPayload struct {
	TraceID      string      `json:"trace_id"`
	Article      string      `json:"article"`
	ArticleTitle string      `json:"article_title,omitempty"`
	Review       interface{} `json:"review"`
	TokenUsage   interface{} `json:"token_usage"`
	PointsUsed   float64     `json:"points_used"`
}

// ErrorPayload
type ErrorPayload struct {
	TraceID string `json:"trace_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Step    string `json:"step,omitempty"`
}

// CancelledPayload
type CancelledPayload struct {
	TraceID string `json:"trace_id"`
}

// PausedPayload
type PausedPayload struct {
	TraceID    string      `json:"trace_id"`
	Step       string      `json:"step"`
	SavedState interface{} `json:"saved_state"`
	Reason     string      `json:"reason,omitempty"` // "disconnect" | "user" | ""
}

// ResumedPayload
type ResumedPayload struct {
	TraceID string `json:"trace_id"`
	Step    string `json:"step"`
}

// CompactionPayload carries conversation history compaction info to the client.
// Sent when the Harness compresses older conversation messages into a summary
// to stay within the LLM's context window. The client uses this to display a
// "history compressed" indicator in the chat UI.
//
// v3.0 扩展：增加 history_version 和 trigger_reason 字段。
// trigger_reason: "threshold" (消息数阈值) | "token_budget" (Token 预算不足，AutoCompactFallback)
type CompactionPayload struct {
	TraceID           string `json:"trace_id"`
	OriginalMessages  int    `json:"original_messages"`
	CompactedMessages int    `json:"compacted_messages"`
	SavedTokens       int    `json:"saved_tokens"`
	SummaryPreview    string `json:"summary_preview,omitempty"`
	HistoryVersion    uint64 `json:"history_version,omitempty"`    // v3.0: 压缩后的历史版本号
	TriggerReason     string `json:"trigger_reason,omitempty"`     // v3.0: "threshold" | "token_budget"
}

// SessionResumePayload is sent by the client to resume a session after reconnect.
type SessionResumePayload struct {
	TraceID string `json:"trace_id"`
}

// SessionResumedPayload is the server response to a session.resume.
type SessionResumedPayload struct {
	TraceID          string      `json:"trace_id"`
	Status           string      `json:"status"`                        // running | paused | completed | error | not_found
	Step             string      `json:"step,omitempty"`                // current running step
	Article          string      `json:"article,omitempty"`             // partial article text
	ArticleTitle     string      `json:"article_title,omitempty"`       // extracted article title
	TaskName         string      `json:"task_name,omitempty"`           // LLM-extracted short title for session list
	Style            string      `json:"style,omitempty"`
	Mode             string      `json:"mode,omitempty"`
	Outline          interface{} `json:"outline,omitempty"`             // current outline if awaiting input
	Review           interface{} `json:"review,omitempty"`              // review result if completed
	Message          string      `json:"message,omitempty"`             // error message if applicable
	StepHistory      interface{} `json:"step_history,omitempty"`        // completed step records
	ReasoningContent string      `json:"reasoning_content,omitempty"`   // model's chain-of-thought
	ConversationID   string      `json:"conversation_id,omitempty"`     // for continuing the conversation
	UserInput        string      `json:"user_input,omitempty"`           // original user request
	CanResume        bool        `json:"can_resume,omitempty"`          // true if paused session can be resumed
}

// ─── Beta: 编辑部模式 DAG 工作流 Payload 类型 ─────────────

// WorkflowStartPayload 客户端发起 workflow.start
type WorkflowStartPayload struct {
	TaskID      string   `json:"task_id"`
	UserInput   string   `json:"user_input,omitempty"`   // 用户写作意图（如果还没有跑 Planner）
	Title       string   `json:"title,omitempty"`        // 任务标题（可选）
	Description string   `json:"description,omitempty"`  // 任务详细描述（可选）
	StyleSlug   string   `json:"style_slug,omitempty"`   // 写作风格标识（可选）
	Tags        []string `json:"tags,omitempty"`          // 栏目标签（可选）
	KBEnabled   *bool    `json:"kb_enabled,omitempty"`   // 知识库搜索开关（nil=默认开启）
}

// WorkflowEditPayload 客户端修改 DAG
type WorkflowEditPayload struct {
	TaskID   string          `json:"task_id"`
	Workflow json.RawMessage `json:"workflow"` // 修改后的 WorkflowSpec JSON
}

// WorkflowControlPayload 用于 pause/resume/cancel
type WorkflowControlPayload struct {
	TaskID string `json:"task_id"`
}

// WorkflowCreatedPayload Planner 返回的角色集 + DAG
type WorkflowCreatedPayload struct {
	TaskID    string      `json:"task_id"`
	Agents    interface{} `json:"agents"`     // []AgentConfig
	Workflow  interface{} `json:"workflow"`   // WorkflowSpec
	Rationale string      `json:"rationale"`  // LLM 设计理由
}

// WorkflowStartedPayload DAG 开始执行
type WorkflowStartedPayload struct {
	TaskID    string `json:"task_id"`
	NodeCount int    `json:"node_count"`
}

// NodeStartedPayload 节点开始执行
type NodeStartedPayload struct {
	TaskID     string `json:"task_id"`
	NodeID     string `json:"node_id"`
	AgentID    string `json:"agent_id"`
	AgentName  string `json:"agent_name"`
	Label      string `json:"label"`
}

// NodeStreamDeltaPayload 节点流式输出
type NodeStreamDeltaPayload struct {
	TaskID string `json:"task_id"`
	NodeID string `json:"node_id"`
	Delta  string `json:"delta"`
}

// NodeCompletedPayload 节点完成
type NodeCompletedPayload struct {
	TaskID         string `json:"task_id"`
	NodeID         string `json:"node_id"`
	ArtifactID     string `json:"artifact_id"`
	ArtifactType   string `json:"artifact_type"`
	DurationMs     int64  `json:"duration_ms"`
	TokensUsed     int    `json:"tokens_used"`
}

// NodeFailedPayload 节点失败
type NodeFailedPayload struct {
	TaskID      string `json:"task_id"`
	NodeID      string `json:"node_id"`
	Error       string `json:"error"`
	DurationMs  int64  `json:"duration_ms"`
}

// WorkflowCompletedPayload 整个 DAG 完成
type WorkflowCompletedPayload struct {
	TaskID      string `json:"task_id"`
	NodeCount   int    `json:"node_count"`
	TotalTokens int64 `json:"total_tokens"`
}

// WorkflowFailedPayload 整个 DAG 失败
type WorkflowFailedPayload struct {
	TaskID  string `json:"task_id"`
	Error   string `json:"error,omitempty"`
}

// WorkflowPausedPayload DAG 已暂停
type WorkflowPausedPayload struct {
	TaskID  string `json:"task_id"`
	Message string `json:"message,omitempty"`
}

// WorkflowResumedPayload DAG 已恢复
type WorkflowResumedPayload struct {
	TaskID string `json:"task_id"`
}
