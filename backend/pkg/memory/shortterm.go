package memory

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"time"
	"unicode/utf8"
)

// ─── 短期记忆：语义裁切 + 动态窗口 ──────────────────────────
//
// 短期记忆管理同一会话内的对话历史，使其参与 LLM 推理。
//
// 核心算法：
//  1. 语义裁切（Semantic Chunking）：将对话历史按语义相似度分组，
//     识别"话题切换边界"，形成语义连贯的对话片段。
//  2. 动态窗口（Dynamic Window）：在 token 预算内，优先保留最近的消息
//     （recency window），然后用语义相关性填充剩余预算（relevance window），
//     确保注入的历史既有时效性又有相关性。

// ConversationRole 对话消息角色
type ConversationRole string

const (
	RoleUser      ConversationRole = "user"
	RoleAssistant ConversationRole = "assistant"
	RoleSystem    ConversationRole = "system"
)

// ContentType 消息内容类型
type ContentType string

const (
	ContentText     ContentType = "text"
	ContentArticle  ContentType = "article"
	ContentReview   ContentType = "review"
	ContentOutline  ContentType = "outline"
)

// ConversationMessage 对话消息 — 短期记忆的基本单元
type ConversationMessage struct {
	ID             string           `json:"id"`
	ConversationID string           `json:"conversation_id"`
	UserID         string           `json:"user_id"`
	TraceID        string           `json:"trace_id,omitempty"`
	Role           ConversationRole `json:"role"`
	Content        string           `json:"content"`
	ContentType    ContentType      `json:"content_type"`
	Intent         string           `json:"intent,omitempty"`
	Embedding      []float32        `json:"-"`
	TokenCount     int              `json:"token_count"`
	Metadata       map[string]any   `json:"metadata,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
}

// SemanticChunk 语义片段 — 一组语义连贯的对话消息
type SemanticChunk struct {
	Messages   []ConversationMessage `json:"messages"`
	StartTime  time.Time             `json:"start_time"`
	EndTime    time.Time             `json:"end_time"`
	TokenCount int                   `json:"token_count"`
	// 与当前查询的语义相似度（检索时填充）
	Relevance float64 `json:"relevance,omitempty"`
	// 在原始历史中的位置索引（0-based）
	Index int `json:"index"`
}

// DynamicWindowConfig 动态窗口配置
type DynamicWindowConfig struct {
	// Token 预算：注入的历史消息总 token 数上限
	TokenBudget int
	// 最近窗口：始终保留的最近 N 条消息（不受语义相关性影响）
	RecencyWindow int
	// 语义相似度阈值：低于此值的旧消息不注入
	RelevanceThreshold float64
	// 语义裁切的相似度下降阈值：相邻消息相似度低于此值时切分新片段
	ChunkSplitThreshold float64
}

// DefaultDynamicWindowConfig 默认动态窗口配置
func DefaultDynamicWindowConfig() DynamicWindowConfig {
	return DynamicWindowConfig{
		TokenBudget:         4096, // 约 4096 tokens 用于历史
		RecencyWindow:       4,    // 始终保留最近 4 条消息
		RelevanceThreshold:  0.35, // 最低语义相关性
		ChunkSplitThreshold: 0.45, // 相邻消息相似度低于 0.45 时切分
	}
}

// ShortTermStore 短期记忆存储接口
type ShortTermStore interface {
	// StoreMessage 存储一条对话消息
	StoreMessage(ctx context.Context, msg *ConversationMessage) error

	// LoadHistory 加载会话的对话历史（按时间正序）
	LoadHistory(ctx context.Context, conversationID string, limit int) ([]ConversationMessage, error)

	// GenerateEmbedding 为消息生成 embedding（如果尚未生成）
	GenerateEmbedding(ctx context.Context, messageID string) error
}

// SemanticChunker 语义裁切器 — 将对话历史切分为语义连贯的片段
type SemanticChunker struct {
	config DynamicWindowConfig
}

// NewSemanticChunker 创建语义裁切器
func NewSemanticChunker(config DynamicWindowConfig) *SemanticChunker {
	return &SemanticChunker{config: config}
}

// Chunk 将对话历史按语义切分为片段
//
// 算法：
//  1. 计算相邻消息之间的语义相似度
//  2. 当相似度低于 ChunkSplitThreshold 时，切分新片段
//  3. 每个片段包含语义连贯的一组消息
func (c *SemanticChunker) Chunk(messages []ConversationMessage) []SemanticChunk {
	if len(messages) == 0 {
		return nil
	}

	var chunks []SemanticChunk
	var currentChunk []ConversationMessage
	currentStart := messages[0].CreatedAt

	for i, msg := range messages {
		if len(currentChunk) > 0 {
			// 计算与前一条消息的语义相似度
			prev := currentChunk[len(currentChunk)-1]
			similarity := cosineSimilarityF32(prev.Embedding, msg.Embedding)

			// 相似度低于阈值 → 切分新片段
			if similarity < c.config.ChunkSplitThreshold {
				chunks = append(chunks, c.finalizeChunk(currentChunk, currentStart, len(chunks)))
				currentChunk = nil
				currentStart = msg.CreatedAt
			}
		}

		currentChunk = append(currentChunk, msg)

		// 最后一条消息 → 收尾
		if i == len(messages)-1 {
			chunks = append(chunks, c.finalizeChunk(currentChunk, currentStart, len(chunks)))
		}
	}

	return chunks
}

func (c *SemanticChunker) finalizeChunk(messages []ConversationMessage, startTime time.Time, index int) SemanticChunk {
	tokenCount := 0
	endTime := startTime
	for _, m := range messages {
		tokenCount += m.TokenCount
		if m.CreatedAt.After(endTime) {
			endTime = m.CreatedAt
		}
	}
	return SemanticChunk{
		Messages:   messages,
		StartTime:  startTime,
		EndTime:    endTime,
		TokenCount: tokenCount,
		Index:      index,
	}
}

// DynamicWindowSelector 动态窗口选择器 — 在 token 预算内选择最优的消息组合
type DynamicWindowSelector struct {
	config DynamicWindowConfig
}

// NewDynamicWindowSelector 创建动态窗口选择器
func NewDynamicWindowSelector(config DynamicWindowConfig) *DynamicWindowSelector {
	return &DynamicWindowSelector{config: config}
}

// Select 在 token 预算内选择对话历史
//
// 算法：
//  1. 最近窗口（Recency Window）：始终保留最近的 N 条消息
//  2. 计算剩余 token 预算
//  3. 语义相关性窗口：从旧到新，按语义相似度排序，贪心填充剩余预算
//  4. 保持消息的时间顺序
func (s *DynamicWindowSelector) Select(chunks []SemanticChunk, queryVector []float32) []ConversationMessage {
	if len(chunks) == 0 {
		return nil
	}

	// 1. 计算每个 chunk 与当前查询的语义相关性
	for i := range chunks {
		chunks[i].Relevance = chunkRelevance(chunks[i], queryVector)
	}

	// 2. 分离最近窗口和语义窗口
	totalMessages := 0
	for _, ch := range chunks {
		totalMessages += len(ch.Messages)
	}

	recencyCount := s.config.RecencyWindow
	if recencyCount > totalMessages {
		recencyCount = totalMessages
	}

	// 收集最近 N 条消息所属的 chunk
	recencyChunks := make(map[int]bool)
	msgCount := 0
	for i := len(chunks) - 1; i >= 0; i-- {
		for range chunks[i].Messages {
			msgCount++
			if msgCount <= recencyCount {
				recencyChunks[i] = true
			}
		}
	}

	// 3. 计算最近窗口的 token 消耗
	recencyTokens := 0
	for i := range chunks {
		if recencyChunks[i] {
			recencyTokens += chunks[i].TokenCount
		}
	}

	// 4. 剩余预算用于语义相关 chunk
	remainingBudget := s.config.TokenBudget - recencyTokens
	if remainingBudget < 0 {
		// 预算不足，只保留最近窗口中能放下的消息
		return s.selectRecentOnly(chunks, recencyCount)
	}

	// 5. 按语义相关性排序旧 chunk（非最近窗口的）
	var semanticCandidates []SemanticChunk
	for i, ch := range chunks {
		if !recencyChunks[i] && ch.Relevance >= s.config.RelevanceThreshold {
			semanticCandidates = append(semanticCandidates, ch)
		}
	}

	sort.Slice(semanticCandidates, func(i, j int) bool {
		return semanticCandidates[i].Relevance > semanticCandidates[j].Relevance
	})

	// 6. 贪心填充
	selectedChunks := make(map[int]bool)
	for i := range chunks {
		if recencyChunks[i] {
			selectedChunks[i] = true
		}
	}

	for _, ch := range semanticCandidates {
		if ch.TokenCount <= remainingBudget {
			selectedChunks[ch.Index] = true
			remainingBudget -= ch.TokenCount
		}
	}

	// 7. 按时间顺序输出选中 chunk 的消息
	var result []ConversationMessage
	for _, ch := range chunks {
		if selectedChunks[ch.Index] {
			result = append(result, ch.Messages...)
		}
	}

	slog.Debug("short-term memory: dynamic window selected",
		"total_chunks", len(chunks),
		"selected_chunks", len(selectedChunks),
		"total_messages", totalMessages,
		"selected_messages", len(result),
		"recency_tokens", recencyTokens,
		"budget", s.config.TokenBudget,
	)

	return result
}

// selectRecentOnly 预算不足时，只保留最近能放下的消息
func (s *DynamicWindowSelector) selectRecentOnly(chunks []SemanticChunk, recencyCount int) []ConversationMessage {
	var result []ConversationMessage
	count := 0
	for i := len(chunks) - 1; i >= 0; i-- {
		for j := len(chunks[i].Messages) - 1; j >= 0; j-- {
			if count >= recencyCount {
				break
			}
			result = append([]ConversationMessage{chunks[i].Messages[j]}, result...)
			count++
		}
	}
	return result
}

// chunkRelevance 计算 chunk 与查询向量的语义相关性（取 chunk 内所有消息 embedding 的平均值）
func chunkRelevance(chunk SemanticChunk, queryVector []float32) float64 {
	if len(chunk.Messages) == 0 || len(queryVector) == 0 {
		return 0
	}

	totalSim := 0.0
	validCount := 0
	for _, msg := range chunk.Messages {
		if len(msg.Embedding) > 0 {
			totalSim += cosineSimilarityF32(msg.Embedding, queryVector)
			validCount++
		}
	}

	if validCount == 0 {
		return 0
	}
	return totalSim / float64(validCount)
}

// cosineSimilarityF32 计算两个 float32 向量的余弦相似度
func cosineSimilarityF32(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// EstimateTokens 估算中文/混合文本的 token 数。
// 经验值：1 个中文字 ≈ 1.5 tokens，1 个英文单词 ≈ 1.3 tokens，
// 1 个 rune（混合平均）≈ 0.6 tokens。
// 使用 rune 计数而非字节计数，避免对中文严重高估（3 字节/字 → 偏高 3 倍）。
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	runeCount := utf8.RuneCountInString(text)
	if runeCount == 0 {
		return 0
	}
	// rune 数 × 0.8（中英文混合的经验系数）
	// 纯中文：1 字 ≈ 1.5 tokens，rune = 字 → 0.8 略低但安全
	// 纯英文：1 词 ≈ 1.3 tokens，rune ≈ 5 字/词 → 0.8 × 5 = 4 略高
	// 混合：综合约 0.8 倍 rune 数接近真实 token 数
	return int(float64(runeCount) * 0.8)
}

// SafeTruncate 安全截断字符串，确保不切断 UTF-8 多字节字符。
// 返回的字符串长度 ≤ maxBytes，且是有效的 UTF-8。
func SafeTruncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// 逐步回退到最后一个完整 rune 边界
	end := maxBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// FormatHistoryForLLM 将选中的对话历史格式化为 LLM 消息列表
func FormatHistoryForLLM(messages []ConversationMessage) []struct {
	Role    string
	Content string
} {
	if len(messages) == 0 {
		return nil
	}

	result := make([]struct {
		Role    string
		Content string
	}, 0, len(messages))

	for _, msg := range messages {
		content := msg.Content
		// 助手的长文章只保留摘要部分，避免 token 爆炸
		if msg.Role == RoleAssistant && msg.ContentType == ContentArticle && len(content) > 500 {
			content = SafeTruncate(content, 500) + "\n...（历史文章已截断）"
		}
		result = append(result, struct {
			Role    string
			Content string
		}{
			Role:    string(msg.Role),
			Content: content,
		})
	}

	return result
}
