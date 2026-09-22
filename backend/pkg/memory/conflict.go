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
//   - 如果同 category+key 不存在 → 新建
//   - 如果同 category+key 存在且 value 相同 → 增加出现次数
//   - 如果同 category+key 存在但 value 不同 → 旧记忆标记 superseded，新建记忆
//   - Two-Strike: candidate 出现第二次 → 升级为 active
//
// qs 是本次提取派生出的质量信号出处（人工录用/人工确认/好评等），
// 用于证据阶梯与质量加权（WP6）。
func (r *ConflictResolver) ResolveAndSave(ctx context.Context, userID string, extracted ExtractedMemory, tier Tier, traceID string, grade ArticleGrade, qs QualitySource) (*Memory, error) {
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

		// 证据阶梯补全（WP6）：新建路径也写入质量出处/证据状态/多源计数
		applyEvidenceOnCreate(mem, grade, qs)

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

		// 强信号（人工录用/人工确认）覆盖较弱的质量出处
		if isStrongQualitySource(qs) {
			latest.QualitySource = qs
		}

		// 证据升级链路（Layer-0 止血④b）：每次被再次观测/正反馈都推进
		// 证据状态。此前 evidence_status 恒为 none，写作场景的严格证据门
		// 会把所有 Tier2/3 静默全拦。
		latest.EvidenceStatus = upgradedEvidenceStatus(latest.EvidenceStatus, grade, latest.QualitySource)

		// 多源计数（WP6）：不同 traceID 的再次观测视为独立证据源。
		// 保留首个出处 SourceTraceID 不覆盖；旧记录 SourceCount 为 0 时先补到 1 再自增。
		if traceID != "" && traceID != latest.SourceTraceID {
			if latest.SourceCount < 1 {
				latest.SourceCount = 1
			}
			latest.SourceCount++
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

	// 证据阶梯补全（WP6）：与 Case 1 新建路径一致
	applyEvidenceOnCreate(mem, grade, qs)

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

// applyEvidenceOnCreate 新建记忆路径的证据阶梯补全（WP6）：
//   - qs 非空且当前无质量出处，或 qs 本身是强信号（manual_approve/workbuddy）
//     → 写入/覆盖 QualitySource
//   - EvidenceStatus 按 initialEvidenceStatus 派生
//   - SourceCount 记多源计数起点 1
func applyEvidenceOnCreate(mem *Memory, grade ArticleGrade, qs QualitySource) {
	if qs != "" && (mem.QualitySource == "" || isStrongQualitySource(qs)) {
		mem.QualitySource = qs
	}
	mem.EvidenceStatus = initialEvidenceStatus(grade, mem.QualitySource)
	mem.SourceCount = 1
}

// initialEvidenceStatus 派生新建记忆的初始证据状态：
//   - 强信号（manual_approve/workbuddy）→ verified
//   - 好评 → supported
//   - 其余（中性/差评且无强信号）→ none
//
// 与 upgradedEvidenceStatus 的"再次出现"分支不同：首次观测本身不构成
// 复现证据，none→supported 的 two-strike 升级只发生在 Case 2 强化路径。
func initialEvidenceStatus(grade ArticleGrade, qs QualitySource) EvidenceStatus {
	if isStrongQualitySource(qs) {
		return EvidenceVerified
	}
	if grade == GradePositive {
		return EvidenceSupported
	}
	return EvidenceNone
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

// upgradedEvidenceStatus 证据升级链路（Layer-0 止血④b）。
// 记忆每被再次观测（two-strike/ reinforcement）或获得更强的质量信号时，
// 证据状态只升不降：
//   - 人工显式确认（manual_approve / workbuddy 录用）→ verified
//   - 正面反馈（好评）→ 至少 supported
//   - 再次出现（第二次观测即 two-strike 晋升）→ none 升为 supported
//   - 其余情况维持现状（conflicted 不因重复观测而洗白，需人工裁决）
func upgradedEvidenceStatus(current EvidenceStatus, grade ArticleGrade, qs QualitySource) EvidenceStatus {
	if qs == QualityManualApprove || qs == QualityWorkbuddy {
		return EvidenceVerified
	}
	if grade == GradePositive {
		if current == EvidenceVerified {
			return EvidenceVerified
		}
		return EvidenceSupported
	}
	if current == "" || current == EvidenceNone {
		return EvidenceSupported
	}
	return current
}
