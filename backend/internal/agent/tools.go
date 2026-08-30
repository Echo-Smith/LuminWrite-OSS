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
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── 写作工具集 ────────────────────────────────────────────
//
// 工具按意图分组，LLM 在持续会话中自主调用。
// search_web 和 search_knowledge 是并行关系 —— LLM 可在同一轮
// 同时调用两个 tool，后端并发执行。
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
// 包含 search_web + search_knowledge + read_source + generate_outline + write_article + review_article + revise_section + retrieve_context。
// 调用方通过 ToolsForIntent 按需裁剪（移除 search_knowledge/generate_outline/search_web）。
func WritingToolDefs() []tools.ToolDef {
	return []tools.ToolDef{
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "search_web",
				Description: "搜索互联网获取最新信息。适用于：时事热点、公开数据、新闻资讯、网络观点。返回结构化研究简报。",
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
				Name:        "retrieve_context",
				Description: "按需检索会话上下文。当你需要某段具体信息但当前 prompt 中没有提供时使用。支持检索：当前文章的特定段落、用户记忆偏好、历史对话中的关键决策、已收集的搜索素材等。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "你想查询的内容描述。例如：'用户喜欢的修辞风格'、'文章第三段的内容'、'关于AI的搜索素材'、'用户之前的修改意见'",
						},
						"source": map[string]any{
							"type":        "string",
							"description": "检索来源：article(当前文章全文)、memory(用户写作偏好和历史记忆)、history(本轮对话历史)、search(已收集的搜索素材)、profile(当前风格配置)",
							"enum":        []string{"article", "memory", "history", "search", "profile"},
						},
						"limit": map[string]any{
							"type":        "integer",
							"description": "返回结果的最大条数/段落数，默认3",
						},
					},
					"required": []string{"query", "source"},
				},
			},
		},
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "search_knowledge",
				Description: "搜索个人素材库（用户上传的参考素材、历史文章等）。适用于：参考素材检索、历史范文参考、内部观点。返回精确到段落的检索结果。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "检索关键词或自然语言问题",
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
				Description: "读取已有搜索结果的完整内容。传入结果序号(1-based)获取更多详情。涵盖 search_web 和 search_knowledge 的结果。",
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
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "word_count_check",
				Description: "检查文章字数是否符合风格要求。返回当前字数、目标范围、达标状态和调整建议。在写完文章后、评审前调用。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"article": map[string]any{
							"type":        "string",
							"description": "文章内容",
						},
					},
					"required": []string{"article"},
				},
			},
		},
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "rewrite_title",
				Description: "标题优化器。传入当前标题和文章内容，返回3个备选标题及推荐理由。适用于对标题不满意时获取灵感。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"current_title": map[string]any{
							"type":        "string",
							"description": "当前标题",
						},
						"article": map[string]any{
							"type":        "string",
							"description": "文章内容",
						},
					},
					"required": []string{"current_title", "article"},
				},
			},
		},
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "fact_check",
				Description: "对文章中的关键事实进行核查。提取文章中的事实性声明，通过搜索验证其准确性。返回核查结果和建议。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"article": map[string]any{
							"type":        "string",
							"description": "文章内容",
						},
						"focus": map[string]any{
							"type":        "string",
							"description": "重点核查的内容（如'数据'、'人名'、'事件时间'），可选",
						},
					},
					"required": []string{"article"},
				},
			},
		},
	}
}

// ChatToolDefs 是对话意图的工具集（搜索 + 读取）。
// hasKnowledge=true 时包含 search_knowledge 工具。
func ChatToolDefs(hasKnowledge bool) []tools.ToolDef {
	defs := []tools.ToolDef{
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "search_web",
				Description: "搜索互联网获取最新信息。适用于时事热点、公开数据、新闻资讯。",
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
	}
	if hasKnowledge {
		defs = append(defs, tools.ToolDef{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "search_knowledge",
				Description: "搜索内部知识库。适用于写作风格规范、历史文章参考、栏目调性。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "检索关键词",
						},
					},
					"required": []string{"query"},
				},
			},
		})
	}
	defs = append(defs, tools.ToolDef{
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
	})
	return defs
}

// ReviseToolDefs 是修改意图的工具集（搜索 + 修正）。
// hasKnowledge=true 时包含 search_knowledge 工具。
func ReviseToolDefs(hasKnowledge bool) []tools.ToolDef {
	defs := []tools.ToolDef{
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
	}
	if hasKnowledge {
		defs = append(defs, tools.ToolDef{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "search_knowledge",
				Description: "搜索知识库获取风格参考或历史文章。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "检索关键词",
						},
					},
					"required": []string{"query"},
				},
			},
		})
	}
	defs = append(defs, tools.ToolDef{
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
	})
	return defs
}

// ToolsForIntent 按意图返回工具集。
// guided=true 时在写作工具集中加入 generate_outline 工具。
// hasKnowledge=true 时加入 search_knowledge 工具。
// 使用 variadic bool 参数保持向后兼容：[0]=guided, [1]=hasKnowledge
func ToolsForIntent(intent Intent, hasSearch bool, flags ...bool) []tools.ToolDef {
	isGuided := len(flags) > 0 && flags[0]
	hasKB := len(flags) > 1 && flags[1]
	switch intent {
	case IntentWriting:
		all := WritingToolDefs()
		// 按 name 过滤，避免索引偏移问题
		all = filterTools(all, func(name string) bool {
			if !isGuided && name == "generate_outline" {
				return false
			}
			if !hasKB && name == "search_knowledge" {
				return false
			}
			if !hasSearch && (name == "search_web" || name == "read_source") {
				return false
			}
			return true
		})
		return all
	case IntentPolish, IntentShorten, IntentExpand, IntentExtract:
		defs := ReviseToolDefs(hasKB)
		if !hasSearch {
			defs = filterTools(defs, func(name string) bool {
				return name != "search_web"
			})
		}
		return defs
	case IntentChat:
		if hasSearch || hasKB {
			return ChatToolDefs(hasKB)
		}
		return nil
	default:
		return nil
	}
}

// filterTools 返回 names 中 keep(name)==true 的工具定义。
func filterTools(defs []tools.ToolDef, keep func(string) bool) []tools.ToolDef {
	result := make([]tools.ToolDef, 0, len(defs))
	for _, d := range defs {
		if keep(d.Function.Name) {
			result = append(result, d)
		}
	}
	return result
}

// ─── 工具执行器 ─────────────────────────────────────────────

// ToolSettleFunc 是工具调用后的扣费回调。
// toolType: search | fact_check | url_fetch
// 返回扣减的积分数量和错误（错误不阻塞工具执行，仅记录日志）。
type ToolSettleFunc func(ctx context.Context, userID, traceID, toolType string) (float64, error)

// ToolExecutorConfig 配置工具执行器。
type ToolExecutorConfig struct {
	Search     *tools.SearchClient     // 网络搜索（Tavily/Bing/腾讯等）
	KBSearcher tools.KnowledgeSearcher // 知识库搜索（本地 BM25+Dense+GraphRAG）
	Session    *WritingSession
	ExecCtx    *engine.ExecutionContext
	Emitter    engine.EventEmitter
	Profile    *profile.StyleProfile
	LLM        *tools.LLMClient
	Jiaozhen   interface{} // *jiaozhen.Client — 事实核查
	Sensitive  interface{} // 敏感词检查

	// Guard: 声明式调用次数限制（tool name → max calls, 0=unlimited）
	// 由 Harness 从 ToolDescriptor.MaxCalls 传入。
	// 当工具调用次数达到上限时，返回礼貌消息而非执行。
	MaxCalls   map[string]int
	callCounts map[string]int // 运行时计数器（非导出，由 BuildToolExecutor 初始化）

	// 计费回调：工具执行成功后按名称扣费
	// 由 Server 创建 Harness 时注入，nil = 不扣费（开源版无此回调）
	SettleFunc ToolSettleFunc
}

// BuildToolExecutor 构建一个 ToolExecutor，用于在 ChatWithTools 中执行 LLM 的工具调用。
//
// Guard 机制：在执行任何工具前，先检查 MaxCalls 限制。
// 如果工具调用次数已达上限，返回礼貌消息而非执行工具逻辑。
// 这替代了以前硬编码在 executeSearchWeb 中的 SearchCallCount 检查。
func BuildToolExecutor(cfg ToolExecutorConfig) tools.ToolExecutor {
	if cfg.callCounts == nil {
		cfg.callCounts = make(map[string]int)
	}
	return func(name string, arguments string) (string, error) {
		// ── Guard: MaxCalls 检查 ──
		if max, ok := cfg.MaxCalls[name]; ok && max > 0 {
			current := cfg.callCounts[name]
			if current >= max {
				slog.Info("tool guard: max calls reached",
					"tool", name,
					"current", current,
					"max", max,
					"trace_id", cfg.ExecCtx.TraceID,
				)
				return fmt.Sprintf("已达到调用次数上限（%d次）。已有 %d 条搜索结果，请直接使用 read_source 读取详情或开始写作。", max, len(cfg.Session.SearchResults)), nil
			}
			cfg.callCounts[name]++
		}

		// ── Record step start in execCtx.StepHistory ──
		startTime := time.Now()
		cfg.ExecCtx.CurrentStep = engine.StepName(name)
		stepRecord := engine.StepRecord{
			Step:      engine.StepName(name),
			Status:    "running",
			StartedAt: &startTime,
		}
		cfg.ExecCtx.StepHistory = append(cfg.ExecCtx.StepHistory, stepRecord)

		result, err := executeToolByName(name, cfg, arguments)
		durationMs := time.Since(startTime).Milliseconds()

		// ── 计费：工具执行成功后按名称扣费 ──
		if err == nil && cfg.SettleFunc != nil {
			if toolType := toolNameToBillingType(name); toolType != "" {
				userID := cfg.ExecCtx.UserID
				traceID := cfg.ExecCtx.TraceID
				if userID != "" && userID != "anonymous" {
					points, settleErr := cfg.SettleFunc(context.Background(), userID, traceID, toolType)
					if settleErr != nil {
						slog.Warn("billing: settle tool points failed",
							"tool", name, "error", settleErr, "trace_id", traceID)
					} else if points > 0 {
						slog.Info("billing: tool points settled",
							"tool", name, "points", points, "trace_id", traceID)
					}
				}
			}
		}

		// ── Update step record with result ──
		if len(cfg.ExecCtx.StepHistory) > 0 {
			last := &cfg.ExecCtx.StepHistory[len(cfg.ExecCtx.StepHistory)-1]
			completedAt := time.Now()
			last.CompletedAt = &completedAt
			last.DurationMs = durationMs
			if err != nil {
				last.Status = "error"
				last.Error = err.Error()
			} else {
				last.Status = "complete"
				// Extract step result from execCtx if available
				if sr := engine.GetStepResult(engine.StepName(name), cfg.ExecCtx); sr != nil {
					last.Result = sr
				}
			}
		}

		return result, err
	}
}

// executeToolByName dispatches tool execution by name.
func executeToolByName(name string, cfg ToolExecutorConfig, arguments string) (string, error) {
	switch name {
	case "search_web":
		return executeSearchWeb(cfg, arguments)
	case "search_knowledge":
		return executeSearchKnowledge(cfg, arguments)
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
	case "word_count_check":
		return executeWordCountCheck(cfg, arguments)
	case "rewrite_title":
		return executeRewriteTitle(cfg, arguments)
	case "fact_check":
		return executeFactCheck(cfg, arguments)
	case "retrieve_context":
		return executeRetrieveContext(cfg, arguments)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// toolNameToBillingType 将工具名映射为计费类型。
// 返回空字符串表示该工具不需要扣费。
func toolNameToBillingType(toolName string) string {
	switch toolName {
	case "search_web", "search_knowledge":
		return "search"
	case "fact_check":
		return "fact_check"
	case "read_source":
		return "url_fetch"
	default:
		return ""
	}
}

// executeSearchWeb 执行网络搜索，并用 LLM 将原始结果压缩为研究简报。
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

	// 注：搜索次数限制已移至 BuildToolExecutor 的声明式 MaxCalls guard，
	// 不再在此处硬编码。由 Harness 通过 ToolExecutorConfig.MaxCalls 配置。

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
	// 增量去重：排除已在 Session.SearchResults 中的重复条目（URL 精确 + 标题 Jaccard）
	if cfg.Session != nil {
		unique := tools.DedupAgainstExisting(cfg.Session.SearchResults, results)
		if len(unique) < len(results) {
			slog.Info("search_web: incremental dedup removed duplicates",
				"query", args.Query,
				"new_results", len(results),
				"unique", len(unique),
				"removed", len(results)-len(unique),
			)
		}

		// 如果所有结果都与已有结果重复，告诉 LLM 不要再用同一 query 搜索
		if len(unique) == 0 && len(cfg.Session.SearchResults) > 0 {
			if cfg.Emitter != nil {
				cfg.Emitter.StepComplete("search_web", map[string]any{
					"query":   args.Query,
					"results": 0,
					"reason":  "all duplicates",
				}, int64(time.Since(start).Milliseconds()))
			}
			return fmt.Sprintf("搜索「%s」的所有结果与已有结果重复（已有 %d 条）。请使用 read_source 读取已有结果，或换一个更具体的关键词。", args.Query, len(cfg.Session.SearchResults)), nil
		}

		cfg.Session.SearchResults = append(cfg.Session.SearchResults, unique...)
	}

	// 用 LLM 将原始搜索结果压缩为研究简报
	brief, _, _ := tools.CompressSearchResults(context.Background(), cfg.LLM, args.Query, results)

	if cfg.Emitter != nil {
		cfg.Emitter.StepComplete("search_web", map[string]any{
			"query":   args.Query,
			"results": len(results),
			"items":   results,
		}, int64(time.Since(start).Milliseconds()))
	}
	return brief, nil
}

// executeSearchKnowledge 执行知识库搜索（BM25 + Dense + GraphRAG）。
// 与 search_web 不同，这里直接返回 chunk 级精确内容，不做 LLM 压缩
// —— 知识库内容是确定性知识，原始片段比 LLM 压缩更可靠。
func executeSearchKnowledge(cfg ToolExecutorConfig, arguments string) (string, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Query == "" {
		return "错误：检索关键词不能为空", nil
	}
	if cfg.KBSearcher == nil {
		return "错误：知识库不可用", nil
	}

	start := time.Now()
	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("search_knowledge", 0)
	}

	// When the style profile binds to a specific KB (KbID), scope the search
	// to that KB only; otherwise search all KBs.
	var results []engine.SearchResult
	var err error
	kbID := ""
	if cfg.Profile != nil {
		kbID = cfg.Profile.KbID
	}
	if kbID != "" {
		results, err = cfg.KBSearcher.SearchKBInKB(context.Background(), cfg.Session.UserID, kbID, args.Query, 5)
	} else {
		results, err = cfg.KBSearcher.SearchKB(context.Background(), cfg.Session.UserID, args.Query, 5)
	}
	if err != nil {
		slog.Warn("search_knowledge: search failed",
			"error", err,
			"query", args.Query,
		)
		if cfg.Emitter != nil {
			cfg.Emitter.StepComplete("search_knowledge", map[string]any{"query": args.Query, "error": err.Error()}, int64(time.Since(start).Milliseconds()))
		}
		return fmt.Sprintf("知识库检索失败: %v", err), nil
	}
	if len(results) == 0 {
		if cfg.Emitter != nil {
			cfg.Emitter.StepComplete("search_knowledge", map[string]any{"query": args.Query, "results": 0}, int64(time.Since(start).Milliseconds()))
		}
		return "未找到相关知识", nil
	}

	// 保存到 session 供 read_source 复用
	// 增量去重：排除已在 Session.SearchResults 中的重复条目
	if cfg.Session != nil {
		unique := tools.DedupAgainstExisting(cfg.Session.SearchResults, results)
		if len(unique) < len(results) {
			slog.Info("search_knowledge: incremental dedup removed duplicates",
				"query", args.Query,
				"new_results", len(results),
				"unique", len(unique),
				"removed", len(results)-len(unique),
			)
		}
		cfg.Session.SearchResults = append(cfg.Session.SearchResults, unique...)
	}

	formatted := formatKnowledgeResults(results)

	if cfg.Emitter != nil {
		cfg.Emitter.StepComplete("search_knowledge", map[string]any{
			"query":   args.Query,
			"results": len(results),
			"items":   results,
		}, int64(time.Since(start).Milliseconds()))
	}
	return formatted, nil
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
	// 净化外部内容：防止搜索结果中的 prompt injection 进入 LLM 上下文
	title := engine.SanitizeExternalContent(r.Title, fmt.Sprintf("read_source[%d].title", args.Index))
	snippet := engine.SanitizeExternalContent(r.Snippet, fmt.Sprintf("read_source[%d].snippet", args.Index))
	formatted := fmt.Sprintf("标题: %s\n来源: %s\n摘要: %s\nURL: %s", title, r.Source, snippet, r.URL)

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

	return "好的，请直接输出完整文章。文章会实时展示给用户。\n\n" + profile.MarkdownArticleOutputReminder, nil
}

// executeReviewArticle 对文章进行质量评审。
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
// 注入 profile.RenderWritingConstraints() 确保修正后的文章仍然符合风格约束，
// 防止修正过程中风格漂移。
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
		cfg.Emitter.StreamReset()
		cfg.Emitter.StepComplete("revise_section", map[string]any{
			"section_hint": args.SectionHint,
			"instruction":  args.Instruction,
		}, int64(time.Since(start).Milliseconds()))
	}

	// 注入风格约束，确保修正后的文章仍符合 Profile 规范
	styleConstraints := ""
	if cfg.Profile != nil {
		styleConstraints = cfg.Profile.RenderWritingConstraints("writing", false, 0)
	}

	return fmt.Sprintf("请修改「%s」：%s。\n直接输出修改后的完整文章。\n%s\n\n%s", args.SectionHint, args.Instruction, styleConstraints, profile.MarkdownArticleOutputReminder), nil
}

// executeWordCountCheck 检查文章字数是否符合风格要求。
func executeWordCountCheck(cfg ToolExecutorConfig, arguments string) (string, error) {
	var args struct {
		Article string `json:"article"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Article == "" {
		return "错误：文章内容不能为空", nil
	}

	start := time.Now()
	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("word_count_check", 0)
	}

	// 计算字数（去除 Markdown 标记和空白）
	cleanText := strings.NewReplacer(
		" ", "", "\t", "", "\n", "", "\r", "",
		"#", "", "*", "", ">", "", "-", "", "`", "", "~", "", "_", "",
	).Replace(args.Article)
	count := len([]rune(cleanText))

	// 从 Profile 获取目标字数范围
	var targetMin, targetMax int
	if cfg.Profile != nil && cfg.Profile.WordRange.Max > 0 {
		targetMin = cfg.Profile.WordRange.Min
		targetMax = cfg.Profile.WordRange.Max
	}
	if targetMin == 0 {
		targetMin = 1000
		targetMax = 2000
	}

	status := "达标"
	suggestion := ""
	if count < targetMin {
		status = "偏少"
		suggestion = fmt.Sprintf("建议扩充内容，至少再写 %d 字。", targetMin-count)
	} else if count > targetMax {
		status = "偏多"
		suggestion = fmt.Sprintf("建议精简内容，删减约 %d 字。", count-targetMax)
	}

	result := fmt.Sprintf("字数检查结果：\n当前字数：%d 字\n目标范围：%d-%d 字\n状态：%s\n%s", count, targetMin, targetMax, status, suggestion)

	if cfg.Emitter != nil {
		cfg.Emitter.StepComplete("word_count_check", map[string]any{
			"count":      count,
			"target_min": targetMin,
			"target_max": targetMax,
			"status":     status,
		}, int64(time.Since(start).Milliseconds()))
	}
	return result, nil
}

// executeRewriteTitle 标题优化器，用 LLM 生成 3 个备选标题。
func executeRewriteTitle(cfg ToolExecutorConfig, arguments string) (string, error) {
	var args struct {
		CurrentTitle string `json:"current_title"`
		Article      string `json:"article"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Article == "" {
		return "错误：文章内容不能为空", nil
	}

	start := time.Now()
	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("rewrite_title", 0)
	}

	// 截取文章前 500 字供 LLM 参考
	articlePreview := args.Article
	if len([]rune(articlePreview)) > 500 {
		articlePreview = string([]rune(articlePreview)[:500]) + "…"
	}

	systemMsg := "你是标题优化专家。根据文章内容生成3个备选标题，每个标题附简要推荐理由。只返回 JSON。"
	userMsg := fmt.Sprintf(`当前标题：%s

文章内容（节选）：
%s

请生成3个备选标题，格式如下：
{
  "titles": [
    {"title": "标题1", "reason": "推荐理由"},
    {"title": "标题2", "reason": "推荐理由"},
    {"title": "标题3", "reason": "推荐理由"}
  ]
}

要求：
1. 标题要简洁有力，15字以内
2. 风格要匹配文章调性
3. 第一个标题偏向正式，第二个偏向文艺，第三个偏向吸引眼球`, args.CurrentTitle, articlePreview)

	resp, _, err := cfg.LLM.Chat(context.Background(), []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0.7), tools.WithThinking(false))
	if err != nil {
		if cfg.Emitter != nil {
			cfg.Emitter.StepComplete("rewrite_title", map[string]any{"error": err.Error()}, int64(time.Since(start).Milliseconds()))
		}
		return fmt.Sprintf("标题生成失败: %v", err), nil
	}

	// 尝试解析 JSON
	jsonStr := tools.ExtractJSONObject(resp)
	result := resp
	if jsonStr != "" {
		var titles struct {
			Titles []struct {
				Title  string `json:"title"`
				Reason string `json:"reason"`
			} `json:"titles"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &titles); err == nil && len(titles.Titles) > 0 {
			var sb strings.Builder
			sb.WriteString("备选标题：\n")
			for i, t := range titles.Titles {
				sb.WriteString(fmt.Sprintf("%d. %s\n   理由：%s\n", i+1, t.Title, t.Reason))
			}
			result = sb.String()
		}
	}

	if cfg.Emitter != nil {
		cfg.Emitter.StepComplete("rewrite_title", map[string]any{
			"current_title": args.CurrentTitle,
			"suggestions":   3,
		}, int64(time.Since(start).Milliseconds()))
	}
	return result, nil
}

// executeFactCheck 事实核查工具，提取关键事实并通过搜索验证。
func executeFactCheck(cfg ToolExecutorConfig, arguments string) (string, error) {
	var args struct {
		Article string `json:"article"`
		Focus   string `json:"focus"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Article == "" {
		return "错误：文章内容不能为空", nil
	}

	start := time.Now()
	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("fact_check", 0)
	}

	// 用 LLM 提取文章中的事实性声明
	systemMsg := "你是事实核查助理。从文章中提取需要核实的关键事实性声明（如数据、人名、事件、时间等）。只返回 JSON。"
	userMsg := fmt.Sprintf(`文章内容：
%s

请提取文章中的关键事实性声明（最多5个），格式如下：
{
  "claims": [
    {"claim": "事实声明", "type": "数据/人名/时间/事件", "verifiable": true}
  ]
}

要求：
1. 只提取事实性声明，不提取观点
2. 优先提取具体的数字、日期、人名
3. 如果文章中没有事实性声明，返回空列表`, args.Article)

	resp, _, err := cfg.LLM.Chat(context.Background(), []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0.1), tools.WithThinking(false))
	if err != nil {
		if cfg.Emitter != nil {
			cfg.Emitter.StepComplete("fact_check", map[string]any{"error": err.Error()}, int64(time.Since(start).Milliseconds()))
		}
		return fmt.Sprintf("事实核查失败: %v", err), nil
	}

	// 解析提取的声明
	jsonStr := tools.ExtractJSONObject(resp)
	var claims struct {
		Claims []struct {
			Claim      string `json:"claim"`
			Type       string `json:"type"`
			Verifiable bool   `json:"verifiable"`
		} `json:"claims"`
	}
	if jsonStr != "" {
		_ = json.Unmarshal([]byte(jsonStr), &claims)
	}

	if len(claims.Claims) == 0 {
		result := "未检测到需要核实的事实性声明。文章中的内容以观点性表述为主。"
		if cfg.Emitter != nil {
			cfg.Emitter.StepComplete("fact_check", map[string]any{"claims": 0}, int64(time.Since(start).Milliseconds()))
		}
		return result, nil
	}

	// 对每个声明进行搜索验证（如果有搜索服务）
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("提取到 %d 条事实性声明：\n\n", len(claims.Claims)))

	if cfg.Search != nil && cfg.Search.HasSources() {
		for i, claim := range claims.Claims {
			results := cfg.Search.Search(context.Background(), claim.Claim, 2)
			sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, claim.Type, claim.Claim))
			if len(results) > 0 {
				sb.WriteString(fmt.Sprintf("   搜索验证：\n   - %s\n   - %s\n", results[0].Title, results[0].Snippet))
			} else {
				sb.WriteString("   未找到相关搜索结果\n")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("建议：以上为搜索到的参考信息，请人工核实关键数据和事实。")
	} else {
		for i, claim := range claims.Claims {
			sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, claim.Type, claim.Claim))
		}
		sb.WriteString("\n注意：搜索服务不可用，无法进行自动验证。请人工核实以上声明。")
	}

	if cfg.Emitter != nil {
		cfg.Emitter.StepComplete("fact_check", map[string]any{
			"claims":   len(claims.Claims),
			"verified": cfg.Search != nil && cfg.Search.HasSources(),
		}, int64(time.Since(start).Milliseconds()))
	}
	return sb.String(), nil
}

// executeRetrieveContext 按需检索会话上下文。
// LLM 通过此工具主动获取所需信息，替代被动全量注入。
func executeRetrieveContext(cfg ToolExecutorConfig, arguments string) (string, error) {
	var args struct {
		Query  string `json:"query"`
		Source string `json:"source"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Query == "" {
		return "错误：查询内容不能为空", nil
	}
	if args.Source == "" {
		return "错误：检索来源不能为空", nil
	}
	if args.Limit == 0 {
		args.Limit = 3
	}

	start := time.Now()
	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("retrieve_context", 0)
	}

	var result string
	switch args.Source {
	case "article":
		result = retrieveFromArticle(cfg, args.Query, args.Limit)
	case "memory":
		result = retrieveFromMemory(cfg, args.Query, args.Limit)
	case "history":
		result = retrieveFromHistory(cfg, args.Query, args.Limit)
	case "search":
		result = retrieveFromSearch(cfg, args.Query, args.Limit)
	case "profile":
		result = retrieveFromProfile(cfg, args.Query, args.Limit)
	default:
		result = fmt.Sprintf("错误：未知的检索来源 '%s'。可选：article, memory, history, search, profile", args.Source)
	}

	if cfg.Emitter != nil {
		cfg.Emitter.StepComplete("retrieve_context", map[string]any{
			"query":  args.Query,
			"source": args.Source,
			"limit":  args.Limit,
		}, int64(time.Since(start).Milliseconds()))
	}
	return result, nil
}

// retrieveFromArticle 从当前文章中检索相关段落。
func retrieveFromArticle(cfg ToolExecutorConfig, query string, limit int) string {
	if cfg.Session == nil || cfg.Session.CurrentArticle == "" {
		return "当前没有文章。请先调用 write_article 写作。"
	}

	// 按段落分割文章
	paragraphs := strings.Split(cfg.Session.CurrentArticle, "\n\n")
	var matches []string

	// 简单关键词匹配（后续可升级为向量检索）
	keywords := extractKeywords(query)
	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		score := matchScore(para, keywords)
		if score > 0 {
			matches = append(matches, para)
		}
	}

	if len(matches) == 0 {
		// 如果没有匹配，返回文章的前几段作为概览
		var sb strings.Builder
		sb.WriteString("未找到精确匹配的段落。以下是文章开头部分：\n\n")
		count := 0
		for _, para := range paragraphs {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}
			sb.WriteString(para)
			sb.WriteString("\n\n")
			count++
			if count >= limit {
				break
			}
		}
		return sb.String()
	}

	// 返回匹配的段落
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 个相关段落：\n\n", len(matches)))
	for i, m := range matches {
		if i >= limit {
			break
		}
		sb.WriteString(fmt.Sprintf("【段落 %d】\n%s\n\n", i+1, m))
	}
	return sb.String()
}

// retrieveFromMemory 从用户记忆中检索相关信息。
func retrieveFromMemory(cfg ToolExecutorConfig, query string, limit int) string {
	if cfg.Session == nil || cfg.Session.MemoryContext == nil {
		return "暂无用户记忆。系统将自动从对话中提取偏好。"
	}

	memCtx, ok := cfg.Session.MemoryContext.(*memory.MemoryContext)
	if !ok || memCtx == nil {
		return "记忆服务暂不可用。"
	}

	var allEntries []memory.MemoryEntry
	allEntries = append(allEntries, memCtx.Injected...)
	allEntries = append(allEntries, memCtx.ReviewGuard...)

	if len(allEntries) == 0 {
		return "暂无用户写作偏好记录。"
	}

	// 关键词匹配
	keywords := extractKeywords(query)
	var matches []memory.MemoryEntry
	for _, entry := range allEntries {
		if matchScore(entry.Value, keywords) > 0 || matchScore(entry.Category, keywords) > 0 {
			matches = append(matches, entry)
		}
	}

	if len(matches) == 0 {
		// 返回所有记忆作为概览
		var sb strings.Builder
		sb.WriteString("未找到精确匹配的记忆。以下是用户的所有写作偏好：\n\n")
		for i, entry := range allEntries {
			if i >= limit {
				break
			}
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", entry.Category, entry.Value))
		}
		return sb.String()
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条相关记忆：\n\n", len(matches)))
	for i, entry := range matches {
		if i >= limit {
			break
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", entry.Category, entry.Value))
	}
	return sb.String()
}

// retrieveFromHistory 从对话历史中检索相关信息。
func retrieveFromHistory(cfg ToolExecutorConfig, query string, limit int) string {
	if cfg.Session == nil || len(cfg.Session.Messages) == 0 {
		return "暂无对话历史。"
	}

	keywords := extractKeywords(query)
	var matches []memory.ConversationMessage

	// 从最近的消息开始搜索
	for i := len(cfg.Session.Messages) - 1; i >= 0; i-- {
		msg := cfg.Session.Messages[i]
		if matchScore(msg.Content, keywords) > 0 {
			matches = append(matches, msg)
		}
	}

	if len(matches) == 0 {
		// 返回最近的几条消息
		var sb strings.Builder
		sb.WriteString("未找到精确匹配的历史消息。以下是最近的几轮对话：\n\n")
		start := len(cfg.Session.Messages) - limit
		if start < 0 {
			start = 0
		}
		for i := start; i < len(cfg.Session.Messages); i++ {
			msg := cfg.Session.Messages[i]
			role := string(msg.Role)
			content := msg.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", role, content))
		}
		return sb.String()
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条相关历史消息：\n\n", len(matches)))
	for i, msg := range matches {
		if i >= limit {
			break
		}
		role := string(msg.Role)
		content := msg.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		sb.WriteString(fmt.Sprintf("【%s】\n%s\n\n", role, content))
	}
	return sb.String()
}

// retrieveFromSearch 从已收集的搜索素材中检索相关信息。
func retrieveFromSearch(cfg ToolExecutorConfig, query string, limit int) string {
	if cfg.Session == nil || len(cfg.Session.SearchResults) == 0 {
		return "暂无搜索素材。请先调用 search_web 或 search_knowledge 搜索。"
	}

	keywords := extractKeywords(query)
	var matches []engine.SearchResult

	for _, result := range cfg.Session.SearchResults {
		if matchScore(result.Title+" "+result.Snippet, keywords) > 0 {
			matches = append(matches, result)
		}
	}

	if len(matches) == 0 {
		// 返回所有搜索结果概览
		var sb strings.Builder
		sb.WriteString("未找到精确匹配的素材。以下是已收集的所有搜索结果：\n\n")
		for i, r := range cfg.Session.SearchResults {
			if i >= limit {
				break
			}
			sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n\n", i+1, r.Title, r.Snippet))
		}
		return sb.String()
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条相关素材：\n\n", len(matches)))
	for i, r := range matches {
		if i >= limit {
			break
		}
		sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n   来源: %s\n\n", i+1, r.Title, r.Snippet, r.Source))
	}
	return sb.String()
}

// retrieveFromProfile 从当前风格配置中检索相关信息。
func retrieveFromProfile(cfg ToolExecutorConfig, query string, limit int) string {
	if cfg.Profile == nil {
		return "当前没有加载风格配置。"
	}

	keywords := extractKeywords(query)
	var matches []string

	// 检查各个配置字段
	checkField := func(name, value string) {
		if value != "" && matchScore(value, keywords) > 0 {
			matches = append(matches, fmt.Sprintf("%s: %s", name, value))
		}
	}

	checkField("结构类型", cfg.Profile.Structure.Type)
	checkField("开头", cfg.Profile.Structure.Opening)
	checkField("正文", cfg.Profile.Structure.Body)
	checkField("结尾", cfg.Profile.Structure.Conclusion)
	checkField("论证模式", cfg.Profile.Structure.ArgumentPattern)
	checkField("修辞要求", cfg.Profile.Rhetoric.MetaphorDescription)
	checkField("标题风格", cfg.Profile.TitleGuidelines.Style)

	if len(matches) == 0 {
		// 返回配置概览
		var sb strings.Builder
		sb.WriteString("未找到精确匹配的配置项。以下是当前风格配置的概览：\n\n")
		sb.WriteString(fmt.Sprintf("结构: %s → %s → %s\n", cfg.Profile.Structure.Opening, cfg.Profile.Structure.Body, cfg.Profile.Structure.Conclusion))
		if cfg.Profile.Rhetoric.RequiredMetaphor {
			sb.WriteString(fmt.Sprintf("核心比喻: %s\n", cfg.Profile.Rhetoric.MetaphorDescription))
		}
		if cfg.Profile.TitleGuidelines.Style != "" {
			sb.WriteString(fmt.Sprintf("标题风格: %s\n", cfg.Profile.TitleGuidelines.Style))
		}
		return sb.String()
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 项相关配置：\n\n", len(matches)))
	for i, m := range matches {
		if i >= limit {
			break
		}
		sb.WriteString(fmt.Sprintf("- %s\n", m))
	}
	return sb.String()
}

// extractKeywords 从查询中提取简单关键词（按空格/标点分割）。
func extractKeywords(query string) []string {
	// 去除常见停用词（中英文）
	stopWords := map[string]bool{
		// 中文停用词
		"的": true, "了": true, "是": true, "我": true, "你": true, "他": true,
		"她": true, "它": true, "们": true, "这": true, "那": true, "有": true,
		"在": true, "和": true, "与": true, "或": true, "就": true, "都": true,
		"要": true, "会": true, "能": true, "可以": true, "请": true, "把": true,
		"将": true, "让": true, "给": true, "对": true, "从": true, "到": true,
		"为": true, "以": true, "及": true, "等": true, "着": true, "过": true,
		"吗": true, "呢": true, "吧": true, "啊": true, "嗯": true, "哦": true,
		// 英文停用词 - 代词
		"i": true, "you": true, "he": true, "she": true, "it": true, "we": true, "they": true,
		"me": true, "him": true, "her": true, "us": true, "them": true, "my": true, "your": true,
		"his": true, "its": true, "our": true, "their": true, "this": true, "that": true,
		"these": true, "those": true,
		// 英文停用词 - 动词/be动词
		"am": true, "is": true, "are": true, "was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true, "did": true,
		"will": true, "would": true, "could": true, "should": true, "may": true, "might": true, "can": true,
		"shall": true, "must": true,
		// 英文停用词 - 连词/介词
		"and": true, "or": true, "but": true, "if": true, "then": true, "else": true,
		"when": true, "where": true, "why": true, "how": true, "what": true, "which": true,
		"who": true, "whom": true, "whose": true,
		"of": true, "to": true, "in": true, "for": true, "on": true, "with": true, "at": true,
		"by": true, "from": true, "as": true, "into": true, "through": true, "during": true,
		"before": true, "after": true, "above": true, "below": true, "between": true, "under": true,
		// 英文停用词 - 限定词
		"the": true, "a": true, "an": true, "all": true, "any": true, "both": true, "each": true,
		"few": true, "more": true, "most": true, "other": true, "some": true, "such": true,
		"no": true, "nor": true, "not": true, "only": true, "own": true, "same": true,
		"so": true, "than": true, "too": true, "very": true, "just": true, "now": true, "also": true,
		"again": true, "further": true, "once": true, "here": true, "there": true,
		// 英文停用词 - 常见动词
		"get": true, "got": true, "go": true, "went": true, "come": true, "came": true,
		"see": true, "saw": true, "know": true, "knew": true, "think": true, "thought": true,
		"take": true, "took": true, "make": true, "made": true, "want": true, "wanted": true,
		"use": true, "used": true, "work": true, "worked": true, "call": true, "called": true,
		"try": true, "tried": true, "need": true, "needed": true, "feel": true, "felt": true,
		"become": true, "became": true, "leave": true, "left": true, "put": true,
		"mean": true, "meant": true, "keep": true, "kept": true, "let": true, "say": true,
		"said": true, "tell": true, "told": true, "ask": true, "asked": true, "seem": true, "seemed": true,
		"turn": true, "turned": true, "start": true, "started": true, "show": true, "showed": true,
		"hear": true, "heard": true, "play": true, "played": true, "run": true, "ran": true,
		"move": true, "moved": true, "live": true, "lived": true, "believe": true, "believed": true,
		"bring": true, "brought": true, "happen": true, "happened": true, "stand": true, "stood": true,
		"lose": true, "lost": true, "pay": true, "paid": true, "meet": true, "met": true,
		"include": true, "included": true, "continue": true, "continued": true, "set": true,
		"learn": true, "learned": true, "change": true, "changed": true, "lead": true, "led": true,
		"understand": true, "understood": true, "watch": true, "watched": true, "follow": true, "followed": true,
		"stop": true, "stopped": true, "create": true, "created": true, "speak": true, "spoke": true,
		"read": true, "allow": true, "allowed": true, "add": true, "added": true, "spend": true, "spent": true,
		"grow": true, "grew": true, "open": true, "opened": true, "walk": true, "walked": true,
		"win": true, "won": true, "offer": true, "offered": true, "remember": true, "remembered": true,
		"love": true, "loved": true, "consider": true, "considered": true, "appear": true, "appeared": true,
		"buy": true, "bought": true, "wait": true, "waited": true, "serve": true, "served": true,
		"die": true, "died": true, "send": true, "sent": true, "expect": true, "expected": true,
		"build": true, "built": true, "stay": true, "stayed": true, "fall": true, "fell": true,
		"cut": true, "reach": true, "reached": true, "kill": true, "killed": true, "remain": true, "remained": true,
		"suggest": true, "suggested": true, "raise": true, "raised": true, "pass": true, "passed": true,
		"sell": true, "sold": true, "require": true, "required": true, "report": true, "reported": true,
		"decide": true, "decided": true, "pull": true, "pulled": true,
	}

	// 分割并过滤
	words := strings.FieldsFunc(query, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == '，' || r == '.' || r == '。' ||
			r == '!' || r == '！' || r == '?' || r == '？' || r == ':' || r == '：' || r == ';' || r == '；' ||
			r == '(' || r == ')' || r == '（' || r == '）' || r == '"' || r == '\'' || r == '“' || r == '”' ||
			r == '[' || r == ']' || r == '【' || r == '】' || r == '-' || r == '_' || r == '/' || r == '|'
	})

	var result []string
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w != "" && !stopWords[w] && len(w) > 1 {
			result = append(result, w)
		}
	}
	return result
}

// matchScore 计算文本与关键词的匹配分数（简单实现）。
func matchScore(text string, keywords []string) int {
	textLower := strings.ToLower(text)
	score := 0
	for _, kw := range keywords {
		if strings.Contains(textLower, kw) {
			score++
		}
	}
	return score
}

// executeGenerateOutline 生成文章提纲并等待用户确认。
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

	outline, err := generateOutlineWithLLM(context.Background(), cfg.LLM, args.Topic, 0.3, cfg.Profile)
	if err != nil {
		return fmt.Sprintf("提纲生成失败: %v", err), nil
	}

	if cfg.Emitter != nil {
		cfg.Emitter.AwaitInput("generate_outline", outline, []string{"confirm", "edit", "regenerate"}, 1, 5)
	}

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

	if confirmedData != nil {
		if action, ok := confirmedData["action"].(string); ok && action == "regenerate" {
			outline, err = generateOutlineWithLLM(context.Background(), cfg.LLM, args.Topic, 0.6, cfg.Profile)
			if err != nil {
				return fmt.Sprintf("提纲重新生成失败: %v", err), nil
			}
		}
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
// 当 profile 非空时，注入结构骨架（RenderStructureSkeleton），
// 确保提纲的 type 字段与风格配置的 Sections 对应，
// 与 Pipeline 的 OutlineStep 和编辑部的 executeGenerateOutline 三引擎一致。
func generateOutlineWithLLM(ctx context.Context, llm *tools.LLMClient, topic string, temperature float64, p *profile.StyleProfile) (*engine.OutlineData, error) {
	if llm == nil {
		return nil, fmt.Errorf("LLM client not available")
	}

	systemMsg := "你是写作提纲生成器。根据话题生成文章提纲。只返回 JSON。"

	// 注入风格配置的结构骨架（如果有）
	structureSkeleton := ""
	if p != nil {
		structureSkeleton = p.RenderStructureSkeleton()
	}

	var userMsg string
	if structureSkeleton != "" {
		userMsg = fmt.Sprintf(`话题：%s

%s

请生成提纲，包含标题和3-5个要点。提纲的每个要点的 type 应与上述结构骨架对应。

返回格式：
{
  "title": "文章标题",
  "outline": [
    {"point": "要点内容", "type": "section_type"}
  ]
}

type 字段说明：用于标注该要点的段落角色，由你根据文章体裁自由决定。常见值包括但不限于：opening、argument、conclusion、intro、method、experiment、discussion、abstract 等，也可使用自定义标签
}`, topic, structureSkeleton)
	} else {
		userMsg = fmt.Sprintf(`话题：%s

请生成提纲，包含标题和3-5个要点。

返回格式：
{
  "title": "文章标题",
  "outline": [
    {"point": "要点内容", "type": "section_type"}
  ]
}

type 字段说明：用于标注该要点的段落角色，由你根据文章体裁自由决定。常见值包括但不限于：opening、argument、conclusion、intro、method、experiment、discussion、abstract 等，也可使用自定义标签
}`, topic)
	}

	resp, _, err := llm.Chat(ctx, []tools.LLMMessage{
		{Role: "user", Content: userMsg},
	}, tools.WithInstructions(systemMsg), tools.WithTemperature(temperature), tools.WithThinking(true), tools.WithReasoningEffort("high"))
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

// formatKnowledgeResults 格式化知识库搜索结果。
// 与 formatSearchResults 不同，这里保留更多内容（500字 vs 150字）
// 因为知识库内容是确定性知识，值得完整呈现。
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

// formatReviewResult formats the review result for display.
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

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// quickReviewArticle 使用 LLM 对文章做快速评审。
// 复用 Pipeline 侧的 profile.RenderReviewCriteria() 注入风格评审标准，
// 并追加规则确定性检查（fact_guard 关键词、title forbidden_patterns、word_range），
// 确保 Harness 模式下的评审标准与 Pipeline 模式一致。
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

	systemMsg := "你是文章正文质量评审员。只评审正文，不评审标题。只返回 JSON。"
	currentTime := time.Now().Format("2006年1月2日")

	// 复用 Pipeline 侧的 RenderReviewCriteria，注入 Fact Guard + 修辞 + 字数 + 结构
	var profileRules strings.Builder
	if p != nil {
		profileRules.WriteString(p.RenderReviewCriteria())
	}
	profileRules.WriteString(fmt.Sprintf("当前日期：%s（请据此判断文章中提及的政策、文件、规划等是已发布还是即将发布）\n", currentTime))

	userMsg := fmt.Sprintf(`请评审以下文章正文：

%s

评审维度：factuality（事实准确性）、structure（结构合规）、style（风格符合）、rhetoric（修辞运用）、length（篇幅控制）、safety（内容安全）

%s
返回格式：
{
  "scores": {"factuality": 0.9, "structure": 0.85, "style": 0.8, "rhetoric": 0.85, "length": 0.9, "safety": 0.95},
  "issues": [{"severity": "high", "type": "fact", "message": "..."}],
  "passed": true
}`, article, profileRules.String())

	resp, _, err := llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0), tools.WithThinking(true), tools.WithReasoningEffort("high"), tools.WithJSONResponse())

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

	if review.Scores == nil {
		review.Scores = map[string]float64{}
	}
	if review.Issues == nil {
		review.Issues = []engine.ReviewIssue{}
	}

	// ── 规则确定性检查（与 Pipeline PostReviewStep 一致）──

	// 1. fact_guard forbidden_results 关键词扫描
	if p != nil && len(p.FactGuard.ForbiddenResults) > 0 {
		articleLower := strings.ToLower(article)
		for _, forbidden := range p.FactGuard.ForbiddenResults {
			if strings.Contains(articleLower, strings.ToLower(forbidden)) {
				review.Issues = append(review.Issues, engine.ReviewIssue{
					Severity: "high",
					Type:     "fact_guard",
					Message:  fmt.Sprintf("文章包含禁止表述「%s」（事实红线：已完成事件不得用结果性动词）", forbidden),
				})
				review.Passed = false
				if score, ok := review.Scores["factuality"]; ok {
					review.Scores["factuality"] = score * 0.5
				} else {
					review.Scores["factuality"] = 0.5
				}
			}
		}
	}

	// 2. word_range 字数检查
	if p != nil && p.WordRange.Max > 0 {
		wordCount := len([]rune(article))
		minWords := p.WordRange.Min
		maxWords := p.WordRange.Max
		if wordCount < minWords || wordCount > maxWords {
			severity := "medium"
			if p.WordRange.HardLimit {
				severity = "high"
				review.Passed = false
			}
			review.Issues = append(review.Issues, engine.ReviewIssue{
				Severity: severity,
				Type:     "length",
				Message:  fmt.Sprintf("字数 %d，要求 %d-%d 字", wordCount, minWords, maxWords),
			})
		}
	}

	return &review
}

// ─── 声明式 Guard：MaxCalls 配置 ───────────────────────────
//
// defaultMaxCalls 按意图返回工具调用次数限制。
// 这是声明式 guard 的配置层：工具名 → 最大调用次数（0=unlimited）。
// 替代了以前硬编码在 executeSearchWeb 中的 SearchCallCount > 3 检查。
//
// 灵感来自 dsh 的 guard/ 包（声明式循环卫生）。
func defaultMaxCalls(intent Intent) map[string]int {
	switch intent {
	case IntentWriting:
		// 写作意图：搜索 5 次、评审 1 次、提纲 1 次、字数检查 1 次、标题优化 1 次、事实核查 1 次、上下文检索 5 次
		return map[string]int{
			"search_web":       5,
			"search_knowledge": 5,
			"review_article":   1,
			"generate_outline": 1,
			"word_count_check": 1,
			"rewrite_title":    1,
			"fact_check":       1,
			"retrieve_context": 5,
		}
	case IntentPolish, IntentShorten, IntentExpand, IntentExtract:
		// 修改意图：搜索 2 次、上下文检索 3 次
		return map[string]int{
			"search_web":       2,
			"search_knowledge": 2,
			"retrieve_context": 3,
		}
	case IntentChat:
		// 对话意图：搜索 2 次、上下文检索 2 次
		return map[string]int{
			"search_web":       2,
			"search_knowledge": 2,
			"retrieve_context": 2,
		}
	default:
		return nil
	}
}
