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
	MsgAgentStreamDone   = "agent.stream.done"
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
	SessionID    string   `json:"session_id,omitempty"`
	UserMaterials []string `json:"user_materials,omitempty"`
	WordLimit    int      `json:"word_limit,omitempty"`
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
	TraceID    string      `json:"trace_id"`
	Article    string      `json:"article"`
	Review     interface{} `json:"review"`
	TokenUsage interface{} `json:"token_usage"`
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
	TraceID   string      `json:"trace_id"`
	Status    string      `json:"status"`              // running | paused | completed | error | not_found
	Step      string      `json:"step,omitempty"`      // current running step
	Article   string      `json:"article,omitempty"`   // partial article text
	Style     string      `json:"style,omitempty"`
	Mode      string      `json:"mode,omitempty"`
	Outline   interface{} `json:"outline,omitempty"`   // current outline if awaiting input
	Review    interface{} `json:"review,omitempty"`    // review result if completed
	Message   string      `json:"message,omitempty"`   // error message if applicable
}
