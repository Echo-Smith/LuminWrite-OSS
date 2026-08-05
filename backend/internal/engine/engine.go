package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// StepHook is called after each step completes (or fails/degrades).
// It allows external observers (e.g., TraceRepo) to persist intermediate state.
type StepHook func(ctx context.Context, execCtx *ExecutionContext)

// AgentEngine orchestrates the writing pipeline steps.
type AgentEngine struct {
	steps   []Step
	emitter EventEmitter
	hook    StepHook // optional callback after each step
}

// NewAgentEngine creates a new AgentEngine with the given steps.
func NewAgentEngine(emitter EventEmitter, steps []Step) *AgentEngine {
	return &AgentEngine{
		steps:   steps,
		emitter: emitter,
	}
}

// SetStepHook sets a callback that is invoked after each step completes,
// fails, or degrades. This is used to persist trace state to the database
// in real-time (step-by-step) rather than only at the end.
func (e *AgentEngine) SetStepHook(hook StepHook) {
	e.hook = hook
}

// Run executes the full pipeline for a writing request.
func (e *AgentEngine) Run(ctx context.Context, execCtx *ExecutionContext) error {
	execCtx.Status = StatusRunning

	slog.Info("agent execution started",
		"trace_id", execCtx.TraceID,
		"user_input", execCtx.UserInput,
		"style", execCtx.StyleSlug,
		"mode", execCtx.Mode,
		"max_tokens", execCtx.MaxTokens,
	)

	for i, step := range e.steps {
		// ── Exit check: cancellation ──
		if execCtx.IsCancelled() {
			e.emitter.Cancelled()
			slog.Info("agent cancelled", "trace_id", execCtx.TraceID)
			return fmt.Errorf("cancelled")
		}

		// ── Exit check: global context timeout ──
		if ctx.Err() != nil {
			e.emitter.Error("timeout", "执行超时（超过全局时间限制）", execCtx.CurrentStep)
			execCtx.Status = StatusFailed
			return ctx.Err()
		}

		// ── Exit check: token budget ──
		if execCtx.CheckBudget() {
			e.emitter.Error("budget_exceeded",
				fmt.Sprintf("Token 预算已用尽（已用 %d / 上限 %d）", execCtx.TotalTokens, execCtx.MaxTokens),
				execCtx.CurrentStep)
			execCtx.Status = StatusFailed
			return ErrBudgetExceeded
		}

		// ── Exit check: client disconnected ──
		if execCtx.IsDisconnected() {
			slog.Info("client disconnected, pausing agent", "trace_id", execCtx.TraceID, "step", step.Name())
			execCtx.Status = StatusPaused
			e.emitter.PausedWithReason(step.Name(), nil, "disconnect")
			return nil
		}

		// ── Exit check: circuit breaker ──
		if execCtx.MaxLLMFails > 0 && execCtx.ConsecutiveLLMFails >= execCtx.MaxLLMFails {
			e.emitter.Error("circuit_breaker",
				fmt.Sprintf("LLM 连续失败 %d 次，已触发断路器", execCtx.ConsecutiveLLMFails),
				execCtx.CurrentStep)
			execCtx.Status = StatusFailed
			return ErrCircuitBreaker
		}

		// Check if the step should be skipped
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

		// ── Per-step timeout ──
		// If the step implements Timeouter, wrap its context with a deadline.
		stepCtx := ctx
		stepCancel := func() {} // no-op by default
		if t, ok := step.(Timeouter); ok {
			if d := t.Timeout(); d > 0 {
				stepCtx, stepCancel = context.WithTimeout(ctx, d)
			}
		}

		// Execute the step
		err := step.Execute(stepCtx, execCtx, e.emitter)
		stepCancel()

		duration := time.Since(startTime)
		durationMs := duration.Milliseconds()

		if err != nil {
			// Check if it was a cancellation
			if err == context.Canceled || execCtx.IsCancelled() {
				e.emitter.Cancelled()
				slog.Info("agent cancelled during step", "trace_id", execCtx.TraceID, "step", stepName)
				return err
			}

			// Check if it was a client disconnect
			if err == ErrClientDisconnected {
				slog.Info("client disconnected during step", "trace_id", execCtx.TraceID, "step", stepName)
				execCtx.Status = StatusPaused
				e.emitter.PausedWithReason(stepName, nil, "disconnect")
				return nil
			}

			// ── Graceful degradation for non-critical steps ──
			isTimeout := errors.Is(err, context.DeadlineExceeded)
			isCritical := true
			if cs, ok := step.(CriticalStep); ok {
				isCritical = cs.Critical()
			}

			if !isCritical && (isTimeout || isLLMError(err)) {
				// Degraded: skip this step and continue
				slog.Warn("non-critical step failed, degrading",
					"trace_id", execCtx.TraceID,
					"step", stepName,
					"error", err,
					"is_timeout", isTimeout,
					"duration_ms", durationMs,
				)
			updateLastStepRecord(execCtx, stepName, "degraded", nil, durationMs, err.Error())
			e.emitter.StepComplete(stepName, map[string]interface{}{
				"degraded": true,
				"error":    err.Error(),
			}, durationMs)
			if e.hook != nil {
				e.hook(ctx, execCtx)
			}
			continue
			}

			// ── Quota exceeded: hard stop, no retry ──
			if isQuotaExceeded(err) {
				slog.Error("LLM API quota exceeded, stopping pipeline",
					"trace_id", execCtx.TraceID,
					"step", stepName,
					"error", err,
				)
				e.emitter.Error("quota_exceeded",
					"AI 模型服务额度不足，请联系管理员充值",
						stepName)
				execCtx.Status = StatusFailed
				updateLastStepRecord(execCtx, stepName, "error", nil, durationMs, err.Error())
				if e.hook != nil {
					e.hook(ctx, execCtx)
				}
				return ErrQuotaExceeded
				}

			// ── Circuit breaker: record LLM failure ──
			if isLLMError(err) {
				if execCtx.RecordLLMFailure() {
					e.emitter.Error("circuit_breaker",
						fmt.Sprintf("LLM 连续失败 %d 次，已触发断路器", execCtx.ConsecutiveLLMFails),
						stepName)
					execCtx.Status = StatusFailed
				updateLastStepRecord(execCtx, stepName, "error", nil, durationMs, err.Error())
				if e.hook != nil {
					e.hook(ctx, execCtx)
				}
				return ErrCircuitBreaker
				}
			}

			// Step failed (critical step or non-LLM error)
			e.emitter.Error("step_failed", err.Error(), stepName)
			execCtx.Status = StatusFailed

			// Update step record
			updateLastStepRecord(execCtx, stepName, "error", nil, durationMs, err.Error())

			if e.hook != nil {
				e.hook(ctx, execCtx)
			}

			slog.Error("step failed",
				"trace_id", execCtx.TraceID,
				"step", stepName,
				"error", err,
				"duration_ms", durationMs,
			)
			return err
		}

		// Step succeeded — reset circuit breaker
		execCtx.RecordLLMSuccess()

		// Step succeeded — get the result from execCtx based on step name
	result := GetStepResult(stepName, execCtx)
	updateLastStepRecord(execCtx, stepName, "complete", result, durationMs, "")

	// Emit step.complete
	e.emitter.StepComplete(stepName, result, durationMs)

		if e.hook != nil {
			e.hook(ctx, execCtx)
		}

		slog.Info("step completed",
			"trace_id", execCtx.TraceID,
			"step", stepName,
			"duration_ms", durationMs,
			"total_tokens", execCtx.TotalTokens,
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
		execCtx.ArticleTitle,
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

// GetStepResult extracts the result for a given step from the context.
func GetStepResult(step StepName, execCtx *ExecutionContext) interface{} {
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
	case StepCompress:
		return map[string]interface{}{
			"compressed":      execCtx.CompressedContext != "",
			"context_length":  len(execCtx.CompressedContext),
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
		// "fixed" = true only if AutoFix actually applied a fix (article was modified)
		// "skipped" = true if AutoFix was skipped (review passed or no fixable issues)
		// "review_passed" = current review status after AutoFix
		return map[string]interface{}{
			"fixed":         execCtx.ReviewResult != nil && execCtx.ReviewResult.Passed,
			"review_passed": execCtx.ReviewResult != nil && execCtx.ReviewResult.Passed,
		}
	case StepChat:
		return map[string]interface{}{
			"article_length": len(execCtx.Article),
		}
	case StepShortTermMemory:
		return map[string]interface{}{
			"history_loaded": execCtx.ConversationHistory != nil,
		}
	case StepShortTermStore:
		return map[string]interface{}{
			"stored": true,
		}
	case StepWorkingMemory:
		return map[string]interface{}{
			"summarized": execCtx.WorkingSummary != nil,
		}
	default:
		return nil
	}
}

// isLLMError checks whether an error originated from an LLM API call.
// Used by the circuit breaker and graceful degradation logic.
func isLLMError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "llm") ||
		strings.Contains(msg, "api request failed") ||
		strings.Contains(msg, "api returned status") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "deepseek") ||
		strings.Contains(msg, "chat completions")
}

// isQuotaExceeded checks whether an error indicates the LLM API quota/balance
// is exhausted (HTTP 402 or 429 after retries). This is a hard stop — no amount
// of retrying will fix it; the user must top up their account.
func isQuotaExceeded(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "quota exceeded") ||
		strings.Contains(msg, "rate limit exhausted") ||
		strings.Contains(msg, "insufficient balance")
}
