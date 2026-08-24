package worldstate

import (
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
)

// ─── 具体的 WorldStateSection 实现 ────────────────────────
//
// 这些 section 覆盖 LuminBuddy V2 全平台 system prompt 的所有组成部分。
// 每个 section 知道如何生成自己的快照和增量 diff。

// ── ProfileSection: 基础身份 + 写作风格 Profile ──

// ProfileSection 包含写作风格 Profile 的 system prompt。
// 在多轮对话中，Profile 通常不变，因此 diff 会返回 nil（无变化）。
type ProfileSection struct {
	profile    *profile.StyleProfile
	intent     string // chat / writing / polish 等
	isGuided   bool
}

// NewProfileSection 创建 Profile section。
func NewProfileSection(p *profile.StyleProfile, intent string, isGuided bool) *ProfileSection {
	return &ProfileSection{profile: p, intent: intent, isGuided: isGuided}
}

func (s *ProfileSection) ID() string { return "profile" }

func (s *ProfileSection) Snapshot() interface{} {
	// Snapshot 返回实际会渲染的内容（包含默认身份），
	// 这样 diff 比较才能正确判断是否有变化。
	if s.profile != nil && s.profile.SystemPrompt != "" {
		return s.profile.SystemPrompt
	}
	return "你是笔润智谈，一个专业的中文写作助手。"
}

func (s *ProfileSection) RenderDiff(previous interface{}) *ContextFragment {
	current := s.Snapshot().(string)

	// 如果 Profile 为空，使用默认身份
	if current == "" {
		if s.intent == "chat" {
			current = "你是笔润智谈，一个专业的中文写作助手。"
		} else {
			current = "你是笔润智谈，一个专业的中文写作助手。"
		}
	}

	// 与前一次比较
	if prev, ok := previous.(string); ok && prev == current {
		return nil // 无变化
	}

	return &ContextFragment{
		Role: "system",
		Body: current,
	}
}

// ── ArticleSection: 当前文章全文 ──

// ArticleSection 包含当前文章全文。
// 在多轮润色场景中，文章会变化，因此需要做 diff。
// 文章全文放在 system prompt 而非 messages，配合 prompt caching。
type ArticleSection struct {
	article string
}

// NewArticleSection 创建 Article section。
func NewArticleSection(article string) *ArticleSection {
	return &ArticleSection{article: article}
}

func (s *ArticleSection) ID() string { return "article" }

func (s *ArticleSection) Snapshot() interface{} {
	return s.article
}

func (s *ArticleSection) RenderDiff(previous interface{}) *ContextFragment {
	if s.article == "" {
		return nil
	}

	current := fmt.Sprintf("\n当前文章（全文）：\n%s\n", s.article)

	// 与前一次比较
	if prev, ok := previous.(string); ok && prev == s.article {
		return nil // 文章未变
	}

	return &ContextFragment{
		Role: "system",
		Body: current,
	}
}

// ── DateSection: 当前日期 ──

// DateSection 注入当前日期，帮助模型判断时间线。
// 每天日期会变，但同一天内多轮对话不变。
type DateSection struct{}

func NewDateSection() *DateSection { return &DateSection{} }

func (s *DateSection) ID() string { return "date" }

func (s *DateSection) Snapshot() interface{} {
	return time.Now().Format("2006-01-02")
}

func (s *DateSection) RenderDiff(previous interface{}) *ContextFragment {
	current := s.Snapshot().(string)

	if prev, ok := previous.(string); ok && prev == current {
		return nil // 同一天，无变化
	}

	return &ContextFragment{
		Role: "system",
		Body: fmt.Sprintf("\n当前日期：%s。", time.Now().Format("2006年1月2日")),
	}
}

// ── MaterialsSection: 用户上传素材 + 已有搜索结果 ──

// MaterialsSection 包含用户上传的素材和已有的搜索结果索引。
type MaterialsSection struct {
	userMaterials  []string
	searchCount    int
}

// NewMaterialsSection 创建素材 section。
func NewMaterialsSection(userMaterials []string, searchCount int) *MaterialsSection {
	return &MaterialsSection{userMaterials: userMaterials, searchCount: searchCount}
}

func (s *MaterialsSection) ID() string { return "materials" }

func (s *MaterialsSection) Snapshot() interface{} {
	return fmt.Sprintf("%d|%v", s.searchCount, s.userMaterials)
}

func (s *MaterialsSection) RenderDiff(previous interface{}) *ContextFragment {
	var sb strings.Builder

	if len(s.userMaterials) > 0 {
		sb.WriteString("\n用户上传素材：\n")
		for i, mat := range s.userMaterials {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, mat))
		}
		sb.WriteString("（用户素材优先级高于 AI 检索结果）\n")
	}

	if s.searchCount > 0 {
		sb.WriteString(fmt.Sprintf("\n已有素材：%d 条搜索结果可用。可以使用 read_source 工具读取详情。\n", s.searchCount))
	}

	if sb.Len() == 0 {
		return nil
	}

	// 与前一次比较
	current := s.Snapshot().(string)
	if prev, ok := previous.(string); ok && prev == current {
		return nil
	}

	return &ContextFragment{
		Role: "system",
		Body: sb.String(),
	}
}

// ── RulesSection: 完整风格约束（复用 Profile 渲染层） ──

// RulesSection 包含写作风格的完整约束，复用 profile.RenderWritingConstraints()
// 实现分层注入（P0 强制约束 / P1 写作要求 / P2 风格参考），与 Pipeline 的
// WriteStep 保持单一真相源，避免两套渲染逻辑割裂导致风格漂移。
//
// 覆盖维度：fact_guard + structure(含 argument_variations) + rhetoric(详细)
// + title_guidelines(含 forbidden_patterns) + word_range + value_orientation
type RulesSection struct {
	profile    *profile.StyleProfile
	intent     string
	isGuided   bool
	wordLimit  int
}

// NewRulesSection 创建规则 section。
func NewRulesSection(p *profile.StyleProfile, intent string) *RulesSection {
	return &RulesSection{profile: p, intent: intent}
}

// NewRulesSectionWithDetails 创建带 guided/wordLimit 的规则 section。
// 用于 Harness 模式下需要传递 guided 标志和字数限制的场景。
func NewRulesSectionWithDetails(p *profile.StyleProfile, intent string, isGuided bool, wordLimit int) *RulesSection {
	return &RulesSection{profile: p, intent: intent, isGuided: isGuided, wordLimit: wordLimit}
}

func (s *RulesSection) ID() string { return "rules" }

func (s *RulesSection) Snapshot() interface{} {
	if s.profile == nil {
		return ""
	}
	// 用完整 RenderWritingConstraints 输出作为快照，
	// 确保任何 Profile 字段变化都能被 diff 检测到
	return s.profile.RenderWritingConstraints(s.intent, s.isGuided, s.wordLimit)
}

func (s *RulesSection) RenderDiff(previous interface{}) *ContextFragment {
	if s.profile == nil || s.intent == "chat" {
		return nil // chat 意图不注入规则
	}

	// 复用 Pipeline 侧的 RenderWritingConstraints，确保 14 维度全量注入
	current := s.profile.RenderWritingConstraints(s.intent, s.isGuided, s.wordLimit)
	if current == "" {
		return nil
	}

	// 与前一次比较
	if prev, ok := previous.(string); ok && prev == current {
		return nil // 无变化
	}

	return &ContextFragment{
		Role: "system",
		Body: current,
	}
}

// ── TaskInstructionsSection: 任务步骤指令 ──

// TaskInstructionsSection 包含根据意图和模式生成的任务步骤指令。
type TaskInstructionsSection struct {
	intent   string
	isGuided bool
}

// NewTaskInstructionsSection 创建任务指令 section。
func NewTaskInstructionsSection(intent string, isGuided bool) *TaskInstructionsSection {
	return &TaskInstructionsSection{intent: intent, isGuided: isGuided}
}

func (s *TaskInstructionsSection) ID() string { return "task_instructions" }

func (s *TaskInstructionsSection) Snapshot() interface{} {
	return fmt.Sprintf("%s|%v", s.intent, s.isGuided)
}

func (s *TaskInstructionsSection) RenderDiff(previous interface{}) *ContextFragment {
	var sb strings.Builder

	switch s.intent {
	case "writing":
		if s.isGuided {
			sb.WriteString("\n\n你现在要帮用户写一篇新文章（引导模式）。请按以下步骤操作：")
			sb.WriteString("\n1. 如果已有素材不足，用 search_web 搜索网络信息，用 search_knowledge 检索知识库范文")
			sb.WriteString("\n   - search_web：时事热点、公开数据、新闻资讯、网络观点")
			sb.WriteString("\n   - search_knowledge：写作风格规范、历史范文、栏目调性、内部观点")
			sb.WriteString("\n   - 两者可同时调用（并行），结果统一通过 read_source 读取详情")
			sb.WriteString("\n2. 调用 generate_outline 生成提纲，等待用户确认")
			sb.WriteString("\n3. 用户确认提纲后，调用 write_article 开始写作，然后在下一轮回复中按以下格式输出文章：\n   先输出标题 JSON（一行），例如：{\"title\":\"文章标题\"}\n   然后换行输出分隔符 ---ARTICLE---\n   再换行输出正文 Markdown（不要重复标题，直接从第一段开始）")
			sb.WriteString("\n4. 文章写完后调用 review_article 评审质量")
			sb.WriteString("\n5. 如果评审发现问题，调用 revise_section 修正")
		} else {
			sb.WriteString("\n\n你现在要帮用户写一篇新文章。请按以下步骤操作：")
			sb.WriteString("\n1. 如果已有素材不足，用 search_web 搜索网络信息，用 search_knowledge 检索知识库范文")
			sb.WriteString("\n   - search_web：时事热点、公开数据、新闻资讯、网络观点")
			sb.WriteString("\n   - search_knowledge：写作风格规范、历史范文、栏目调性、内部观点")
			sb.WriteString("\n   - 两者可同时调用（并行），结果统一通过 read_source 读取详情")
			sb.WriteString("\n2. 用 read_source 读取重要素材的详细内容")
			sb.WriteString("\n3. 调用 write_article 开始写作，然后在下一轮回复中按以下格式输出文章：\n   先输出标题 JSON（一行），例如：{\"title\":\"文章标题\"}\n   然后换行输出分隔符 ---ARTICLE---\n   再换行输出正文 Markdown（不要重复标题，直接从第一段开始）")
			sb.WriteString("\n4. 文章写完后调用 review_article 评审质量")
			sb.WriteString("\n5. 如果评审发现问题，调用 revise_section 修正")
		}
	case "polish", "shorten", "expand", "extract":
		sb.WriteString("\n\n用户要修改已有文章。请调用 revise_section 定向修改，不要重写全文。")
		sb.WriteString("\n调用后直接输出修改后的完整文章。")
	case "chat":
		sb.WriteString("\n\n用户在和你对话。直接回复即可。")
		sb.WriteString("\n如果需要查资料可以调用 search_web（网络信息）或 search_knowledge（内部知识库），但简单问题直接回答。")
	}

	if sb.Len() == 0 {
		return nil
	}

	// 与前一次比较
	current := s.Snapshot().(string)
	if prev, ok := previous.(string); ok && prev == current {
		return nil
	}

	return &ContextFragment{
		Role: "system",
		Body: sb.String(),
	}
}

// ── SecuritySection: Prompt 注入防御指令 ──

// SecuritySection 包含 Prompt 注入防御指令。
// 这个内容永远不变，因此 diff 只在首次推送时返回。
type SecuritySection struct{}

func NewSecuritySection() *SecuritySection { return &SecuritySection{} }

func (s *SecuritySection) ID() string { return "security" }

func (s *SecuritySection) Snapshot() interface{} {
	return "fixed" // 固定内容
}

func (s *SecuritySection) RenderDiff(previous interface{}) *ContextFragment {
	if prev, ok := previous.(string); ok && prev == "fixed" {
		return nil // 防御指令不变
	}

	return &ContextFragment{
		Role: "system",
		Body: engine.PromptInjectionDefenseDirective,
	}
}

// ── BuildWorldStateForHarness 为 Harness 构建 WorldState ──

// BuildWorldStateForHarness 根据 session 状态和意图构建完整的 WorldState。
// 适用于 Harness 模式（chat/writing/polish）。
func BuildWorldStateForHarness(
	p *profile.StyleProfile,
	intent string,
	isGuided bool,
	article string,
	userMaterials []string,
	searchCount int,
) *WorldState {
	ws := NewWorldState()

	ws.Register(NewProfileSection(p, intent, isGuided))
	ws.Register(NewArticleSection(article))
	ws.Register(NewDateSection())
	ws.Register(NewMaterialsSection(userMaterials, searchCount))
	ws.Register(NewRulesSectionWithDetails(p, intent, isGuided, 0))
	ws.Register(NewTaskInstructionsSection(intent, isGuided))
	ws.Register(NewSecuritySection())

	return ws
}
