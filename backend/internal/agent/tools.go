package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── 写作工具集 ────────────────────────────────────────────
//
// 工具按意图分组，LLM 在持续会话中自主调用。
//
// 混合粒度设计：
//   - write_article: 整篇流式输出（用于首次写作）
//   - revise_section: 分段定向修改（用于多轮修改）
//
// write_article 和 revise_section 是"信号工具"：
//   LLM 调用后，Harness 返回指令让 LLM 流式输出文章内容。
//   onDelta 回调将内容实时转发给前端。

// ─── 工具定义 ───────────────────────────────────────────────

// WritingToolDefs 是写作意图的完整工具集。
// includeOutline=true 时包含 generate_outline 工具（用于 Guided 模式）。
func WritingToolDefs() []tools.ToolDef {
	return append([]tools.ToolDef{
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "search_web",
				Description: "搜索网络获取信息。用于补充写作素材或回答用户问题。返回搜索结果摘要列表。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "搜索关键词",
						},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "read_source",
				Description: "读取已有搜索结果的完整内容。传入结果序号(1-based)获取更多详情。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"index": map[string]any{
							"type":        "integer",
							"description": "搜索结果的序号(从1开始)",
						},
					},
					"required": []string{"index"},
				},
			},
		},
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "generate_outline",
				Description: "生成文章提纲供用户确认。调用后会弹出提纲让用户确认/编辑/重新生成。用户确认后方可开始写作。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"topic": map[string]any{
							"type":        "string",
							"description": "文章主题",
						},
					},
					"required": []string{"topic"},
				},
			},
		},
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "write_article",
				Description: "开始写作并流式输出完整文章。调用此工具后，请在下一轮回复中直接输出文章内容（Markdown格式，以##开头作为标题）。文章会实时流式展示给用户。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"topic": map[string]any{
							"type":        "string",
							"description": "文章主题",
						},
					},
					"required": []string{"topic"},
				},
			},
		},
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "review_article",
				Description: "对文章进行质量评审，返回评分和问题列表。在写完文章后调用。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"article": map[string]any{
							"type":        "string",
							"description": "要评审的文章内容",
						},
					},
					"required": []string{"article"},
				},
			},
		},
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "revise_section",
				Description: "定向修改文章的某一部分。调用后请在下一轮回复中输出修改后的完整文章。会先清空之前的流式内容再重新输出。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"section_hint": map[string]any{
							"type":        "string",
							"description": "要修改的部分，如'标题'、'第二段'、'结尾'",
						},
						"instruction": map[string]any{
							"type":        "string",
							"description": "修改指令，描述要怎么改",
						},
					},
					"required": []string{"section_hint", "instruction"},
				},
			},
		},
	})
}

// ChatToolDefs 是对话意图的工具集（仅搜索和读取）。
func ChatToolDefs() []tools.ToolDef {
	return []tools.ToolDef{
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "search_web",
				Description: "搜索网络获取信息。当用户的问题需要查资料时调用。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "搜索关键词",
						},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "read_source",
				Description: "读取搜索结果的完整内容。传入序号(1-based)。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"index": map[string]any{
							"type":        "integer",
							"description": "搜索结果的序号(从1开始)",
						},
					},
					"required": []string{"index"},
				},
			},
		},
	}
}

// ReviseToolDefs 是修改意图的工具集（搜索 + 修正）。
func ReviseToolDefs() []tools.ToolDef {
	return []tools.ToolDef{
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "search_web",
				Description: "搜索网络获取补充信息。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "搜索关键词",
						},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "revise_section",
				Description: "定向修改文章的某一部分。调用后输出修改后的完整文章。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"section_hint": map[string]any{
							"type":        "string",
							"description": "要修改的部分",
						},
						"instruction": map[string]any{
							"type":        "string",
							"description": "修改指令",
						},
					},
					"required": []string{"section_hint", "instruction"},
				},
			},
		},
	}
}

// ToolsForIntent 按意图返回工具集。
// guided=true 时在写作工具集中加入 generate_outline 工具。
func ToolsForIntent(intent Intent, hasSearch bool, guided ...bool) []tools.ToolDef {
	isGuided := len(guided) > 0 && guided[0]
	switch intent {
	case IntentWriting:
		all := WritingToolDefs()
		if !isGuided {
			// 非 Guided 模式：移除 generate_outline（索引 2）
			all = append(all[:2], all[3:]...)
		}
		if hasSearch {
			return all
		}
		// 无搜索能力时移除 search_web 和 read_source
		start := 0
		if len(all) > 0 && all[0].Function.Name == "search_web" {
			start = 2 // 跳过 search_web 和 read_source
		}
		return all[start:]
	case IntentPolish, IntentShorten, IntentExpand, IntentExtract:
		if hasSearch {
			return ReviseToolDefs()
		}
		return ReviseToolDefs()[1:] // 只保留 revise_section
	case IntentChat:
		if hasSearch {
			return ChatToolDefs()
		}
		return nil // 纯流式，不携带工具
	default:
		return nil
	}
}

// ─── 工具执行器 ─────────────────────────────────────────────

// ToolExecutorConfig 配置工具执行器。
type ToolExecutorConfig struct {
	Search     *tools.SearchClient
	Session    *WritingSession
	ExecCtx    *engine.ExecutionContext
	Emitter    engine.EventEmitter
	Profile    *profile.StyleProfile
	LLM        *tools.LLMClient
	Jiaozhen   interface{} // *jiaozhen.Client — 事实核查
	Sensitive  interface{} // 敏感词检查
}

// BuildToolExecutor 构建一个 ToolExecutor，用于在 ChatWithTools 中执行 LLM 的工具调用。
func BuildToolExecutor(cfg ToolExecutorConfig) tools.ToolExecutor {
	return func(name string, arguments string) (string, error) {
		switch name {
		case "search_web":
			return executeSearchWeb(cfg, arguments)
		case "read_source":
			return executeReadSource(cfg, arguments)
		case "generate_outline":
			return executeGenerateOutline(cfg, arguments)
		case "write_article":
			return executeWriteArticle(cfg, arguments)
		case "review_article":
			return executeReviewArticle(cfg, arguments)
		case "revise_section":
			return executeReviseSection(cfg, arguments)
		default:
			return "", fmt.Errorf("unknown tool: %s", name)
		}
	}
}

// executeSearchWeb 执行网络搜索，并用 LLM 将原始结果压缩为研究简报。
// 单次会话最多搜索 3 次，防止 agent loop 过度消耗 token。
//
// 流程：
//  1. 调用搜索引擎获取原始结果（标题 + 摘要 + URL）
//  2. 将原始结果喂给 LLM，生成结构化研究简报（关键事实 + 数据 + 观点）
//  3. 将简报返回给 agent loop，而非原始搜索结果
//
// 这样做的优势：
//  - 原始结果可能 2000+ token，压缩后约 400-600 token，节省 70%+
//  - LLM 预处理提取关键信息，减少后续 agent loop 的认知负担
//  - 避免后续多轮请求重复携带冗长原始结果
func executeSearchWeb(cfg ToolExecutorConfig, arguments string) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Query == "" {
		return "错误：搜索关键词不能为空", nil
	}
	if cfg.Search == nil || !cfg.Search.HasSources() {
		return "错误：搜索服务不可用", nil
	}

	// 搜索次数限制（防正 token 超支）
	if cfg.Session != nil {
		cfg.Session.SearchCallCount++
		if cfg.Session.SearchCallCount > 3 {
			slog.Info("search_web: call limit reached",
				"trace_id", cfg.ExecCtx.TraceID,
				"call_count", cfg.Session.SearchCallCount,
			)
			return fmt.Sprintf("已达到搜索次数上限（3次）。已有 %d 条搜索结果，请直接使用 read_source 读取详情或开始写作。", len(cfg.Session.SearchResults)), nil
		}
	}

	start := time.Now()
	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("search_web", 0)
	}

	results := cfg.Search.Search(context.Background(), args.Query, 5)
	if len(results) == 0 {
		if cfg.Emitter != nil {
			cfg.Emitter.StepComplete("search_web", map[string]any{"query": args.Query, "results": 0}, int64(time.Since(start).Milliseconds()))
		}
		return "未找到搜索结果", nil
	}

	// 保存到 session 供后续复用（read_source 可以读取原始结果）
	if cfg.Session != nil {
		cfg.Session.SearchResults = append(cfg.Session.SearchResults, results...)
	}

	// 用 LLM 将原始搜索结果压缩为研究简报
	brief := compressSearchResults(context.Background(), cfg.LLM, args.Query, results)

	if cfg.Emitter != nil {
		cfg.Emitter.StepComplete("search_web", map[string]any{
			"query":   args.Query,
			"results": len(results),
			"items":   results,
		}, int64(time.Since(start).Milliseconds()))
	}
	return brief, nil
}

// executeReadSource 读取搜索结果全文。
func executeReadSource(cfg ToolExecutorConfig, arguments string) (string, error) {
	var args struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if cfg.Session == nil || args.Index < 1 || args.Index > len(cfg.Session.SearchResults) {
		return fmt.Sprintf("错误：序号超出范围 (1-%d)", lenSafe(cfg.Session)), nil
	}

	r := cfg.Session.SearchResults[args.Index-1]
	formatted := fmt.Sprintf("标题: %s\n来源: %s\n摘要: %s\nURL: %s", r.Title, r.Source, r.Snippet, r.URL)

	start := time.Now()
	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("read_source", 0)
		cfg.Emitter.StepComplete("read_source", map[string]any{
			"index":  args.Index,
			"title":  r.Title,
			"source": r.Source,
		}, int64(time.Since(start).Milliseconds()))
	}
	return formatted, nil
}

// executeWriteArticle 是"信号工具"——告诉 LLM 开始流式输出文章。
func executeWriteArticle(cfg ToolExecutorConfig, arguments string) (string, error) {
	var args struct {
		Topic string `json:"topic"`
	}
	_ = json.Unmarshal([]byte(arguments), &args)

	start := time.Now()
	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("write_article", 0)
		cfg.Emitter.StepComplete("write_article", map[string]any{"topic": args.Topic}, int64(time.Since(start).Milliseconds()))
	}

	// 返回指令，让 LLM 在下一轮直接输出文章
	return "好的，请直接输出文章内容（Markdown格式，以##开头作为标题）。文章会实时展示给用户。", nil
}

// executeReviewArticle 对文章进行质量评审。
// 复用现有 PostReviewStep 的评审逻辑。
func executeReviewArticle(cfg ToolExecutorConfig, arguments string) (string, error) {
	var args struct {
		Article string `json:"article"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	start := time.Now()
	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("review_article", 0)
	}

	// 使用 LLM 做快速评审
	review := quickReviewArticle(context.Background(), cfg.LLM, args.Article, cfg.Profile)

	if cfg.Session != nil {
		cfg.Session.ReviewResult = review
		cfg.Session.Reviewed = true
	}

	formatted := formatReviewResult(review)

	if cfg.Emitter != nil {
		cfg.Emitter.StepComplete("review_article", review, int64(time.Since(start).Milliseconds()))
	}
	return formatted, nil
}

// executeReviseSection 是"信号工具"——告诉 LLM 定向修改并输出修改后的完整文章。
func executeReviseSection(cfg ToolExecutorConfig, arguments string) (string, error) {
	var args struct {
		SectionHint string `json:"section_hint"`
		Instruction string `json:"instruction"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	start := time.Now()
	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("revise_section", 0)
		// 修正前先重置流式内容
		cfg.Emitter.StreamReset()
		cfg.Emitter.StepComplete("revise_section", map[string]any{
			"section_hint": args.SectionHint,
			"instruction": args.Instruction,
		}, int64(time.Since(start).Milliseconds()))
	}

	return fmt.Sprintf("请修改「%s」：%s。直接输出修改后的完整文章。", args.SectionHint, args.Instruction), nil
}

// executeGenerateOutline 生成文章提纲并等待用户确认。
// 这是 Guided 模式的核心工具：
//  1. 调用 LLM 生成提纲（标题 + 3-5 个要点）
//  2. 通过 emitter.AwaitInput 推送给前端
//  3. 阻塞等待用户确认/编辑/重新生成
//  4. 将确认后的提纲存入 session 和 execCtx
func executeGenerateOutline(cfg ToolExecutorConfig, arguments string) (string, error) {
	var args struct {
		Topic string `json:"topic"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Topic == "" {
		args.Topic = cfg.ExecCtx.UserInput
	}

	start := time.Now()
	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("generate_outline", 0)
	}

	// 1. 调用 LLM 生成提纲
	outline, err := generateOutlineWithLLM(context.Background(), cfg.LLM, args.Topic, 0.3)
	if err != nil {
		return fmt.Sprintf("提纲生成失败: %v", err), nil
	}

	// 2. 推送给前端
	if cfg.Emitter != nil {
		cfg.Emitter.AwaitInput("generate_outline", outline, []string{"confirm", "edit", "regenerate"}, 1, 5)
	}

	// 3. 等待用户确认
	if cfg.ExecCtx == nil {
		return "错误：无法等待用户确认（execCtx 不可用）", nil
	}

	confirmedData, err := cfg.ExecCtx.WaitForConfirmWithTimeout(context.Background(), cfg.ExecCtx.ConfirmTimeout)
	if err != nil {
		if err == engine.ErrConfirmTimeout {
			slog.Warn("outline confirm timeout, auto-confirming",
				"trace_id", cfg.ExecCtx.TraceID)
		} else if err == engine.ErrClientDisconnected {
			return "用户已断开连接，提纲未确认", nil
		} else {
			return fmt.Sprintf("等待确认失败: %v", err), nil
		}
	}

	// 4. 处理用户反馈
	if confirmedData != nil {
		if action, ok := confirmedData["action"].(string); ok && action == "regenerate" {
			// 重新生成
			outline, err = generateOutlineWithLLM(context.Background(), cfg.LLM, args.Topic, 0.6)
			if err != nil {
				return fmt.Sprintf("提纲重新生成失败: %v", err), nil
			}
		}
		// 用户编辑了提纲
		if title, ok := confirmedData["title"].(string); ok && title != "" {
			outline.Title = title
		}
		if outlineArr, ok := confirmedData["outline"].([]interface{}); ok {
			items := make([]engine.OutlineItem, 0, len(outlineArr))
			for _, item := range outlineArr {
				if m, ok := item.(map[string]interface{}); ok {
					items = append(items, engine.OutlineItem{
						Point: getString(m, "point"),
						Type:  getString(m, "type"),
					})
				}
			}
			if len(items) > 0 {
				outline.Outline = items
			}
		}
	}

	// 5. 存入 session 和 execCtx
	if cfg.Session != nil {
		cfg.Session.Outline = outline
	}
	cfg.ExecCtx.Outline = outline

	if cfg.Emitter != nil {
		cfg.Emitter.StepComplete("generate_outline", outline, int64(time.Since(start).Milliseconds()))
	}

	return fmt.Sprintf("提纲已确认。标题：%s。请开始按提纲写作，标题必须原样使用。", outline.Title), nil
}

// generateOutlineWithLLM 调用 LLM 生成文章提纲。
func generateOutlineWithLLM(ctx context.Context, llm *tools.LLMClient, topic string, temperature float64) (*engine.OutlineData, error) {
	if llm == nil {
		return nil, fmt.Errorf("LLM client not available")
	}

	systemMsg := "你是写作提纲生成器。根据话题生成文章提纲。只返回 JSON。"
	userMsg := fmt.Sprintf(`话题：%s

请生成提纲，包含标题和3-5个要点。

返回格式：
{
  "title": "文章标题",
  "outline": [
    {"point": "要点内容", "type": "opening"},
    {"point": "要点内容", "type": "argument"},
    {"point": "要点内容", "type": "conclusion"}
  ]
}`, topic)

	resp, _, err := llm.Chat(ctx, []tools.LLMMessage{
		{Role: "user", Content: userMsg},
	}, tools.WithInstructions(systemMsg), tools.WithModel(tools.ModelV4Pro), tools.WithTemperature(temperature), tools.WithThinking(true), tools.WithReasoningEffort("high"))
	if err != nil {
		return nil, fmt.Errorf("outline generation failed: %w", err)
	}

	jsonStr := tools.ExtractJSONObject(resp)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON in outline response")
	}

	var outline engine.OutlineData
	if err := json.Unmarshal([]byte(jsonStr), &outline); err != nil {
		return nil, fmt.Errorf("failed to parse outline: %w", err)
	}

	return &outline, nil
}

// ─── 辅助函数 ───────────────────────────────────────────────

// compressSearchResults 用 LLM 将原始搜索结果压缩为结构化研究简报。
//
// 输入：搜索关键词 + 原始结果列表（标题 + 摘要 + URL）
// 输出：结构化研究简报（关键事实、数据、观点、来源引用编号）
//
// 优势：
//   - 原始结果可能 2000+ token，压缩后约 400-600 token，节省 70%+
//   - LLM 预处理提取关键信息，减少后续 agent loop 的认知负担
//   - 避免后续多轮请求重复携带冗长原始结果
//
// 如果 LLM 不可用或压缩失败，回退到 formatSearchResults（简单截断摘要）。
func compressSearchResults(ctx context.Context, llm *tools.LLMClient, query string, results []engine.SearchResult) string {
	if llm == nil || len(results) == 0 {
		return formatSearchResults(results)
	}

	// 构建原始结果文本（带序号，供 LLM 引用）
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
	}, tools.WithModel(tools.ModelV4Pro), tools.WithTemperature(0.1), tools.WithThinking(false))

	if err != nil {
		slog.Warn("compressSearchResults: LLM compression failed, falling back",
			"error", err,
			"query", query,
			"results", len(results),
		)
		return formatSearchResults(results)
	}

	if resp == "" {
		return formatSearchResults(results)
	}

	slog.Info("search_web: results compressed to research brief",
		"query", query,
		"raw_results", len(results),
		"brief_chars", len([]rune(resp)),
	)
	return resp
}

// formatSearchResults 是 compressSearchResults 的 fallback。
// 当 LLM 不可用或压缩失败时，使用简单截断摘要。
func formatSearchResults(results []engine.SearchResult) string {
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

func formatReviewResult(r *engine.ReviewResult) string {
	if r == nil {
		return "评审未返回结果"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("评审结果：%s\n", map[bool]string{true: "通过", false: "未通过"}[r.Passed]))
	sb.WriteString("评分：\n")
	for dim, score := range r.Scores {
		sb.WriteString(fmt.Sprintf("  %s: %.2f\n", dim, score))
	}
	if len(r.Issues) > 0 {
		sb.WriteString("问题：\n")
		for _, issue := range r.Issues {
			sb.WriteString(fmt.Sprintf("  [%s] %s: %s\n", issue.Severity, issue.Type, issue.Message))
		}
	}
	return sb.String()
}

func lenSafe(s *WritingSession) int {
	if s == nil {
		return 0
	}
	return len(s.SearchResults)
}

// getString safely extracts a string from a map[string]interface{}.
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// quickReviewArticle 使用 LLM 对文章做快速评审。
// 这是 review_article 工具的后端实现。
func quickReviewArticle(ctx context.Context, llm *tools.LLMClient, article string, p *profile.StyleProfile) *engine.ReviewResult {
	if llm == nil || len(article) < 50 {
		return &engine.ReviewResult{
			Scores: map[string]float64{},
			Issues: []engine.ReviewIssue{
				{Severity: "low", Type: "review_skipped", Message: "文章过短或 LLM 不可用，跳过评审"},
			},
			Passed: true,
		}
	}

	systemMsg := "你是文章质量评审员。只返回 JSON。"
	currentTime := time.Now().Format("2006年1月2日")

	userMsg := fmt.Sprintf(`请评审以下文章正文：

%s

评审维度：factuality（事实准确性）、structure（结构合规）、style（风格符合）、rhetoric（修辞运用）、length（篇幅控制）、safety（内容安全）
当前日期：%s

返回格式：
{
  "scores": {"factuality": 0.9, "structure": 0.85, "style": 0.8, "rhetoric": 0.85, "length": 0.9, "safety": 0.95},
  "issues": [{"severity": "high", "type": "fact", "message": "..."}],
  "passed": true
}`, article, currentTime)

	resp, _, err := llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithModel(tools.ModelV4Pro), tools.WithTemperature(0), tools.WithThinking(true), tools.WithReasoningEffort("high"), tools.WithJSONResponse())

	if err != nil {
		return &engine.ReviewResult{
			Scores: map[string]float64{},
			Issues: []engine.ReviewIssue{
				{Severity: "medium", Type: "review_skipped", Message: "质量评审因服务异常被跳过（已自动放行）"},
			},
			Passed: true,
		}
	}

	jsonStr := tools.ExtractJSONObject(resp)
	if jsonStr == "" {
		return &engine.ReviewResult{
			Scores: map[string]float64{},
			Issues: []engine.ReviewIssue{
				{Severity: "medium", Type: "review_skipped", Message: "质量评审因响应格式异常被跳过"},
			},
			Passed: true,
		}
	}

	var review engine.ReviewResult
	if err := json.Unmarshal([]byte(jsonStr), &review); err != nil {
		return &engine.ReviewResult{
			Scores: map[string]float64{},
			Issues: []engine.ReviewIssue{
				{Severity: "medium", Type: "review_skipped", Message: "质量评审因解析异常被跳过"},
			},
			Passed: true,
		}
	}

	return &review
}
