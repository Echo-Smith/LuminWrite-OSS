package memory

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// SDK 是记忆系统的统一入口
type SDK struct {
	store         Store
	embedder      Embedder
	extractor     LLMExtractor
	gate          *Gate
	resolver      *ConflictResolver
	qualityCalc   *QualityCalculator
	config        Config
	emitter       EventEmitter
	contentChecker ContentChecker // P0: 内容检查器（PII 检测）
}

// NewSDK 创建记忆 SDK 实例
func NewSDK(cfg Config, store Store, embedder Embedder, extractor LLMExtractor, emitter EventEmitter) *SDK {
	if emitter == nil {
		emitter = NoopEmitter{}
	}
	return &SDK{
		store:         store,
		embedder:      embedder,
		extractor:     extractor,
		gate:          NewGate(store, embedder, cfg),
		resolver:      NewConflictResolver(store),
		qualityCalc:   NewQualityCalculator(),
		config:        cfg,
		emitter:       emitter,
		contentChecker: NoopContentChecker{}, // 默认空实现，由外部注入
	}
}

// ─── 写作流程集成 ──────────────────────────────────────────

// Retrieve 在写作前调用：检索用户记忆 + 场景门控
func (s *SDK) Retrieve(ctx context.Context, req RetrieveRequest) (*MemoryContext, error) {
	return s.gate.RetrieveAndGate(ctx, req)
}

// Extract 在写作完成后异步调用：从 trace + 文章 + 反馈中提取记忆
func (s *SDK) Extract(ctx context.Context, session ExtractSession) error {
	// 1. 计算质量评级
	grade := s.qualityCalc.CalculateGrade(session.Signals, session.Feedback)

	// 2. 确定性提取（从 ExecutionContext 字段）
	deterministic := extractDeterministic(&session)

	// 3. LLM 提取（仅当非差评时）
	var llmExtracted []ExtractedMemory
	if grade != GradeNegative && len(session.Article) > 100 {
		var err error
		llmExtracted, err = s.extractor.ExtractFromArticle(ctx, session.Article, session.StyleSlug)
		if err != nil {
			slog.Warn("memory: LLM extraction failed", "error", err)
		}
	}

	// 4. 反馈提取（差评时也提取，生成 Tier 3）
	var feedbackExtracted []ExtractedMemory
	if len(session.Feedback) > 0 {
		var err error
		feedbackExtracted, err = s.extractor.ExtractFromFeedback(ctx, session.Feedback)
		if err != nil {
			slog.Warn("memory: feedback extraction failed", "error", err)
		}
	}

	// 5. 保存 Tier 2 模式记忆（确定性 + LLM）
	totalSaved := 0
	if grade != GradeNegative {
		// P0-2: PII 检测 — 在保存前检查敏感内容
		for _, ext := range deterministic {
			checkedValue, skipped := s.checkAndCleanMemoryValue(ctx, ext.Value, ext.Category)
			if skipped {
				slog.Warn("memory: skipped sensitive content", 
					"category", ext.Category, "key", ext.Key, "trace_id", session.TraceID)
				continue
			}
			ext.Value = checkedValue
			mem, err := s.resolver.ResolveAndSave(ctx, session.UserID, ext, TierPattern, session.TraceID, grade)
			if err != nil {
				slog.Warn("memory: failed to save pattern", "category", ext.Category, "error", err)
				continue
			}
			if mem != nil {
				totalSaved++
			}
		}
		for _, ext := range llmExtracted {
			checkedValue, skipped := s.checkAndCleanMemoryValue(ctx, ext.Value, ext.Category)
			if skipped {
				slog.Warn("memory: skipped sensitive LLM extraction", 
					"category", ext.Category, "key", ext.Key, "trace_id", session.TraceID)
				continue
			}
			ext.Value = checkedValue
			mem, err := s.resolver.ResolveAndSave(ctx, session.UserID, ext, TierPattern, session.TraceID, grade)
			if err != nil {
				slog.Warn("memory: failed to save LLM pattern", "category", ext.Category, "error", err)
				continue
			}
			if mem != nil {
				totalSaved++
			}
		}
	}

	// 6. 保存 Tier 3 反馈记忆
	for _, ext := range feedbackExtracted {
		mem, err := s.resolver.ResolveAndSave(ctx, session.UserID, ext, TierFeedback, session.TraceID, grade)
		if err != nil {
			slog.Warn("memory: failed to save feedback memory", "category", ext.Category, "error", err)
			continue
		}
		if mem != nil {
			totalSaved++
		}
	}

	// 7. 为新记忆生成 embedding（异步）
	// 这里简单同步处理，实际可以改为队列
	if s.embedder != nil {
		go s.generateEmbeddings(context.Background(), session.UserID)
	}

	s.emitter.EmitMemoryExtracted(session.TraceID, totalSaved)

	slog.Info("memory: extraction completed",
		"user_id", session.UserID,
		"trace_id", session.TraceID,
		"grade", grade,
		"deterministic", len(deterministic),
		"llm", len(llmExtracted),
		"feedback", len(feedbackExtracted),
		"saved", totalSaved,
	)

	return nil
}

// ─── 记忆管理 ──────────────────────────────────────────────

// SetContentChecker 设置内容检查器（用于 PII 检测）
// 通常在创建 Service 后由外部注入
func (s *SDK) SetContentChecker(checker ContentChecker) {
	s.contentChecker = checker
}

// List 列出用户记忆
func (s *SDK) List(ctx context.Context, userID string, opts ListOptions) ([]*Memory, error) {
	return s.store.List(ctx, userID, opts)
}

// Get 获取单条记忆
func (s *SDK) Get(ctx context.Context, memoryID string) (*Memory, error) {
	return s.store.Get(ctx, memoryID)
}

// Create 创建 Tier 1 硬偏好
func (s *SDK) Create(ctx context.Context, userID, category, key, value string) (*Memory, error) {
	mem := &Memory{
		UserID:     userID,
		Tier:       TierHard,
		Category:   category,
		Key:        key,
		Value:      value,
		Confidence: 1.0,
		Status:     StatusActive,
		FirstSeen:  time.Now(),
		LastSeen:   time.Now(),
	}

	// 检查冲突
	existing, _ := s.store.FindByCategoryKey(ctx, userID, category, key)
	for _, m := range existing {
		if m.Status == StatusActive || m.Status == StatusCandidate {
			if m.Value != value {
				_ = s.store.Supersede(ctx, m.ID, "")
			}
		}
	}

	if err := s.store.Save(ctx, mem); err != nil {
		return nil, fmt.Errorf("failed to create memory: %w", err)
	}

	// 生成 embedding
	if s.embedder != nil {
		go func() {
			vec, err := s.embedder.Embed(context.Background(), value)
			if err != nil {
				slog.Warn("memory: embed failed for new memory", "error", err)
				return
			}
			mem.Embedding = vec
			_ = s.store.Save(ctx, mem)
		}()
	}

	return mem, nil
}

// Update 更新记忆
func (s *SDK) Update(ctx context.Context, memoryID, value string) (*Memory, error) {
	mem, err := s.store.Get(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	mem.Value = value
	mem.UpdatedAt = time.Now()
	if err := s.store.Save(ctx, mem); err != nil {
		return nil, err
	}
	return mem, nil
}

// Delete 删除记忆
func (s *SDK) Delete(ctx context.Context, memoryID string) error {
	return s.store.Delete(ctx, memoryID)
}

// Dismiss 本次会话不注入某条记忆
func (s *SDK) Dismiss(ctx context.Context, memoryID, sessionID string) error {
	return s.store.DismissForSession(ctx, memoryID, sessionID)
}

// ─── 内部方法 ──────────────────────────────────────────────

// extractDeterministic 从 ExecutionContext 确定性提取行为模式
func extractDeterministic(session *ExtractSession) []ExtractedMemory {
	var memories []ExtractedMemory

	if session.WordLimit > 0 {
		memories = append(memories, ExtractedMemory{
			Category: "word_count",
			Key:      "requested_word_limit",
			Value:    fmt.Sprintf("%d", session.WordLimit),
			Source:   "deterministic",
		})
	}

	if session.StyleSlug != "" {
		memories = append(memories, ExtractedMemory{
			Category: "style",
			Key:      "selected_style",
			Value:    session.StyleSlug,
			Source:   "deterministic",
		})
	}

	if session.Mode != "" {
		memories = append(memories, ExtractedMemory{
			Category: "mode",
			Key:      "writing_mode",
			Value:    session.Mode,
			Source:   "deterministic",
		})
	}

	return memories
}

// generateEmbeddings 为缺少 embedding 的记忆生成向量
func (s *SDK) generateEmbeddings(ctx context.Context, userID string) {
	activeStatus := StatusActive
	candidateStatus := StatusCandidate

	for _, status := range []MemoryStatus{activeStatus, candidateStatus} {
		memories, err := s.store.List(ctx, userID, ListOptions{Status: &status, Limit: 50})
		if err != nil {
			continue
		}
		for _, mem := range memories {
			if mem.Embedding == nil || len(mem.Embedding) == 0 {
				vec, err := s.embedder.Embed(ctx, mem.Value)
				if err != nil {
					slog.Debug("memory: embed failed", "memory_id", mem.ID, "error", err)
					continue
				}
				mem.Embedding = vec
				_ = s.store.Save(ctx, mem)
			}
		}
	}
}

// ─── P0-2: 安全与内容过滤辅助方法 ─────────────────────────────

// checkAndCleanMemoryValue 检查记忆值是否包含敏感内容
// 返回 (清理后的值, 是否跳过保存)
func (s *SDK) checkAndCleanMemoryValue(ctx context.Context, value, category string) (string, bool) {
	if s.contentChecker == nil || !s.config.Safety.PIIFilterEnabled {
		return value, false
	}

	result := s.contentChecker.Check(ctx, value)
	if result == nil {
		return value, false
	}

	// 如果被 block（含 blocking 级敏感词），跳过保存
	if !result.Passed {
		slog.Debug("memory: content check blocked memory", "category", category, "summary", result.Summary)
		return "", true
	}

	// 如果有 warn 级敏感词，使用清理后的值
	if len(result.Hits) > 0 && result.Cleaned != "" {
		slog.Debug("memory: content cleaned", "category", category, "summary", result.Summary)
		return result.Cleaned, false
	}

	return value, false
}
