package engine

import (
	"sync"
	"testing"
	"time"
)

// TestRunEventFanOut proves the unified contract: a single Record* call both
// fills the persisted array AND forwards a RunEvent to the installed sink, so
// benchmark / WABench / live timeline share one instrumentation point.
func TestRunEventFanOut(t *testing.T) {
	ctx := NewExecutionContext("trace_x", "user_x", "hello")

	var mu sync.Mutex
	var got []RunEvent
	ctx.SetTraceSink(FuncSink{Fn: func(e RunEvent) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e)
	}})

	start := time.Now()
	ctx.RecordLLMCall(LLMCallRecord{
		CallID:           "llm_1",
		Step:             StepWrite,
		Model:            "deepseek-chat",
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
		StartedAt:        start,
		CompletedAt:      start.Add(50 * time.Millisecond),
		LatencyMs:        50,
		Success:          true,
	})
	ctx.RecordToolCall(ToolCallRecord{
		CallID:     "tool_1",
		Step:       StepSearch,
		ToolName:   "search_web",
		StartedAt:  start,
		CompletedAt: start.Add(10 * time.Millisecond),
		DurationMs: 10,
		Success:    true,
	})
	ctx.EmitStepEvent(StepOutline, RunEventComplete, start, 25, "")

	if len(ctx.LLMCalls) != 1 {
		t.Fatalf("LLMCalls len = %d, want 1", len(ctx.LLMCalls))
	}
	if len(ctx.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(ctx.ToolCalls))
	}
	// ctx.TotalTokens is the caller's accumulator (steps add to it); Record*
	// only fills the per-call arrays — assert the recorded value instead.
	if ctx.LLMCalls[0].TotalTokens != 120 {
		t.Fatalf("recorded TotalTokens = %d, want 120", ctx.LLMCalls[0].TotalTokens)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("sink events = %d, want 3 (llm/tool/step)", len(got))
	}
	if got[0].Kind != TraceKindLLM || got[0].Tokens != 120 {
		t.Fatalf("event[0] = %+v, want llm kind with 120 tokens", got[0])
	}
	if got[1].Kind != TraceKindTool || got[1].Name != "search_web" {
		t.Fatalf("event[1] = %+v, want tool search_web", got[1])
	}
	if got[2].Kind != TraceKindStep || got[2].Step != string(StepOutline) {
		t.Fatalf("event[2] = %+v, want step outline", got[2])
	}
}

// TestRunEventErrorStatus maps a failed call to an error RunEvent.
func TestRunEventErrorStatus(t *testing.T) {
	ctx := NewExecutionContext("trace_e", "user_e", "hi")
	var last *RunEvent
	ctx.SetTraceSink(FuncSink{Fn: func(e RunEvent) { last = &e }})

	ctx.RecordLLMCall(LLMCallRecord{
		Step:      StepWrite,
		Model:     "m",
		Success:   false,
		Error:     "boom",
		TotalTokens: 5,
		StartedAt: time.Now(),
	})
	if last == nil {
		t.Fatal("expected a sink event")
	}
	if last.Status != RunEventError {
		t.Fatalf("status = %s, want error", last.Status)
	}
}

// TestNoSinkIsSafe ensures Record* works with no sink installed (nil-safe),
// which is the default for existing call paths.
func TestNoSinkIsSafe(t *testing.T) {
	ctx := NewExecutionContext("trace_n", "user_n", "hi")
	ctx.RecordLLMCall(LLMCallRecord{Step: StepWrite, Success: true, TotalTokens: 3})
	if len(ctx.LLMCalls) != 1 || ctx.LLMCalls[0].TotalTokens != 3 {
		t.Fatalf("arrays not filled with nil sink: llm=%d tokens=%d", len(ctx.LLMCalls), ctx.LLMCalls[0].TotalTokens)
	}
}
