package engine

import (
	"context"
	"fmt"
	"log/slog"
)

// ─── StepTool: Step → AgentTool Adapter ──────────────────
//
// StepTool wraps an existing engine.Step as an AgentTool.
// This allows the Harness to select and execute pipeline steps
// (intent, search, write, review, etc.) the same way it selects
// MCP tools and built-in tools — via LLM-driven agent loop.
//
// The adapter handles:
//   - ShouldSkip: if the step would be skipped, returns immediately
//   - Result summarization: builds a concise summary from execCtx state
//   - Done detection: marks "done" for terminal steps (write, chat)

// StepTool wraps an engine.Step as an AgentTool.
type StepTool struct {
	step        Step
	description string
	// doneSteps marks which step names signal completion (write, chat).
	// After these steps, the agent should check if it's done.
	isTerminal bool
}

// NewStepTool creates an AgentTool from an existing Step.
// If isTerminal is true, the tool result will set Done=true after execution.
func NewStepTool(step Step, description string, isTerminal bool) *StepTool {
	return &StepTool{
		step:        step,
		description: description,
		isTerminal:  isTerminal,
	}
}

func (t *StepTool) Name() string       { return string(t.step.Name()) }
func (t *StepTool) Description() string { return t.description }

func (t *StepTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{},
		"description": fmt.Sprintf("Execute the %s pipeline step. No arguments needed.", t.step.Name()),
	}
}

func (t *StepTool) Execute(ctx context.Context, args map[string]any, execCtx *ExecutionContext, emitter EventEmitter) (*ToolResult, error) {
	// Check ShouldSkip — if the step would be skipped, return immediately
	if skipper, ok := t.step.(Skipper); ok && skipper.ShouldSkip(execCtx) {
		return &ToolResult{
			Summary: fmt.Sprintf("Step %s skipped (not needed for current context)", t.step.Name()),
			Done:    false,
		}, nil
	}

	slog.Info("StepTool executing", "step", t.step.Name(), "trace_id", execCtx.TraceID)

	// Execute the underlying step
	err := t.step.Execute(ctx, execCtx, emitter)
	if err != nil {
		return nil, fmt.Errorf("step %s failed: %w", t.step.Name(), err)
	}

	// Build a concise summary of the execution result
	summary := summarizeStepResult(t.step.Name(), execCtx)

	slog.Info("StepTool completed", "step", t.step.Name(), "summary_len", len(summary), "trace_id", execCtx.TraceID)

	return &ToolResult{
		Summary: summary,
		Done:    t.isTerminal && execCtx.Article != "",
	}, nil
}

// summarizeStepResult builds a concise summary of what the step produced.
// This is fed back to the LLM as the "observation" in the ReAct loop.
// Keep it under 300 chars to avoid context bloat.
func summarizeStepResult(stepName StepName, execCtx *ExecutionContext) string {
	switch stepName {
	case StepIntent:
		if execCtx.TaskIntent != nil {
			return fmt.Sprintf("意图分类完成: mode=%s, confidence=%.2f, source=%s",
				execCtx.TaskIntent.TaskMode, execCtx.TaskIntent.Confidence, execCtx.TaskIntent.Source)
		}
		return "意图分类完成（无结果）"

	case StepQueryPlan:
		if execCtx.WritingTask != nil {
			return fmt.Sprintf("检索规划完成: topic=%s, queries=%v", execCtx.WritingTask.Topic, execCtx.WritingTask.SearchQueries)
		}
		return "检索规划完成（无搜索需求）"

	case StepSearch:
		return fmt.Sprintf("搜索完成: %d 条结果", len(execCtx.SearchResults))

	case StepRelevance:
		return fmt.Sprintf("相关性过滤完成: 保留 %d 条结果", len(execCtx.SearchResults))

	case StepCompress:
		if execCtx.CompressedContext != "" {
			return fmt.Sprintf("素材压缩完成: %d 字", len(execCtx.CompressedContext))
		}
		return "素材压缩跳过（结果不足或LLM不可用）"

	case StepMemoryGate:
		if execCtx.MemoryContext != nil {
			return "记忆检索完成"
		}
		return "记忆检索跳过"

	case StepOutline:
		if execCtx.Outline != nil {
			return fmt.Sprintf("提纲生成: 标题=%s, %d 个要点", execCtx.Outline.Title, len(execCtx.Outline.Outline))
		}
		return "提纲生成跳过"

	case StepWrite:
		articleLen := len([]rune(execCtx.Article))
		return fmt.Sprintf("文章生成完成: %d 字, 标题=%s", articleLen, execCtx.ArticleTitle)

	case StepPostReview:
		if execCtx.ReviewResult != nil {
			return fmt.Sprintf("质量评审完成: passed=%v, issues=%d, scores=%v",
				execCtx.ReviewResult.Passed, len(execCtx.ReviewResult.Issues), execCtx.ReviewResult.Scores)
		}
		return "质量评审跳过"

	case StepAutoFix:
		if execCtx.ReviewResult != nil {
			return fmt.Sprintf("自动修正完成: passed=%v", execCtx.ReviewResult.Passed)
		}
		return "自动修正跳过（无需修正）"

	case StepChat:
		return fmt.Sprintf("对话回复完成: %d 字", len([]rune(execCtx.Article)))

	case StepMemoryExtract:
		return "记忆提取完成（异步）"

	default:
		return fmt.Sprintf("步骤 %s 执行完成", stepName)
	}
}

// ─── FunctionTool: Built-in Go function as AgentTool ─────

// FunctionTool wraps a Go function as an AgentTool.
// Used for built-in micro tools like search_web, get_topic_context.
type FunctionTool struct {
	name        string
	description string
	schema      map[string]any
	fn          func(ctx context.Context, args map[string]any, execCtx *ExecutionContext) (string, error)
}

// NewFunctionTool creates a tool from a Go function.
func NewFunctionTool(name, description string, schema map[string]any, fn func(ctx context.Context, args map[string]any, execCtx *ExecutionContext) (string, error)) *FunctionTool {
	return &FunctionTool{
		name:        name,
		description: description,
		schema:      schema,
		fn:          fn,
	}
}

func (t *FunctionTool) Name() string        { return t.name }
func (t *FunctionTool) Description() string  { return t.description }
func (t *FunctionTool) Schema() map[string]any { return t.schema }

func (t *FunctionTool) Execute(ctx context.Context, args map[string]any, execCtx *ExecutionContext, emitter EventEmitter) (*ToolResult, error) {
	result, err := t.fn(ctx, args, execCtx)
	if err != nil {
		return nil, err
	}
	// Truncate long results to keep context manageable
	if len(result) > 2000 {
		result = result[:2000] + "...(截断)"
	}
	return &ToolResult{Summary: result, Done: false}, nil
}
