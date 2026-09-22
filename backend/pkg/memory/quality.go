package memory

import "time"

// QualityCalculator 质量信号计算器 — 计算文章的综合质量评级
type QualityCalculator struct{}

// NewQualityCalculator 创建质量计算器
func NewQualityCalculator() *QualityCalculator {
	return &QualityCalculator{}
}

// CalculateGrade 根据质量信号和反馈评级计算综合质量评级
//
// 规则：
//   - 差评（1-2 星）→ GradeNegative（不管其他信号）
//   - 有强信号（weight >= 0.8）→ GradePositive
//   - 4-5 星反馈 → GradePositive
//   - 3 星反馈 + 无信号 → GradeNeutral
//   - 无反馈 + 无信号 → GradeNeutral
func (qc *QualityCalculator) CalculateGrade(signals []QualitySignal, feedback []FeedbackInfo) ArticleGrade {
	// 差评优先
	for _, fb := range feedback {
		if fb.Rating <= 2 {
			return GradeNegative
		}
	}

	// 检查强信号
	for _, sig := range signals {
		if sig.Weight >= 0.8 && time.Since(sig.EvidencedAt) < 7*24*time.Hour {
			return GradePositive
		}
	}

	// 高分反馈
	for _, fb := range feedback {
		if fb.Rating >= 4 {
			return GradePositive
		}
	}

	return GradeNeutral
}

// CollectSignals 从外部数据收集质量信号
// 当前实现两种信号源：workbuddy_adopt 和 high_rating
// 未来可扩展 user_copy / user_share / manual_approve
func CollectSignals(isAdopted bool, feedback []FeedbackInfo, traceCompletedAt time.Time) []QualitySignal {
	var signals []QualitySignal

	// Workbuddy 录用信号
	if isAdopted {
		signals = append(signals, QualitySignal{
			Source:      QualityWorkbuddy,
			Weight:      0.9, // 录用是最强信号
			EvidencedAt: traceCompletedAt,
		})
	}

	// 高分反馈信号
	hasHighRating := false
	for _, fb := range feedback {
		if fb.Rating >= 4 {
			hasHighRating = true
			break
		}
	}
	if hasHighRating {
		signals = append(signals, QualitySignal{
			Source:      QualityHighRating,
			Weight:      0.7,
			EvidencedAt: traceCompletedAt,
		})
	}

	return signals
}

// isStrongQualitySource 判断是否为强质量信号（人工显式确认类）。
func isStrongQualitySource(qs QualitySource) bool {
	return qs == QualityManualApprove || qs == QualityWorkbuddy
}

// DeriveSource 从质量信号中派生本次提取的质量出处（WP6）。
// 规则：
//   - 只采纳 7 天内的信号（与 CalculateGrade 的时效窗口一致）
//   - 权重 >= 0.8 的 manual_approve/workbuddy 强信号优先于 high_rating
//   - 其余取权重最高的信号
//   - 无合格信号返回 QualityNone
func (qc *QualityCalculator) DeriveSource(signals []QualitySignal) QualitySource {
	const window = 7 * 24 * time.Hour
	now := time.Now()
	var bestStrong, bestAny *QualitySignal
	for i := range signals {
		sig := &signals[i]
		if now.Sub(sig.EvidencedAt) >= window {
			continue // 过期信号忽略
		}
		if bestAny == nil || sig.Weight > bestAny.Weight {
			bestAny = sig
		}
		if sig.Weight >= 0.8 && isStrongQualitySource(sig.Source) {
			if bestStrong == nil || sig.Weight > bestStrong.Weight {
				bestStrong = sig
			}
		}
	}
	if bestStrong != nil {
		return bestStrong.Source
	}
	if bestAny != nil {
		return bestAny.Source
	}
	return QualityNone
}
