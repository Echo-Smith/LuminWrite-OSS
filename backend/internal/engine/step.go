package engine

import (
	"context"
	"time"
)

// Step is a single pluggable stage in the writing pipeline.
type Step interface {
	// Name returns the step identifier.
	Name() StepName

	// CanPause returns true if this step supports pause/resume.
	CanPause() bool

	// Execute runs the step logic.
	// Returns error if the step fails or is cancelled.
	Execute(ctx context.Context, execCtx *ExecutionContext, emitter EventEmitter) error
}

// Skipper is an optional interface that steps can implement to indicate
// whether they should be skipped for the current execution context.
// The engine checks this before calling Execute; if ShouldSkip returns true,
// the step is silently skipped (no step.start/step.complete events emitted).
type Skipper interface {
	ShouldSkip(execCtx *ExecutionContext) bool
}

// Timeouter is an optional interface for steps that want a per-step timeout.
// If implemented and Timeout() returns a positive duration, the engine wraps
// the step's Execute call with context.WithTimeout.
type Timeouter interface {
	Timeout() time.Duration
}

// CriticalStep is an optional interface for steps to indicate whether their
// failure should terminate the entire pipeline.
//   - Critical() == true  → failure stops the pipeline (default behavior)
//   - Critical() == false → failure triggers graceful degradation (skip & continue)
//
// Steps that don't implement this interface are treated as critical by default.
type CriticalStep interface {
	Critical() bool
}

// EventEmitter is the interface for emitting events to the WebSocket client.
type EventEmitter interface {
	// StepStart emits a step.start event.
	StepStart(step StepName, stepIndex int)

	// StepComplete emits a step.complete event.
	StepComplete(step StepName, result interface{}, durationMs int64)

	// StreamDelta emits a stream delta.
	StreamDelta(delta string)

	// StreamReset emits a stream.reset event, instructing the client to
	// discard all text content streamed so far in the current step.
	// Used by the agent loop when an intermediate tool-call round
	// erroneously produced content that was optimistically streamed.
	StreamReset()

	// ReasoningDelta emits a reasoning (thinking) delta from the model.
	// This is used to visualize the model's chain-of-thought during writing.
	ReasoningDelta(delta string)

	// ArticleTitle emits the resolved article title.
	ArticleTitle(title string)

	// StreamDone emits stream.done.
	StreamDone(fullText string)

	// AwaitInput emits an await_input event.
	AwaitInput(step StepName, data interface{}, options []string, attempt int, maxAttempts int)

	// Paused emits a paused event.
	Paused(step StepName, savedState interface{})

	// PausedWithReason emits a paused event with a reason (e.g. "disconnect").
	PausedWithReason(step StepName, savedState interface{}, reason string)

	// Resumed emits a resumed event.
	Resumed(step StepName)

	// Error emits an error event.
	Error(code, message string, step StepName)

	// Completed emits the final completed event.
	Completed(article string, articleTitle string, review interface{}, tokenUsage interface{})

	// Cancelled emits a cancelled event.
	Cancelled()
}
