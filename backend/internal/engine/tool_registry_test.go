package engine

import (
	"context"
	"fmt"
	"testing"
)

// ─── ToolRegistry Tests ──────────────────────────────────

// mockTool is a test AgentTool implementation.
type mockTool struct {
	name        string
	description string
	execResult  *ToolResult
	execError   error
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string  { return m.description }
func (m *mockTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (m *mockTool) Execute(ctx context.Context, args map[string]any, execCtx *ExecutionContext, emitter EventEmitter) (*ToolResult, error) {
	if m.execError != nil {
		return nil, m.execError
	}
	if m.execResult == nil {
		return &ToolResult{Summary: "ok"}, nil
	}
	return m.execResult, nil
}

func TestToolRegistry_RegisterAndGet(t *testing.T) {
	r := NewToolRegistry()
	tool := &mockTool{name: "test_tool", description: "A test tool"}
	r.Register(tool)

	got := r.Get("test_tool")
	if got == nil {
		t.Fatal("expected to get registered tool")
	}
	if got.Name() != "test_tool" {
		t.Errorf("expected name 'test_tool', got '%s'", got.Name())
	}
}

func TestToolRegistry_GetNonExistent(t *testing.T) {
	r := NewToolRegistry()
	if r.Get("nonexistent") != nil {
		t.Fatal("expected nil for non-existent tool")
	}
}

func TestToolRegistry_All(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&mockTool{name: "tool_a", description: "A"})
	r.Register(&mockTool{name: "tool_b", description: "B"})

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(all))
	}
}

func TestToolRegistry_ExecuteTool(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&mockTool{
		name:       "exec_test",
		execResult: &ToolResult{Summary: "executed", Done: false},
	})

	execCtx := NewExecutionContext("trace_test", "user1", "test input")
	result, err := r.ExecuteTool(context.Background(), "exec_test", nil, execCtx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary != "executed" {
		t.Errorf("expected summary 'executed', got '%s'", result.Summary)
	}
}

func TestToolRegistry_ExecuteUnknownTool(t *testing.T) {
	r := NewToolRegistry()
	execCtx := NewExecutionContext("trace_test", "user1", "test input")
	_, err := r.ExecuteTool(context.Background(), "unknown", nil, execCtx, nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestToolRegistry_ToolDefs(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&mockTool{name: "tool1", description: "First tool"})
	r.Register(&mockTool{name: "tool2", description: "Second tool"})

	defs := r.ToolDefs()
	if len(defs) != 2 {
		t.Fatalf("expected 2 defs, got %d", len(defs))
	}
}

// ─── StepTool Tests ──────────────────────────────────────

// mockStep is a test Step implementation.
type mockStep struct {
	name      StepName
	canPause  bool
	executed  bool
	skipResult bool
}

func (m *mockStep) Name() StepName         { return m.name }
func (m *mockStep) CanPause() bool          { return m.canPause }
func (m *mockStep) Execute(ctx context.Context, execCtx *ExecutionContext, emitter EventEmitter) error {
	m.executed = true
	return nil
}

// mockSkipperStep implements Skipper
type mockSkipperStep struct {
	mockStep
	shouldSkip bool
}

func (m *mockSkipperStep) ShouldSkip(execCtx *ExecutionContext) bool {
	return m.shouldSkip
}

func TestStepTool_BasicExecution(t *testing.T) {
	step := &mockStep{name: "test_step", canPause: false}
	tool := NewStepTool(step, "A test step", false)

	execCtx := NewExecutionContext("trace_test", "user1", "test")
	result, err := tool.Execute(context.Background(), nil, execCtx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if !step.executed {
		t.Error("expected step to be executed")
	}
}

func TestStepTool_ShouldSkip(t *testing.T) {
	step := &mockSkipperStep{
		mockStep:   mockStep{name: "skip_step"},
		shouldSkip: true,
	}
	// Pass the mockSkipperStep directly (it implements both Step and Skipper)
	tool := NewStepTool(step, "A skip step", false)

	execCtx := NewExecutionContext("trace_test", "user1", "test")
	result, err := tool.Execute(context.Background(), nil, execCtx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if step.executed {
		t.Error("expected step to be skipped, not executed")
	}
	if result.Summary == "" {
		t.Error("expected non-empty summary even when skipped")
	}
}

func TestStepTool_TerminalDone(t *testing.T) {
	step := &mockStep{name: "write"}
	tool := NewStepTool(step, "Write step", true)

	execCtx := NewExecutionContext("trace_test", "user1", "test")
	execCtx.Article = "some article content"
	result, err := tool.Execute(context.Background(), nil, execCtx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Done {
		t.Error("expected Done=true for terminal step with article")
	}
}

func TestStepTool_TerminalNotDone(t *testing.T) {
	step := &mockStep{name: "write"}
	tool := NewStepTool(step, "Write step", true)

	execCtx := NewExecutionContext("trace_test", "user1", "test")
	// Article is empty → Done should be false
	result, _ := tool.Execute(context.Background(), nil, execCtx, nil)
	if result.Done {
		t.Error("expected Done=false when article is empty")
	}
}

func TestSummarizeStepResult(t *testing.T) {
	execCtx := NewExecutionContext("trace_test", "user1", "test")
	execCtx.TaskIntent = &TaskIntent{TaskMode: "writing", Confidence: 0.95, Source: "rules"}
	execCtx.SearchResults = []SearchResult{{Title: "test", Snippet: "snippet"}}
	execCtx.Article = "这是一篇文章"

	tests := []struct {
		step    StepName
		wantSub string
	}{
		{StepIntent, "意图分类完成"},
		{StepSearch, "搜索完成"},
		{StepWrite, "文章生成完成"},
	}

	for _, tt := range tests {
		got := summarizeStepResult(tt.step, execCtx)
		if len(got) == 0 {
			t.Errorf("summarizeStepResult(%s): empty summary", tt.step)
		}
	}
}

// ─── FunctionTool Tests ──────────────────────────────────

func TestFunctionTool_Basic(t *testing.T) {
	tool := NewFunctionTool(
		"search_web",
		"Search the web",
		map[string]any{"type": "object"},
		func(ctx context.Context, args map[string]any, execCtx *ExecutionContext) (string, error) {
			return "search results here", nil
		},
	)

	if tool.Name() != "search_web" {
		t.Errorf("expected name 'search_web', got '%s'", tool.Name())
	}

	execCtx := NewExecutionContext("trace_test", "user1", "test")
	result, err := tool.Execute(context.Background(), nil, execCtx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary != "search results here" {
		t.Errorf("unexpected summary: %s", result.Summary)
	}
}

func TestFunctionTool_Truncation(t *testing.T) {
	longText := fmt.Sprintf("%5000s", "x")
	tool := NewFunctionTool(
		"long_tool",
		"Produces long output",
		map[string]any{"type": "object"},
		func(ctx context.Context, args map[string]any, execCtx *ExecutionContext) (string, error) {
			return longText, nil
		},
	)

	execCtx := NewExecutionContext("trace_test", "user1", "test")
	result, err := tool.Execute(context.Background(), nil, execCtx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2000 chars + "...(截断)" suffix should be well under 5000
	if len(result.Summary) > 2100 {
		t.Errorf("expected truncation to ~2000 chars, got length %d", len(result.Summary))
	}
}

func TestFunctionTool_Error(t *testing.T) {
	tool := NewFunctionTool(
		"error_tool",
		"Always errors",
		map[string]any{"type": "object"},
		func(ctx context.Context, args map[string]any, execCtx *ExecutionContext) (string, error) {
			return "", fmt.Errorf("tool error")
		},
	)

	execCtx := NewExecutionContext("trace_test", "user1", "test")
	_, err := tool.Execute(context.Background(), nil, execCtx, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
