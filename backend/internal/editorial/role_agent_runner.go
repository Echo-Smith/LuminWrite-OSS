package editorial

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── RoleAgentRunner: 角色化 Agent 执行器 ──────────────────────
//
// 复用 tools.LLMClient.ChatWithTools 的工具调用循环，
// 但去掉 Harness 中面向多轮对话的复杂度（SessionStore、WorldState、
// 意图判定），替换为角色化的设计：
//   - 角色固定（researcher / writer / reviewer）
//   - Persona 注入 system prompt
//   - 工具集由角色决定（从 EditorialToolRegistry 动态拉取）
//   - 信号工具标志完成

// RoleAgentRunner 角色化 Agent 执行器
type RoleAgentRunner struct {
	llm          *tools.LLMClient
	search       *tools.SearchClient
	kbSearcher   tools.KnowledgeSearcher
	profile      *profile.StyleProfile
	emitter      engine.EventEmitter
	toolRegistry *EditorialToolRegistry
}

// NewRoleAgentRunner 创建角色化 Agent 执行器
func NewRoleAgentRunner(
	llm *tools.LLMClient,
	search *tools.SearchClient,
	kb tools.KnowledgeSearcher,
	p *profile.StyleProfile,
	emitter engine.EventEmitter,
	toolRegistry *EditorialToolRegistry,
) *RoleAgentRunner {
	return &RoleAgentRunner{
		llm:          llm,
		search:       search,
		kbSearcher:   kb,
		profile:      p,
		emitter:      emitter,
		toolRegistry: toolRegistry,
	}
}

// RoleRunConfig 单次角色化执行配置
type RoleRunConfig struct {
	// AgentConfig 角色配置（来自 Planner 或 BuiltinRoles）
	AgentConfig *AgentConfig

	// Task 当前编辑部任务
	Task *Task

	// AgentContext 含上游 Artifact + OrgKnowledge
	AgentContext *AgentContext

	// ExecutionContext 执行上下文（已预填充 UserInput、Article 等）
	ExecutionContext *engine.ExecutionContext

	// RoleSystemPromptExtra 角色特定的 system prompt 额外内容
	// （如研究简报、草稿内容、栏目偏好等上游 Artifact 摘要）
	RoleSystemPromptExtra string
}

// RoleRunResult 角色化执行结果
type RoleRunResult struct {
	// Output LLM 的最终文本输出
	Output string
	// Tokens 总 token 消耗
	Tokens int
	// SignalToolArgs 信号工具的参数（如果 LLM 调用了信号工具）
	// key = tool name, value = raw JSON arguments
	SignalToolArgs map[string]string
	// SearchResults 搜索过程中收集的搜索结果（search_web + search_knowledge）
	SearchResults []engine.SearchResult
}

// Run 执行角色化 Agent 循环
// 返回 LLM 的最终文本输出 + Token 消耗 + 信号工具参数
func (r *RoleAgentRunner) Run(ctx context.Context, cfg RoleRunConfig) (*RoleRunResult, error) {
	agentCfg := cfg.AgentConfig
	execCtx := cfg.ExecutionContext

	// ── 1. 从 Registry 动态拉取角色化工具集 ──
	hasSearch := r.search != nil && r.search.HasSources()
	hasKB := r.kbSearcher != nil
	toolDefs := r.toolRegistry.ToolsForRole(agentCfg.Role, hasSearch, hasKB)

	// ── 2. 构建 system prompt ──
	systemPrompt := r.buildSystemPrompt(cfg, hasSearch, hasKB)

	// ── 3. 构建 messages ──
	messages := []tools.LLMMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: execCtx.UserInput},
	}

	// ── 4. 构建信号工具拦截器 ──
	signalArgs := make(map[string]string)
	var searchResultsMu sync.Mutex
	var allSearchResults []engine.SearchResult

	completionTool := r.toolRegistry.SignalToolForRole(agentCfg.Role)

	// ── 5. 构建工具执行器（从 Registry 动态分发）──
	runCtx := &ToolRunContext{
		LLM:           r.llm,
		Search:        r.search,
		KBSearcher:    r.kbSearcher,
		Profile:       r.profile,
		ExecCtx:       execCtx,
		Emitter:       r.emitter,
		SearchResults: &allSearchResults,
		SearchMu:      &searchResultsMu,
	}
	maxCalls := r.toolRegistry.MaxCallsMapForRole(agentCfg.Role)

	executor := buildRoleToolExecutor(r.toolRegistry, runCtx, maxCalls, signalArgs, completionTool)

	// ── 6. 构建 LLM 选项 ──
	opts := []tools.ChatOption{
		tools.WithThinking(true),
		tools.WithReasoningEffort("high"),
	}

	// ── 7. 流式回调 ──
	onDelta := func(delta string) {
		if r.emitter != nil {
			r.emitter.StreamDelta(delta)
		}
	}
	onReasoning := func(delta string) {
		if r.emitter != nil {
			r.emitter.ReasoningDelta(delta)
		}
	}
	onReset := func() {
		if r.emitter != nil {
			r.emitter.StreamReset()
		}
	}

	// ── 8. 启动 ChatWithTools 循环 ──
	slog.Info("role agent runner: starting",
		"role", agentCfg.Role,
		"task_id", cfg.Task.ID,
		"tools", len(toolDefs),
		"has_search", hasSearch,
		"has_kb", hasKB,
	)

	output, tokens, err := r.llm.ChatWithTools(
		ctx, messages,
		onDelta, onReasoning, onReset,
		toolDefs, executor,
		opts...,
	)
	if err != nil {
		return nil, fmt.Errorf("role agent runner: %w", err)
	}

	slog.Info("role agent runner: completed",
		"role", agentCfg.Role,
		"task_id", cfg.Task.ID,
		"output_chars", len([]rune(output)),
		"tokens", tokens,
		"signal_tools", len(signalArgs),
		"search_results", len(allSearchResults),
	)

	return &RoleRunResult{
		Output:         output,
		Tokens:         tokens,
		SignalToolArgs: signalArgs,
		SearchResults:  allSearchResults,
	}, nil
}

// buildSystemPrompt 构建角色化 system prompt
func (r *RoleAgentRunner) buildSystemPrompt(cfg RoleRunConfig, hasSearch, hasKB bool) string {
	agentCfg := cfg.AgentConfig
	var sb strings.Builder

	// [1. Persona]
	sb.WriteString("--- 角色设定 ---\n")
	sb.WriteString(agentCfg.Persona)
	sb.WriteString("\n\n")

	// [2. 任务上下文]
	sb.WriteString("--- 任务上下文 ---\n")
	sb.WriteString(fmt.Sprintf("任务标题：%s\n", cfg.Task.Title))
	sb.WriteString(fmt.Sprintf("任务描述：%s\n", cfg.Task.Description))
	if cfg.Task.StyleSlug != "" {
		sb.WriteString(fmt.Sprintf("写作风格：%s\n", cfg.Task.StyleSlug))
	}

	// [3. 角色特定额外上下文]
	if cfg.RoleSystemPromptExtra != "" {
		sb.WriteString("\n--- 上游素材 ---\n")
		sb.WriteString(cfg.RoleSystemPromptExtra)
		sb.WriteString("\n")
	}

	// [4. 组织知识]
	if org := cfg.AgentContext.GetOrgKnowledge(); org != nil {
		if len(org.ActiveKnowledge) > 0 {
			sb.WriteString("\n--- 组织知识 ---\n")
			for _, k := range org.ActiveKnowledge {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", k.Title, k.Content))
			}
		}
		if org.ColumnPref != nil {
			cp := org.ColumnPref
			if cp.Tone != "" {
				sb.WriteString(fmt.Sprintf("\n栏目偏好 - 语气风格：%s\n", cp.Tone))
			}
			if len(cp.ForbiddenWords) > 0 {
				sb.WriteString(fmt.Sprintf("栏目偏好 - 禁用词：%s\n", strings.Join(cp.ForbiddenWords, ", ")))
			}
			if cp.PreferredLengthMin > 0 || cp.PreferredLengthMax > 0 {
				sb.WriteString(fmt.Sprintf("栏目偏好 - 建议字数：%d-%d\n", cp.PreferredLengthMin, cp.PreferredLengthMax))
			}
			if cp.ReviewCriteria != "" {
				sb.WriteString(fmt.Sprintf("栏目偏好 - 审校标准：%s\n", cp.ReviewCriteria))
			}
		}
		if len(org.TopSources) > 0 {
			sb.WriteString("\n高可信度信源（优先参考）：\n")
			for _, s := range org.TopSources {
				sb.WriteString(fmt.Sprintf("- %s (可信度: %.1f, 已验证: %d次)\n",
					s.SourceDomain, s.CredibilityScore, s.VerifiedCount))
			}
		}
	}

	// [5. 风格 Profile]
	if r.profile != nil {
		sb.WriteString("\n--- 写作风格 Profile ---\n")
		sb.WriteString(fmt.Sprintf("风格名称：%s\n", r.profile.Name))
		if r.profile.Description != "" {
			sb.WriteString(fmt.Sprintf("风格描述：%s\n", r.profile.Description))
		}
		if r.profile.WordRange.Max > 0 {
			sb.WriteString(fmt.Sprintf("目标字数：%d-%d\n", r.profile.WordRange.Min, r.profile.WordRange.Max))
		}
	}

	// [6. 工具使用指引 — 从 Registry 动态生成]
	sb.WriteString("\n--- 工具使用指引 ---\n")
	sb.WriteString(r.toolRegistry.ToolGuideForRole(agentCfg.Role, hasSearch, hasKB))

	sb.WriteString("\n--- 安全规则 ---\n")
	sb.WriteString("不得编造事实、数据或引用来源。\n")
	sb.WriteString("不得输出违法违规内容。\n")
	sb.WriteString("如果素材不足以支撑写作，应在报告中说明而非编造。\n")

	return sb.String()
}

// ─── 工具执行器（从 Registry 动态分发）────────────────────────

// buildRoleToolExecutor 构建工具执行器
// 信号工具被拦截后存入 signalArgs，其他工具通过 Registry 查找并执行
func buildRoleToolExecutor(registry *EditorialToolRegistry, runCtx *ToolRunContext, maxCalls map[string]int, signalArgs map[string]string, completionTool string) tools.ToolExecutor {
	callCounts := make(map[string]int)

	return func(name string, arguments string) (string, error) {
		// ── 信号工具拦截 ──
		if name == completionTool {
			signalArgs[name] = arguments
			slog.Info("role agent: completion tool called",
				"tool", name,
				"args_len", len(arguments),
			)
			switch name {
			case "submit_research_brief":
				return "研究简报已提交。请不要再调用其他工具，直接结束。", nil
			case "submit_review_report":
				return "审查报告已提交。请不要再调用其他工具，直接结束。", nil
			case "write_article":
				if runCtx.Emitter != nil {
					runCtx.Emitter.StepStart("write_article", 0)
					runCtx.Emitter.StepComplete("write_article", map[string]any{}, 0)
				}
				return "好的，请直接输出文章内容（Markdown格式，以##开头作为标题）。文章会实时展示。", nil
			}
		}

		// ── MaxCalls guard ──
		if max, ok := maxCalls[name]; ok && max > 0 {
			current := callCounts[name]
			if current >= max {
				slog.Info("role agent tool guard: max calls reached",
					"tool", name, "current", current, "max", max)
				return fmt.Sprintf("已达到调用次数上限（%d次）。请直接使用已有素材或调用完成工具。", max), nil
			}
			callCounts[name]++
		}

		// ── 记录 step ──
		if runCtx.ExecCtx != nil {
			runCtx.ExecCtx.CurrentStep = engine.StepName(name)
		}

		// ── 从 Registry 查找工具并执行 ──
		tool, ok := registry.Get(name)
		if !ok {
			return "", fmt.Errorf("unknown tool: %s", name)
		}

		result, err := tool.Execute(context.Background(), arguments, runCtx)
		if err != nil {
			return fmt.Sprintf("工具执行错误: %v", err), nil
		}
		return result, nil
	}
}

// ─── 辅助函数 ─────────────────────────────────────────────

// formatKnowledgeResults 格式化知识库搜索结果。
func formatKnowledgeResults(results []engine.SearchResult) string {
	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. [知识库] %s\n", i+1, r.Title))
		snippet := r.Snippet
		if len([]rune(snippet)) > 500 {
			snippet = string([]rune(snippet)[:500]) + "…"
		}
		sb.WriteString(fmt.Sprintf("   %s\n\n", snippet))
	}
	return sb.String()
}

// compressRoleSearchResults 用 LLM 将原始搜索结果压缩为结构化研究简报。
func compressRoleSearchResults(ctx context.Context, llm *tools.LLMClient, query string, results []engine.SearchResult) string {
	if llm == nil || len(results) == 0 {
		return formatRoleSearchResults(results)
	}

	var rawBuf strings.Builder
	for i, r := range results {
		snippet := r.Snippet
		if len([]rune(snippet)) > 200 {
			snippet = string([]rune(snippet)[:200]) + "…"
		}
		rawBuf.WriteString(fmt.Sprintf("[%d] %s\n    %s\n    来源: %s\n\n", i+1, r.Title, snippet, r.Source))
	}

	systemMsg := "你是研究助理。将搜索结果压缩为结构化研究简报。只输出简报内容，不要寒暄。"
	userMsg := fmt.Sprintf(`搜索关键词：%s

原始搜索结果：
%s

请将以上搜索结果压缩为一份研究简报，格式如下：

## 研究简报：<关键词>

### 关键事实
- （提取最重要的事实，每条一行，用 [序号] 标注来源）

### 数据与细节
- （提取具体数据、数字、日期等）

### 多方观点
- （如有不同视角的信息，分条列出）

### 写作建议
- （基于以上素材，给出可用方向提示）

要求：
1. 简报总长度控制在 300-500 字
2. 每条事实后用 [序号] 标注来源，方便后续 read_source 查阅原文
3. 去重：不同来源的相同信息只保留一条
4. 如果素材不足以支撑某些维度，直接省略该维度`, query, rawBuf.String())

	resp, _, err := llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0.1), tools.WithThinking(false))

	if err != nil {
		slog.Warn("compressRoleSearchResults: LLM compression failed, falling back",
			"error", err,
			"query", query,
			"results", len(results),
		)
		return formatRoleSearchResults(results)
	}

	if resp == "" {
		return formatRoleSearchResults(results)
	}

	slog.Info("search_web: results compressed to research brief",
		"query", query,
		"raw_results", len(results),
		"brief_chars", len([]rune(resp)),
	)
	return resp
}

// formatRoleSearchResults 是 compressRoleSearchResults 的 fallback。
func formatRoleSearchResults(results []engine.SearchResult) string {
	var sb strings.Builder
	for i, r := range results {
		snippet := r.Snippet
		if len([]rune(snippet)) > 150 {
			snippet = string([]rune(snippet)[:150]) + "…"
		}
		sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n\n", i+1, r.Title, snippet))
	}
	return sb.String()
}
