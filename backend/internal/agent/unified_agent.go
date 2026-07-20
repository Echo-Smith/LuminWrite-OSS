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

	toolDefs := a.buildToolDefs()
	if len(toolDefs) == 0 {
		return fmt.Errorf("no tools registered in registry")
	}

	for iteration := 0; iteration < a.maxIterations; iteration++ {
		if execCtx.IsCancelled() {
			a.emitter.Cancelled()
			return fmt.Errorf("cancelled")
		}

		slog.Info("unified agent iteration",
			"trace_id", execCtx.TraceID,
			"iteration", iteration,
		)

		resp, llmResp, err := a.llm.Chat(ctx, conversation,
			tools.WithThinking(true),
			tools.WithReasoningEffort("high"),
			tools.WithTools(toolDefs),
		)
		if err != nil {
			return fmt.Errorf("unified agent LLM call failed at iteration %d: %w", iteration, err)
		}
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
				a.emitter.StepComplete(engine.StepName(toolName), map[string]interface{}{
					"summary": result.Summary,
				}, durationMs)
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
	sb.WriteString("你是一个写作 Agent 的编排器。你的职责是根据用户请求和当前执行状态，选择下一步要执行的工具。\n\n")
	sb.WriteString("可用工具说明：\n")
	sb.WriteString("- intent: 意图分类（第一步，必须执行）\n")
	sb.WriteString("- memory_gate: 检索用户写作偏好记忆\n")
	sb.WriteString("- query_plan: 生成搜索计划（仅写作模式需要）\n")
	sb.WriteString("- search: 执行多源搜索（知乎/IMA/Tavily/腾讯新闻/微博）\n")
	sb.WriteString("- relevance: 搜索结果相关性过滤和语义去重\n")
	sb.WriteString("- compress: 将搜索结果压缩为结构化研究简报\n")
	sb.WriteString("- outline: 生成文章提纲（仅引导模式）\n")
	sb.WriteString("- write: 按风格 Profile 生成文章\n")
	sb.WriteString("- post_review: 质量评审和敏感检查\n")
	sb.WriteString("- auto_fix: 自动修正质量问题\n")
	sb.WriteString("- chat: 对话回复（仅聊天模式）\n")
	sb.WriteString("- memory_extract: 提取写作偏好记忆（异步，最后执行）\n")
	for _, tool := range a.registry.All() {
		if strings.HasPrefix(tool.Name(), "mcp__") {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", tool.Name(), tool.Description()))
		}
	}
	sb.WriteString("\n决策规则：\n")
	sb.WriteString("1. 第一步永远是 intent（意图分类）\n")
	sb.WriteString("2. 根据意图选择后续步骤：\n")
	sb.WriteString("   - chat 意图: memory_gate → chat → memory_extract → 完成\n")
	sb.WriteString("   - writing 意图: memory_gate + (query_plan → search → relevance → compress) 并行 → outline(引导模式) → write → post_review → auto_fix(如有问题) → memory_extract → 完成\n")
	sb.WriteString("   - polish/shorten/expand 意图: memory_gate → write → post_review → memory_extract → 完成\n")
	sb.WriteString("3. 如果 post_review 发现问题且 auto_fix 可以修复，调用 auto_fix\n")
	sb.WriteString("4. 如果 auto_fix 后仍有严重问题，可以再次调用 post_review\n")
	sb.WriteString("5. 文章生成并评审通过后，调用 memory_extract，然后结束\n")
	sb.WriteString("6. 可以使用 MCP 工具（mcp__ 开头）获取额外信息，如事实核查、文件读取等\n")
	sb.WriteString("7. 每次只调用一个工具，根据执行结果决定下一步\n")
	sb.WriteString("\n重要：如果当前状态已经满足用户需求，直接回复完成（不调用工具）。\n")
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
	if len(execCtx.SearchResults) > 0 {
		sb.WriteString(fmt.Sprintf("- 搜索: %d 条结果\n", len(execCtx.SearchResults)))
	} else {
		sb.WriteString("- 搜索: 未执行\n")
	}
	if execCtx.CompressedContext != "" {
		sb.WriteString(fmt.Sprintf("- 素材压缩: 已完成 (%d 字)\n", len(execCtx.CompressedContext)))
	}
	if execCtx.Outline != nil {
		sb.WriteString(fmt.Sprintf("- 提纲: 已生成 (标题: %s, %d 个要点)\n",
			execCtx.Outline.Title, len(execCtx.Outline.Outline)))
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

func (a *UnifiedAgent) buildToolDefs() []tools.ToolDef {
	all := a.registry.All()
	defs := make([]tools.ToolDef, 0, len(all))
	for _, t := range all {
		defs = append(defs, tools.ToolDef{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		})
	}
	return defs
}
