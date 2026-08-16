package agent

import (
	"strings"
)

// ─── 意图判定：Harness 规则路由 ────────────────────────────
//
// 意图判定分两层：
//   1. Harness 规则（毫秒级，不调 LLM）— 处理明确意图
//   2. LLM fallback — 只在有歧义时调用
//
// 规则判定基于关键词匹配 + 会话上下文（是否有当前文章）。
// 这不是精确分类，只是快速路由 — 边界 case 由 LLM 在会话中自然处理。

// Intent 表示用户本轮请求的意图。
type Intent string

const (
	IntentWriting  Intent = "writing"
	IntentChat     Intent = "chat"
	IntentPolish   Intent = "polish"
	IntentShorten  Intent = "shorten"
	IntentExpand   Intent = "expand"
	IntentExtract  Intent = "extract_points"
)

// ClassifyIntent 基于规则判定用户意图。
// session 参数提供上下文：是否有已有文章等。
func ClassifyIntent(input string, session *WritingSession) Intent {
	normalized := strings.ToLower(strings.TrimSpace(input))
	if normalized == "" {
		return IntentChat
	}

	// 如果有当前文章，检查是否是修改意图
	if session != nil && session.HasArticle() {
		if matchesAny(normalized,
			"改", "修改", "换成", "调整", "润色", "改写",
			"重写", "替换", "删掉", "去掉", "加入", "添加") {
			// 进一步细分
			if matchesAny(normalized, "缩短", "精简", "缩写", "短") {
				return IntentShorten
			}
			if matchesAny(normalized, "扩写", "展开", "扩充", "补充") {
				return IntentExpand
			}
			if matchesAny(normalized, "提炼", "提取观点", "提取要点") {
				return IntentExtract
			}
			return IntentPolish
		}
	}

	// 写作意图 — 明确的写作请求
	if matchesAny(normalized,
		"写一篇", "写稿", "撰写", "写评论", "基于热搜",
		"写文章", "帮我写", "写个", "写一段",
		"再写一篇", "换角度") {
		return IntentWriting
	}

	// 润色/缩写/扩写 — 操作已有文本（可能有用户粘贴的文本）
	if matchesAny(normalized, "润色", "优化", "改写") {
		return IntentPolish
	}
	if matchesAny(normalized, "缩写", "缩短", "精简") {
		return IntentShorten
	}
	if matchesAny(normalized, "扩写", "扩充", "展开") {
		return IntentExpand
	}
	if matchesAny(normalized, "提炼", "提取观点") {
		return IntentExtract
	}

	// 默认：对话
	return IntentChat
}

// matchesAny 检查 input 是否包含任意一个关键词。
func matchesAny(input string, keywords ...string) bool {
	for _, kw := range keywords {
		if strings.Contains(input, kw) {
			return true
		}
	}
	return false
}

// IntentStepName 返回意图对应的 step 名称（用于前端 step 事件）。
func (i Intent) StepName() string {
	switch i {
	case IntentWriting:
		return "write_article"
	case IntentPolish, IntentShorten, IntentExpand:
		return "revise_section"
	case IntentExtract:
		return "revise_section"
	default:
		return "chat"
	}
}
