package memory

import (
	"context"
	"log/slog"
	"time"
)

// ConflictResolver 冲突解决器 — 处理同维度新旧记忆的冲突
type ConflictResolver struct {
	store Store
}

// NewConflictResolver 创建冲突解决器
func NewConflictResolver(store Store) *ConflictResolver {
	return &ConflictResolver{store: store}
}

// ResolveAndSave 处理一条新提取的记忆：
//  - 如果同 category+key 不存在 → 新建
//  - 如果同 category+key 存在且 value 相同 → 增加出现次数
//  - 如果同 category+key 存在但 value 不同 → 旧记忆标记 superseded，新建记忆
//  - Two-Strike: candidate 出现第二次 → 升级为 active
func (r *ConflictResolver) ResolveAndSave(ctx context.Context, userID string, extracted ExtractedMemory, tier Tier, traceID string, grade ArticleGrade) (*Memory, error) {
	existing, err := r.store.FindByCategoryKey(ctx, userID, extracted.Category, extracted.Key)
	if err != nil {
		return nil, err
	}

	// 计算初始置信度
	confidence := calculateInitialConfidence(tier, grade)

	// Case 1: 无已有记忆 → 新建
	if len(existing) == 0 {
		mem := &Memory{
			UserID:        userID,
			Tier:          tier,
			Category:      extracted.Category,
			Key:           extracted.Key,
			Value:         extracted.Value,
			Confidence:    confidence,
			Occurrences:   1,
			SourceTraceID: traceID,
			Status:        initialStatus(tier),
			FirstSeen:     time.Now(),
			LastSeen:      time.Now(),
		}

		// 应用质量加权
		applyQualityWeight(mem, grade)

		if err := r.store.Save(ctx, mem); err != nil {
			return nil, err
		}

		slog.Debug("memory: new memory created",
			"category", extracted.Category, "key", extracted.Key,
			"tier", tier, "status", mem.Status, "confidence", mem.Confidence)
		return mem, nil
	}

	// 找到最新的活跃/候选记忆
	var latest *Memory
	for _, m := range existing {
		if m.Status == StatusActive || m.Status == StatusCandidate {
			if latest == nil || m.LastSeen.After(latest.LastSeen) {
				latest = m
			}
		}
	}

	if latest == nil {
		// 所有旧记忆都已 superseded/dismissed，新建
		mem := &Memory{
			UserID:        userID,
			Tier:          tier,
			Category:      extracted.Category,
			Key:           extracted.Key,
			Value:         extracted.Value,
			Confidence:    confidence,
			Occurrences:   1,
			SourceTraceID: traceID,
			Status:        initialStatus(tier),
			FirstSeen:     time.Now(),
			LastSeen:      time.Now(),
		}
		applyQualityWeight(mem, grade)
		if err := r.store.Save(ctx, mem); err != nil {
			return nil, err
		}
		return mem, nil
	}

	// Case 2: value 相同 → 增加出现次数
	if latest.Value == extracted.Value {
		// Two-Strike: candidate → active
		if latest.Status == StatusCandidate {
			latest.Status = StatusActive
			latest.Confidence = maxFloat(latest.Confidence+0.2, 0.6)
			slog.Info("memory: candidate promoted to active (two-strike)",
				"category", extracted.Category, "key", extracted.Key)
		}

		latest.Occurrences++
		latest.LastSeen = time.Now()

		// 质量加权
		if grade == GradePositive {
			latest.Confidence = minFloat(latest.Confidence+0.05, 1.0)
			if latest.QualitySource == "" {
				latest.QualitySource = QualityHighRating
				latest.QualityWeight = 0.8
			}
		}

		if err := r.store.Save(ctx, latest); err != nil {
			return nil, err
		}

		slog.Debug("memory: existing memory reinforced",
			"category", extracted.Category, "key", extracted.Key,
			"occurrences", latest.Occurrences, "confidence", latest.Confidence)
		return latest, nil
	}

	// Case 3: value 不同 → 旧记忆标记 superseded，新建
	if err := r.store.Supersede(ctx, latest.ID, ""); err != nil {
		slog.Warn("memory: failed to supersede old memory", "error", err)
	}

	mem := &Memory{
		UserID:        userID,
		Tier:          tier,
		Category:      extracted.Category,
		Key:           extracted.Key,
		Value:         extracted.Value,
		Confidence:    confidence,
		Occurrences:   1,
		SourceTraceID: traceID,
		Status:        initialStatus(tier),
		FirstSeen:     time.Now(),
		LastSeen:      time.Now(),
	}
	applyQualityWeight(mem, grade)
	if err := r.store.Save(ctx, mem); err != nil {
		return nil, err
	}

	// 更新 superseded_by 指向新记忆
	_ = r.store.Supersede(ctx, latest.ID, mem.ID)

	slog.Info("memory: conflict resolved, old superseded",
		"category", extracted.Category, "key", extracted.Key,
		"old_value", latest.Value, "new_value", extracted.Value,
		"old_id", latest.ID, "new_id", mem.ID)
	return mem, nil
}

// calculateInitialConfidence 根据分层和评级计算初始置信度
func calculateInitialConfidence(tier Tier, grade ArticleGrade) float64 {
	switch tier {
	case TierHard:
		return 1.0
	case TierPattern:
		if grade == GradePositive {
			return 0.5 // 强信号加分后约 0.6
		}
		return 0.3 // candidate 起步
	case TierFeedback:
		return 0.5 // 反馈记忆初始置信度
	default:
		return 0.3
	}
}

// initialStatus 根据分层返回初始状态
func initialStatus(tier Tier) MemoryStatus {
	if tier == TierHard {
		return StatusActive
	}
	// Tier 2 和 Tier 3 首次出现都是 candidate
	return StatusCandidate
}

// applyQualityWeight 应用质量信号加权
func applyQualityWeight(mem *Memory, grade ArticleGrade) {
	switch grade {
	case GradePositive:
		if mem.QualitySource == "" {
			mem.QualitySource = QualityHighRating
			mem.QualityWeight = 0.8
		}
		mem.Confidence = minFloat(mem.Confidence+0.1, 1.0)
	case GradeNegative:
		// 差评不提取 Tier 2，所以这里只影响 Tier 3
		mem.QualitySource = QualityNone
		mem.QualityWeight = 0
	}
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
