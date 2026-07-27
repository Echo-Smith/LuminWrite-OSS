package memory

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"
)

// ─── 工作记忆：增量摘要 + 随机态 ──────────────────────────────
//
// 工作记忆在单次任务执行期间维护一个紧凑的执行状态摘要。
//
// 核心机制：
//  1. 增量摘要（Incremental Summarization）：每个关键步骤完成后，
//     将该步骤的输出摘要追加到工作记忆中。新摘要与旧摘要通过 LLM
//     压缩合并，保持工作记忆在固定长度内。
//  2. 随机态（Stochastic State）：在搜索结果选择、大纲生成等环节
//     引入受控随机性，避免 Agent 在相似输入下产生完全相同的输出，
//     增加多样性，同时通过种子保证可复现性。

// ─── 增量摘要 ──────────────────────────────────────────────

// WorkingSummary 工作记忆摘要 — 随执行进度增量更新
type WorkingSummary struct {
	// ConversationID 所属会话
	ConversationID string `json:"conversation_id,omitempty"`
	// TraceID 所属执行 trace
	TraceID string `json:"trace_id,omitempty"`
	// CurrentTopic 当前写作主题
	CurrentTopic string `json:"current_topic,omitempty"`
	// StepSummaries 每步的摘要（按执行顺序）
	StepSummaries []StepSummary `json:"step_summaries"`
	// SummarizedSteps 已摘要的步骤名集合，避免重复摘要
	SummarizedSteps map[string]bool `json:"-"`
	// CompressedSummary 压缩后的全局摘要（由 LLM 生成）
	CompressedSummary string `json:"compressed_summary,omitempty"`
	// LastUpdatedAt 最后更新时间
	LastUpdatedAt time.Time `json:"last_updated_at"`
	// TokenCount 估算的 token 数
	TokenCount int `json:"token_count"`
}

// StepSummary 单步摘要
type StepSummary struct {
	Step       string `json:"step"`
	Summary    string `json:"summary"`
	Timestamp  time.Time `json:"timestamp"`
	TokensUsed int    `json:"tokens_used,omitempty"`
}

// SummarizerConfig 摘要器配置
type SummarizerConfig struct {
	// MaxStepSummaries 保留的最大步骤摘要数（超出后触发压缩）
	MaxStepSummaries int
	// MaxCompressedLength 压缩摘要的最大字符数
	MaxCompressedLength int
	// CompressThreshold 触发压缩的步骤数阈值
	CompressThreshold int
}

// DefaultSummarizerConfig 默认摘要器配置
func DefaultSummarizerConfig() SummarizerConfig {
	return SummarizerConfig{
		MaxStepSummaries:    8,
		MaxCompressedLength: 800,
		CompressThreshold:   5,
	}
}

// LLMSummarizer LLM 摘要接口（避免直接依赖 tools.LLMClient）
type LLMSummarizer interface {
	Summarize(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// IncrementalSummarizer 增量摘要器
type IncrementalSummarizer struct {
	config SummarizerConfig
	llm    LLMSummarizer
}

// NewIncrementalSummarizer 创建增量摘要器
func NewIncrementalSummarizer(config SummarizerConfig, llm LLMSummarizer) *IncrementalSummarizer {
	return &IncrementalSummarizer{config: config, llm: llm}
}

// AddStepSummary 向工作记忆追加一步摘要
func (s *IncrementalSummarizer) AddStepSummary(summary *WorkingSummary, step string, stepSummary string, tokensUsed int) {
	if summary == nil {
		return
	}

	summary.StepSummaries = append(summary.StepSummaries, StepSummary{
		Step:       step,
		Summary:    stepSummary,
		Timestamp:  time.Now(),
		TokensUsed: tokensUsed,
	})
	summary.LastUpdatedAt = time.Now()
	summary.TokenCount += EstimateTokens(stepSummary)

	// 超过阈值时触发压缩
	if len(summary.StepSummaries) > s.config.CompressThreshold && s.llm != nil {
		s.compress(summary)
	}

	// 超过最大保留数时，截断旧摘要
	if len(summary.StepSummaries) > s.config.MaxStepSummaries {
		// 保留最新的 MaxStepSummaries 条
		summary.StepSummaries = summary.StepSummaries[len(summary.StepSummaries)-s.config.MaxStepSummaries:]
	}
}

// compress 使用 LLM 压缩历史摘要
func (s *IncrementalSummarizer) compress(summary *WorkingSummary) {
	if s.llm == nil || len(summary.StepSummaries) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("以下是写作 Agent 的执行步骤摘要，请压缩为一段简洁的上下文摘要（不超过200字）：\n\n")
	for _, ss := range summary.StepSummaries {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", ss.Step, ss.Summary))
	}
	if summary.CompressedSummary != "" {
		sb.WriteString(fmt.Sprintf("\n之前的摘要：%s\n", summary.CompressedSummary))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	compressed, err := s.llm.Summarize(ctx,
		"你是写作助手的记忆压缩器。将执行步骤摘要压缩为简洁的上下文摘要，保留关键信息：主题、已完成的步骤、关键决策。",
		sb.String(),
	)
	if err != nil {
		slog.Warn("working memory: compress failed", "error", err)
		return
	}

	// 截断到最大长度（安全截断，不切断 UTF-8）
	if len(compressed) > s.config.MaxCompressedLength {
		compressed = SafeTruncate(compressed, s.config.MaxCompressedLength) + "..."
	}

	summary.CompressedSummary = compressed
	summary.TokenCount = EstimateTokens(compressed)
	slog.Debug("working memory: compressed", "steps", len(summary.StepSummaries), "compressed_len", len(compressed))
}

// FormatForPrompt 将工作记忆格式化为 prompt 文本
func FormatWorkingSummaryForPrompt(summary *WorkingSummary) string {
	if summary == nil {
		return ""
	}

	var sb strings.Builder

	// 优先使用压缩摘要
	if summary.CompressedSummary != "" {
		sb.WriteString("\n\n--- 上下文摘要 ---\n")
		sb.WriteString(summary.CompressedSummary)
		sb.WriteString("\n")
	}

	// 追加最近的步骤摘要（最多 3 条）
	recentCount := 3
	if len(summary.StepSummaries) < recentCount {
		recentCount = len(summary.StepSummaries)
	}
	if recentCount > 0 {
		sb.WriteString("\n--- 最近执行 ---\n")
		for _, ss := range summary.StepSummaries[len(summary.StepSummaries)-recentCount:] {
			sb.WriteString(fmt.Sprintf("[%s] %s\n", ss.Step, ss.Summary))
		}
	}

	return sb.String()
}

// ─── 随机态 ────────────────────────────────────────────────

// StochasticState 随机态 — 在 Agent 执行中引入受控随机性
type StochasticState struct {
	// Seed 随机种子（同一 seed + 同一输入 → 同一输出，保证可复现）
	Seed int64 `json:"seed"`
	// RNG 随机数生成器
	rng *rand.Rand `json:"-"`
	// TemperatureShift 温度偏移（-0.1 ~ +0.1）
	TemperatureShift float64 `json:"temperature_shift,omitempty"`
	// SearchSamplingRate 搜索结果采样率（0.0-1.0，1.0 = 全部保留）
	SearchSamplingRate float64 `json:"search_sampling_rate,omitempty"`
}

// NewStochasticState 创建随机态
func NewStochasticState(seed int64) *StochasticState {
	rng := rand.New(rand.NewSource(seed))
	return &StochasticState{
		Seed:               seed,
		rng:                rng,
		TemperatureShift:   (rng.Float64() - 0.5) * 0.2,   // ±0.1
		SearchSamplingRate: 0.7 + rng.Float64()*0.3,        // 0.7-1.0
	}
}

// SampleSearchResults 对搜索结果进行随机采样
// 保留 top-K 结果，但从剩余结果中随机采样一部分
func (s *StochasticState) SampleSearchResults(results []SearchResultStub, keepTop int) []SearchResultStub {
	if s == nil || s.rng == nil || len(results) <= keepTop {
		return results
	}

	// 保留 top-K
	selected := make([]SearchResultStub, 0, len(results))
	selected = append(selected, results[:keepTop]...)

	// 从剩余结果中随机采样
	remaining := results[keepTop:]
	for _, r := range remaining {
		if s.rng.Float64() < s.SearchSamplingRate {
			selected = append(selected, r)
		}
	}

	return selected
}

// AdjustedTemperature 根据随机态调整 LLM 温度
func (s *StochasticState) AdjustedTemperature(baseTemp float64) float64 {
	if s == nil {
		return baseTemp
	}
	adjusted := baseTemp + s.TemperatureShift
	if adjusted < 0 {
		return 0
	}
	if adjusted > 2 {
		return 2
	}
	return adjusted
}

// ShouldExplore 是否应该进行探索（而非利用已有最优路径）
// 以一定概率返回 true，用于决定是否尝试新的搜索策略或大纲方案
func (s *StochasticState) ShouldExplore(exploreRate float64) bool {
	if s == nil || s.rng == nil {
		return false
	}
	return s.rng.Float64() < exploreRate
}

// ShouldKeep 基于搜索采样率决定是否保留一条结果。
// top-K 结果应直接保留，此方法用于对剩余结果进行随机采样。
func (s *StochasticState) ShouldKeep() bool {
	if s == nil || s.rng == nil {
		return true // 无随机态时全部保留
	}
	return s.rng.Float64() < s.SearchSamplingRate
}

// SearchResultStub 搜索结果桩（避免直接依赖 engine.SearchResult）
type SearchResultStub struct {
	Title    string  `json:"title"`
	Snippet  string  `json:"snippet"`
	URL      string  `json:"url,omitempty"`
	Source   string  `json:"source"`
	Score    float64 `json:"score,omitempty"`
	IsMock   bool    `json:"is_mock,omitempty"`
}

// GenerateSeedFromInput 从用户输入生成确定性种子。
// 同一输入 → 同一种子 → 同一随机态，保证可复现性。
// 如需引入多样性，应由调用方在种子上叠加外部随机值，
// 而非在此函数内部破坏确定性。
func GenerateSeedFromInput(input string) int64 {
	var hash int64
	for _, ch := range input {
		hash = hash*31 + int64(ch)
	}
	// 不再加入时间戳噪声，保证可复现性
	return hash
}
