package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"
)

// ParallelGroup runs multiple step-branches concurrently.
// Each branch is a sequence of steps that run sequentially within the branch.
// All branches execute in parallel; the group waits for all to complete
// (or the first error cancels remaining branches via errgroup).
//
// Thread-safety contract:
//   - Steps in different branches MUST write to non-overlapping ExecutionContext fields.
//   - The EventEmitter passed to sub-steps must be goroutine-safe
//     (WSEmitter uses channel-based Hub, which is safe).
type ParallelGroup struct {
	name    StepName
	branches [][]Step
}

// NewParallelGroup creates a parallel group with the given name and branches.
// Each branch is a slice of steps that will run sequentially within that branch.
func NewParallelGroup(name StepName, branches ...[]Step) *ParallelGroup {
	return &ParallelGroup{
		name:    name,
		branches: branches,
	}
}

func (g *ParallelGroup) Name() StepName { return g.name }
func (g *ParallelGroup) CanPause() bool { return false }

// ShouldSkip returns true only if ALL sub-steps in ALL branches would be skipped.
// This allows the engine to skip the entire group when no sub-step needs to run
// (e.g., chat intent skips both MemoryGateStep and the search chain).
func (g *ParallelGroup) ShouldSkip(execCtx *ExecutionContext) bool {
	for _, branch := range g.branches {
		for _, s := range branch {
			skipper, ok := s.(Skipper)
			if !ok || !skipper.ShouldSkip(execCtx) {
				return false // at least one step would run
			}
		}
	}
	return true
}

// Execute runs all branches concurrently.
// Within each branch, steps run sequentially.
// If any branch returns an error, the context is cancelled and other branches
// will receive a cancellation signal.
func (g *ParallelGroup) Execute(ctx context.Context, execCtx *ExecutionContext, emitter EventEmitter) error {
	// Filter out fully-skipped branches, and within each branch filter skipped steps
	type activeBranch struct {
		steps []Step
	}
	var active []activeBranch
	for _, branch := range g.branches {
		var runnable []Step
		for _, s := range branch {
			skipper, ok := s.(Skipper)
			if ok && skipper.ShouldSkip(execCtx) {
				continue
			}
			runnable = append(runnable, s)
		}
		if len(runnable) > 0 {
			active = append(active, activeBranch{steps: runnable})
		}
	}

	if len(active) == 0 {
		return nil
	}

	// Single active branch — run sequentially, no need for errgroup overhead
	if len(active) == 1 {
		for _, s := range active[0].steps {
			if err := s.Execute(ctx, execCtx, emitter); err != nil {
				return err
			}
		}
		return nil
	}

	// Multiple active branches — run concurrently
	eg, parallelCtx := errgroup.WithContext(ctx)

	for branchIdx, branch := range active {
		branchIdx := branchIdx
		steps := branch.steps

		eg.Go(func() error {
			for stepIdx, s := range steps {
				if parallelCtx.Err() != nil {
					return parallelCtx.Err()
				}

				stepName := s.Name()
				startTime := time.Now()

				slog.Debug("parallel group: sub-step starting",
					"group", g.name,
					"branch", branchIdx,
					"step", stepName,
					"step_idx", stepIdx,
				)

				// Emit sub-step start (best-effort; not all emitters support this)
				if subEmitter, ok := emitter.(interface {
					SubStepStart(group StepName, step StepName, branchIdx int)
				}); ok {
					subEmitter.SubStepStart(g.name, stepName, branchIdx)
				}

				err := s.Execute(parallelCtx, execCtx, emitter)
				durationMs := time.Since(startTime).Milliseconds()

				if subEmitter, ok := emitter.(interface {
					SubStepComplete(group StepName, step StepName, branchIdx int, durationMs int64)
				}); ok {
					subEmitter.SubStepComplete(g.name, stepName, branchIdx, durationMs)
				}

				if err != nil {
					slog.Warn("parallel group: sub-step failed",
						"group", g.name,
						"branch", branchIdx,
						"step", stepName,
						"error", err,
					)
					return fmt.Errorf("parallel group %s branch %d step %s: %w", g.name, branchIdx, stepName, err)
				}

				slog.Debug("parallel group: sub-step completed",
					"group", g.name,
					"branch", branchIdx,
					"step", stepName,
					"duration_ms", durationMs,
				)
			}
			return nil
		})
	}

	return eg.Wait()
}
