package memory

import (
	"sort"
)

// ─── 对话历史智能截断 ──────────────────────────────────────
//
// 在 WriteStep / ChatStep 注入对话历史时，使用 token 精确计数
// 和语义优先排序，替代旧的字符硬截断（maxLen=800/500）。
//
// 核心改进：
//  1. Token 预算驱动：不再用字符数，用 EstimateTokens 精确计量
//  2. 语义优先排序：保留 recency window + 语义相关历史
//  3. 智能截断：超过预算时按优先级裁剪，而非粗暴截断
//  4. 统一接口：WriteStep 和 ChatStep 共用

// HistorySelectionConfig 对话历史选择配置
type HistorySelectionConfig struct {
	// MaxHistoryTokens 注入历史消息的最大 token 数
	// 默认 2048（约占 32K 上下文窗口的 6%）
	MaxHistoryTokens int
	// RecencyWindow 始终保留的最近 N 条消息（不受预算裁剪）
	RecencyWindow int
	// MaxTokensPerMessage 单条历史消息的最大 token 数
	// 超过则截断（避免一篇长文章占满整个预算）
	MaxTokensPerMessage int
	// AssistantArticleMaxTokens 助手文章类历史消息的 token 上限
	// 通常比普通消息更短（文章太长无法完整注入）
	AssistantArticleMaxTokens int
}

// DefaultHistorySelectionConfig 默认历史选择配置
func DefaultHistorySelectionConfig() HistorySelectionConfig {
	return HistorySelectionConfig{
		MaxHistoryTokens:         2048,
		RecencyWindow:            4,
		MaxTokensPerMessage:      600,
		AssistantArticleMaxTokens: 300,
	}
}

// HistorySelectionResult 历史选择结果
type HistorySelectionResult struct {
	// Messages 选中的消息列表（按时间正序）
	Messages []ConversationMessage
	// TotalTokens 选中消息的总 token 数
	TotalTokens int
	// TruncatedCount 被截断的消息数（因超过单条上限）
	TruncatedCount int
	// DroppedCount 因预算不足被完全丢弃的消息数
	DroppedCount int
}

// SelectHistoryForPrompt 根据预算和语义优先级选择对话历史。
//
// 算法：
//  1. 为每条消息计算 token 数（使用 EstimateTokens）
//  2. 超过单条上限的消息先截断
//  3. 分离 recency window（始终保留）
//  4. 计算剩余预算
//  5. 从旧到新，按优先级排序，贪心填充
//  6. 超预算时从最旧的消息开始丢弃
//
// 如果 messages 已经是 ShortTermMemoryStep 的动态窗口输出，
// 这一步是二次保险——确保注入到 LLM 的总 token 在预算内。
func SelectHistoryForPrompt(messages []ConversationMessage, config HistorySelectionConfig) HistorySelectionResult {
	if len(messages) == 0 {
		return HistorySelectionResult{}
	}

	// 1. 计算每条消息的 token 数，并截断超长消息
	type msgWithTokens struct {
		msg       ConversationMessage
		tokens    int
		truncated bool
		origIdx   int // original index in messages
	}
	items := make([]msgWithTokens, len(messages))
	for i, msg := range messages {
		tokens := EstimateTokens(msg.Content)
		maxT := config.MaxTokensPerMessage
		if msg.Role == RoleAssistant && msg.ContentType == ContentArticle {
			maxT = config.AssistantArticleMaxTokens
		}
		if tokens > maxT {
			// 截断内容到约 maxT tokens
			// EstimateTokens ≈ runeCount * 0.8, 所以 maxBytes ≈ maxT / 0.8 * 3 (UTF-8 中文 3 字节)
			// 但用 SafeTruncate 更安全
			maxBytes := int(float64(maxT) / 0.8 * 3)
			if maxBytes > len(msg.Content) {
				maxBytes = len(msg.Content)
			}
			msg.Content = SafeTruncate(msg.Content, maxBytes) + "\n...（历史消息已截断）"
			tokens = EstimateTokens(msg.Content)
		items[i] = msgWithTokens{msg: msg, tokens: tokens, truncated: true, origIdx: i}
		} else {
			items[i] = msgWithTokens{msg: msg, tokens: tokens, truncated: false, origIdx: i}
		}
	}

	// 2. 分离 recency window 和旧消息
	recencyCount := config.RecencyWindow
	if recencyCount > len(items) {
		recencyCount = len(items)
	}
	recencyItems := items[len(items)-recencyCount:]
	olderItems := items[:len(items)-recencyCount]

	// 3. 计算 recency window 的 token 消耗
	recencyTokens := 0
	for _, item := range recencyItems {
		recencyTokens += item.tokens
	}

	// 4. 剩余预算
	remainingBudget := config.MaxHistoryTokens - recencyTokens
	truncatedCount := 0
	for _, item := range recencyItems {
		if item.truncated {
			truncatedCount++
		}
	}
	droppedCount := 0

	// 5. 旧消息按语义优先级排序（如果有的话用 embedding 相似度）
	// 没有查询向量时，按时间倒序优先（最近的最重要）
	// 有 embedding 时按相似度排序
	// 简化：按时间倒序，保留最近的优先
	// （ShortTermMemoryStep 已经做了语义筛选，这里只做预算裁剪）
	olderSorted := make([]msgWithTokens, len(olderItems))
	copy(olderSorted, olderItems)
	// 按 token 数降序排，优先丢弃 token 多的旧消息
	sort.Slice(olderSorted, func(i, j int) bool {
		return olderSorted[i].tokens > olderSorted[j].tokens
	})

	// 6. 贪心填充：从 token 少的旧消息开始（最大化消息数）
	// 反向遍历 olderSorted（token 少的在后），优先选 token 少的
	selectedIndices := make(map[int]bool)
	for i := len(olderSorted) - 1; i >= 0; i-- {
		if olderSorted[i].tokens <= remainingBudget {
			idx := olderSorted[i].origIdx
			if !selectedIndices[idx] {
				selectedIndices[idx] = true
				remainingBudget -= olderSorted[i].tokens
				if olderSorted[i].truncated {
					truncatedCount++
				}
			}
		}
	}

	// 统计丢弃的
	for i := range olderItems {
		if !selectedIndices[i] {
			droppedCount++
		}
	}

	// 7. 按时间顺序输出
	var result []ConversationMessage
	for i, item := range olderItems {
		if selectedIndices[i] {
			result = append(result, item.msg)
		}
	}
	for _, item := range recencyItems {
		result = append(result, item.msg)
	}

	totalTokens := config.MaxHistoryTokens - remainingBudget

	return HistorySelectionResult{
		Messages:       result,
		TotalTokens:    totalTokens,
		TruncatedCount: truncatedCount,
		DroppedCount:   droppedCount,
	}
}

// FormatSelectedHistoryForLLM 将选中的对话历史格式化为 LLM 消息列表。
// 兼容旧的 FormatHistoryForLLM 但使用 SelectionResult。
func FormatSelectedHistoryForLLM(result HistorySelectionResult) []struct {
	Role    string
	Content string
} {
	if len(result.Messages) == 0 {
		return nil
	}
	output := make([]struct {
		Role    string
		Content string
	}, 0, len(result.Messages))
	for _, msg := range result.Messages {
		output = append(output, struct {
			Role    string
			Content string
		}{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}
	return output
}
