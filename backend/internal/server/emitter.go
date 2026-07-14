package server

import (
	"log/slog"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/websocket"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// WSEmitter implements engine.EventEmitter by sending events via WebSocket.
type WSEmitter struct {
	hub     *websocket.Hub
	traceID string
}

// NewWSEmitter creates a new WebSocket-based event emitter.
func NewWSEmitter(hub *websocket.Hub, traceID string) *WSEmitter {
	return &WSEmitter{hub: hub, traceID: traceID}
}

func (e *WSEmitter) StepStart(step engine.StepName, stepIndex int) {
	e.hub.SendToTraceDirect(e.traceID, &websocket.ServerMessage{
		Type: websocket.MsgAgentStepStart,
		Payload: websocket.StepStartPayload{
			TraceID:   e.traceID,
			Step:      string(step),
			StepIndex: stepIndex,
		},
	})
}

func (e *WSEmitter) StepComplete(step engine.StepName, result interface{}, durationMs int64) {
	e.hub.SendToTraceDirect(e.traceID, &websocket.ServerMessage{
		Type: websocket.MsgAgentStepComplete,
		Payload: websocket.StepCompletePayload{
			TraceID:    e.traceID,
			Step:       string(step),
			Result:     result,
			DurationMs: durationMs,
		},
	})
}

func (e *WSEmitter) StreamDelta(delta string) {
	e.hub.SendToTraceDirect(e.traceID, &websocket.ServerMessage{
		Type: websocket.MsgAgentStream,
		Payload: websocket.StreamPayload{
			TraceID: e.traceID,
			Delta:   delta,
		},
	})
}

func (e *WSEmitter) StreamDone(fullText string) {
	e.hub.SendToTraceDirect(e.traceID, &websocket.ServerMessage{
		Type: websocket.MsgAgentStreamDone,
		Payload: websocket.StreamDonePayload{
			TraceID:  e.traceID,
			FullText: fullText,
		},
	})
}

func (e *WSEmitter) AwaitInput(step engine.StepName, data interface{}, options []string, attempt int, maxAttempts int) {
	e.hub.SendToTraceDirect(e.traceID, &websocket.ServerMessage{
		Type: websocket.MsgAgentAwaitInput,
		Payload: websocket.AwaitInputPayload{
			TraceID:     e.traceID,
			Step:        string(step),
			Data:        data,
			Options:     options,
			Attempt:     attempt,
			MaxAttempts: maxAttempts,
		},
	})
}

func (e *WSEmitter) Paused(step engine.StepName, savedState interface{}) {
	e.hub.SendToTraceDirect(e.traceID, &websocket.ServerMessage{
		Type: websocket.MsgAgentPaused,
		Payload: websocket.PausedPayload{
			TraceID:    e.traceID,
			Step:       string(step),
			SavedState: savedState,
		},
	})
}

func (e *WSEmitter) Resumed(step engine.StepName) {
	e.hub.SendToTraceDirect(e.traceID, &websocket.ServerMessage{
		Type: websocket.MsgAgentResumed,
		Payload: websocket.ResumedPayload{
			TraceID: e.traceID,
			Step:    string(step),
		},
	})
}

func (e *WSEmitter) Error(code, message string, step engine.StepName) {
	slog.Error("agent error",
		"trace_id", e.traceID,
		"code", code,
		"message", message,
		"step", string(step),
	)
	e.hub.SendToTraceDirect(e.traceID, &websocket.ServerMessage{
		Type: websocket.MsgAgentError,
		Payload: websocket.ErrorPayload{
			TraceID: e.traceID,
			Code:    code,
			Message: message,
			Step:    string(step),
		},
	})
}

func (e *WSEmitter) Completed(article string, review interface{}, tokenUsage interface{}) {
	e.hub.SendToTraceDirect(e.traceID, &websocket.ServerMessage{
		Type: websocket.MsgAgentCompleted,
		Payload: websocket.CompletedPayload{
			TraceID:    e.traceID,
			Article:    article,
			Review:     review,
			TokenUsage: tokenUsage,
		},
	})
}

func (e *WSEmitter) Cancelled() {
	e.hub.SendToTraceDirect(e.traceID, &websocket.ServerMessage{
		Type: websocket.MsgAgentCancelled,
		Payload: websocket.CancelledPayload{
			TraceID: e.traceID,
		},
	})
}

// EmitMemoryUsed pushes the memory.used event to the client,
// showing which memories were injected for this writing session.
// Includes observability dimensions: user_id, session_id, recall counts, quality signals.
func (e *WSEmitter) EmitMemoryUsed(traceID string, memCtx *memory.MemoryContext) {
	// Calculate observability metrics
	totalRecall := len(memCtx.Injected) + len(memCtx.ReviewGuard)
	hasHighQuality := false
	var maxConfidence float64
	for _, m := range memCtx.Injected {
		if m.Confidence > maxConfidence {
			maxConfidence = m.Confidence
		}
		if m.Confidence >= 0.8 {
			hasHighQuality = true
		}
	}
	for _, m := range memCtx.ReviewGuard {
		if m.Confidence > maxConfidence {
			maxConfidence = m.Confidence
		}
	}

	e.hub.SendToTraceDirect(e.traceID, &websocket.ServerMessage{
		Type: websocket.MsgMemoryUsed,
		Payload: map[string]interface{}{
			"trace_id":          traceID,
			"injected":          memCtx.Injected,
			"review_guard":      memCtx.ReviewGuard,
			"dismissed":         memCtx.Dismissed,

			// Observability dimensions
			"recall_count":      totalRecall,
			"injected_count":    len(memCtx.Injected),
			"review_guard_count": len(memCtx.ReviewGuard),
			"dismissed_count":   len(memCtx.Dismissed),
			"max_confidence":    maxConfidence,
			"has_high_quality":  hasHighQuality,
		},
	})
}
