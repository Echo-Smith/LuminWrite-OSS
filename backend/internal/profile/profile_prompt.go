package profile

import (
	"fmt"
	"strings"
)

// ─── Prompt Layer Priority ──────────────────────────────
//
// Style constraints are layered by priority so the LLM knows
// which rules are hard constraints vs soft guidance.
//
//   Priority 0 (强制约束): Fact guard — violations make the article defective.
//   Priority 1 (写作要求): Structure, rhetoric, word limit — core style identity.
//   Priority 2 (风格参考): Value orientation keywords — nice to have.
//
// This mirrors dsh's system-prompt assembly where different plugins
// register prompt sections with varying priority, and Pi Agent's
// convertToLlm pattern where context is structured before reaching the LLM.

const (
	priorityMandatory = "【强制约束——违反将导致文章不合格】"
	priorityRequired  = "【写作要求——尽量遵循】"
	prioritySoft      = "【风格参考——适当融入】"
)

// RenderWritingConstraints renders all style constraints for the
// WriteStep user prompt. It is the single source of truth for how
// StyleProfile fields become prompt text during article generation.
//
// Parameters:
//   - taskMode: "writing" | "polish" | "shorten" | "expand" | "extract_points"
//   - hasOutlineTitle: true when guided mode provides a title (skips structure + title guidelines)
func (p *StyleProfile) RenderWritingConstraints(taskMode string, hasOutlineTitle bool, wordLimit int) string {
	if p == nil {
		return ""
	}

	var mandatory, required, soft strings.Builder

	// ── Priority 0: Fact Guard (applies to all modes) ──
	p.appendFactGuard(&mandatory)

	// ── Priority 1: Writing requirements ──
	if taskMode == "writing" {
		if !hasOutlineTitle {
			p.appendStructure(&required)
			p.appendTitleGuidelines(&required)
		}
		p.appendRhetoric(&required, true) // detailed=true for writing
	}

	// Word range applies to all modes (writing, polish, shorten, expand)
	p.appendWordRange(&required, taskMode, wordLimit)

	// ── Priority 2: Soft guidance ──
	if taskMode == "writing" {
		p.appendValueOrientation(&soft)
	}

	// Assemble with priority headers
	var result strings.Builder
	if mandatory.Len() > 0 {
		result.WriteString("\n" + priorityMandatory + "\n")
		result.WriteString(mandatory.String())
	}
	if required.Len() > 0 {
		result.WriteString("\n" + priorityRequired + "\n")
		result.WriteString(required.String())
	}
	if soft.Len() > 0 {
		result.WriteString("\n" + prioritySoft + "\n")
		result.WriteString(soft.String())
	}
	return result.String()
}

// RenderStructureSkeleton renders the structure skeleton for outline generation.
// This is used by OutlineStep to guide the LLM in generating outlines that
// conform to the profile's structure type.
//
// For custom type with Sections, returns the section names as a guide.
// For three_part / free_form, returns the Opening/Body/Conclusion if set.
// Returns empty string if no structure is configured.
func (p *StyleProfile) RenderStructureSkeleton() string {
	if p == nil || p.Structure.Type == "" {
		return ""
	}

	// custom 类型：使用 Sections 数组
	if p.Structure.Type == "custom" && len(p.Structure.Sections) > 0 {
		var parts []string
		for _, s := range p.Structure.Sections {
			if s.Description != "" {
				parts = append(parts, fmt.Sprintf("%s（%s）", s.Name, s.Description))
			} else {
				parts = append(parts, s.Name)
			}
		}
		return "结构骨架：" + strings.Join(parts, " → ")
	}

	// 非 custom 类型：使用 Opening/Body/Conclusion
	var parts []string
	if p.Structure.Opening != "" {
		parts = append(parts, p.Structure.Opening)
	}
	if p.Structure.Body != "" {
		parts = append(parts, p.Structure.Body)
	}
	if p.Structure.Conclusion != "" {
		parts = append(parts, p.Structure.Conclusion)
	}
	if len(parts) > 0 {
		return "结构骨架：" + strings.Join(parts, " → ")
	}

	return ""
}

// RenderReviewCriteria renders style constraints for the PostReviewStep
// review prompt. This is a separate rendering path because:
//  1. Rhetoric is listed as category names only (not full descriptions)
//  2. Title guidelines are excluded (handled by independent title review)
//  3. Word range uses the global value (not per-task-type length_profiles)
//  4. Structure is rendered as a summary line, not with argument variations
func (p *StyleProfile) RenderReviewCriteria() string {
	if p == nil {
		return ""
	}

	var mandatory, required strings.Builder

	// ── Priority 0: Fact Guard ──
	p.appendFactGuard(&mandatory)

	// ── Priority 1: Review-specific requirements ──
	p.appendRhetoric(&required, false) // detailed=false for review
	p.appendWordRangeSummary(&required)
	p.appendStructureSummary(&required)

	var result strings.Builder
	if mandatory.Len() > 0 {
		result.WriteString(priorityMandatory + "\n")
		result.WriteString(mandatory.String())
	}
	if required.Len() > 0 {
		result.WriteString(priorityRequired + "\n")
		result.WriteString(required.String())
	}
	return result.String()
}

// RenderTitleConstraints renders title-specific constraints for
// AutoFixStep.fixTitle. This is used when regenerating a title that
// failed review.
func (p *StyleProfile) RenderTitleConstraints() string {
	if p == nil {
		return ""
	}

	var b strings.Builder

	// ── Title length ──
	if p.TitleGuidelines.Length.Max > 0 {
		b.WriteString(fmt.Sprintf("标题字数限制：%d-%d字\n",
			p.TitleGuidelines.Length.Min, p.TitleGuidelines.Length.Max))
	}

	// ── Title style ──
	if p.TitleGuidelines.Style != "" {
		b.WriteString(fmt.Sprintf("标题风格要求：%s\n", p.TitleGuidelines.Style))
	}

	// ── Title examples ──
	if len(p.TitleGuidelines.Examples) > 0 {
		b.WriteString(fmt.Sprintf("标题参考示例：%s\n",
			strings.Join(p.TitleGuidelines.Examples, " / ")))
	}

	// ── Title forbidden patterns ──
	if len(p.TitleGuidelines.ForbiddenPatterns) > 0 {
		b.WriteString(fmt.Sprintf("标题禁止模式（正则）：%s\n",
			strings.Join(p.TitleGuidelines.ForbiddenPatterns, ", ")))
	}

	return b.String()
}

// RenderOutputFormat renders the output format instruction based on
// the profile's OutputFormat settings and whether an outline title exists.
// Returns empty string if no format constraint is needed.
func (p *StyleProfile) RenderOutputFormat(taskMode string, outlineTitle string) string {
	if !IsArticleTaskMode(taskMode) {
		return ""
	}

	// Full-article tasks always use the canonical Markdown contract. Style profiles
	// may shape the content, but they cannot replace the transport contract.
	return RenderMarkdownArticleFormat(outlineTitle)
}

// IsArticleTaskMode reports whether a task returns a complete article rather
// than chat text or a point extraction result.
func IsArticleTaskMode(taskMode string) bool {
	switch taskMode {
	case "writing", "polish", "shorten", "expand":
		return true
	default:
		return false
	}
}

// MarkdownArticleOutputReminder is intentionally short so callers can repeat it
// near the final user/tool message even after a long context.
const MarkdownArticleOutputReminder = "【完整文章输出格式】仅当本轮输出完整文章时：第一行必须是“## 标题”，空一行后输出正文；标题前不要添加客套话，不要输出 JSON 或额外分隔符。"

// RenderMarkdownArticleFormat renders the canonical article contract.
func RenderMarkdownArticleFormat(outlineTitle string) string {
	if outlineTitle != "" {
		return fmt.Sprintf("\n输出格式（标题必须与已确认标题完全一致）：\n## %s\n\n正文内容\n", outlineTitle)
	}
	return "\n输出格式：\n## 文章标题\n\n正文内容\n"
}

// ── Internal appenders ──────────────────────────────────
// These are shared between RenderWritingConstraints and RenderReviewCriteria.

func (p *StyleProfile) appendFactGuard(b *strings.Builder) {
	if len(p.FactGuard.ForbiddenResults) > 0 {
		b.WriteString(fmt.Sprintf("事实红线——禁止使用以下表述（已完成事件不得用结果性动词）：%s\n",
			strings.Join(p.FactGuard.ForbiddenResults, ", ")))
	}
	if len(p.FactGuard.FutureTenseRequired) > 0 {
		b.WriteString(fmt.Sprintf("事实红线——未发生事件须使用以下时态标记：%s\n",
			strings.Join(p.FactGuard.FutureTenseRequired, ", ")))
	}
	if p.FactGuard.UserMaterialPriority {
		b.WriteString("事实红线——用户提供的素材优先于 AI 检索结果，如有冲突以用户素材为准\n")
	}
}

func (p *StyleProfile) appendStructure(b *strings.Builder) {
	if p.Structure.Type == "" {
		return
	}

	// custom 类型优先使用 Sections 数组渲染结构骨架
	if p.Structure.Type == "custom" && len(p.Structure.Sections) > 0 {
		var parts []string
		for _, s := range p.Structure.Sections {
			if s.Description != "" {
				parts = append(parts, fmt.Sprintf("%s（%s）", s.Name, s.Description))
			} else {
				parts = append(parts, s.Name)
			}
		}
		b.WriteString(fmt.Sprintf("结构要求（自定义骨架）：%s\n", strings.Join(parts, " → ")))
		// custom 类型不追加 argument_pattern/variations（这些属于三段式特有概念）
		if p.Structure.ArgumentInstruction != "" {
			b.WriteString(fmt.Sprintf("论述要求：%s\n", p.Structure.ArgumentInstruction))
		}
		return
	}

	// 非 custom 类型：动态拼接结构段，收集所有非空的部分，用 → 连接
	var parts []string
	if p.Structure.Opening != "" {
		parts = append(parts, p.Structure.Opening)
	}
	if p.Structure.Body != "" {
		parts = append(parts, p.Structure.Body)
	}
	if p.Structure.Conclusion != "" {
		parts = append(parts, p.Structure.Conclusion)
	}
	if len(parts) > 0 {
		b.WriteString(fmt.Sprintf("结构要求：%s\n", strings.Join(parts, " → ")))
	}
	if p.Structure.ArgumentPattern != "" {
		b.WriteString(fmt.Sprintf("论证模式：%s\n", p.Structure.ArgumentPattern))
	}
	if len(p.Structure.ArgumentVariations) > 0 {
		b.WriteString(fmt.Sprintf("可选递进变式：%s\n",
			strings.Join(p.Structure.ArgumentVariations, " / ")))
	}
	if p.Structure.ArgumentInstruction != "" {
		b.WriteString(fmt.Sprintf("论述要求：%s\n", p.Structure.ArgumentInstruction))
	}
}

func (p *StyleProfile) appendStructureSummary(b *strings.Builder) {
	if p.Structure.Type == "" {
		return
	}

	// custom 类型优先使用 Sections 数组
	if p.Structure.Type == "custom" && len(p.Structure.Sections) > 0 {
		var parts []string
		for _, s := range p.Structure.Sections {
			parts = append(parts, s.Name)
		}
		b.WriteString(fmt.Sprintf("结构类型：%s（%s）\n",
			p.Structure.Type, strings.Join(parts, " → ")))
		return
	}

	// 非 custom 类型：动态拼接结构摘要
	var parts []string
	if p.Structure.Opening != "" {
		parts = append(parts, p.Structure.Opening)
	}
	if p.Structure.Body != "" {
		parts = append(parts, p.Structure.Body)
	}
	if p.Structure.Conclusion != "" {
		parts = append(parts, p.Structure.Conclusion)
	}
	if len(parts) > 0 {
		b.WriteString(fmt.Sprintf("结构类型：%s（%s）\n",
			p.Structure.Type, strings.Join(parts, " → ")))
	} else {
		b.WriteString(fmt.Sprintf("结构类型：%s\n", p.Structure.Type))
	}
}

func (p *StyleProfile) appendRhetoric(b *strings.Builder, detailed bool) {
	var parts []string
	if p.Rhetoric.RequiredMetaphor {
		if detailed && p.Rhetoric.MetaphorDescription != "" {
			parts = append(parts, "正文核心比喻: "+p.Rhetoric.MetaphorDescription+"（仅用于正文，不影响标题）")
		} else {
			parts = append(parts, "核心比喻")
		}
	}
	if p.Rhetoric.RequiredParallelism {
		if detailed {
			parts = append(parts, "正文必须使用排比")
		} else {
			parts = append(parts, "排比")
		}
	}
	if p.Rhetoric.RequiredRhetoricalQuestion {
		if detailed {
			parts = append(parts, "正文必须使用设问")
		} else {
			parts = append(parts, "设问")
		}
	}
	if len(parts) > 0 {
		sep := "；"
		if !detailed {
			sep = "、"
		}
		label := "修辞要求"
		if !detailed {
			label = "修辞要求——必须包含"
		}
		b.WriteString(fmt.Sprintf("%s：%s\n", label, strings.Join(parts, sep)))
	}
}

func (p *StyleProfile) appendTitleGuidelines(b *strings.Builder) {
	if len(p.TitleGuidelines.ForbiddenPatterns) > 0 {
		b.WriteString(fmt.Sprintf("标题禁止模式（正则）：%s\n",
			strings.Join(p.TitleGuidelines.ForbiddenPatterns, ", ")))
	}
	if p.TitleGuidelines.Style != "" {
		b.WriteString(fmt.Sprintf("标题风格要求：%s\n", p.TitleGuidelines.Style))
	}
	if len(p.TitleGuidelines.Examples) > 0 {
		b.WriteString(fmt.Sprintf("标题参考示例：%s\n",
			strings.Join(p.TitleGuidelines.Examples, " / ")))
	}
}

func (p *StyleProfile) appendWordRange(b *strings.Builder, taskMode string, wordLimit int) {
	// Try length_profiles first (per-task-type word ranges)
	if p.LengthProfiles != nil {
		var key string
		switch taskMode {
		case "polish", "shorten", "expand":
			if wordLimit > 0 && wordLimit <= 600 {
				key = "polish_short"
			} else {
				key = "polish_long"
			}
		case "writing":
			key = "writing"
		}

		if wr, ok := p.LengthProfiles[key]; ok && wr.Max > 0 {
			b.WriteString(fmt.Sprintf("\n字数要求：%d-%d字\n", wr.Min, wr.Max))
			if wr.HardLimit {
				b.WriteString("（字数限制为硬性要求，超出范围将不合格）\n")
			}
			return
		}
	}

	// Fall back to profile's global word_range
	if p.WordRange.Max > 0 {
		b.WriteString(fmt.Sprintf("\n字数要求：%d-%d字\n", p.WordRange.Min, p.WordRange.Max))
		if p.WordRange.HardLimit {
			b.WriteString("（字数限制为硬性要求，超出范围将不合格）\n")
		}
		return
	}

	// Fall back to explicit wordLimit (from execCtx)
	if wordLimit > 0 {
		b.WriteString(fmt.Sprintf("\n字数要求：约%d字\n", wordLimit))
	}
}

func (p *StyleProfile) appendWordRangeSummary(b *strings.Builder) {
	if p.WordRange.Max > 0 {
		b.WriteString(fmt.Sprintf("字数范围：%d-%d字\n", p.WordRange.Min, p.WordRange.Max))
	}
}

func (p *StyleProfile) appendValueOrientation(b *strings.Builder) {
	if len(p.ValueOrientation.Keywords) > 0 {
		b.WriteString(fmt.Sprintf("价值导向关键词（适当融入）：%s\n",
			strings.Join(p.ValueOrientation.Keywords, ", ")))
	}
}
