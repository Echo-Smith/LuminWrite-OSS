package memory

import (
	"context"
	"log/slog"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// Service 是 V2 项目的记忆服务封装
// 将 SDK 与 V2 的具体基础设施（DB、LLM、Embedding）连接
type Service struct {
	sdk             *memory.SDK
	cfg             memory.Config
	shortTermStore  *PgShortTermStore
	entityStore     *PgEntityStore
	embedder        *DashscopeEmbedder
	entityExtractor *LLMEntityExtractor
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

	shortTermStore := NewPgShortTermStore(db)
	entityStore := NewPgEntityStore(db)
	entityExtractor := NewLLMEntityExtractor(llm)

	slog.Info("memory: service initialized (with short-term + entity network)")
	return &Service{
		sdk:             sdk,
		cfg:             memory.DefaultConfig(),
		shortTermStore:  shortTermStore,
		entityStore:     entityStore,
		embedder:        embedder,
		entityExtractor: entityExtractor,
	}
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
// 同时触发模式记忆提取和实体记忆网络提取
func (s *Service) Extract(ctx context.Context, session memory.ExtractSession) {
	if !s.IsAvailable() {
		return
	}
	go func() {
		// 1. 原有模式记忆提取
		if err := s.sdk.Extract(ctx, session); err != nil {
			slog.Warn("memory: pattern extraction failed", "error", err, "trace_id", session.TraceID)
		}
		// 2. 实体记忆网络提取
		if s.entityExtractor != nil && s.entityStore != nil && len(session.Article) > 100 {
			s.extractEntities(ctx, session)
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

// ─── 短期记忆 ──────────────────────────────────────────────

// LoadHistory 加载会话对话历史
func (s *Service) LoadHistory(ctx context.Context, conversationID string, limit int) ([]memory.ConversationMessage, error) {
	if s.shortTermStore == nil {
		return nil, nil
	}
	return s.shortTermStore.LoadHistory(ctx, conversationID, limit)
}

// StoreMessage 存储对话消息
func (s *Service) StoreMessage(ctx context.Context, msg *memory.ConversationMessage) error {
	if s.shortTermStore == nil {
		return nil
	}
	return s.shortTermStore.StoreMessage(ctx, msg)
}

// UpdateMessageEmbedding 更新消息的 embedding
func (s *Service) UpdateMessageEmbedding(ctx context.Context, messageID string, embedding []float32) error {
	if s.shortTermStore == nil {
		return nil
	}
	return s.shortTermStore.UpdateEmbedding(ctx, messageID, embedding)
}

// ─── 工作记忆持久化 ──────────────────────────────────────────

// SaveWorkingSummary 持久化工作记忆摘要
func (s *Service) SaveWorkingSummary(ctx context.Context, ws *memory.WorkingSummary) error {
	if s.shortTermStore == nil {
		return nil
	}
	return s.shortTermStore.SaveWorkingSummary(ctx, ws)
}

// LoadWorkingSummary 加载会话最近的工作记忆摘要
func (s *Service) LoadWorkingSummary(ctx context.Context, conversationID string) (*memory.WorkingSummary, error) {
	if s.shortTermStore == nil {
		return nil, nil
	}
	return s.shortTermStore.LoadWorkingSummary(ctx, conversationID)
}

// ─── 实体记忆网络 ─────────────────────────────────────────

// RetrieveEntityGraph 检索实体记忆网络
func (s *Service) RetrieveEntityGraph(ctx context.Context, req memory.EntityGraphQuery) (*memory.EntityGraphResult, error) {
	if s.entityStore == nil {
		return &memory.EntityGraphResult{}, nil
	}
	retriever := memory.NewEntityGraphRetriever(s.entityStore)
	return retriever.Retrieve(ctx, req)
}

// StoreEntity 存储实体。如果实体没有 embedding，自动从名称+描述生成。
func (s *Service) StoreEntity(ctx context.Context, entity *memory.Entity) error {
	if s.entityStore == nil {
		return nil
	}
	// Fix 5: 自动生成 embedding（如果缺失）
	if len(entity.Embedding) == 0 && s.embedder != nil {
		embedText := entity.Name
		if entity.Description != "" {
			embedText = entity.Name + " " + entity.Description
		}
		vec, err := s.embedder.Embed(ctx, embedText)
		if err != nil {
			slog.Warn("memory: failed to generate entity embedding", "error", err, "entity", entity.Name)
		} else {
			entity.Embedding = vec
		}
	}
	return s.entityStore.StoreEntity(ctx, entity)
}

// StoreRelation 存储关系
func (s *Service) StoreRelation(ctx context.Context, relation *memory.Relation) error {
	if s.entityStore == nil {
		return nil
	}
	return s.entityStore.StoreRelation(ctx, relation)
}

// extractEntities 从文章中提取实体和关系，生成 embedding 并存储到实体记忆网络
func (s *Service) extractEntities(ctx context.Context, session memory.ExtractSession) {
	entities, err := s.entityExtractor.ExtractFromArticle(ctx, session.Article, session.StyleSlug)
	if err != nil {
		slog.Warn("memory: entity extraction failed", "error", err, "trace_id", session.TraceID)
		return
	}
	if len(entities) == 0 {
		return
	}

	// 存储实体并建立名称→ID 映射
	entityIDs := make(map[string]string)
	storedCount := 0
	for _, ext := range entities {
		// 查找已有实体（同 user+type+name）
		existing, err := s.entityStore.FindEntity(ctx, session.UserID, ext.EntityType, ext.Name)
		var entity *memory.Entity
		if err == nil && existing != nil {
			// 更新已有实体：增加出现次数
			entity = existing
			entity.OccurrenceCount++
			entity.Confidence = minf(entity.Confidence+0.05, 1.0)
			entity.LastSeen = time.Now()
			if ext.Description != "" && entity.Description == "" {
				entity.Description = ext.Description
			}
		} else {
			// 创建新实体
			entity = &memory.Entity{
				UserID:          session.UserID,
				EntityType:      ext.EntityType,
				Name:            ext.Name,
				Description:     ext.Description,
				Confidence:      0.5,
				OccurrenceCount: 1,
				SourceTraceID:   session.TraceID,
				Status:          "active",
				FirstSeen:       time.Now(),
				LastSeen:        time.Now(),
			}
		}

		if err := s.StoreEntity(ctx, entity); err != nil {
			slog.Warn("memory: failed to store entity", "error", err, "name", ext.Name)
			continue
		}
		entityIDs[ext.Name] = entity.ID
		storedCount++
	}

	// 存储关系
	relationCount := 0
	for _, ext := range entities {
		sourceID, ok := entityIDs[ext.Name]
		if !ok {
			continue
		}
		for _, rel := range ext.Relations {
			targetID, ok := entityIDs[rel.TargetName]
			if !ok {
				continue
			}
			relation := &memory.Relation{
				UserID:         session.UserID,
				SourceEntityID: sourceID,
				TargetEntityID: targetID,
				RelationType:   rel.RelationType,
				Weight:         rel.Weight,
				EvidenceCount:  1,
				SourceTraceID:  session.TraceID,
				FirstSeen:      time.Now(),
				LastSeen:       time.Now(),
			}
			if err := s.StoreRelation(ctx, relation); err != nil {
				slog.Warn("memory: failed to store relation", "error", err)
				continue
			}
			relationCount++
		}
	}

	slog.Info("memory: entity extraction completed",
		"trace_id", session.TraceID,
		"user_id", session.UserID,
		"entities_stored", storedCount,
		"relations_stored", relationCount,
	)
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// Embed 生成文本的 embedding（供 engine steps 使用）
func (s *Service) Embed(ctx context.Context, text string) ([]float32, error) {
	if s.embedder == nil {
		return nil, nil
	}
	return s.embedder.Embed(ctx, text)
}
