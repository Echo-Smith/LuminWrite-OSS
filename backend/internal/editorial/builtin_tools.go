package editorial

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── 内置工具实现 ─────────────────────────────────────────────
//
// 每个工具实现 EditorialTool 接口。
// 信号工具的 Execute 不会被调用（拦截器会处理），
// 但仍需实现以满足接口约束。

// ─── 通用基础结构 ──────────────────────────────────────────────

// baseTool 提供 EditorialTool 接口的通用字段
type baseTool struct {
	name        string
	description string
	schema      map[string]any
	roles       []string
	maxCalls    int
	isSignal    bool
	category    string
}

func (t *baseTool) Name() string             { return t.name }
func (t *baseTool) Description() string      { return t.description }
func (t *baseTool) Schema() map[string]any   { return t.schema }
func (t *baseTool) Roles() []string          { return t.roles }
func (t *baseTool) MaxCalls() int            { return t.maxCalls }
func (t *baseTool) IsSignal() bool           { return t.isSignal }
func (t *baseTool) Category() string         { return t.category }
func (t *baseTool) Execute(_ context.Context, _ string, _ *ToolRunContext) (string, error) {
	return "", nil // 信号工具由拦截器处理，不会走到这里
}

// ─── 搜索类工具 ───────────────────────────────────────────────

// SearchWebTool 网络搜索工具
type SearchWebTool struct{ baseTool }

func NewSearchWebTool() *SearchWebTool {
	return &SearchWebTool{baseTool{
		name:        "search_web",
		description: "搜索互联网获取信息。返回结构化搜索结果。",
		category:    "retrieval",
		roles:       []string{"researcher"},
		maxCalls:    10,
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "搜索关键词"},
			},
			"required": []string{"query"},
		},
	}}
}

func (t *SearchWebTool) Execute(ctx context.Context, args string, runCtx *ToolRunContext) (string, error) {
	return executeSearchWeb(args, runCtx)
}

// SearchKnowledgeTool 知识库搜索工具
type SearchKnowledgeTool struct{ baseTool }

func NewSearchKnowledgeTool() *SearchKnowledgeTool {
	return &SearchKnowledgeTool{baseTool{
		name:        "search_knowledge",
		description: "搜索个人素材库。返回精确到段落的检索结果。",
		category:    "retrieval",
		roles:       []string{"researcher", "writer", "reviewer"},
		maxCalls:    5,
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "检索关键词或自然语言问题"},
			},
			"required": []string{"query"},
		},
	}}
}

func (t *SearchKnowledgeTool) Execute(ctx context.Context, args string, runCtx *ToolRunContext) (string, error) {
	return executeSearchKnowledge(args, runCtx)
}

// ReadSourceTool 读取搜索结果详情
type ReadSourceTool struct{ baseTool }

func NewReadSourceTool() *ReadSourceTool {
	return &ReadSourceTool{baseTool{
		name:        "read_source",
		description: "读取搜索结果的完整内容。传入序号(1-based)。",
		category:    "retrieval",
		roles:       []string{"researcher", "writer", "reviewer"},
		maxCalls:    8,
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"index": map[string]any{"type": "integer", "description": "搜索结果的序号(从1开始)"},
			},
			"required": []string{"index"},
		},
	}}
}

func (t *ReadSourceTool) Execute(ctx context.Context, args string, runCtx *ToolRunContext) (string, error) {
	return executeReadSource(args, runCtx)
}

// ─── 写作类工具 ───────────────────────────────────────────────

// GenerateOutlineTool 生成文章提纲
type GenerateOutlineTool struct{ baseTool }

func NewGenerateOutlineTool() *GenerateOutlineTool {
	return &GenerateOutlineTool{baseTool{
		name:        "generate_outline",
		description: "生成文章提纲。",
		category:    "writing",
		roles:       []string{"writer"},
		maxCalls:    1,
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic": map[string]any{"type": "string", "description": "文章主题"},
			},
			"required": []string{"topic"},
		},
	}}
}

func (t *GenerateOutlineTool) Execute(ctx context.Context, args string, runCtx *ToolRunContext) (string, error) {
	return executeGenerateOutline(args, runCtx)
}

// WriteArticleSignalTool 写作信号工具（writer 的完成信号）
type WriteArticleSignalTool struct{ baseTool }

func NewWriteArticleSignalTool() *WriteArticleSignalTool {
	return &WriteArticleSignalTool{baseTool{
		name:        "write_article",
		description: "开始写作并流式输出完整文章。调用后请在下一轮回复中直接输出文章内容（Markdown格式，以##开头作为标题）。",
		category:    "signal",
		roles:       []string{"writer"},
		isSignal:    true,
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic": map[string]any{"type": "string", "description": "文章主题"},
			},
			"required": []string{"topic"},
		},
	}}
}

// ─── 审校类工具 ───────────────────────────────────────────────

// FactCheckTool 事实核查工具
type FactCheckTool struct{ baseTool }

func NewFactCheckTool() *FactCheckTool {
	return &FactCheckTool{baseTool{
		name:        "fact_check",
		description: "对文章中的关键事实进行核查。提取事实性声明并通过搜索验证。",
		category:    "review",
		roles:       []string{"reviewer"},
		maxCalls:    5,
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"article": map[string]any{"type": "string", "description": "文章内容"},
				"focus":   map[string]any{"type": "string", "description": "重点核查的内容（如'数据'、'人名'），可选"},
			},
			"required": []string{"article"},
		},
	}}
}

func (t *FactCheckTool) Execute(ctx context.Context, args string, runCtx *ToolRunContext) (string, error) {
	return executeFactCheck(args, runCtx)
}

// SubmitResearchBriefSignalTool 研究简报信号工具
type SubmitResearchBriefSignalTool struct{ baseTool }

func NewSubmitResearchBriefSignalTool() *SubmitResearchBriefSignalTool {
	return &SubmitResearchBriefSignalTool{baseTool{
		name:        "submit_research_brief",
		description: "提交研究简报。当你已完成搜索和素材收集，调用此工具提交结构化研究简报。",
		category:    "signal",
		roles:       []string{"researcher"},
		isSignal:    true,
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary": map[string]any{"type": "string", "description": "研究简报摘要（500-1000字）"},
				"sources": map[string]any{
					"type":        "array",
					"description": "信源列表",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url":       map[string]any{"type": "string"},
							"source":    map[string]any{"type": "string"},
							"relevance": map[string]any{"type": "string"},
							"title":     map[string]any{"type": "string"},
							"snippet":   map[string]any{"type": "string"},
						},
					},
				},
				"claims": map[string]any{
					"type":        "array",
					"description": "事实声明列表",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"claim":      map[string]any{"type": "string"},
							"source_url": map[string]any{"type": "string"},
							"source":     map[string]any{"type": "string"},
							"relevance":  map[string]any{"type": "string"},
						},
					},
				},
			},
			"required": []string{"summary"},
		},
	}}
}

// SubmitReviewReportSignalTool 审查报告信号工具
type SubmitReviewReportSignalTool struct{ baseTool }

func NewSubmitReviewReportSignalTool() *SubmitReviewReportSignalTool {
	return &SubmitReviewReportSignalTool{baseTool{
		name:        "submit_review_report",
		description: "提交审查报告。当你已完成审校，调用此工具提交结构化审查结果。",
		category:    "signal",
		roles:       []string{"reviewer"},
		isSignal:    true,
		schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"passed": map[string]any{"type": "boolean", "description": "是否通过审校"},
				"severity": map[string]any{
					"type":        "string",
					"description": "严重程度: low/medium/high",
					"enum":        []string{"low", "medium", "high"},
				},
				"issues": map[string]any{
					"type":        "array",
					"description": "问题列表",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type":     map[string]any{"type": "string"},
							"severity": map[string]any{"type": "string"},
							"message":  map[string]any{"type": "string"},
						},
					},
				},
				"scores": map[string]any{
					"type":        "object",
					"description": "评分（accuracy, style, risk, structure 等，0-1）",
				},
			},
			"required": []string{"passed"},
		},
	}}
}

// ─── 工具执行函数（从 role_agent_runner.go 迁移）──────────────

func executeSearchWeb(arguments string, cfg *ToolRunContext) (string, error) {
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

	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("search_web", 0)
	}

	results := cfg.Search.Search(context.Background(), args.Query, 5)
	if len(results) == 0 {
		if cfg.Emitter != nil {
			cfg.Emitter.StepComplete("search_web", map[string]any{"query": args.Query, "results": 0}, 0)
		}
		return "未找到搜索结果", nil
	}

	cfg.SearchMu.Lock()
	*cfg.SearchResults = append(*cfg.SearchResults, results...)
	cfg.SearchMu.Unlock()

	brief := compressRoleSearchResults(context.Background(), cfg.LLM, args.Query, results)

	if cfg.Emitter != nil {
		cfg.Emitter.StepComplete("search_web", map[string]any{
			"query":   args.Query,
			"results": len(results),
		}, 0)
	}
	return brief, nil
}

func executeSearchKnowledge(arguments string, cfg *ToolRunContext) (string, error) {
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

	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("search_knowledge", 0)
	}

	results, err := cfg.KBSearcher.SearchKB(context.Background(), cfg.ExecCtx.UserID, args.Query, 5)
	if err != nil {
		if cfg.Emitter != nil {
			cfg.Emitter.StepComplete("search_knowledge", map[string]any{"query": args.Query, "error": err.Error()}, 0)
		}
		return fmt.Sprintf("知识库检索失败: %v", err), nil
	}
	if len(results) == 0 {
		if cfg.Emitter != nil {
			cfg.Emitter.StepComplete("search_knowledge", map[string]any{"query": args.Query, "results": 0}, 0)
		}
		return "未找到相关知识", nil
	}

	cfg.SearchMu.Lock()
	*cfg.SearchResults = append(*cfg.SearchResults, results...)
	cfg.SearchMu.Unlock()

	formatted := formatKnowledgeResults(results)

	if cfg.Emitter != nil {
		cfg.Emitter.StepComplete("search_knowledge", map[string]any{
			"query":   args.Query,
			"results": len(results),
		}, 0)
	}
	return formatted, nil
}

func executeReadSource(arguments string, cfg *ToolRunContext) (string, error) {
	var args struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	cfg.SearchMu.Lock()
	results := *cfg.SearchResults
	cfg.SearchMu.Unlock()

	if args.Index < 1 || args.Index > len(results) {
		return fmt.Sprintf("错误：序号超出范围 (1-%d)", len(results)), nil
	}

	r := results[args.Index-1]
	formatted := fmt.Sprintf("标题: %s\n来源: %s\n摘要: %s\nURL: %s", r.Title, r.Source, r.Snippet, r.URL)

	if cfg.Emitter != nil {
		cfg.Emitter.StepStart("read_source", 0)
		cfg.Emitter.StepComplete("read_source", map[string]any{
			"index":  args.Index,
			"title":  r.Title,
			"source": r.Source,
		}, 0)
	}
	return formatted, nil
}

func executeGenerateOutline(arguments string, cfg *ToolRunContext) (string, error) {
	var args struct {
		Topic string `json:"topic"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	systemMsg := "你是资深撰稿人。根据主题生成文章提纲。只返回 JSON。"

	// 注入风格配置的结构骨架（如果有）
	structureSkeleton := ""
	if cfg.Profile != nil {
		structureSkeleton = cfg.Profile.RenderStructureSkeleton()
	}

	var userMsg string
	if structureSkeleton != "" {
		userMsg = fmt.Sprintf(`主题：%s

%s

请生成文章提纲，提纲的每个要点的 type 应与上述结构骨架对应。格式如下：
{
  "title": "文章标题",
  "outline": [
    {"type": "section_type", "point": "要点描述"}
  ]
}

type 字段说明：用于标注该要点的段落角色，由你根据文章体裁自由决定。常见值包括但不限于：opening、argument、conclusion、intro、method、experiment、discussion、abstract 等，也可使用自定义标签。}`, args.Topic, structureSkeleton)
	} else {
		userMsg = fmt.Sprintf(`主题：%s

请生成文章提纲，格式如下：
{
  "title": "文章标题",
  "outline": [
    {"type": "section_type", "point": "要点描述"}
  ]
}

type 字段说明：用于标注该要点的段落角色，由你根据文章体裁自由决定。常见值包括但不限于：opening、argument、conclusion、intro、method、experiment、discussion、abstract 等，也可使用自定义标签。}`, args.Topic)
	}

	resp, _, err := cfg.LLM.Chat(context.Background(), []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0.3), tools.WithThinking(false))
	if err != nil {
		return fmt.Sprintf("提纲生成失败: %v", err), nil
	}

	jsonStr := tools.ExtractJSONObject(resp)
	if jsonStr != "" {
		var outline engine.OutlineData
		if err := json.Unmarshal([]byte(jsonStr), &outline); err == nil {
			if cfg.ExecCtx != nil {
				cfg.ExecCtx.Outline = &outline
			}
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("标题：%s\n", outline.Title))
			typeLabels := map[string]string{"opening": "开头", "argument": "分论点", "conclusion": "结尾", "intro": "引言", "method": "方法", "experiment": "实验", "discussion": "讨论", "abstract": "摘要"}
			for i, item := range outline.Outline {
				label := typeLabels[item.Type]
				if label == "" {
					label = item.Type
				}
				sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, label, item.Point))
			}
			return sb.String(), nil
		}
	}
	return resp, nil
}

func executeFactCheck(arguments string, cfg *ToolRunContext) (string, error) {
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

	systemMsg := "你是事实核查助理。从文章中提取需要核实的关键事实性声明。只返回 JSON。"
	userMsg := fmt.Sprintf(`文章内容：
%s

请提取文章中的关键事实性声明（最多5个），格式如下：
{
  "claims": [
    {"claim": "事实声明", "type": "数据/人名/时间/事件", "verifiable": true}
  ]
}`, args.Article)

	resp, _, err := cfg.LLM.Chat(context.Background(), []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0.1), tools.WithThinking(false))
	if err != nil {
		return fmt.Sprintf("事实核查失败: %v", err), nil
	}

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
		return "未检测到需要核实的事实性声明。", nil
	}

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

	return sb.String(), nil
}

// ─── 内置工具注册函数 ─────────────────────────────────────────

// RegisterBuiltinTools 注册所有内置工具到 Registry
func RegisterBuiltinTools(registry *EditorialToolRegistry) {
	// 搜索类
	registry.Register(NewSearchWebTool())
	registry.Register(NewSearchKnowledgeTool())
	registry.Register(NewReadSourceTool())

	// 写作类
	registry.Register(NewGenerateOutlineTool())
	registry.Register(NewWriteArticleSignalTool())

	// 审校类
	registry.Register(NewFactCheckTool())
	registry.Register(NewSubmitResearchBriefSignalTool())
	registry.Register(NewSubmitReviewReportSignalTool())
}
