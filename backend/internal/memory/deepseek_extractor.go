package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/luminbuddy/writing-agent-v2/internal/tools"
	"github.com/luminbuddy/writing-agent-v2/pkg/memory"
)

// DeepSeekExtractor 是 memory.LLMExtractor 的 DeepSeek 实现
type DeepSeekExtractor struct {
	llm *tools.LLMClient
}

// NewDeepSeekExtractor 创建 DeepSeek LLM 提取器
func NewDeepSeekExtractor(llm *tools.LLMClient) *DeepSeekExtractor {
	return &DeepSeekExtractor{llm: llm}
}

// ExtractFromArticle 从完整文章中提取结构化偏好
func (e *DeepSeekExtractor) ExtractFromArticle(ctx context.Context, article, styleSlug string) ([]memory.ExtractedMemory, error) {
	if e.llm == nil || len(article) < 100 {
		return nil, nil
	}

	prompt := fmt.Sprintf(`分析以下文章的写作风格特征，提取作者的偏好模式。

文章（风格：%s）：
%s

请分析以下维度，输出 JSON 数组（不需要的维度可省略）：
[
  {"category": "tone", "key": "tone_preference", "value": "理性/感性/幽默/严肃", "source": "llm"},
  {"category": "structure", "key": "argument_pattern", "value": "因果/对比/递进/举例", "source": "llm"},
  {"category": "title", "key": "title_style", "value": "标题特征描述（长度、修辞、情感）", "source": "llm"},
  {"category": "sentence", "key": "sentence_style", "value": "长短句比例/排比/设问等特征", "source": "llm"},
  {"category": "topic", "key": "topic_domain", "value": "文章话题领域", "source": "llm"}
]

只输出 JSON 数组，不要其他文字。`, styleSlug, truncate(article, 2000))

	resp, _, err := e.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: "你是写作风格分析师，只输出 JSON 格式结果。"},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM extraction failed: %w", err)
	}

	return parseExtractedMemories(resp, "llm"), nil
}

// ExtractFromFeedback 从反馈评论中提取改进信号
func (e *DeepSeekExtractor) ExtractFromFeedback(ctx context.Context, feedback []memory.FeedbackInfo) ([]memory.ExtractedMemory, error) {
	if e.llm == nil || len(feedback) == 0 {
		return nil, nil
	}

	// 构建反馈摘要
	var feedbackParts []string
	for i, fb := range feedback {
		if fb.Comment == "" && fb.Rating > 3 {
			continue // 无评论的好评不提取
		}
		feedbackParts = append(feedbackParts, fmt.Sprintf(
			"反馈%d: 类型=%s, 评分=%d星, 评论=%s",
			i+1, fb.SegmentType, fb.Rating, fb.Comment,
		))
	}
	if len(feedbackParts) == 0 {
		return nil, nil
	}

	prompt := fmt.Sprintf(`从以下用户反馈中提取改进信号。每条反馈代表用户对文章某部分的不满或建议。

反馈列表：
%s

请提取改进信号，输出 JSON 数组：
[
  {"category": "title", "key": "改进点标识", "value": "用户期望的标题风格", "source": "feedback"},
  {"category": "structure", "key": "改进点标识", "value": "用户期望的结构", "source": "feedback"},
  {"category": "tone", "key": "改进点标识", "value": "用户期望的语气", "source": "feedback"}
]

注意：
- 只提取有明确改进方向的反馈
- key 用英文 snake_case，描述具体问题（如 dislikes_long_title, prefers_concise）
- value 用中文描述用户期望
- 只输出 JSON 数组，不要其他文字`,
		strings.Join(feedbackParts, "\n"))

	resp, _, err := e.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: "你是反馈分析师，只输出 JSON 格式结果。"},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM feedback extraction failed: %w", err)
	}

	return parseExtractedMemories(resp, "feedback"), nil
}

// parseExtractedMemories 从 LLM 响应中解析 JSON 数组
func parseExtractedMemories(resp string, source string) []memory.ExtractedMemory {
	// 清理 markdown 代码块
	resp = strings.TrimSpace(resp)
	resp = strings.TrimPrefix(resp, "```json")
	resp = strings.TrimPrefix(resp, "```")
	resp = strings.TrimSuffix(resp, "```")
	resp = strings.TrimSpace(resp)

	// 提取 JSON 数组
	arrStart := strings.Index(resp, "[")
	arrEnd := strings.LastIndex(resp, "]")
	if arrStart < 0 || arrEnd < 0 || arrEnd <= arrStart {
		slog.Debug("memory: no JSON array found in LLM response", "response", resp[:min(200, len(resp))])
		return nil
	}

	jsonStr := resp[arrStart : arrEnd+1]

	var results []memory.ExtractedMemory
	if err := json.Unmarshal([]byte(jsonStr), &results); err != nil {
		slog.Warn("memory: failed to parse extracted memories", "error", err, "json", jsonStr[:min(200, len(jsonStr))])
		return nil
	}

	// 确保所有条目都有 source
	for i := range results {
		if results[i].Source == "" {
			results[i].Source = source
		}
	}

	return results
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
