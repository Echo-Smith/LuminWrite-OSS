package memory

import (
	"context"
	"log/slog"
)

// Gate 场景门控 — 决定哪些记忆应该被注入
type Gate struct {
	store     Store
	embedder  Embedder
	config    Config
}

// NewGate 创建场景门控
func NewGate(store Store, embedder Embedder, config Config) *Gate {
	return &Gate{store: store, embedder: embedder, config: config}
}

// RetrieveAndGate 检索用户记忆 + 场景门控 + 生成 MemoryContext
func (g *Gate) RetrieveAndGate(ctx context.Context, req RetrieveRequest) (*MemoryContext, error) {
	// 1. 生成用户输入的 embedding 用于语义检索
	queryVector, err := g.embedder.Embed(ctx, req.UserInput)
	if err != nil {
		slog.Warn("memory: embed failed, falling back to no semantic search", "error", err)
	}

	// 2. 检索用户活跃记忆
	var candidates []*Memory
	if queryVector != nil {
		candidates, err = g.store.Search(ctx, req.UserID, queryVector, 20)
		if err != nil {
			slog.Warn("memory: semantic search failed", "error", err)
		}
	}
	// Fallback: list all active memories
	if len(candidates) == 0 {
		activeStatus := StatusActive
		candidates, err = g.store.List(ctx, req.UserID, ListOptions{Status: &activeStatus, Limit: 50})
		if err != nil {
			slog.Warn("memory: list failed", "error", err)
			return &MemoryContext{}, nil
		}
	}

	// 3. 获取本次会话已 dismiss 的记忆
	dismissedIDs, _ := g.store.GetDismissals(ctx, req.SessionID)
	dismissedSet := make(map[string]bool, len(dismissedIDs))
	for _, id := range dismissedIDs {
		dismissedSet[id] = true
	}

	// 4. 场景门控：过滤记忆
	var injected []MemoryEntry
	var reviewGuard []MemoryEntry

	for _, mem := range candidates {
		// 跳过非活跃/非候选记忆
		if mem.Status != StatusActive && mem.Status != StatusCandidate {
			continue
		}

		// 跳过本次会话已 dismiss 的记忆
		if dismissedSet[mem.ID] {
			continue
		}

		// 候选记忆（第一次提取）不注入
		if mem.Status == StatusCandidate {
			continue
		}

		// 计算有效置信度（含衰减）
		var halfLife int
		if mem.Tier == TierPattern {
			halfLife = g.config.HalfLife.Pattern
		} else if mem.Tier == TierFeedback {
			halfLife = g.config.HalfLife.Feedback
		}
		effectiveConf := mem.EffectiveConfidence(halfLife)

		// 低于注入阈值，跳过
		if effectiveConf < g.config.Thresholds.Inject {
			continue
		}

		// 场景门控：如果用户已显式指定该维度，跳过
		if isExplicitlySpecified(req.Explicit, mem.Category, mem.Key) {
			slog.Debug("memory: skipped by gate (explicit override)",
				"category", mem.Category, "key", mem.Key)
			continue
		}

		entry := MemoryEntry{
			ID:          mem.ID,
			Tier:        mem.Tier,
			Category:    mem.Category,
			Value:       mem.Value,
			Confidence:  effectiveConf,
			Dismissible: mem.Tier != TierHard, // 硬偏好不可 dismiss
		}

		// Tier 3 反馈记忆 → 注入 PostReviewStep，不注入 WriteStep
		if mem.Tier == TierFeedback {
			reviewGuard = append(reviewGuard, entry)
		} else {
			// Tier 1 + Tier 2 → 注入 WriteStep
			injected = append(injected, entry)
		}
	}

	result := &MemoryContext{
		Injected:    injected,
		ReviewGuard: reviewGuard,
		Dismissed:   dismissedIDs,
	}

	slog.Info("memory: gate completed",
		"user_id", req.UserID,
		"candidates", len(candidates),
		"injected", len(injected),
		"review_guard", len(reviewGuard),
		"dismissed", len(dismissedIDs),
	)

	return result, nil
}

// isExplicitlySpecified 检查用户是否已显式指定了某个维度
func isExplicitlySpecified(explicit map[string]any, category, key string) bool {
	if len(explicit) == 0 {
		return false
	}

	// 映射 category → explicit map 的 key
	categoryMapping := map[string][]string{
		"word_count": {"word_count", "word_limit"},
		"style":      {"style", "style_slug"},
		"structure":  {"structure", "outline"},
		"mode":       {"mode"},
		"topic":      {"topic", "message"},
	}

	keys, ok := categoryMapping[category]
	if !ok {
		return false
	}

	for _, k := range keys {
		if v, exists := explicit[k]; exists && v != nil && v != "" {
			return true
		}
	}

	return false
}
