package websocket

import "encoding/json"

// ClientMessage types (client → server)
const (
	MsgAgentStart   = "agent.start"
	MsgAgentPause   = "agent.pause"
	MsgAgentResume  = "agent.resume"
	MsgAgentCancel  = "agent.cancel"
	MsgAgentConfirm = "agent.confirm"
	MsgAgentEdit    = "agent.edit"
	MsgFeedback     = "feedback.submit"
	MsgSessionResume = "session.resume"
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
