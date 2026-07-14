package memory

import (
	"context"
	"log/slog"

	"github.com/luminbuddy/writing-agent-v2/internal/database"
	"github.com/luminbuddy/writing-agent-v2/internal/tools"
	"github.com/luminbuddy/writing-agent-v2/pkg/memory"
)

// Service 是 V2 项目的记忆服务封装
// 将 SDK 与 V2 的具体基础设施（DB、LLM、Embedding）连接
type Service struct {
	sdk *memory.SDK
	cfg memory.Config
}

// NewService 创建记忆服务
func NewService(db *database.DB, llm *tools.LLMClient, embedding *tools.EmbeddingClient) *Service {
	if db == nil {
		slog.Warn("memory: database not available, memory service disabled")
		return &Service{sdk: nil}
	}

	store := NewPgStore(db)
	embedder := NewDashscopeEmbedder(embedding)
	extractor := NewDeepSeekExtractor(llm)

	sdk := memory.NewSDK(
		memory.DefaultConfig(),
		store,
		embedder,
		extractor,
		memory.NoopEmitter{}, // emitter 由 Server 注入
	)

	slog.Info("memory: service initialized")
	return &Service{sdk: sdk, cfg: memory.DefaultConfig()}
}

// IsAvailable 检查记忆服务是否可用
func (s *Service) IsAvailable() bool {
	return s.sdk != nil
}

// IsEnabledForUser 根据灰度配置判断某用户是否启用记忆
func (s *Service) IsEnabledForUser(userID string) bool {
	if !s.IsAvailable() {
		return false
	}
	return s.cfg.IsEnabledForUser(userID)
}

// SetConfig 更新运行时配置（如灰度开关）
func (s *Service) SetConfig(cfg memory.Config) {
	s.cfg = cfg
}

// Retrieve 写作前检索记忆
func (s *Service) Retrieve(ctx context.Context, req memory.RetrieveRequest) (*memory.MemoryContext, error) {
	if !s.IsAvailable() {
		return &memory.MemoryContext{}, nil
	}
	return s.sdk.Retrieve(ctx, req)
}

// Extract 写作完成后提取记忆（异步）
func (s *Service) Extract(ctx context.Context, session memory.ExtractSession) {
	if !s.IsAvailable() {
		return
	}
	go func() {
		if err := s.sdk.Extract(ctx, session); err != nil {
			slog.Warn("memory: extraction failed", "error", err, "trace_id", session.TraceID)
		}
	}()
}

// List 列出用户记忆
func (s *Service) List(ctx context.Context, userID string, opts memory.ListOptions) ([]*memory.Memory, error) {
	if !s.IsAvailable() {
		return []*memory.Memory{}, nil
	}
	return s.sdk.List(ctx, userID, opts)
}

// Create 创建硬偏好
func (s *Service) Create(ctx context.Context, userID, category, key, value string) (*memory.Memory, error) {
	if !s.IsAvailable() {
		return nil, ErrServiceUnavailable
	}
	return s.sdk.Create(ctx, userID, category, key, value)
}

// Delete 删除记忆
func (s *Service) Delete(ctx context.Context, memoryID string) error {
	if !s.IsAvailable() {
		return ErrServiceUnavailable
	}
	return s.sdk.Delete(ctx, memoryID)
}

// Dismiss 关闭记忆
func (s *Service) Dismiss(ctx context.Context, memoryID, sessionID string) error {
	if !s.IsAvailable() {
		return ErrServiceUnavailable
	}
	return s.sdk.Dismiss(ctx, memoryID, sessionID)
}
