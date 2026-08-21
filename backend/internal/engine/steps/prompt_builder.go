package steps

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── Section Priority ───────────────────────────────────
//
// When the total prompt exceeds the token budget, sections are
// truncated or dropped in reverse priority order.
//
//   Priority 0 (critical): task, output_format — never dropped
//   Priority 1 (high):     style, outline — truncated, not dropped
//   Priority 2 (medium):   search, materials — truncated heavily
//   Priority 3 (low):      memory — first to be dropped

const (
	priorityCritical = 0 // task, output_format
	priorityHigh     = 1 // style, outline
	priorityMedium   = 2 // search, materials
	priorityLow      = 3 // memory
)

// PromptBuilder is a lightweight section-based prompt assembler
// with token budget management.
//
// It structures the user prompt into named sections, making the
// assembly logic declarative and easy to reason about.
//
// Budget management:
//   - Each section tracks its estimated token count
//   - When the total exceeds the budget, low-priority sections
//     are truncated or dropped first
//   - Critical sections (task, output_format) are never dropped
//
// v3.0 增强（借鉴 Codex TokenBudgetContext）：
//   - 支持动态预算：WithDynamicBudget(remaining) 根据全局剩余 Token 计算配额
//   - 保留预留量给 system prompt + history + response
//   - 当预算紧张时，自动收紧 section 配额
//
// This mirrors dsh's prompt assembly pattern where different plugins
// register prompt fragments, and Pi Agent's structured context assembly
// where messages are composed from typed parts rather than raw string concatenation.
type PromptBuilder struct {
	sections       []promptSection
	budget         int  // 0 = unlimited
	dynamicBudget  bool // true if budget was set dynamically
}

// 预留 Token 常量：system prompt + conversation history + LLM response
// 这些部分不属于 user prompt，需要从总预算中扣除。
const (
	reserveSystemPrompt = 2000 // system prompt 估算
	reserveHistory     = 2048  // 对话历史估算
	reserveResponse    = 8192  // LLM 响应估算（thinking + output）
	minUserPromptBudget = 2000 // user prompt 最低预算，低于此值时强制收紧
)

type promptSection struct {
	name     string
	body     string
	priority int
	tokens   int
}

// NewPromptBuilder creates a new PromptBuilder.
func NewPromptBuilder() *PromptBuilder {
	return &PromptBuilder{}
}

// WithBudget sets a static token budget for the assembled prompt.
// When the total exceeds this budget, low-priority sections
// are truncated or dropped to fit.
func (pb *PromptBuilder) WithBudget(maxTokens int) *PromptBuilder {
	pb.budget = maxTokens
	pb.dynamicBudget = false
	return pb
}

// WithDynamicBudget calculates and sets the token budget dynamically
// based on the global remaining token budget.
//
// remainingGlobal is the total remaining tokens from the execution context
// (MaxTokens - TotalTokens). If 0 (unlimited), falls back to a default budget.
//
// The calculation:
//   userPromptBudget = remainingGlobal - reserveSystemPrompt - reserveHistory - reserveResponse
//   but not less than minUserPromptBudget.
//
// This ensures that when the context window is nearly full, the user prompt
// automatically shrinks to leave room for the LLM response.
func (pb *PromptBuilder) WithDynamicBudget(remainingGlobal int) *PromptBuilder {
	if remainingGlobal <= 0 {
		// Unlimited context — use default budget
		pb.budget = 12000
		pb.dynamicBudget = false
		return pb
	}

	// Calculate available budget for user prompt
	reserved := reserveSystemPrompt + reserveHistory + reserveResponse
	budget := remainingGlobal - reserved

	if budget < minUserPromptBudget {
		budget = minUserPromptBudget
		slog.Warn("prompt budget: dynamic budget very low, clamped to minimum",
			"remaining_global", remainingGlobal,
			"reserved", reserved,
			"budget", budget,
		)
	}

	pb.budget = budget
	pb.dynamicBudget = true
	slog.Info("prompt budget: dynamic budget calculated",
		"remaining_global", remainingGlobal,
		"reserved", reserved,
		"user_prompt_budget", budget,
	)
	return pb
}

// Add appends a named section with default priority (medium).
// Empty bodies are silently skipped.
func (pb *PromptBuilder) Add(name, body string) *PromptBuilder {
	return pb.AddWithPriority(name, body, priorityMedium)
}

// AddWithPriority appends a named section with the given priority.
// Lower priority value = higher importance (kept when budget is tight).
func (pb *PromptBuilder) AddWithPriority(name, body string, priority int) *PromptBuilder {
	if strings.TrimSpace(body) != "" {
		pb.sections = append(pb.sections, promptSection{
			name:     name,
			body:     body,
			priority: priority,
			tokens:   memory.EstimateTokens(body),
		})
	}
	return pb
}

// AddTaskPrompt appends the task-specific prompt (writing/polish/shorten/expand/extract_points).
func (pb *PromptBuilder) AddTaskPrompt(taskMode string, execCtx *engine.ExecutionContext) *PromptBuilder {
	var b strings.Builder
	pb.writeTaskPrompt(&b, taskMode, execCtx)
	return pb.AddWithPriority("task", b.String(), priorityCritical)
}

// AddSearchResults appends search results or compressed context as reference material.
func (pb *PromptBuilder) AddSearchResults(taskMode string, execCtx *engine.ExecutionContext) *PromptBuilder {
	if taskMode != "writing" {
		return pb
	}
	var b strings.Builder
	if execCtx.CompressedContext != "" {
		b.WriteString("参考素材（结构化研究简报）：\n")
		b.WriteString(execCtx.CompressedContext)
		b.WriteString("\n")
	} else if len(execCtx.SearchResults) > 0 {
		hasMockResults := false
		for _, result := range execCtx.SearchResults {
			if result.IsMock {
				hasMockResults = true
				break
			}
		}
		if hasMockResults {
			b.WriteString("⚠️ 参考素材（注意：以下为 AI 生成的背景参考，非真实搜索结果，使用前请核实事实）：\n")
		} else {
			b.WriteString("参考素材：\n")
		}
		for i, result := range execCtx.SearchResults {
			b.WriteString(fmt.Sprintf("%d. %s\n   %s\n\n", i+1, result.Title, result.Snippet))
		}
	}
	return pb.AddWithPriority("search", b.String(), priorityMedium)
}

// AddOutline appends the guided-mode outline (title + outline points).
func (pb *PromptBuilder) AddOutline(taskMode string, execCtx *engine.ExecutionContext) *PromptBuilder {
	if taskMode != "writing" || execCtx.Outline == nil {
		return pb
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("【标题（必须原样使用，不得修改）】：%s\n", execCtx.Outline.Title))
	b.WriteString("【写作提纲（必须严格按照以下提纲展开，每个要点对应一个段落，不得增删或更改要点顺序）】：\n")
	typeLabels := map[string]string{
		"opening":    "开头",
		"argument":   "分论点",
		"conclusion": "结尾",
	}
	for i, item := range execCtx.Outline.Outline {
		label := typeLabels[item.Type]
		if label == "" {
			label = item.Type
		}
		b.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, label, item.Point))
	}
	b.WriteString("\n")
	return pb.AddWithPriority("outline", b.String(), priorityHigh)
}

// AddStyleConstraints appends the style profile constraints (fact guard, structure, rhetoric, etc.).
// For nil profile, falls back to a basic word limit.
func (pb *PromptBuilder) AddStyleConstraints(p *profile.StyleProfile, taskMode string, hasOutlineTitle bool, wordLimit int) *PromptBuilder {
	if p != nil {
		return pb.AddWithPriority("style", p.RenderWritingConstraints(taskMode, hasOutlineTitle, wordLimit), priorityHigh)
	}
	if wordLimit > 0 {
		return pb.AddWithPriority("style", fmt.Sprintf("\n字数要求：约%d字\n", wordLimit), priorityHigh)
	}
	return pb
}

// AddUserMaterials appends user-provided materials.
func (pb *PromptBuilder) AddUserMaterials(materials []string) *PromptBuilder {
	if len(materials) == 0 {
		return pb
	}
	var b strings.Builder
	b.WriteString("用户提供的素材：\n")
	for i, mat := range materials {
		b.WriteString(fmt.Sprintf("%d. %s\n\n", i+1, mat))
	}
	b.WriteString("（用户素材优先级高于 AI 检索结果）\n")
	return pb.AddWithPriority("materials", b.String(), priorityMedium)
}

// AddMemory appends memory context (preferences, entity network, working summary).
func (pb *PromptBuilder) AddMemory(execCtx *engine.ExecutionContext) *PromptBuilder {
	var b strings.Builder

	// User memory preferences
	if execCtx.MemoryContext != nil {
		if memCtx, ok := execCtx.MemoryContext.(*memory.MemoryContext); ok {
			if memStr := FormatMemoryForPrompt(memCtx); memStr != "" {
				b.WriteString(memStr)
			}
		}
	}

	// Entity memory network context
	if execCtx.EntityContext != nil {
		if entityCtx, ok := execCtx.EntityContext.(*memory.EntityGraphResult); ok {
			b.WriteString(entityCtx.FormattedContext)
		}
	}

	// Working memory summary
	if execCtx.WorkingSummary != nil {
		if ws, ok := execCtx.WorkingSummary.(*memory.WorkingSummary); ok {
			if wsStr := memory.FormatWorkingSummaryForPrompt(ws); wsStr != "" {
				b.WriteString(wsStr)
			}
		}
	}

	return pb.AddWithPriority("memory", b.String(), priorityLow)
}

// AddOutputFormat appends the output format instruction.
func (pb *PromptBuilder) AddOutputFormat(p *profile.StyleProfile, taskMode, outlineTitle, separator string) *PromptBuilder {
	var formatPrompt string
	if p != nil {
		formatPrompt = p.RenderOutputFormat(taskMode, outlineTitle, separator)
	} else if taskMode == "writing" {
		formatPrompt = profile.RenderJSONTitleFormat(outlineTitle, separator)
	}
	return pb.AddWithPriority("output_format", formatPrompt, priorityCritical)
}

// String renders all sections into a single prompt string.
// If a token budget is set, sections are auto-truncated to fit.
func (pb *PromptBuilder) String() string {
	if pb.budget <= 0 {
		// No budget — render everything
		var b strings.Builder
		for _, s := range pb.sections {
			b.WriteString(s.body)
		}
		return b.String()
	}

	// Calculate total tokens
	totalTokens := 0
	for _, s := range pb.sections {
		totalTokens += s.tokens
	}

	if totalTokens <= pb.budget {
		// Within budget — render everything
		var b strings.Builder
		for _, s := range pb.sections {
			b.WriteString(s.body)
		}
		return b.String()
	}

	// Over budget — apply truncation strategy
	return pb.renderWithBudget()
}

// renderWithBudget truncates or drops sections to fit within the token budget.
//
// Strategy:
//  1. Start with critical sections (task, output_format) — always kept
//  2. Add high-priority sections (style, outline) — truncated if needed
//  3. Add medium-priority sections (search, materials) — truncated heavily
//  4. Add low-priority sections (memory) — dropped first if no room
func (pb *PromptBuilder) renderWithBudget() string {
	// Separate sections by priority
	byPriority := make(map[int][]promptSection)
	for _, s := range pb.sections {
		byPriority[s.priority] = append(byPriority[s.priority], s)
	}

	var result strings.Builder
	usedTokens := 0

	// Priority 0: Critical (always kept)
	for _, s := range byPriority[priorityCritical] {
		result.WriteString(s.body)
		usedTokens += s.tokens
	}

	// Priority 1: High (truncate if needed, never drop)
	remaining := pb.budget - usedTokens
	for _, s := range byPriority[priorityHigh] {
		if remaining <= 0 {
			// No room — truncate to 50% (keep the beginning)
			half := len(s.body) / 2
			if half > 0 {
				result.WriteString(memory.SafeTruncate(s.body, half))
				result.WriteString("\n...（风格约束已截断）\n")
			}
			continue
		}
		if s.tokens <= remaining {
			result.WriteString(s.body)
			usedTokens += s.tokens
			remaining -= s.tokens
		} else {
			// Truncate to fit
			maxBytes := int(float64(remaining) / 0.8 * 3) // tokens → bytes (Chinese)
			if maxBytes > 0 && maxBytes < len(s.body) {
				result.WriteString(memory.SafeTruncate(s.body, maxBytes))
				result.WriteString("\n...（已截断）\n")
				usedTokens += remaining
				remaining = 0
			} else if maxBytes >= len(s.body) {
				result.WriteString(s.body)
				usedTokens += s.tokens
				remaining -= s.tokens
			}
		}
	}

	// Priority 2: Medium (truncate heavily, then drop)
	remaining = pb.budget - usedTokens
	for _, s := range byPriority[priorityMedium] {
		if remaining <= 0 {
			slog.Debug("prompt budget: dropped medium-priority section",
				"section", s.name, "tokens", s.tokens,
				"dynamic", pb.dynamicBudget)
			continue
		}
		if s.tokens <= remaining {
			result.WriteString(s.body)
			usedTokens += s.tokens
			remaining -= s.tokens
		} else {
			// Truncate to 30% of original
			thirty := len(s.body) * 30 / 100
			if thirty > 0 && thirty < len(s.body) {
				result.WriteString(memory.SafeTruncate(s.body, thirty))
				result.WriteString("\n...（参考素材已截断）\n")
				usedTokens += remaining
				remaining = 0
			}
		}
	}

	// Priority 3: Low (drop entirely if no room)
	remaining = pb.budget - usedTokens
	for _, s := range byPriority[priorityLow] {
		if remaining <= 0 {
			slog.Debug("prompt budget: dropped low-priority section",
				"section", s.name, "tokens", s.tokens,
				"dynamic", pb.dynamicBudget)
			continue
		}
		if s.tokens <= remaining {
			result.WriteString(s.body)
			usedTokens += s.tokens
			remaining -= s.tokens
		} else {
			// Truncate to 50% of original
			half := len(s.body) / 2
			if half > 0 && half < len(s.body) {
				result.WriteString(memory.SafeTruncate(s.body, half))
				result.WriteString("\n...（记忆上下文已截断）\n")
				usedTokens += remaining
				remaining = 0
			}
		}
	}

	slog.Info("prompt budget: assembled with truncation",
		"budget", pb.budget,
		"used", usedTokens,
		"sections", len(pb.sections),
		"dynamic", pb.dynamicBudget,
	)
	return result.String()
}

// TotalTokens returns the total estimated token count of all sections.
func (pb *PromptBuilder) TotalTokens() int {
	total := 0
	for _, s := range pb.sections {
		total += s.tokens
	}
	return total
}

// SectionCount returns the number of non-empty sections.
func (pb *PromptBuilder) SectionCount() int {
	return len(pb.sections)
}

// writeTaskPrompt writes the task-specific prompt section.
func (pb *PromptBuilder) writeTaskPrompt(b *strings.Builder, taskMode string, execCtx *engine.ExecutionContext) {
	normalizedInput := execCtx.NormalizedInput
	if normalizedInput == "" {
		normalizedInput = execCtx.UserInput
	}

	switch taskMode {
	case "polish":
		b.WriteString("请对以下文章进行润色优化。保持原文的核心观点和结构不变，重点优化语言表达、修辞效果和行文流畅度。\n\n")
		b.WriteString("原文：\n")
		b.WriteString(normalizedInput)
		b.WriteString("\n\n")
		b.WriteString("润色要求：\n")
		b.WriteString("1. 保持原文的核心论点和逻辑结构\n")
		b.WriteString("2. 优化遣词造句，提升表达力\n")
		b.WriteString("3. 补充必要的修辞手法（如适用风格 Profile 中的要求）\n")
		b.WriteString("4. 修正语病、冗余和不流畅之处\n")
		b.WriteString("5. 标题如有需要可微调，但不可改变主旨\n\n")

	case "shorten":
		b.WriteString("请将以下文章缩短到指定字数范围内。保持核心观点和关键论证完整，删除冗余表述和重复论证。\n\n")
		b.WriteString("原文：\n")
		b.WriteString(normalizedInput)
		b.WriteString("\n\n")
		b.WriteString("缩写要求：\n")
		b.WriteString("1. 保留原文的核心论点和主要论据\n")
		b.WriteString("2. 删除冗余表述、重复论证和过度展开\n")
		b.WriteString("3. 保持文章的逻辑连贯性\n")
		b.WriteString("4. 保持原文的风格和语气\n")
		b.WriteString("5. 标题保持不变或微调\n\n")

	case "expand":
		b.WriteString("请将以下文章扩充到指定字数范围内。在不改变核心观点的前提下，增加论证深度、补充论据和细节。\n\n")
		b.WriteString("原文：\n")
		b.WriteString(normalizedInput)
		b.WriteString("\n\n")
		b.WriteString("扩写要求：\n")
		b.WriteString("1. 保持原文的核心论点和结构框架\n")
		b.WriteString("2. 增加论证深度，补充具体论据和案例\n")
		b.WriteString("3. 丰富修辞手法，增强表达力\n")
		b.WriteString("4. 保持原文的风格和语气\n")
		b.WriteString("5. 扩充内容须与主题紧密相关，不可偏题\n\n")

	case "extract_points":
		b.WriteString("请从以下文章中提炼核心观点，以结构化的方式输出。\n\n")
		b.WriteString("原文：\n")
		b.WriteString(normalizedInput)
		b.WriteString("\n\n")
		b.WriteString("提取要求：\n")
		b.WriteString("1. 提炼 3-5 个核心观点，按重要性排序\n")
		b.WriteString("2. 每个观点用一句话概括，附简要说明\n")
		b.WriteString("3. 保持原文的价值立场，不添加新观点\n")
		b.WriteString("4. 输出格式：\n")
		b.WriteString("## 核心观点\n\n")
		b.WriteString("1. **观点一**：概括（说明）\n")
		b.WriteString("2. **观点二**：概括（说明）\n")
		b.WriteString("3. **观点三**：概括（说明）\n\n")

	default: // writing
		topic := execCtx.UserInput
		if execCtx.WritingTask != nil && execCtx.WritingTask.Topic != "" {
			topic = execCtx.WritingTask.Topic
		}
		b.WriteString("请写一篇文章。\n\n")
		b.WriteString(fmt.Sprintf("话题：%s\n\n", topic))
	}
}
