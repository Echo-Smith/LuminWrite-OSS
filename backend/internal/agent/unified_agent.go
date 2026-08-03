package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── UnifiedAgent: LLM-Driven ReAct Loop ─────────────────
//
// UnifiedAgent replaces the fixed []Step pipeline with a ReAct
// (Reasoning + Acting) loop where the LLM is the orchestrator.
//
// This package lives outside engine/ to avoid import cycles:
//   engine → tools (engine.SearchResult used by tools.SearchClient)
//   agent → engine + tools (no cycle: agent imports both, neither imports agent)

// UnifiedAgent is the LLM-driven ReAct agent loop.
type UnifiedAgent struct {
	registry      *engine.ToolRegistry
	llm           *tools.LLMClient
	emitter       engine.EventEmitter
	maxIterations int
}

// NewUnifiedAgent creates a UnifiedAgent with the given tool registry and LLM.
func NewUnifiedAgent(registry *engine.ToolRegistry, llm *tools.LLMClient, emitter engine.EventEmitter) *UnifiedAgent {
	return &UnifiedAgent{
		registry:      registry,
		llm:           llm,
		emitter:       emitter,
		maxIterations: 12,
	}
}

// Run executes the ReAct loop.
func (a *UnifiedAgent) Run(ctx context.Context, execCtx *engine.ExecutionContext) error {
	execCtx.Status = engine.StatusRunning

	slog.Info("unified agent started",
		"trace_id", execCtx.TraceID,
		"user_input", execCtx.UserInput,
		"style", execCtx.StyleSlug,
		"mode", execCtx.Mode,
		"tools", len(a.registry.All()),
	)

	systemPrompt := a.buildPlannerPrompt()

	var conversation []tools.LLMMessage
	conversation = append(conversation, tools.LLMMessage{
		Role:    "system",
		Content: systemPrompt,
	})

	initialState := a.buildStateSummary(execCtx)
	conversation = append(conversation, tools.LLMMessage{
		Role:    "user",
		Content: fmt.Sprintf("用户请求：%s\n\n当前状态：\n%s\n\n请选择下一步要执行的工具，或如果已经完成则直接回复完成。", execCtx.UserInput, initialState),
	})

	// Build initial tool defs; will be rebuilt each iteration to exclude
	// already-executed non-repeatable tools (prevents LLM from calling e.g.
	// "intent" again after it was already done).
	toolDefs := a.buildToolDefs(execCtx)
	if len(toolDefs) == 0 {
		return fmt.Errorf("no tools registered in registry")
	}

	for iteration := 0; iteration < a.maxIterations; iteration++ {
		// ── Exit check: cancellation ──
		if execCtx.IsCancelled() {
			a.emitter.Cancelled()
			return fmt.Errorf("cancelled")
		}

		// ── Exit check: global context timeout ──
		if ctx.Err() != nil {
			a.emitter.Error("timeout", "执行超时（超过全局时间限制）", execCtx.CurrentStep)
			execCtx.Status = engine.StatusFailed
			return ctx.Err()
		}

		// ── Exit check: token budget ──
		if execCtx.CheckBudget() {
			a.emitter.Error("budget_exceeded",
				fmt.Sprintf("Token 预算已用尽（已用 %d / 上限 %d）", execCtx.TotalTokens, execCtx.MaxTokens),
				execCtx.CurrentStep)
			execCtx.Status = engine.StatusFailed
			return engine.ErrBudgetExceeded
		}

		// ── Exit check: client disconnected ──
		if execCtx.IsDisconnected() {
			slog.Info("client disconnected, pausing unified agent", "trace_id", execCtx.TraceID, "iteration", iteration)
			execCtx.Status = engine.StatusPaused
			a.emitter.PausedWithReason(execCtx.CurrentStep, nil, "disconnect")
			return nil
		}

		// ── Exit check: circuit breaker ──
		if execCtx.MaxLLMFails > 0 && execCtx.ConsecutiveLLMFails >= execCtx.MaxLLMFails {
			a.emitter.Error("circuit_breaker",
				fmt.Sprintf("LLM 连续失败 %d 次，已触发断路器", execCtx.ConsecutiveLLMFails),
				execCtx.CurrentStep)
			execCtx.Status = engine.StatusFailed
			return engine.ErrCircuitBreaker
		}

		// ── Rule-based next step: bypass LLM for deterministic paths ──
		// This prevents the LLM planner from skipping critical steps
		// (e.g. post_review after write) or calling tools in wrong order.
		if forced, ok := a.determineNextStep(execCtx); ok {
			slog.Info("unified agent: rule-engine forced step",
				"trace_id", execCtx.TraceID,
				"iteration", iteration,
				"step", forced,
			)

			a.emitter.StepStart(engine.StepName(forced), iteration)
			startTime := time.Now()

			result, err := a.registry.ExecuteTool(ctx, string(forced), map[string]any{}, execCtx, a.emitter)
			durationMs := time.Since(startTime).Milliseconds()

			if err != nil {
				slog.Warn("unified agent: forced step failed",
					"trace_id", execCtx.TraceID,
					"step", forced,
					"error", err,
				)
			} else if result != nil {
				stepResult := engine.GetStepResult(engine.StepName(forced), execCtx)
					if stepResult == nil {
						stepResult = map[string]interface{}{
							"summary": result.Summary,
						}
					}
					a.emitter.StepComplete(engine.StepName(forced), stepResult, durationMs)
				slog.Info("unified agent: forced step completed",
					"trace_id", execCtx.TraceID,
					"step", forced,
					"duration_ms", durationMs,
				)
				if result.Done && a.canFinish(execCtx) {
					a.finish(execCtx)
					return nil
				}
			}
			continue
		}

		slog.Info("unified agent iteration",
			"trace_id", execCtx.TraceID,
			"iteration", iteration,
			"total_tokens", execCtx.TotalTokens,
		)

	// Adaptive reasoning effort: use high for early iterations (intent + planning),
	// low for later iterations (simple tool selection reduces token cost).
	reasoningEffort := "high"
	if iteration >= 3 {
		reasoningEffort = "low"
	}

	resp, llmResp, err := a.llm.Chat(ctx, conversation,
		tools.WithThinking(true),
		tools.WithReasoningEffort(reasoningEffort),
		tools.WithTools(a.buildToolDefs(execCtx)),
	)
		if err != nil {
			// ── Quota exceeded: hard stop, no retry ──
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(errMsg, "quota exceeded") ||
				strings.Contains(errMsg, "rate limit exhausted") ||
				strings.Contains(errMsg, "insufficient balance") {
				slog.Error("unified agent: LLM API quota exceeded",
					"trace_id", execCtx.TraceID,
					"iteration", iteration,
					"error", err,
				)
				a.emitter.Error("quota_exceeded",
					"AI 模型服务额度不足，请联系管理员充值",
					execCtx.CurrentStep)
				execCtx.Status = engine.StatusFailed
				return engine.ErrQuotaExceeded
			}

			// ── Circuit breaker: record LLM failure ──
			if execCtx.RecordLLMFailure() {
				a.emitter.Error("circuit_breaker",
					fmt.Sprintf("LLM 连续失败 %d 次，已触发断路器", execCtx.ConsecutiveLLMFails),
					execCtx.CurrentStep)
				execCtx.Status = engine.StatusFailed
				return fmt.Errorf("circuit breaker tripped after LLM error at iteration %d: %w", iteration, err)
			}
			return fmt.Errorf("unified agent LLM call failed at iteration %d: %w", iteration, err)
		}
		// LLM call succeeded — reset circuit breaker
		execCtx.RecordLLMSuccess()

		if llmResp != nil {
			execCtx.TotalTokens += llmResp.Usage.TotalTokens
		}

		var assistantMsg tools.LLMMessage
		if len(llmResp.Choices) > 0 {
			assistantMsg = llmResp.Choices[0].Message
		} else {
			assistantMsg = tools.LLMMessage{Role: "assistant", Content: resp}
		}

		if len(assistantMsg.ToolCalls) == 0 {
			// LLM wants to finish — but check if critical steps are missing
		if !a.canFinish(execCtx) {
			slog.Info("unified agent: LLM tried to finish but requirements not met",
				"trace_id", execCtx.TraceID,
				"iteration", iteration,
				"has_review", execCtx.ReviewResult != nil,
				"review_passed", execCtx.ReviewResult != nil && execCtx.ReviewResult.Passed,
				"fix_attempts", execCtx.FixAttempts,
			)
			conversation = append(conversation, assistantMsg)
			var hint string
			if execCtx.ReviewResult == nil {
				hint = "文章已生成但尚未经过质量评审。请调用 post_review 工具进行质量评审。"
			} else if !execCtx.ReviewResult.Passed {
				hint = fmt.Sprintf("质量评审未通过（%d 个问题待修正）。请调用 auto_fix 工具进行修正（已修正 %d/%d 次）。",
					len(execCtx.ReviewResult.Issues), execCtx.FixAttempts, execCtx.MaxFixAttempts)
			}
			conversation = append(conversation, tools.LLMMessage{
				Role:    "user",
				Content: hint,
			})
			continue
		}

			slog.Info("unified agent finished by LLM",
				"trace_id", execCtx.TraceID,
				"iteration", iteration,
				"response_len", len(resp),
			)
			if execCtx.Article == "" && resp != "" {
				execCtx.Article = resp
			}
			break
		}

		conversation = append(conversation, assistantMsg)

		for _, tc := range assistantMsg.ToolCalls {
			toolName := tc.Function.Name
			slog.Info("unified agent executing tool",
				"trace_id", execCtx.TraceID,
				"iteration", iteration,
				"tool", toolName,
			)

			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			if args == nil {
				args = map[string]any{}
			}

			a.emitter.StepStart(engine.StepName(toolName), iteration)
			startTime := time.Now()

			result, err := a.registry.ExecuteTool(ctx, toolName, args, execCtx, a.emitter)
			durationMs := time.Since(startTime).Milliseconds()

			var toolResult string
			if err != nil {
				toolResult = fmt.Sprintf("Error: %v", err)
				slog.Warn("unified agent tool failed",
					"trace_id", execCtx.TraceID,
					"tool", toolName,
					"error", err,
					"duration_ms", durationMs,
				)
			} else if result != nil {
				toolResult = result.Summary
				// Send the full structured result data (not just the summary)
				// so the frontend can render search results, review scores, etc.
				stepResult := engine.GetStepResult(engine.StepName(toolName), execCtx)
				if stepResult == nil {
					stepResult = map[string]interface{}{
						"summary": result.Summary,
					}
				}
				a.emitter.StepComplete(engine.StepName(toolName), stepResult, durationMs)
				slog.Info("unified agent tool completed",
					"trace_id", execCtx.TraceID,
					"tool", toolName,
					"duration_ms", durationMs,
					"summary_len", len(result.Summary),
				)
				if result.Done {
					a.finish(execCtx)
					return nil
				}
			}

			conversation = append(conversation, tools.LLMMessage{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID,
			})

			// ── Exit check: disconnect during tool execution ──
			if execCtx.IsDisconnected() {
				slog.Info("client disconnected during tool execution", "trace_id", execCtx.TraceID, "tool", toolName)
				execCtx.Status = engine.StatusPaused
				a.emitter.PausedWithReason(engine.StepName(toolName), nil, "disconnect")
				return nil
			}

			if execCtx.CheckPause(ctx, a.emitter, engine.StepName(toolName)) != nil {
				if execCtx.IsCancelled() {
					a.emitter.Cancelled()
					return fmt.Errorf("cancelled")
				}
			}
		}
	}

	if execCtx.Article == "" {
		slog.Warn("unified agent exhausted iterations without producing article",
			"trace_id", execCtx.TraceID,
			"max_iterations", a.maxIterations,
		)
	}

	a.finish(execCtx)
	return nil
}

func (a *UnifiedAgent) finish(execCtx *engine.ExecutionContext) {
	execCtx.Status = engine.StatusCompleted
	a.emitter.Completed(
		execCtx.Article,
		execCtx.ArticleTitle,
		execCtx.ReviewResult,
		map[string]interface{}{
			"total_tokens": execCtx.TotalTokens,
		},
	)
	slog.Info("unified agent completed",
		"trace_id", execCtx.TraceID,
		"duration_ms", time.Since(execCtx.StartedAt).Milliseconds(),
		"total_tokens", execCtx.TotalTokens,
		"article_length", len([]rune(execCtx.Article)),
	)
}

func (a *UnifiedAgent) buildPlannerPrompt() string {
	var sb strings.Builder
	sb.WriteString(`你是写作 Agent 的编排大脑。你的职责是根据用户请求和当前执行状态，自主选择下一步要执行的工具。

设计理念：
- 你有充分的自主权来决定工具调用顺序
- 不需要遵循固定流程，根据实际情况灵活决策
- 每次只调用一个工具，根据执行结果决定下一步
- 如果当前状态已满足用户需求，直接回复完成（不调用工具）

可用工具：`)
	for _, tool := range a.registry.All() {
		sb.WriteString(fmt.Sprintf("\n- %s: %s", tool.Name(), tool.Description()))
	}
	sb.WriteString(`

决策原则：
1. 根据意图和当前状态，选择最有价值的下一步
2. 写作类意图通常需要：素材准备（query_plan → search → relevance → compress）→ 写作 → 质量评审 → 记忆提取
   - 但你可以根据情况调整，例如搜索结果质量很高时可以跳过 compress
   - 评审发现问题时可以调用 auto_fix 修复
   - auto_fix 最多执行 2 次，修正后会自动触发重新评审
3. 对话类意图通常更简单：记忆检索 → 对话回复 → 记忆提取
4. 审查通过后，调用 memory_extract 提取写作偏好，然后结束

重要：文章生成后必须经过 post_review 质量评审通过才能结束。`)
	return sb.String()
}

func (a *UnifiedAgent) buildStateSummary(execCtx *engine.ExecutionContext) string {
	var sb strings.Builder
	if execCtx.TaskIntent != nil {
		sb.WriteString(fmt.Sprintf("- 意图: %s (置信度: %.2f, 来源: %s)\n",
			execCtx.TaskIntent.TaskMode, execCtx.TaskIntent.Confidence, execCtx.TaskIntent.Source))
	} else {
		sb.WriteString("- 意图: 未分类\n")
	}
	if execCtx.MemoryContext != nil {
		sb.WriteString("- 记忆: 已检索\n")
	} else {
		sb.WriteString("- 记忆: 未检索\n")
	}
	if execCtx.WritingTask != nil {
		sb.WriteString(fmt.Sprintf("- 写作任务: 话题「%s」, 主查询「%s」, %d 个查询词",
			execCtx.WritingTask.Topic, execCtx.WritingTask.PrimarySearchQuery, len(execCtx.WritingTask.SearchQueries)))
		if execCtx.WritingTask.WordLimit > 0 {
			sb.WriteString(fmt.Sprintf(", 建议字数: %d", execCtx.WritingTask.WordLimit))
		}
		sb.WriteString("\n")
	}
	if len(execCtx.SearchResults) > 0 {
		sb.WriteString(fmt.Sprintf("- 搜索: %d 条结果\n", len(execCtx.SearchResults)))
	} else {
		sb.WriteString("- 搜索: 未执行\n")
	}
	if execCtx.CompressedContext != "" {
		sb.WriteString(fmt.Sprintf("- 素材压缩: 已完成 (%d 字)\n", len(execCtx.CompressedContext)))
	} else {
		sb.WriteString("- 素材压缩: 未执行\n")
	}
	if execCtx.Outline != nil {
		sb.WriteString(fmt.Sprintf("- 提纲: 已生成 (标题: %s, %d 个要点)\n",
			execCtx.Outline.Title, len(execCtx.Outline.Outline)))
	} else {
		sb.WriteString("- 提纲: 未生成\n")
	}
	if execCtx.Article != "" {
		sb.WriteString(fmt.Sprintf("- 文章: 已生成 (%d 字, 标题: %s)\n",
			len([]rune(execCtx.Article)), execCtx.ArticleTitle))
	} else {
		sb.WriteString("- 文章: 未生成\n")
	}
	if execCtx.ReviewResult != nil {
		sb.WriteString(fmt.Sprintf("- 评审: %s (问题: %d 个)\n",
			map[bool]string{true: "通过", false: "未通过"}[execCtx.ReviewResult.Passed],
			len(execCtx.ReviewResult.Issues)))
	} else {
		sb.WriteString("- 评审: 未执行\n")
	}
	// Auto-fix count
	fixCount := 0
	for _, rec := range execCtx.StepHistory {
		if rec.Step == engine.StepAutoFix {
			fixCount++
		}
	}
	if fixCount > 0 {
		sb.WriteString(fmt.Sprintf("- 自动修正: 已执行 %d 次 (上限 2 次)\n", fixCount))
	}
	// Memory extract status
	memoryExtracted := false
	for _, rec := range execCtx.StepHistory {
		if rec.Step == engine.StepMemoryExtract {
			memoryExtracted = true
			break
		}
	}
	if memoryExtracted {
		sb.WriteString("- 记忆提取: 已完成\n")
	}
	if execCtx.StyleSlug != "" {
		sb.WriteString(fmt.Sprintf("- 风格: %s\n", execCtx.StyleSlug))
	}
	if execCtx.Mode != "" {
		sb.WriteString(fmt.Sprintf("- 模式: %s\n", execCtx.Mode))
	}
	if len(execCtx.UserMaterials) > 0 {
		sb.WriteString(fmt.Sprintf("- 用户素材: %d 条\n", len(execCtx.UserMaterials)))
	}
	return sb.String()
}

// nonRepeatableTools are tools that should only be executed once per session.
// After execution, they are excluded from the LLM's tool list to prevent
// redundant calls (e.g. calling "intent" again after it was already done).
var nonRepeatableTools = map[string]bool{
	"intent":         true,
	"memory_gate":    true,
	"query_plan":     true,
	"outline":        true,
	"memory_extract": true,
}

// toolDependencies defines which tools require other tools to have run first.
// This prevents the LLM planner from calling tools out of order (e.g. calling
// "relevance" before "search" has produced any results to filter).
var toolDependencies = map[string][]string{
	"search":    {"query_plan"}, // search needs queries from query_plan
	"relevance": {"search"},     // relevance needs search results to filter
	"compress":  {"relevance"},   // compress needs filtered results
	"auto_fix":  {"post_review"}, // auto_fix needs review issues to fix
}

func (a *UnifiedAgent) buildToolDefs(execCtx *engine.ExecutionContext) []tools.ToolDef {
	// Build a set of all executed tool names from step history
	executed := make(map[string]bool)
	for _, rec := range execCtx.StepHistory {
		executed[string(rec.Step)] = true
	}

	all := a.registry.All()
	defs := make([]tools.ToolDef, 0, len(all))
	for _, t := range all {
		name := t.Name()

		// Skip non-repeatable tools that have already been executed
		if nonRepeatableTools[name] && executed[name] {
			continue
		}

		// Skip tools whose dependencies haven't been met
		if deps, ok := toolDependencies[name]; ok {
			depsMet := true
			for _, dep := range deps {
				if !executed[dep] {
					depsMet = false
					break
				}
			}
			if !depsMet {
				continue
			}
		}

		defs = append(defs, tools.ToolDef{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        name,
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		})
	}
	return defs
}

// determineNextStep enforces minimal critical invariants only.
// The LLM planner is free to choose tool ordering for everything else.
//
// Invariants:
//  1. intent must run first (before any other tool)
//  2. post_review must run before finishing (if an article was produced)
//  3. after auto_fix, force re-review to verify the fix (if review didn't pass)
//
// All other ordering decisions (memory_gate, query_plan, search, relevance,
// compress, outline, write, memory_extract) are left to the LLM.
func (a *UnifiedAgent) determineNextStep(execCtx *engine.ExecutionContext) (string, bool) {
	// Invariant 1: intent classification must happen first
	if execCtx.TaskIntent == nil {
		return "intent", true
	}

	// Invariant 2: post_review must run before the agent can finish
	if execCtx.Article != "" && execCtx.ReviewResult == nil && a.hasTool("post_review") {
		return "post_review", true
	}

	// Invariant 3: force re-review after auto_fix if review didn't pass
	// This ensures AutoFix results are verified by a fresh review, not auto-passed.
	if execCtx.Article != "" && execCtx.ReviewResult != nil && !execCtx.ReviewResult.Passed &&
		execCtx.FixAttempts > 0 && a.hasTool("post_review") {
		// Only force re-review if the last executed step was auto_fix
		if len(execCtx.StepHistory) > 0 {
			last := execCtx.StepHistory[len(execCtx.StepHistory)-1]
			if last.Step == engine.StepAutoFix {
				return "post_review", true
			}
		}
	}

	// Let the LLM decide everything else
	return "", false
}

// canFinish checks whether the agent is in a valid state to finish.
// Prevents finishing without post_review when article was produced.
// Also prevents finishing if review didn't pass and fix attempts remain.
func (a *UnifiedAgent) canFinish(execCtx *engine.ExecutionContext) bool {
	// No article → can finish (e.g. chat mode or error)
	if execCtx.Article == "" {
		return true
	}
	// Article exists → must have post_review
	if execCtx.ReviewResult == nil {
		return false
	}
	// Review exists but didn't pass → only finish if fix attempts exhausted
	if !execCtx.ReviewResult.Passed {
		if execCtx.MaxFixAttempts > 0 && execCtx.FixAttempts < execCtx.MaxFixAttempts {
			return false
		}
		// Fix attempts exhausted — allow finishing with warning
		slog.Warn("allowing finish despite review failure — max fix attempts exhausted",
			"trace_id", execCtx.TraceID,
			"fix_attempts", execCtx.FixAttempts,
			"max_fix_attempts", execCtx.MaxFixAttempts,
		)
		return true
	}
	return true
}

// hasTool checks if a tool is registered in the registry.
func (a *UnifiedAgent) hasTool(name string) bool {
	for _, t := range a.registry.All() {
		if t.Name() == name {
			return true
		}
	}
	return false
}
