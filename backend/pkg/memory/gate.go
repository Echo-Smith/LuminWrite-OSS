package memory

import (
	"context"
	"log/slog"
	"sort"
	"strings"
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

	// 2. 检索用户活跃记忆 —— 多信号召回（P2-2）：
	//    语义 Top-20 ∪ 关键词命中 ∪ 最近缺 embedding 的活跃记忆。
	//    修复两个召回缺陷：(a) 纯语义对"偏好类短文本"召回差；
	//    (b) 新记忆在 embedding 生成前是语义盲区（旧逻辑仅在语义
	//    结果为空时才 List 兜底，盲区场景兜底根本不触发）。
	var candidates []*Memory
	if queryVector != nil {
		candidates, err = g.store.Search(ctx, req.UserID, queryVector, 20)
		if err != nil {
			slog.Warn("memory: semantic search failed", "error", err)
		}
	}
	activeStatus := StatusActive
	recent, listErr := g.store.List(ctx, req.UserID, ListOptions{Status: &activeStatus, Limit: 50})
	if listErr != nil {
		slog.Warn("memory: list failed", "error", listErr)
		if len(candidates) == 0 {
			return &MemoryContext{}, nil
		}
	} else {
		candidates = unionRecall(candidates, recent, req.UserInput)
	}

	// 3. 获取本次会话已 dismiss 的记忆
	slog.Debug("memory: gate candidate pool",
		"candidates", len(candidates),
		"user_id", req.UserID,
		"intent", req.Intent)
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
			slog.Debug("memory: filtered low confidence",
				"memory_id", mem.ID,
				"tier", mem.Tier,
				"category", mem.Category,
				"effective_confidence", effectiveConf,
				"threshold", g.config.Thresholds.Inject)
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

	// 6. 最小披露限制 (P0-3)：按意图限制注入条数（WP6 默认 8）。
	//    injected 与 reviewGuard 同用 MaxInjectedPerIntent 上限，
	//    按置信度降序稳定排序后截断。
	injected = topByConfidence(injected, g.config.Safety.MaxInjectedPerIntent)
	reviewGuard = topByConfidence(reviewGuard, g.config.Safety.MaxInjectedPerIntent)

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

// topByConfidence 最小披露预算：按 Confidence 降序稳定排序后取前 n 条。
// n <= 0 表示不限制，原样返回。
func topByConfidence(entries []MemoryEntry, n int) []MemoryEntry {
	if n <= 0 {
		return entries
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Confidence > entries[j].Confidence
	})
	if len(entries) > n {
		return entries[:n]
	}
	return entries
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

// unionRecall 多信号召回合并（P2-2a）：
//   - 语义 Top-K 为基础；
//   - 并入关键词命中的活跃记忆（查询与记忆值的 rune-bigram 重叠——
//     中文无分词器场景下等价子串语义匹配）；
//   - 并入缺 embedding 的最近活跃记忆（修复盲区：写入后 embedding
//     尚未生成的窗口期内依然可被召回）。
//
// 去重按 ID，结果保持语义排序在前、补充召回在后，总量不超过池上限。
func unionRecall(semantic, recent []*Memory, query string) []*Memory {
	const poolCap = 50
	seen := make(map[string]bool, len(semantic)+len(recent))
	out := make([]*Memory, 0, len(semantic)+len(recent))
	for _, m := range semantic {
		if m == nil || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		out = append(out, m)
	}
	for _, m := range recent {
		if m == nil || seen[m.ID] {
			continue
		}
		// 缺 embedding：无条件并入（盲区修复）
		// 有 embedding：仅当关键词命中才并入（避免全量注入）
		if len(m.Embedding) == 0 || (query != "" && bigramOverlap(query, m.Value)) {
			seen[m.ID] = true
			out = append(out, m)
		}
		if len(out) >= poolCap {
			break
		}
	}
	return out
}

// bigramOverlap 判断 query 的任一相邻双字（rune bigram）是否出现在
// value 中。对中文短文本偏好（无空格分词）是低成本的子串语义近似。
func bigramOverlap(query, value string) bool {
	if query == "" || value == "" {
		return false
	}
	q := []rune(query)
	if len(q) < 2 {
		return strings.Contains(value, query)
	}
	for i := 0; i+2 <= len(q); i++ {
		if strings.Contains(value, string(q[i:i+2])) {
			return true
		}
	}
	return false
}
