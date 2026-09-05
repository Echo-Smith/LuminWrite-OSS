package engine

import "context"

// SequentialGroup runs a fixed list of steps in order as a single Step, so a
// multi-stage branch (e.g. the research chain QueryPlan→Search→Relevance→
// Compress) can be bound to one governed capability node. It mirrors
// ParallelGroup's skip semantics: the group is skipped only when every
// sub-step would skip, and Execute runs the runnable sub-steps in order,
// stopping at the first error.
type SequentialGroup struct {
	name  StepName
	steps []Step
}

func NewSequentialGroup(name StepName, steps ...Step) *SequentialGroup {
	return &SequentialGroup{name: name, steps: steps}
}

func (g *SequentialGroup) Name() StepName { return g.name }

// CanPause is false: a composite pauses only at sub-step boundaries, which
// the governed runtime does not surface as a resumable step pause.
func (g *SequentialGroup) CanPause() bool { return false }

// ShouldSkip returns true only when every sub-step would skip, so the engine
// can bypass the whole group (e.g. a chat intent that skips the search chain).
func (g *SequentialGroup) ShouldSkip(execCtx *ExecutionContext) bool {
	if len(g.steps) == 0 {
		return true
	}
	for _, step := range g.steps {
		if skipper, ok := step.(Skipper); !ok || !skipper.ShouldSkip(execCtx) {
			return false
		}
	}
	return true
}

func (g *SequentialGroup) Execute(ctx context.Context, execCtx *ExecutionContext, emitter EventEmitter) error {
	for _, step := range g.steps {
		if skipper, ok := step.(Skipper); ok && skipper.ShouldSkip(execCtx) {
			continue
		}
		if err := step.Execute(ctx, execCtx, emitter); err != nil {
			return err
		}
	}
	return nil
}
