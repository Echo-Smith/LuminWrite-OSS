package memory

import "time"

// ─── 核心类型 ──────────────────────────────────────────────

// Tier 记忆分层
type Tier string

const (
	TierHard     Tier = "hard"     // 用户手动设置的硬偏好
	TierPattern  Tier = "pattern"  // 自动提取的行为模式
	TierFeedback Tier = "feedback" // 反馈驱动的改进信号
)

// MemoryStatus 记忆状态
type MemoryStatus string

const (
	StatusCandidate  MemoryStatus = "candidate"  // 候选：第一次提取，未确认
	StatusActive     MemoryStatus = "active"     // 活跃：已确认，正常注入
	StatusSuperseded MemoryStatus = "superseded" // 已被更新记忆替代
	StatusDismissed  MemoryStatus = "dismissed"  // 用户关闭
	StatusArchived   MemoryStatus = "archived"   // 归档（不再使用但不删除）
)

// QualitySource 质量信号来源
type QualitySource string

const (
	QualityNone          QualitySource = ""                // 无信号
	QualityWorkbuddy     QualitySource = "workbuddy_adopt" // 预设评论风格录用
	QualityHighRating    QualitySource = "high_rating"     // 4-5 星反馈
	QualityUserCopy      QualitySource = "user_copy"       // 用户复制了文章（未来）
	QualityUserShare     QualitySource = "user_share"      // 用户分享了文章（未来）
	QualityManualApprove QualitySource = "manual_approve"  // 用户手动标记满意
)

// EvidenceStatus 证据状态 — 标识一条记忆背后的证据强度
// 用于证据边界召回（Evidence-Bounded Recall）：
// 写作场景只注入有证据支撑的记忆，聊天场景可放宽。
type EvidenceStatus string

const (
	EvidenceVerified    EvidenceStatus = "verified"    // 可追溯证据或人工确认
	EvidenceSupported   EvidenceStatus = "supported"   // LLM 判断有信源支持
	EvidenceConflicted  EvidenceStatus = "conflicted"  // 信源间存在矛盾
	EvidenceUnknown     EvidenceStatus = "unknown"     // LLM 无法判断
	EvidenceNone        EvidenceStatus = "none"        // 无证据信号（默认）
)

// IsSafeForInjection 判断该证据状态是否可安全注入到写作上下文
func (e EvidenceStatus) IsSafeForInjection(requireVerified bool) bool {
	if requireVerified {
		return e == EvidenceVerified || e == EvidenceSupported
	}
	return e != EvidenceConflicted && e != EvidenceUnknown
}

// QualitySignal 代表"这篇文章值得学习"的外部信号
type QualitySignal struct {
	Source       QualitySource `json:"source"`
	Weight       float64       `json:"weight"`        // 0.0-1.0
	EvidencedAt  time.Time     `json:"evidenced_at"`
}

// ArticleGrade 文章质量评级
type ArticleGrade string

const (
	GradePositive ArticleGrade = "positive" // 有强信号或高分 → 提取 Tier 2 并加权
	GradeNeutral  ArticleGrade = "neutral"  // 无信号 + 中评 → 提取但不加权
	GradeNegative ArticleGrade = "negative" // 差评 → 不提取 Tier 2，只提取 Tier 3
)

// Memory 是一条用户记忆的完整表示
type Memory struct {
	ID             string          `json:"id"`
	UserID         string          `json:"user_id"`
	Tier           Tier            `json:"tier"`
	Category       string          `json:"category"`        // word_count | style | structure | tone | title | topic | argument
	Key            string          `json:"key"`
	Value          string          `json:"value"`
	Embedding      []float32       `json:"-"`
	Confidence     float64         `json:"confidence"`       // 基础置信度 0.0-1.0
	Occurrences    int             `json:"occurrences"`
	SourceTraceID  string          `json:"source_trace_id,omitempty"`
	QualitySource  QualitySource   `json:"quality_source,omitempty"`
	QualityWeight  float64         `json:"quality_weight,omitempty"`
	Status         MemoryStatus    `json:"status"`
	SupersededBy   string          `json:"superseded_by,omitempty"`
	FirstSeen      time.Time       `json:"first_seen"`
	LastSeen       time.Time       `json:"last_seen"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	// ─── 安全与证据边界 (P0) ────────────────────────────────
	EvidenceStatus EvidenceStatus `json:"evidence_status,omitempty"` // 证据状态（默认 none）
	SourceCount    int             `json:"source_count,omitempty"`    // 支撑来源数量
}

// EffectiveConfidence 计算衰减后的有效置信度
func (m *Memory) EffectiveConfidence(halfLifeDays int) float64 {
	if m.Tier == TierHard {
		return m.Confidence // 硬偏好不衰减
	}
	if halfLifeDays <= 0 {
		return m.Confidence
	}
	days := time.Since(m.LastSeen).Hours() / 24
	decay := 0.5
	if days < float64(halfLifeDays) {
		// exponential decay: confidence * 0.5^(days/halfLife)
		decay = 1.0
		for i := 0; i < int(days); i++ {
			decay *= (1.0 - 0.6931471805599453/float64(halfLifeDays))
		}
	} else {
		decay = 0.5
	}
	// quality weight boost
	effective := m.Confidence * decay
	if m.QualityWeight > 0 {
		effective = effective + m.QualityWeight*0.1 // 最多 +0.1 加权
	}
	if effective > 1.0 {
		effective = 1.0
	}
	return effective
}

// ─── 检索与注入 ────────────────────────────────────────────

// RetrieveRequest 封装写作请求上下文
type RetrieveRequest struct {
	UserID    string         `json:"user_id"`
	UserInput string         `json:"user_input"`
	Intent    string         `json:"intent"`           // writing | polish | chat
	Explicit  map[string]any `json:"explicit"`         // 用户显式指定的维度
	SessionID string         `json:"session_id"`       // 当前会话 ID（用于 dismiss 追踪）
}

// MemoryEntry 是注入到 prompt 中的记忆条目
type MemoryEntry struct {
	ID             string          `json:"id"`
	Tier           Tier            `json:"tier"`
	Category       string          `json:"category"`
	Value          string          `json:"value"`
	Confidence     float64         `json:"confidence"`
	Dismissible    bool            `json:"dismissible"`   // 用户是否可关闭
	EvidenceStatus EvidenceStatus  `json:"evidence_status,omitempty"` // 证据状态
}

// MemoryContext 是门控后的记忆上下文
type MemoryContext struct {
	Injected      []MemoryEntry `json:"injected"`       // 注入 WriteStep 的记忆（Tier 1 + Tier 2）
	ReviewGuard   []MemoryEntry `json:"review_guard"`    // 注入 PostReviewStep 的反馈记忆（Tier 3）
	Dismissed     []string      `json:"dismissed"`      // 本次被关闭的记忆 ID
	RefusalReason string        `json:"refusal_reason,omitempty"` // 拒答原因（非空时表示记忆不足或安全过滤触发拒答）
}

// ─── 提取 ──────────────────────────────────────────────────

// ExtractSession 封装写作完成后的提取上下文
type ExtractSession struct {
	UserID       string          `json:"user_id"`
	TraceID      string          `json:"trace_id"`
	Article      string          `json:"article"`
	StyleSlug    string          `json:"style_slug"`
	Mode         string          `json:"mode"`
	WordLimit    int             `json:"word_limit"`
	Feedback     []FeedbackInfo  `json:"feedback"`
	Signals      []QualitySignal `json:"signals"`
	Grade        ArticleGrade    `json:"grade"`
}

// FeedbackInfo 用户反馈信息
type FeedbackInfo struct {
	SegmentType string `json:"segment_type"` // title | paragraph | overall
	Rating      int    `json:"rating"`       // 1-5
	Comment     string `json:"comment"`
}

// ExtractedMemory 是提取器输出的候选记忆
type ExtractedMemory struct {
	Category string `json:"category"`
	Key      string `json:"key"`
	Value    string `json:"value"`
	Source   string `json:"source"` // deterministic | llm | feedback
}

// ─── 配置 ──────────────────────────────────────────────────

// HalfLifeConfig 衰减半衰期配置
type HalfLifeConfig struct {
	Pattern  int // Tier 2 半衰期（天），默认 30
	Feedback int // Tier 3 半衰期（天），默认 60
}

// ThresholdConfig 置信度阈值配置
type ThresholdConfig struct {
	Inject    float64 // 注入阈值，默认 0.6
	Candidate float64 // 候选阈值，默认 0.3
	Confirmed float64 // 确认阈值（出现次数 × 置信度），默认 0.6
}

// RolloutConfig 灰度控制配置
type RolloutConfig struct {
	Enabled       bool     // 全局开关，默认 true
	WhitelistUIDs []string // 白名单用户 ID（为空表示不限制）
	Percentage    int      // 灰度百分比 0-100（默认 100）
}

// SafetyConfig 安全与证据边界配置
type SafetyConfig struct {
	Enabled              bool    // 安全过滤总开关，默认 true
	EnableRefusal        bool    // 拒答开关：无足够证据记忆时返回拒答而非空结果，默认 true
	RequireVerifiedForWriting bool // 写作场景要求 verified/supported 证据，默认 true
	RequireVerifiedForChat    bool // 聊天场景要求 verified/supported 证据，默认 false
	PIIFilterEnabled    bool    // PII 过滤开关：保存记忆前检查敏感内容，默认 true
	MaxInjectedPerIntent int    // 每次注入的最大记忆条数（最小披露），默认 0=不限制
}

// DefaultSafetyConfig 返回默认安全配置
func DefaultSafetyConfig() SafetyConfig {
	return SafetyConfig{
		Enabled:               true,
		EnableRefusal:         true,
		RequireVerifiedForWriting: true,
		RequireVerifiedForChat:    false,
		PIIFilterEnabled:     true,
		MaxInjectedPerIntent: 0,
	}
}

// Config SDK 完整配置 — 注入给 SDK，避免读取宿主配置
type Config struct {
	HalfLife   HalfLifeConfig
	Thresholds ThresholdConfig
	Rollout    RolloutConfig
	Safety     SafetyConfig
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		HalfLife: HalfLifeConfig{
			Pattern:  30,
			Feedback: 60,
		},
		Thresholds: ThresholdConfig{
			Inject:    0.6,
			Candidate: 0.3,
			Confirmed: 0.6,
		},
		Rollout: RolloutConfig{
			Enabled:    true,
			Percentage: 100,
		},
		Safety: DefaultSafetyConfig(),
	}
}

// IsEnabledForUser 根据灰度配置判断某用户是否启用记忆
func (c *Config) IsEnabledForUser(userID string) bool {
	if !c.Rollout.Enabled {
		return false
	}
	// 白名单优先：如果配置了白名单，只允许白名单内用户
	if len(c.Rollout.WhitelistUIDs) > 0 {
		for _, uid := range c.Rollout.WhitelistUIDs {
			if uid == userID {
				return true
			}
		}
		return false
	}
	// 百分比灰度：基于 userID hash
	if c.Rollout.Percentage >= 100 {
		return true
	}
	if c.Rollout.Percentage <= 0 {
		return false
	}
	// Simple hash for deterministic rollout
	hash := uint32(0)
	for _, ch := range userID {
		hash = hash*31 + uint32(ch)
	}
	return int(hash%100) < c.Rollout.Percentage
}
