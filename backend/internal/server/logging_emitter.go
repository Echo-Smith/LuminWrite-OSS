package server

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── LoggingEmitter: Append-only Event Log Wrapper ──────
//
// LoggingEmitter wraps an EventEmitter (typically WSEmitter) and
// records every event to the session_events table in addition to
// forwarding it to the WebSocket client.
//
// This provides:
//   - Full session replay capability (reconstruct UI from events)
//   - Fork-from-step support (re-run from any event boundary)
//   - Compliance audit trail (immutable, append-only)
//   - Telemetry export for evaluation pipelines
//
// Design decisions:
//   - Events are written asynchronously via a buffered channel to avoid
//     blocking the WS path (streaming performance is critical).
//   - The channel has a bounded buffer; if the buffer fills (DB slow),
//     events are dropped with a counter increment (never block WS).
//   - On agent completion, a flush is called to drain remaining events.
//
// Inspired by:
//   - dsh's session-log: structured JSON events, not free-text logs
//   - Pi Agent's stream-json: each event is a self-contained JSON object
//   - OpenAI Assistants API's run-steps: discrete, queryable steps

// EventLogLevel controls the verbosity of the event log.
type EventLogLevel int

const (
	// EventLogAll records all events including stream.delta and reasoning.delta.
	// This produces the most complete log but can be very large for long articles.
	EventLogAll EventLogLevel = iota
	// EventLogCoarse records only structural events (step.start/complete, paused,
	// resumed, error, completed, cancelled). Stream deltas are omitted.
	// This is the default level — sufficient for replay and fork.
	EventLogCoarse
	// EventLogErrorsOnly records only error, cancelled, and circuit_breaker events.
	EventLogErrorsOnly
)

// LoggingEmitter wraps another EventEmitter and records events to the database.
type LoggingEmitter struct {
	inner  engine.EventEmitter // the wrapped emitter (typically WSEmitter)
	repo   *database.SessionEventRepo
	trace  string
	level  EventLogLevel

	// Async write queue: events are buffered and written in a background goroutine
	// to avoid blocking the WebSocket forwarding path.
	queue   chan logEntry
	dropped atomic.Int64 // count of events dropped due to full buffer
	wg      sync.WaitGroup
}

// logEntry is a single queued event to be written to the database.
type logEntry struct {
	eventType string
	step      string
	data      map[string]interface{}
}

const eventQueueSize = 512 // bounded buffer; drops events if DB is slow

// NewLoggingEmitter wraps the given emitter with event logging.
// If repo is nil, the wrapper is a no-op (just forwards to inner).
func NewLoggingEmitter(inner engine.EventEmitter, repo *database.SessionEventRepo, traceID string, level EventLogLevel) *LoggingEmitter {
	e := &LoggingEmitter{
		inner:  inner,
		repo:   repo,
		trace:  traceID,
		level:  level,
		queue:  make(chan logEntry, eventQueueSize),
	}

	// Start background writer goroutine
	e.wg.Add(1)
	go e.writeLoop()

	return e
}

// writeLoop processes the event queue and writes events to the database.
func (e *LoggingEmitter) writeLoop() {
	defer e.wg.Done()

	for entry := range e.queue {
		if e.repo == nil {
			continue
		}
		if err := e.repo.AppendEvent(context.Background(), e.trace, entry.eventType, entry.step, entry.data); err != nil {
			slog.Warn("session event log write failed",
				"error", err,
				"trace_id", e.trace,
				"event_type", entry.eventType,
			)
		}
	}
}

// enqueue attempts to add an event to the async write queue.
// If the queue is full, the event is dropped (never block the WS path).
func (e *LoggingEmitter) enqueue(eventType, step string, data map[string]interface{}) {
	if e.repo == nil {
		return
	}
	select {
	case e.queue <- logEntry{eventType: eventType, step: step, data: data}:
	default:
		e.dropped.Add(1)
		if c := e.dropped.Load(); c%100 == 1 {
			slog.Warn("session event log: queue full, dropping events",
				"trace_id", e.trace,
				"dropped_count", c,
			)
		}
	}
}

// Flush drains the write queue and waits for all pending events to be written.
// Call this before the emitter is discarded (e.g. after agent completion).
func (e *LoggingEmitter) Flush() {
	close(e.queue)
	e.wg.Wait()
}

// ─── EventEmitter interface implementation ──────────────

func (e *LoggingEmitter) StepStart(step engine.StepName, stepIndex int) {
	e.inner.StepStart(step, stepIndex)
	if e.level <= EventLogCoarse {
		e.enqueue(EventStepStart, string(step), map[string]interface{}{
			"step_index": stepIndex,
		})
	}
}

func (e *LoggingEmitter) StepComplete(step engine.StepName, result interface{}, durationMs int64) {
	e.inner.StepComplete(step, result, durationMs)
	if e.level <= EventLogCoarse {
		data := map[string]interface{}{
			"duration_ms": durationMs,
		}
		if result != nil {
			data["result"] = result
		}
		e.enqueue(EventStepComplete, string(step), data)
	}
}

func (e *LoggingEmitter) StreamDelta(delta string) {
	e.inner.StreamDelta(delta)
	if e.level == EventLogAll {
		e.enqueue(EventStreamDelta, "", map[string]interface{}{
			"delta": delta,
		})
	}
}

func (e *LoggingEmitter) StreamReset() {
	e.inner.StreamReset()
	if e.level == EventLogAll {
		e.enqueue(EventStreamReset, "", nil)
	}
}

func (e *LoggingEmitter) ReasoningDelta(delta string) {
	e.inner.ReasoningDelta(delta)
	if e.level == EventLogAll {
		e.enqueue(EventReasoningDelta, "", map[string]interface{}{
			"delta": delta,
		})
	}
}

func (e *LoggingEmitter) ArticleTitle(title string) {
	e.inner.ArticleTitle(title)
	e.enqueue(EventArticleTitle, "", map[string]interface{}{
		"title": title,
	})
}

func (e *LoggingEmitter) StreamDone(fullText string) {
	e.inner.StreamDone(fullText)
	e.enqueue(EventStreamDone, "", map[string]interface{}{
		"full_text_length": len(fullText),
	})
}

func (e *LoggingEmitter) AwaitInput(step engine.StepName, data interface{}, options []string, attempt int, maxAttempts int) {
	e.inner.AwaitInput(step, data, options, attempt, maxAttempts)
	e.enqueue(EventAwaitInput, string(step), map[string]interface{}{
		"data":         data,
		"options":      options,
		"attempt":      attempt,
		"max_attempts": maxAttempts,
	})
}

func (e *LoggingEmitter) Paused(step engine.StepName, savedState interface{}) {
	e.inner.Paused(step, savedState)
	e.enqueue(EventPaused, string(step), map[string]interface{}{
		"saved_state": savedState,
	})
}

func (e *LoggingEmitter) PausedWithReason(step engine.StepName, savedState interface{}, reason string) {
	e.inner.PausedWithReason(step, savedState, reason)
	e.enqueue(EventPaused, string(step), map[string]interface{}{
		"saved_state": savedState,
		"reason":      reason,
	})
}

func (e *LoggingEmitter) Resumed(step engine.StepName) {
	e.inner.Resumed(step)
	e.enqueue(EventResumed, string(step), nil)
}

func (e *LoggingEmitter) Error(code, message string, step engine.StepName) {
	e.inner.Error(code, message, step)
	e.enqueue(EventError, string(step), map[string]interface{}{
		"code":    code,
		"message": message,
	})
}

func (e *LoggingEmitter) Completed(article string, articleTitle string, review interface{}, tokenUsage interface{}) {
	e.inner.Completed(article, articleTitle, review, tokenUsage)
	e.enqueue(EventCompleted, "", map[string]interface{}{
		"article_title":  articleTitle,
		"article_length": len(article),
		"review":         review,
		"token_usage":    tokenUsage,
	})
	// Flush after completion to ensure all events are persisted
	go e.Flush()
}

func (e *LoggingEmitter) Cancelled() {
	e.inner.Cancelled()
	e.enqueue(EventCancelled, "", nil)
	go e.Flush()
}

func (e *LoggingEmitter) Compaction(originalMessages, savedTokens int, summaryPreview string) {
	e.inner.Compaction(originalMessages, savedTokens, summaryPreview)
	e.enqueue(EventCompaction, "", map[string]interface{}{
		"original_messages": originalMessages,
		"saved_tokens":     savedTokens,
		"summary_preview":  summaryPreview,
	})
}

// EmitMemoryUsed delegates to WSEmitter's memory event.
// This is not part of the EventEmitter interface but is called directly
// by the server. We pass it through to the inner emitter if it supports it.
func (e *LoggingEmitter) EmitMemoryUsed(traceID string, memCtx *memory.MemoryContext) {
	if ws, ok := e.inner.(*WSEmitter); ok {
		ws.EmitMemoryUsed(traceID, memCtx)
	}
	e.enqueue(EventMemoryUsed, "", map[string]interface{}{
		"trace_id":           traceID,
		"injected_count":     len(memCtx.Injected),
		"review_guard_count": len(memCtx.ReviewGuard),
		"dismissed_count":    len(memCtx.Dismissed),
	})
}

// ─── Event Type Constants ────────────────────────────────
//
// These are the canonical event type strings stored in the
// session_events.event_type column. They match the WebSocket
// message types for consistency, but use dot notation for
// hierarchical grouping.

const (
	EventStepStart      = "step.start"
	EventStepComplete   = "step.complete"
	EventStreamDelta    = "stream.delta"
	EventStreamReset    = "stream.reset"
	EventStreamDone     = "stream.done"
	EventReasoningDelta = "reasoning.delta"
	EventArticleTitle   = "article.title"
	EventAwaitInput     = "await_input"
	EventPaused         = "paused"
	EventResumed        = "resumed"
	EventError          = "error"
	EventCompleted      = "completed"
	EventCancelled      = "cancelled"
	EventMemoryUsed     = "memory.used"
	EventCompaction     = "agent.compaction"
)
