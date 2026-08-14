package memory

import (
	"context"
	"log/slog"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// SensitiveCheckAdapter 将 services.SensitiveCheckService 适配为 memory.ContentChecker
type SensitiveCheckAdapter struct {
	service *services.SensitiveCheckService
}

func (a *SensitiveCheckAdapter) Check(ctx context.Context, text string) *memory.ContentCheckResult {
	if a.service == nil {
		return &memory.ContentCheckResult{Passed: true, Summary: "no checker configured"}
	}
	result := a.service.Check(ctx, text)
	
	// 转换为 memory.ContentCheckResult 格式
	hits := make([]memory.ContentHit, len(result.Hits))
	for i := range result.Hits {
		hits[i] = memory.ContentHit{
			Word:        result.Hits[i].Word,
			Category:    result.Hits[i].Category,
			Severity:    result.Hits[i].Severity,
			Action:      result.Hits[i].Action,
			Count:       result.Hits[i].Count,
			Replacement: result.Hits[i].Replacement,
		}
	}
	
	return &memory.ContentCheckResult{
		Passed:  result.Passed,
		Hits:    hits,
		Summary: result.Summary,
		Cleaned: result.Cleaned,
	}
}

// Service 是 V2项目的记忆服务封装
// 将 SDK 与 V2 的具体基础设施（DB、LLM、Embedding）连接
type Service struct {
	sdk             *memory.SDK
	cfg             memory.Config
	pgStore         *PgStore
	shortTermStore  *PgShortTermStore
	entityStore     *PgEntityStore
	embedder        *DashscopeEmbedder
	entityExtractor *LLMEntityExtractor
	fileStore       *memory.FileStore  // 文件记忆层
	fileSyncer      *memory.FileMemorySyncer
}

// NewService 创建记忆服务
func NewService(db *database.DB, llm *tools.LLMClient, embedding *tools.EmbeddingClient, sensitiveCheck *services.SensitiveCheckService) *Service {
	if db == nil {
		slog.Warn("memory: database not available, memory service disabled")
		return &Service{sdk: nil}
	}

	store := NewPgStore(db)
	embedder := NewDashscopeEmbedder(embedding)
	extractor := NewDeepSeekExtractor(llm)
	
	// P0-2: 注入 PII 检查器
	var contentChecker memory.ContentChecker
	if sensitiveCheck != nil {
		contentChecker = &SensitiveCheckAdapter{service: sensitiveCheck}
	}

	sdk := memory.NewSDK(
		memory.DefaultConfig(),
		store,
		embedder,
		extractor,
		memory.NoopEmitter{}, // emitter 由 Server 注入
	)
	
	// P0-2: 将 checker 注入 SDK（需要 SDK 添加 setter 或通过直接修改 sdk 字段）
	// 由于 SDK 结构字段是私有的，我们通过封装的方式注入：
	// 这里暂时直接赋值（实际应该通过构造函数参数或 setter 方法）
	if checker := contentChecker; checker != nil {
		sdk.SetContentChecker(contentChecker)
	}

	shortTermStore := NewPgShortTermStore(db)
	entityStore := NewPgEntityStore(db)
	entityExtractor := NewLLMEntityExtractor(llm)

	// Initialize file-based memory layer
	fileStore := memory.NewFileStore("data/memory")
	fileSyncer := memory.NewFileMemorySyncer(fileStore, store)

	slog.Info("memory: service initialized (with short-term + entity network + file layer + PII checker)")
	return &Service{
		sdk:             sdk,
		cfg:             memory.DefaultConfig(),
		pgStore:         store,
		shortTermStore:  shortTermStore,
		entityStore:     entityStore,
		embedder:        embedder,
		entityExtractor: entityExtractor,
		fileStore:       fileStore,
		fileSyncer:      fileSyncer,
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

// ─── 文件记忆层 ─────────────────────────────────────────────

// ExportMemoryFile exports a user's DB memories to a Markdown file.
func (s *Service) ExportMemoryFile(ctx context.Context, userID string) error {
	if !s.IsAvailable() {
		return ErrServiceUnavailable
	}
	return s.fileSyncer.SyncFromDB(ctx, userID)
}

// ImportMemoryFile imports a Markdown memory file and syncs entries to DB.
func (s *Service) ImportMemoryFile(ctx context.Context, userID, content string) (int, error) {
	if !s.IsAvailable() {
		return 0, ErrServiceUnavailable
	}
	_, err := s.fileStore.ImportUserMemory(userID, content)
	if err != nil {
		return 0, err
	}
	return s.fileSyncer.SyncToDB(ctx, userID)
}

// GetMemoryFileMarkdown returns the raw Markdown content of a user's memory file.
func (s *Service) GetMemoryFileMarkdown(userID string) (string, error) {
	return s.fileStore.GetUserMemoryMarkdown(userID)
}

// LoadFileMemories loads file-based memories as prompt-injectable entries.
func (s *Service) LoadFileMemories(ctx context.Context, userID string) ([]memory.MemoryEntry, error) {
	if s.fileStore == nil {
		return nil, nil
	}
	return s.fileStore.GetUserMemoriesAsEntries(ctx, userID)
}

// GetGlobalMemoryFile returns the global memory file content.
func (s *Service) GetGlobalMemoryFile(ctx context.Context) (string, []memory.MemoryFileEntry, error) {
	if s.fileStore == nil {
		return "", nil, nil
	}
	md, err := s.fileStore.GetGlobalMemoryMarkdown()
	if err != nil {
		return "", nil, err
	}
	entries, err := s.fileStore.GetGlobalMemory(ctx)
	if err != nil {
		return md, nil, err
	}
	return md, entries, nil
}

// SaveGlobalMemoryFile saves the global memory file.
func (s *Service) SaveGlobalMemoryFile(content string) error {
	if s.fileStore == nil {
		return ErrServiceUnavailable
	}
	return s.fileStore.SaveGlobalMemory(content)
}

// StartFileWatch starts the file watcher for hot-reloading memory files.
func (s *Service) StartFileWatch(ctx context.Context) {
	if s.fileStore != nil {
		s.fileStore.Watch(ctx, 1*time.Minute)
	}
}

// ─── Memory Forgetting (TTL + Salience Decay) ───────────────

// ApplyForgettingPolicy applies TTL and salience-based decay to user memories
func (s *Service) ApplyForgettingPolicy(ctx context.Context) error {
	if s.pgStore == nil {
		return nil
	}

	// Apply TTL: archive memories that have expired
	if _, err := s.pgStore.db.ExecContext(ctx, `
		UPDATE user_memories
		SET status = 'archived', updated_at = NOW()
		WHERE status IN ('active', 'candidate')
		  AND expires_at IS NOT NULL
		  AND expires_at < NOW()
	`); err != nil {
		slog.Warn("failed to archive expired memories", "error", err)
	}

	// Apply salience decay: reduce memory_weight for low-salience memories
	// Salience decay rate: 0.01 per day (1% daily reduction, compounded)
	if _, err := s.pgStore.db.ExecContext(ctx, `
		UPDATE user_memories
		SET memory_weight = GREATEST(0.1, memory_weight * 0.99),
		    updated_at = NOW()
		WHERE status = 'active'
		  AND last_seen < NOW() - INTERVAL '1 day'
		  AND salience_score < 0.3
	`); err != nil {
		slog.Warn("failed to apply salience decay", "error", err)
	}

	// Auto-archive very low weight memories
	if _, err := s.pgStore.db.ExecContext(ctx, `
		UPDATE user_memories
		SET status = 'archived', updated_at = NOW()
		WHERE status = 'active'
		  AND memory_weight < 0.05
	`); err != nil {
		slog.Warn("failed to archive low-weight memories", "error", err)
	}

	slog.Info("memory forgetting policy applied")
	return nil
}

// StartForgettingScheduler runs the forgetting policy periodically
func (s *Service) StartForgettingScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 24 * time.Hour // Default: daily
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("memory forgetting scheduler started", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.ApplyForgettingPolicy(ctx); err != nil {
				slog.Warn("forgetting policy execution failed", "error", err)
			}
		}
	}
}
