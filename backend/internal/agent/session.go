package agent

// ─── WritingSession: 单次执行核运行的状态容器 ──────────────
//
// WritingSession 只在一次 RunCore 内存活（⑥D 后）：
//   - 当前文章（支持修订）
//   - 已有素材（避免重复搜索）
//   - 最近评审结果
//   - 记忆上下文（工具循环内的检索结果）
//
// 对话历史加载/持久化（SessionStore）随 Legacy 交互路径移除：
// 执行核的 session 是调用方创建的隔离容器，无 DB 通道。

import (
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// WritingSession 持有单次执行核运行的累积状态。
type WritingSession struct {
	ConversationID string
	UserID         string
	StyleSlug      string

	// 本次运行内保留的产出物
	CurrentArticle  string
	ArticleTitle    string
	ArticleVersions []string // 本次运行内的文章版本（修订回溯）
	SearchResults   []engine.SearchResult
	ReviewResult    *engine.ReviewResult
	UserMaterials   []string
	Outline         *engine.OutlineData // Guided 模式下的用户确认提纲
	Reviewed        bool                // review_article 是否已执行

	// 记忆上下文（工具检索结果，仅内存）
	MemoryContext interface{}

	// 调用方预置的对话历史（仅内存；执行核不加载也不持久化）
	Messages []memory.ConversationMessage
}

// NewWritingSession 创建一个新的执行状态容器。
func NewWritingSession(conversationID, userID, styleSlug string) *WritingSession {
	return &WritingSession{
		ConversationID: conversationID,
		UserID:         userID,
		StyleSlug:      styleSlug,
	}
}

// RecentMessages 返回最近 N 条对话消息（本次运行内由调用方预置的历史）。
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
