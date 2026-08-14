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

// RetrieveAndGate 检索用户记忆 + 场景门控 + 证据边界过滤 + 生成 MemoryContext
func (g *Gate) RetrieveAndGate(ctx context.Context, req RetrieveRequest) (*MemoryContext, error) {
	// 0. 用户隔离验证 — 确保 user_id 不为空
	if req.UserID == "" {
		return &MemoryContext{RefusalReason: "missing_user_id"}, nil
	}

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

	// 4. 根据意图确定证据要求
	requireVerified := g.config.Safety.RequireVerifiedForChat
	if req.Intent == "writing" || req.Intent == "polish" {
		requireVerified = g.config.Safety.RequireVerifiedForWriting
	}

	// 5. 场景门控 + 证据边界过滤
	var injected []MemoryEntry
	var reviewGuard []MemoryEntry
	safetyFiltered := 0

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

		// ─── 证据边界过滤 (P0-1) ────────────────────────────
		// 安全过滤开启时，根据意图和证据状态决定是否注入
		if g.config.Safety.Enabled {
			status := mem.EvidenceStatus
			if status == "" {
				status = EvidenceNone // 未设置时默认为 none
			}

			// 硬偏好（Tier 1）跳过证据检查 — 用户手动设置的偏好始终可信
			if mem.Tier != TierHard {
				if !status.IsSafeForInjection(requireVerified) {
					safetyFiltered++
					slog.Debug("memory: filtered by evidence boundary",
						"memory_id", mem.ID,
						"category", mem.Category,
						"evidence_status", status,
						"require_verified", requireVerified)
					continue
				}
			}
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
			ID:             mem.ID,
			Tier:           mem.Tier,
			Category:       mem.Category,
			Value:          mem.Value,
			Confidence:     effectiveConf,
			Dismissible:    mem.Tier != TierHard, // 硬偏好不可 dismiss
			EvidenceStatus: mem.EvidenceStatus,
		}

		// Tier 3 反馈记忆 → 注入 PostReviewStep，不注入 WriteStep
		if mem.Tier == TierFeedback {
			reviewGuard = append(reviewGuard, entry)
		} else {
			// Tier 1 + Tier 2 → 注入 WriteStep
			injected = append(injected, entry)
		}
	}

	// 6. 最小披露限制 (P0-3)：按意图限制注入条数
	if g.config.Safety.MaxInjectedPerIntent > 0 && len(injected) > g.config.Safety.MaxInjectedPerIntent {
		// 按置信度排序，只保留 Top-N
		for i := 0; i < len(injected); i++ {
			for j := i + 1; j < len(injected); j++ {
				if injected[j].Confidence > injected[i].Confidence {
					injected[i], injected[j] = injected[j], injected[i]
				}
			}
		}
		injected = injected[:g.config.Safety.MaxInjectedPerIntent]
	}

	// 7. 拒答协议 (P0-3)：无足够证据记忆时触发拒答
	result := &MemoryContext{
		Injected:    injected,
		ReviewGuard: reviewGuard,
		Dismissed:   dismissedIDs,
	}

	if g.config.Safety.EnableRefusal && len(injected) == 0 && len(reviewGuard) == 0 {
		if safetyFiltered > 0 {
			result.RefusalReason = "insufficient_evidence"
		} else if len(candidates) == 0 {
			result.RefusalReason = "no_memories"
		} else {
			result.RefusalReason = "low_confidence"
		}
		slog.Info("memory: refusal triggered",
			"user_id", req.UserID,
			"intent", req.Intent,
			"reason", result.RefusalReason,
			"candidates", len(candidates),
			"safety_filtered", safetyFiltered)
	}

	slog.Info("memory: gate completed",
		"user_id", req.UserID,
		"intent", req.Intent,
		"candidates", len(candidates),
		"injected", len(injected),
		"review_guard", len(reviewGuard),
		"dismissed", len(dismissedIDs),
		"safety_filtered", safetyFiltered,
		"refusal_reason", result.RefusalReason,
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
