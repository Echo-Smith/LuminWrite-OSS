package engine

import "time"

// ─── Unified RunEvent contract ───────────────────────────────────────────────
//
// A single, transport-neutral event that every producer emits as the run
// happens: the LLM client wrapper, the tool executor, and the pipeline step
// runner. One stream feeds three consumers without duplicating instrumentation:
//
//   - ExecutionContext.LLMCalls / ToolCalls arrays  (persisted via CompleteTrace)
//   - a TraceSink: the live WebSocket timeline (debug UX), the benchmark runner,
//     and WABench's trace_ref->agent_traces path all read the same events.
//
// This mirrors llm-space, whose workflow `report({type,label,status})` IS the
// trace source — the primitives that run the pipeline also produce the trace,
// so there is never a second place to instrument.

// TraceKind classifies what a RunEvent describes.
type TraceKind string

const (
	TraceKindLLM  TraceKind = "llm"
	TraceKindTool TraceKind = "tool"
	TraceKindStep TraceKind = "step"
)

// RunEventStatus is the lifecycle position of a RunEvent.
type RunEventStatus string

const (
	RunEventStart    RunEventStatus = "start"
	RunEventComplete RunEventStatus = "complete"
	RunEventError    RunEventStatus = "error"
)

// RunEvent is the unified observability event for one unit of work.
// Callers usually use the Emit*/Record* helpers rather than building it by hand.
type RunEvent struct {
	Kind     TraceKind      `json:"kind"`
	Status   RunEventStatus `json:"status"`
	CallID   string         `json:"call_id,omitempty"`
	Step     string         `json:"step"`
	Name     string         `json:"name,omitempty"` // model id (llm), tool name (tool), step name (step)
	StartedAt time.Time     `json:"started_at"`
	CompletedAt time.Time   `json:"completed_at,omitempty"`
	DurationMs int64        `json:"duration_ms,omitempty"`
	Tokens   int            `json:"tokens,omitempty"`
	Cost     float64        `json:"cost,omitempty"`
	Error    string         `json:"error,omitempty"`

	// Raw carries the typed underlying record (*LLMCallRecord / ToolCallRecord /
	// StepRecord) for consumers that need full detail. Never serialized.
	Raw any `json:"-"`
}

// TraceSink receives the ordered RunEvent stream for one execution. Implement
// must be safe for concurrent use when the pipeline runs parallel steps
// (see engine/parallel.go).
type TraceSink interface {
	EmitRunEvent(RunEvent)
}

// FuncSink adapts a function to TraceSink; handy for tests and forwarding.
type FuncSink struct {
	Fn func(RunEvent)
}

func (s FuncSink) EmitRunEvent(e RunEvent) {
	if s.Fn != nil {
		s.Fn(e)
	}
}

// SetTraceSink installs (or replaces) the live event sink for this run.
// It is intentionally outside the persisted JSON (see context.go field).
func (ctx *ExecutionContext) SetTraceSink(sink TraceSink) {
	ctx.traceMu.Lock()
	ctx.traceSink = sink
	ctx.traceMu.Unlock()
}

// emitRunEvent forwards to the installed sink if any. Cheap and nil-safe, so
// producers can call it unconditionally.
func (ctx *ExecutionContext) emitRunEvent(e RunEvent) {
	ctx.traceMu.Lock()
	sink := ctx.traceSink
	ctx.traceMu.Unlock()
	if sink != nil {
		sink.EmitRunEvent(e)
	}
}

// EmitStepEvent records a pipeline step lifecycle event. The step arrays
// (StepHistory) remain the durable record; this is the live-stream mirror.
func (ctx *ExecutionContext) EmitStepEvent(step StepName, status RunEventStatus, startedAt time.Time, durationMs int64, errMsg string) {
	ctx.emitRunEvent(RunEvent{
		Kind:       TraceKindStep,
		Status:     status,
		Step:       string(step),
		Name:       string(step),
		StartedAt:  startedAt,
		DurationMs: durationMs,
		Error:      errMsg,
	})
}

// runEventFromLLM maps a completed LLM call record to a RunEvent.
func runEventFromLLM(r LLMCallRecord) RunEvent {
	status := RunEventComplete
	if !r.Success {
		status = RunEventError
	}
	return RunEvent{
		Kind:        TraceKindLLM,
		Status:      status,
		CallID:      r.CallID,
		Step:        string(r.Step),
		Name:        r.Model,
		StartedAt:   r.StartedAt,
		CompletedAt: r.CompletedAt,
		DurationMs:  r.LatencyMs,
		Tokens:      r.TotalTokens,
		Error:       r.Error,
		Raw:         r,
	}
}

// runEventFromTool maps a completed tool call record to a RunEvent.
func runEventFromTool(r ToolCallRecord) RunEvent {
	status := RunEventComplete
	if !r.Success {
		status = RunEventError
	}
	return RunEvent{
		Kind:        TraceKindTool,
		Status:      status,
		CallID:      r.CallID,
		Step:        string(r.Step),
		Name:        r.ToolName,
		StartedAt:   r.StartedAt,
		CompletedAt: r.CompletedAt,
		DurationMs:  r.DurationMs,
		Error:       r.Error,
		Raw:         r,
	}
}
