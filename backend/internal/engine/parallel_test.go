package engine

import (
	"context"
	"sync"
	"testing"
)

// ─── ParallelGroup Tests ──────────────────────────────────

// slowStep is a step that sleeps for a configurable duration.
type slowStep struct {
	name     StepName
	duration int // nanoseconds
	wg       *sync.WaitGroup
}

func (s *slowStep) Name() StepName         { return s.name }
func (s *slowStep) CanPause() bool          { return false }
func (s *slowStep) Execute(ctx context.Context, execCtx *ExecutionContext, emitter EventEmitter) error {
	if s.wg != nil {
		defer s.wg.Done()
	}
	// Simulate work
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}

func TestParallelGroup_AllSkip(t *testing.T) {
	stepA := &mockSkipperStep{
		mockStep:   mockStep{name: "skip_a"},
		shouldSkip: true,
	}
	stepB := &mockSkipperStep{
		mockStep:   mockStep{name: "skip_b"},
		shouldSkip: true,
	}

	group := NewParallelGroup("test_group", []Step{stepA}, []Step{stepB})
	execCtx := NewExecutionContext("trace_test", "user1", "test")

	if !group.ShouldSkip(execCtx) {
		t.Error("expected ShouldSkip=true when all sub-steps skip")
	}

	err := group.Execute(context.Background(), execCtx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParallelGroup_OneSkips(t *testing.T) {
	stepA := &mockSkipperStep{
		mockStep:   mockStep{name: "run_a"},
		shouldSkip: false,
	}
	stepB := &mockSkipperStep{
		mockStep:   mockStep{name: "skip_b"},
		shouldSkip: true,
	}

	group := NewParallelGroup("test_group", []Step{&stepA.mockStep}, []Step{&stepB.mockStep})
	execCtx := NewExecutionContext("trace_test", "user1", "test")

	if group.ShouldSkip(execCtx) {
		t.Error("expected ShouldSkip=false when at least one sub-step runs")
	}
}

func TestParallelGroup_BothRun(t *testing.T) {
	stepA := &slowStep{name: "step_a"}
	stepB := &slowStep{name: "step_b"}

	group := NewParallelGroup("test_group", []Step{stepA}, []Step{stepB})
	execCtx := NewExecutionContext("trace_test", "user1", "test")

	err := group.Execute(context.Background(), execCtx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParallelGroup_SingleBranch(t *testing.T) {
	stepA := &slowStep{name: "only_step"}
	group := NewParallelGroup("test_group", []Step{stepA})
	execCtx := NewExecutionContext("trace_test", "user1", "test")

	err := group.Execute(context.Background(), execCtx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParallelGroup_Empty(t *testing.T) {
	group := NewParallelGroup("test_group")
	execCtx := NewExecutionContext("trace_test", "user1", "test")

	err := group.Execute(context.Background(), execCtx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParallelGroup_Name(t *testing.T) {
	group := NewParallelGroup("my_group")
	if group.Name() != "my_group" {
		t.Errorf("expected name 'my_group', got '%s'", group.Name())
	}
}

func TestParallelGroup_CanPause(t *testing.T) {
	group := NewParallelGroup("my_group")
	if group.CanPause() {
		t.Error("expected CanPause=false")
	}
}
