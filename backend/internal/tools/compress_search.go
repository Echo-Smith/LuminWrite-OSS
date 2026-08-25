package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// ─── 源感知搜索结果压缩（全局统一） ──────────────────────
//
// CompressSearchResults 是三个模式（Pipeline / Harness / Editorial）共享的
// 搜索结果压缩函数。它将多源搜索结果压缩为结构化研究简报，供下游 LLM 使用。
//
// 源感知策略（核心价值）：
//   - Source 含 "_ai" 后缀的（如 weibo_ai）是 AI 生成的结构化摘要，
//     这已经是高质量摘要，不需要 LLM 二次压缩——直接注入上下文
//   - 当结果全部是 AI 摘要时：直接格式化返回，完全跳过 LLM 压缩调用
//   - 当混合了 AI 摘要和普通摘要时：AI 摘要原文直接注入 + 普通摘要走 LLM 压缩
//   - 普通网页摘要（snippet）按总预算截断后走 LLM 压缩
//   - Pipeline 模式传入的 results 已经过 RelevanceStep 相关性过滤和
//     SanitizeExternalContent 净化，这里不再重复处理
//   - Harness/Editorial 模式传入的 results 尚未净化，此处统一做净化
//
// 调用方：
//   - Harness: agent/tools.go → executeSearchWeb
//   - Editorial: editorial/builtin_tools.go → executeSearchWeb
//   - Pipeline: engine/steps/compress.go → CompressStep.Execute

// maxRawChars 限制传入 LLM 压缩器的原始文本总量。
const maxRawChars = 12000

// CompressSearchResults 用 LLM 将原始搜索结果压缩为结构化研究简报。
//
// 源感知跳过逻辑：
//   - 全部是 AI 摘要（_ai 后缀）→ 直接格式化返回，跳过 LLM 调用
//   - 混合 AI 摘要 + 普通摘要 → AI 摘要原文注入 + 普通摘要 LLM 压缩
//   - 全部是普通摘要 → 走 LLM 压缩
//
// 返回：(压缩后的简报文本, 原始文本字数, LLM 响应[含 token 统计])。
func CompressSearchResults(ctx context.Context, llm *LLMClient, query string, results []engine.SearchResult) (string, int, *LLMResponse) {
	if llm == nil || len(results) == 0 {
		return FormatSearchResults(results), 0, nil
	}

	// ── 统一净化：对所有搜索结果做 Prompt 注入防御 ──
	// Pipeline 模式的 results 已在 SearchStep/CompressStep 中净化过，
	// 这里对已净化的内容是幂等的（正则不匹配 → 原样返回）。
	// Harness/Editorial 模式的 results 未净化过，在此统一补全。
	results = engine.SanitizeSearchResults(results)

	// ── 源感知分流：将结果分为 AI 摘要和普通摘要两组 ──
	var aiResults, normalResults []engine.SearchResult
	for _, r := range results {
		if isAISummary(r.Source) {
			aiResults = append(aiResults, r)
		} else {
			normalResults = append(normalResults, r)
		}
	}

	// ── 情况 1：全部是 AI 摘要 → 直接格式化返回，跳过 LLM ──
	if len(normalResults) == 0 {
		formatted := formatAISummaries(aiResults)
		slog.Info("CompressSearchResults: all AI summaries, skipping LLM compression",
			"query", query,
			"ai_results", len(aiResults),
			"output_chars", len([]rune(formatted)),
		)
		return formatted, len(formatted), nil
	}

	// ── 情况 2：混合 AI 摘要 + 普通摘要 ──
	// AI 摘要原文直接注入，普通摘要走 LLM 压缩
	var aiInjectText string
	var aiChars int
	if len(aiResults) > 0 {
		aiInjectText, aiChars = formatAISummariesWithBudget(aiResults, maxRawChars/2)
	}

	// 普通摘要用剩余预算走 LLM 压缩
	normalRawText, normalChars := buildRawSearchText(normalResults)

	systemMsg := "你是搜索结果压缩器。将搜索结果压缩为结构化的研究简报，保留关键事实和多元视角，删除冗余信息。只输出简报内容，不要解释。" + engine.PromptInjectionDefenseDirective

	userMsg := fmt.Sprintf(`搜索关键词：%s

以下分两部分：
第一部分是「AI 研究摘要」，已经是高质量结构化摘要，请原样保留其关键信息，不要改写。
第二部分是「网页搜索结果」，请按以下要求压缩。

=== AI 研究摘要（原样保留） ===
%s
=== 网页搜索结果（需压缩） ===
%s

压缩要求（仅针对网页搜索结果）：
1. 提取核心事实（数据、事件、政策等客观信息）
2. 提取多元视角（不同立场、观点、争议）
3. 提取关键数据（数字、比例、时间等）
4. 删除重复信息、广告内容、无关细节
5. 每条信息尽量简短（一行以内）
6. 每条事实后用 [序号] 标注来源
7. 去重：不同来源的相同信息只保留一条
8. 如果素材不足以支撑某些维度，直接省略该维度

输出格式：
## 研究简报

### AI 研究摘要
（原样保留上方 AI 研究摘要内容）

### 补充事实
- 事实1 [序号]

### 多元视角
- 视角1

### 关键数据
- 数据1

### 写作建议
- 建议1`, query, aiInjectText, normalRawText)

	resp, llmResp, err := llm.Chat(ctx, []LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, WithTemperature(0.1), WithThinking(false))

	if err != nil {
		slog.Warn("CompressSearchResults: LLM compression failed, falling back",
			"error", err,
			"query", query,
			"ai_results", len(aiResults),
			"normal_results", len(normalResults),
		)
		return FormatSearchResults(results), normalChars + aiChars, nil
	}

	if resp == "" {
		return FormatSearchResults(results), normalChars + aiChars, nil
	}

	slog.Info("search results compressed to research brief",
		"query", query,
		"ai_results", len(aiResults),
		"normal_results", len(normalResults),
		"ai_chars", aiChars,
		"normal_chars", normalChars,
		"brief_chars", len([]rune(resp)),
	)
	return resp, normalChars + aiChars, llmResp
}

// buildRawSearchText 构建传入 LLM 压缩器的原始搜索文本（仅普通摘要）。
// 总上限 maxRawChars (12000 字)，按顺序截断。
func buildRawSearchText(results []engine.SearchResult) (string, int) {
	var rawBuf strings.Builder
	totalChars := 0
	for i, r := range results {
		entry := fmt.Sprintf("[%d] %s\n    %s\n    来源: %s\n\n", i+1, r.Title, r.Snippet, r.Source)
		if totalChars+len(entry) > maxRawChars {
			rawBuf.WriteString(fmt.Sprintf("... (还有 %d 条结果省略)\n", len(results)-i))
			break
		}
		rawBuf.WriteString(entry)
		totalChars += len(entry)
	}
	return rawBuf.String(), totalChars
}

// isAISummary 判断搜索结果来源是否是 AI 生成的摘要。
// 约定：Source 含 "_ai" 后缀的（如 "weibo_ai"）为 AI 摘要。
func isAISummary(source string) bool {
	return strings.HasSuffix(source, "_ai")
}

// FormatSearchResults 是 CompressSearchResults 的 fallback（LLM 不可用时使用）。
// 直接输出搜索结果，总上限 12000 字，不截断单条。
func FormatSearchResults(results []engine.SearchResult) string {
	var sb strings.Builder
	totalChars := 0
	for i, r := range results {
		entry := fmt.Sprintf("%d. %s\n   %s\n\n", i+1, r.Title, r.Snippet)
		if totalChars+len(entry) > maxRawChars {
			sb.WriteString(fmt.Sprintf("... (还有 %d 条结果省略)\n", len(results)-i))
			break
		}
		sb.WriteString(entry)
		totalChars += len(entry)
	}
	return sb.String()
}

// formatAISummaries 格式化 AI 摘要结果，直接注入上下文（不经过 LLM 压缩）。
// 不截断单条，总上限 maxRawChars。
func formatAISummaries(results []engine.SearchResult) string {
	var sb strings.Builder
	totalChars := 0
	for i, r := range results {
		entry := fmt.Sprintf("### AI 研究摘要 [%d]\n来源: %s\n%s\n\n", i+1, r.Source, r.Snippet)
		if totalChars+len(entry) > maxRawChars {
			sb.WriteString(fmt.Sprintf("... (还有 %d 条 AI 摘要省略)\n", len(results)-i))
			break
		}
		sb.WriteString(entry)
		totalChars += len(entry)
	}
	return sb.String()
}

// formatAISummariesWithBudget 格式化 AI 摘要结果，带指定预算。
// 用于混合模式下给 AI 摘要分配一半预算。
func formatAISummariesWithBudget(results []engine.SearchResult, budget int) (string, int) {
	var sb strings.Builder
	totalChars := 0
	for i, r := range results {
		entry := fmt.Sprintf("### AI 研究摘要 [%d]\n来源: %s\n%s\n\n", i+1, r.Source, r.Snippet)
		if totalChars+len(entry) > budget {
			sb.WriteString(fmt.Sprintf("... (还有 %d 条 AI 摘要省略)\n", len(results)-i))
			break
		}
		sb.WriteString(entry)
		totalChars += len(entry)
	}
	return sb.String(), totalChars
}
