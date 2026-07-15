package engine

import "context"

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

// EventEmitter is the interface for emitting events to the WebSocket client.
type EventEmitter interface {
	// StepStart emits a step.start event.
	StepStart(step StepName, stepIndex int)

	// StepComplete emits a step.complete event.
	StepComplete(step StepName, result interface{}, durationMs int64)

	// StreamDelta emits a stream delta.
	StreamDelta(delta string)

	// StreamDone emits stream.done.
	StreamDone(fullText string)

	// AwaitInput emits an await_input event.
	AwaitInput(step StepName, data interface{}, options []string, attempt int, maxAttempts int)

	// Paused emits a paused event.
	Paused(step StepName, savedState interface{})

	// Resumed emits a resumed event.
	Resumed(step StepName)

	// Error emits an error event.
	Error(code, message string, step StepName)

	// Completed emits the final completed event.
	Completed(article string, review interface{}, tokenUsage interface{})

	// Cancelled emits a cancelled event.
	Cancelled()
}
