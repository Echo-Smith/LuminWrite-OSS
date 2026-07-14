package memory

import (
	"context"
)

// Store 是记忆存储的抽象接口 — 可替换为任何存储实现
type Store interface {
	// Save 保存或更新一条记忆
	Save(ctx context.Context, m *Memory) error

	// Get 按 ID 获取记忆
	Get(ctx context.Context, id string) (*Memory, error)

	// List 列出用户记忆（带过滤）
	List(ctx context.Context, userID string, opts ListOptions) ([]*Memory, error)

	// Search 语义检索用户活跃记忆
	Search(ctx context.Context, userID string, queryVector []float32, limit int) ([]*Memory, error)

	// FindByCategoryKey 按 category+key 查找（用于冲突检测）
	FindByCategoryKey(ctx context.Context, userID, category, key string) ([]*Memory, error)

	// UpdateStatus 更新记忆状态
	UpdateStatus(ctx context.Context, id string, status MemoryStatus) error

	// IncrementOccurrence 增加出现次数并更新 last_seen
	IncrementOccurrence(ctx context.Context, id string) error

	// Supersede 将旧记忆标记为 superseded
	Supersede(ctx context.Context, oldID, newID string) error

	// Delete 删除记忆
	Delete(ctx context.Context, id string) error

	// DismissForSession 记录会话级关闭
	DismissForSession(ctx context.Context, memoryID, sessionID string) error

	// GetDismissals 获取会话中已关闭的记忆 ID
	GetDismissals(ctx context.Context, sessionID string) ([]string, error)
}

// ListOptions 列表过滤选项
type ListOptions struct {
	Tier    *Tier
	Status  *MemoryStatus
	Limit   int
	Offset  int
}

// Embedder 是 Embedding 生成接口
type Embedder interface {
	// Embed 生成文本的向量表示
	Embed(ctx context.Context, text string) ([]float32, error)

	// Dimension 返回向量维度
	Dimension() int
}

// LLMExtractor 是 LLM 提取接口 — 从文章中提取结构化偏好
type LLMExtractor interface {
	// ExtractFromArticle 从完整文章中提取偏好
	ExtractFromArticle(ctx context.Context, article, styleSlug string) ([]ExtractedMemory, error)

	// ExtractFromFeedback 从反馈评论中提取改进信号
	ExtractFromFeedback(ctx context.Context, feedback []FeedbackInfo) ([]ExtractedMemory, error)
}

// EventEmitter 是事件推送接口 — 用于 WebSocket 通知前端
type EventEmitter interface {
	// EmitMemoryUsed 推送记忆使用事件
	EmitMemoryUsed(traceID string, ctx *MemoryContext)

	// EmitMemoryExtracted 推送记忆提取完成事件
	EmitMemoryExtracted(traceID string, count int)
}

// NoopEmitter 空实现
type NoopEmitter struct{}

func (NoopEmitter) EmitMemoryUsed(_ string, _ *MemoryContext)          {}
func (NoopEmitter) EmitMemoryExtracted(_ string, _ int)                 {}
