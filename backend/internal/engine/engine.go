package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// AgentEngine orchestrates the writing pipeline steps.
type AgentEngine struct {
	steps   []Step
	emitter EventEmitter
}

// NewAgentEngine creates a new AgentEngine with the given steps.
func NewAgentEngine(emitter EventEmitter, steps []Step) *AgentEngine {
	return &AgentEngine{
		steps:   steps,
		emitter: emitter,
	}
}

// Run executes the full pipeline for a writing request.
func (e *AgentEngine) Run(ctx context.Context, execCtx *ExecutionContext) error {
	execCtx.Status = StatusRunning

	slog.Info("agent execution started",
		"trace_id", execCtx.TraceID,
		"user_input", execCtx.UserInput,
		"style", execCtx.StyleSlug,
		"mode", execCtx.Mode,
	)

	for i, step := range e.steps {
		// Check for cancellation before each step
		if execCtx.IsCancelled() {
			e.emitter.Cancelled()
			slog.Info("agent cancelled", "trace_id", execCtx.TraceID)
			return fmt.Errorf("cancelled")
		}

		// Check if the step should be skipped (e.g. chat intent skips writing steps)
		if skipper, ok := step.(Skipper); ok && skipper.ShouldSkip(execCtx) {
			slog.Debug("step skipped",
				"trace_id", execCtx.TraceID,
				"step", step.Name(),
			)
			continue
		}

		stepName := step.Name()
		execCtx.CurrentStep = stepName

		// Emit step.start
		e.emitter.StepStart(stepName, i)

		startTime := time.Now()

		// Record step start
		startCopy := startTime
		record := StepRecord{
			Step:      stepName,
			Status:    "running",
			StartedAt: &startCopy,
		}
		execCtx.StepHistory = append(execCtx.StepHistory, record)

		slog.Info("step starting",
			"trace_id", execCtx.TraceID,
			"step", stepName,
			"index", i,
		)

		// Execute the step
		err := step.Execute(ctx, execCtx, e.emitter)

		duration := time.Since(startTime)
		durationMs := duration.Milliseconds()

		if err != nil {
			// Check if it was a cancellation
			if err == context.Canceled || execCtx.IsCancelled() {
				e.emitter.Cancelled()
				slog.Info("agent cancelled during step", "trace_id", execCtx.TraceID, "step", stepName)
				return err
			}

			// Step failed
			e.emitter.Error("step_failed", err.Error(), stepName)
			execCtx.Status = StatusFailed

			// Update step record
			updateLastStepRecord(execCtx, stepName, "error", nil, durationMs, err.Error())

			slog.Error("step failed",
				"trace_id", execCtx.TraceID,
				"step", stepName,
				"error", err,
				"duration_ms", durationMs,
			)
			return err
		}

		// Step succeeded — get the result from execCtx based on step name
		result := getStepResult(stepName, execCtx)
		updateLastStepRecord(execCtx, stepName, "complete", result, durationMs, "")

		// Emit step.complete
		e.emitter.StepComplete(stepName, result, durationMs)

		slog.Info("step completed",
			"trace_id", execCtx.TraceID,
			"step", stepName,
			"duration_ms", durationMs,
		)

		// Check pause after pausable steps
		if step.CanPause() {
			if err := execCtx.CheckPause(ctx, e.emitter, stepName); err != nil {
				if err == context.Canceled {
					e.emitter.Cancelled()
				}
				return err
			}
		}
	}

	// Pipeline complete
	execCtx.Status = StatusCompleted

	e.emitter.Completed(
		execCtx.Article,
		execCtx.ReviewResult,
		map[string]interface{}{
			"total_tokens": execCtx.TotalTokens,
		},
	)

	slog.Info("agent execution completed",
		"trace_id", execCtx.TraceID,
		"duration_ms", time.Since(execCtx.StartedAt).Milliseconds(),
		"total_tokens", execCtx.TotalTokens,
	)

	return nil
}

// GenerateTraceID creates a new unique trace ID.
func GenerateTraceID() string {
	return "trace_" + uuid.New().String()[:8]
}

// updateLastStepRecord updates the last step record for the given step.
func updateLastStepRecord(execCtx *ExecutionContext, step StepName, status string, result interface{}, durationMs int64, errMsg string) {
	for i := len(execCtx.StepHistory) - 1; i >= 0; i-- {
		if execCtx.StepHistory[i].Step == step && execCtx.StepHistory[i].Status == "running" {
			now := time.Now()
			execCtx.StepHistory[i].Status = status
			execCtx.StepHistory[i].CompletedAt = &now
			execCtx.StepHistory[i].DurationMs = durationMs
			execCtx.StepHistory[i].Result = result
			execCtx.StepHistory[i].Error = errMsg
			break
		}
	}
}

// getStepResult extracts the result for a given step from the context.
func getStepResult(step StepName, execCtx *ExecutionContext) interface{} {
	switch step {
	case StepIntent:
		return execCtx.TaskIntent
	case StepQueryPlan:
		return map[string]interface{}{
			"search_plan":       execCtx.SearchPlan,
			"should_search":     len(execCtx.SearchPlan) > 0,
		}
	case StepSearch:
		return map[string]interface{}{
			"count":   len(execCtx.SearchResults),
			"results": execCtx.SearchResults,
		}
	case StepRelevance:
		return map[string]interface{}{
			"count":   len(execCtx.SearchResults),
		}
	case StepOutline:
		return execCtx.Outline
	case StepWrite:
		return map[string]interface{}{
			"article_length": len(execCtx.Article),
		}
	case StepPostReview:
		return execCtx.ReviewResult
	case StepAutoFix:
		return map[string]interface{}{
			"fixed": execCtx.ReviewResult != nil && execCtx.ReviewResult.Passed,
		}
	case StepChat:
		return map[string]interface{}{
			"article_length": len(execCtx.Article),
		}
	default:
		return nil
	}
}
