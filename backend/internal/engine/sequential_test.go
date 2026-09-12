package engine

import (
	"context"
	"errors"
	"testing"
)

type recordingStep struct {
	name  StepName
	skip  bool
	err   error
	order *[]string
	calls *int
}

func (s *recordingStep) Name() StepName                    { return s.name }
func (s *recordingStep) CanPause() bool                    { return false }
func (s *recordingStep) ShouldSkip(*ExecutionContext) bool { return s.skip }
func (s *recordingStep) Execute(context.Context, *ExecutionContext, EventEmitter) error {
	*s.calls++
	*s.order = append(*s.order, string(s.name))
	return s.err
}

func TestSequentialGroupRunsInOrderAndShortCircuits(t *testing.T) {
	var order []string
	calls := 0
	boom := errors.New("step failed")
	group := NewSequentialGroup("research",
		&recordingStep{name: "a", order: &order, calls: &calls},
		&recordingStep{name: "b", err: boom, order: &order, calls: &calls},
		&recordingStep{name: "c", order: &order, calls: &calls},
	)
	err := group.Execute(context.Background(), &ExecutionContext{}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("expected sub-step error to propagate, got %v", err)
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("group did not stop at the failing step: %v", order)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want 2 (c must not run)", calls)
	}
}

func TestSequentialGroupSkipsPerSubStep(t *testing.T) {
	var order []string
	calls := 0
	group := NewSequentialGroup("research",
		&recordingStep{name: "a", skip: true, order: &order, calls: &calls},
		&recordingStep{name: "b", order: &order, calls: &calls},
	)
	if group.ShouldSkip(&ExecutionContext{}) {
		t.Fatal("group with one runnable step must not skip")
	}
	if err := group.Execute(context.Background(), &ExecutionContext{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || order[0] != "b" {
		t.Fatalf("skipped sub-step ran: %v", order)
	}
}

func TestSequentialGroupAllSkip(t *testing.T) {
	var order []string
	calls := 0
	group := NewSequentialGroup("research",
		&recordingStep{name: "a", skip: true, order: &order, calls: &calls},
		&recordingStep{name: "b", skip: true, order: &order, calls: &calls},
	)
	if !group.ShouldSkip(&ExecutionContext{}) {
		t.Fatal("group of all-skippable steps must report skip")
	}
	if err := group.Execute(context.Background(), &ExecutionContext{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(order) != 0 {
		t.Fatalf("all-skippable group ran steps: %v", order)
	}
}
