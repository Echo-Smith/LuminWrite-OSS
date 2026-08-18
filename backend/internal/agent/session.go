package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── WritingSession: 会话级状态 ────────────────────────────
//
// WritingSession 跨越单次 agent.start 请求，在同对话框内累积状态：
//   - 当前文章（支持多轮修改）
//   - 已有素材（避免重复搜索）
//   - 用户素材引用
//   - 最近评审结果
//
// 对话历史从 DB 加载（复用现有 conversation_messages 表），
// 这里只持有内存中的产出物。

// SessionStore 是 WritingSession 的持久化接口。
// 由 memory.Service 实现（复用短期记忆存储）。
type SessionStore interface {
	LoadHistory(ctx context.Context, conversationID string, limit int) ([]memory.ConversationMessage, error)
	StoreMessage(ctx context.Context, msg *memory.ConversationMessage) error
	IsEnabledForUser(userID string) bool
}

// MemoryRetriever 是可选的记忆检索接口。
// 由 memory.Service 实现，用于主动检索用户写作偏好和反馈记忆。
// Harness 在构建 system prompt 前调用此接口。
// 如果 SessionStore 不实现此接口，Harness 静默跳过记忆注入。
type MemoryRetriever interface {
	Retrieve(ctx context.Context, req memory.RetrieveRequest) (*memory.MemoryContext, error)
}

// WritingSession 持有同一对话内的累积状态。
type WritingSession struct {
	ConversationID string
	UserID         string
	StyleSlug      string

	// 跨轮保留的产出物
	CurrentArticle   string
	ArticleTitle     string
	ArticleVersions  []string          // 所有版本的文章（用于版本回溯）
	SearchResults    []engine.SearchResult
	ReviewResult     *engine.ReviewResult
	UserMaterials    []string
	Outline          *engine.OutlineData // Guided 模式下的用户确认提纲
	Reviewed         bool                // review_article 是否已执行

	// 对话历史（从 DB 加载）
	Messages []memory.ConversationMessage

	// 记忆上下文
	MemoryContext interface{}
}

// NewWritingSession 创建一个新的写作会话。
func NewWritingSession(conversationID, userID, styleSlug string) *WritingSession {
	return &WritingSession{
		ConversationID: conversationID,
		UserID:         userID,
		StyleSlug:      styleSlug,
	}
}

// LoadHistory 从 DB 加载对话历史。
// 如果记忆服务不可用或用户未启用，静默跳过。
func (s *WritingSession) LoadHistory(ctx context.Context, store SessionStore, limit int) {
	if store == nil || !store.IsEnabledForUser(s.UserID) {
		return
	}
	if s.ConversationID == "" {
		return
	}

	history, err := store.LoadHistory(ctx, s.ConversationID, limit)
	if err != nil {
		slog.Warn("writing session: load history failed",
			"error", err,
			"conversation_id", s.ConversationID,
		)
		return
	}

	s.Messages = history
	slog.Info("writing session: history loaded",
		"conversation_id", s.ConversationID,
		"messages", len(s.Messages),
	)
}

// StoreMessage 存储一条对话消息到 DB。
func (s *WritingSession) StoreMessage(ctx context.Context, store SessionStore, role, content, contentType string) {
	if store == nil || !store.IsEnabledForUser(s.UserID) {
		return
	}
	if s.ConversationID == "" {
		return
	}

	msg := &memory.ConversationMessage{
		ConversationID: s.ConversationID,
		UserID:         s.UserID,
		Role:           memory.ConversationRole(role),
		Content:        content,
		ContentType:    memory.ContentType(contentType),
		CreatedAt:      time.Now(),
	}

	if err := store.StoreMessage(ctx, msg); err != nil {
		slog.Warn("writing session: store message failed",
			"error", err,
			"conversation_id", s.ConversationID,
		)
	}
}

// RecentMessages 返回最近 N 条对话消息，格式化为 LLM 消息。
func (s *WritingSession) RecentMessages(n int) []memory.ConversationMessage {
	if len(s.Messages) <= n {
		return s.Messages
	}
	return s.Messages[len(s.Messages)-n:]
}

// HasArticle 返回 true 如果会话中已有文章。
func (s *WritingSession) HasArticle() bool {
	return s.CurrentArticle != ""
}

// PushArticleVersion 将当前文章保存为新版本。
// 在文章更新前调用，保留历史版本用于回溯。
func (s *WritingSession) PushArticleVersion(article string) {
	if article == "" {
		return
	}
	s.ArticleVersions = append(s.ArticleVersions, article)
	// 限制保留最近 5 个版本，防止内存溢出
	if len(s.ArticleVersions) > 5 {
		s.ArticleVersions = s.ArticleVersions[len(s.ArticleVersions)-5:]
	}
}
