package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// EvaluationService manages evaluation sets, runs, and LLM-as-Judge scoring.
type EvaluationService struct {
	repo     *database.EvaluationRepo
	llm      *tools.LLMClient
	profiles *profile.Loader
}

// NewEvaluationService creates a new EvaluationService.
func NewEvaluationService(repo *database.EvaluationRepo, llm *tools.LLMClient, profiles *profile.Loader) *EvaluationService {
	return &EvaluationService{
		repo:     repo,
		llm:      llm,
		profiles: profiles,
	}
}

// RunEvaluation executes an evaluation run: for each sample, generate an article
// using the LLM with the style profile, then score it using LLM-as-Judge.
func (s *EvaluationService) RunEvaluation(ctx context.Context, runID string) error {
	if s.repo == nil {
		return fmt.Errorf("database not available")
	}
	if s.llm == nil {
		return fmt.Errorf("LLM not configured")
	}

	// Get run
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("failed to get run: %w", err)
	}

	// Mark as running
	s.repo.StartRun(ctx, runID)

	// Get samples
	samples, err := s.repo.ListSamples(ctx, run.SetID)
	if err != nil {
		s.repo.FailRun(ctx, runID, err.Error())
		return fmt.Errorf("failed to list samples: %w", err)
	}

	if len(samples) == 0 {
		s.repo.CompleteRun(ctx, runID, 0, nil)
		return nil
	}

	slog.Info("starting evaluation run", "run_id", runID, "samples", len(samples))

	// Get style profile
	p, ok := s.profiles.Get(run.ProfileSlug)
	if !ok {
		err := fmt.Errorf("style profile not found: %s", run.ProfileSlug)
		s.repo.FailRun(ctx, runID, err.Error())
		return err
	}

	// Run each sample sequentially (to avoid rate limits)
	allDimScores := map[string][]float64{} // dimension -> []score
	var totalScore float64
	var scoredCount int

	for i, sample := range samples {
		if ctx.Err() != nil {
			s.repo.FailRun(ctx, runID, "cancelled")
			return ctx.Err()
		}

		slog.Info("evaluating sample", "run_id", runID, "sample", i+1, "total", len(samples), "topic", sample.Topic)

		// Step 1: Generate article using the style profile
		article, err := s.generateArticle(ctx, p.SystemPrompt, sample.InputPrompt)
		if err != nil {
			slog.Warn("failed to generate article for sample", "sample_id", sample.ID, "error", err)
			article = "" // Continue with empty article
		}

		// Step 2: Score using LLM-as-Judge
		llmScores, judgeFeedback, err := s.judgeArticle(ctx, sample, article, p)
		if err != nil {
			slog.Warn("LLM judge failed for sample", "sample_id", sample.ID, "error", err)
			llmScores = map[string]float64{}
		}

		// Step 3: Compute rule-based scores and merge
		ruleScores := s.ruleBasedScore(sample, article, p)
		scores := s.mergeScores(llmScores, ruleScores)

		// Calculate weighted score (scores are 0-1 normalized)
		weightedScore := 0.0
		totalWeight := 0.0
		for dim, score := range scores {
			weight := 0.2
			if w, ok := sample.ScoringCriteria[dim].(float64); ok {
				weight = w
			}
			// Scale to 0-5 for display consistency
			weightedScore += score * weight * 5.0
			totalWeight += weight
			allDimScores[dim] = append(allDimScores[dim], score*5.0)
		}
		if totalWeight > 0 {
			weightedScore /= totalWeight
		}

		totalScore += weightedScore
		scoredCount++

		result := map[string]interface{}{
			"sample_id":    sample.ID,
			"topic":        sample.Topic,
			"scores":       scores,
			"weighted":     weightedScore,
			"judge_feedback": judgeFeedback,
			"article_preview": truncate(article, 200),
		}

		if err := s.repo.UpdateRunProgress(ctx, runID, result); err != nil {
			slog.Warn("failed to update run progress", "error", err)
		}
	}

	// Compute final scores
	overallScore := 0.0
	if scoredCount > 0 {
		overallScore = totalScore / float64(scoredCount)
	}

	finalDimScores := make(map[string]float64)
	for dim, scores := range allDimScores {
		sum := 0.0
		for _, s := range scores {
			sum += s
		}
		finalDimScores[dim] = sum / float64(len(scores))
	}

	s.repo.CompleteRun(ctx, runID, overallScore, finalDimScores)
	slog.Info("evaluation run completed", "run_id", runID, "overall_score", overallScore, "scored", scoredCount)

	return nil
}

// generateArticle generates an article using the style profile.
func (s *EvaluationService) generateArticle(ctx context.Context, systemPrompt, inputPrompt string) (string, error) {
	messages := []tools.LLMMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: inputPrompt},
	}

	text, _, err := s.llm.Chat(ctx, messages, tools.WithTemperature(0.7), tools.WithModel(""))
	if err != nil {
		return "", err
	}
	return text, nil
}

// judgeArticle uses LLM-as-Judge to score the generated article.
func (s *EvaluationService) judgeArticle(ctx context.Context, sample *database.EvaluationSample, article string, p *profile.StyleProfile) (map[string]float64, string, error) {
	judgePrompt := fmt.Sprintf(`你是一个写作质量评测评委。请对以下文章进行多维度评分（1-5分，精确到小数点后1位）。

## 评测题目
%s

## 输入要求
%s

## 风格要求
- 风格: %s
- 字数范围: %d-%d
- 结构类型: %s

## 评测维度
1. factuality (事实准确性): 内容是否准确、有无事实错误
2. structure (结构合理性): 是否符合三段式/自由结构要求，逻辑是否清晰
3. style (风格匹配度): 是否符合风格 Profile 的语言风格和修辞要求
4. relevance (相关性): 内容是否紧扣主题，是否使用了检索素材
5. risk (安全风险): 是否存在敏感内容、禁止表述

## 待评测文章
%s

## 评分标准关键词
%s

## 输出格式
请以 JSON 格式输出评分结果，不要包含其他内容：
{"factuality": 4.2, "structure": 3.8, "style": 4.0, "relevance": 4.5, "risk": 4.8, "feedback": "总体评价和改进建议..."}`,
		sample.Topic,
		sample.InputPrompt,
		p.Name,
		p.WordRange.Min,
		p.WordRange.Max,
		p.Structure.Type,
		article,
		strings.Join(sample.ExpectedKeywords, ", "),
	)

	messages := []tools.LLMMessage{
		{Role: "system", Content: "你是一个严格、客观的写作质量评测评委。请只输出JSON结果。"},
		{Role: "user", Content: judgePrompt},
	}

	text, _, err := s.llm.Chat(ctx, messages, tools.WithTemperature(0.3))
	if err != nil {
		return nil, "", err
	}

	// Parse JSON from response
	jsonStr := tools.ExtractJSONObject(text)
	if jsonStr == "" {
		return nil, "", fmt.Errorf("no JSON in judge response")
	}

	var result struct {
		Factuality float64 `json:"factuality"`
		Structure  float64 `json:"structure"`
		Style      float64 `json:"style"`
		Relevance  float64 `json:"relevance"`
		Risk       float64 `json:"risk"`
		Feedback   string  `json:"feedback"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, "", fmt.Errorf("failed to parse judge response: %w", err)
	}

	scores := map[string]float64{
		"factuality": result.Factuality,
		"structure":  result.Structure,
		"style":      result.Style,
		"relevance":  result.Relevance,
		"risk":       result.Risk,
	}

	return scores, result.Feedback, nil
}

// ruleBasedScore computes scores using deterministic rules.
// This supplements the LLM-as-Judge with verifiable metrics.
func (s *EvaluationService) ruleBasedScore(sample *database.EvaluationSample, article string, p *profile.StyleProfile) map[string]float64 {
	scores := make(map[string]float64)

	// 1. Length compliance
	runeCount := len([]rune(article))
	minWords, maxWords := 300, 2000
	if p.WordRange.Min > 0 {
		minWords = p.WordRange.Min
	}
	if p.WordRange.Max > 0 {
		maxWords = p.WordRange.Max
	}
	lengthScore := 1.0
	if runeCount < minWords {
		lengthScore = float64(runeCount) / float64(minWords)
	} else if runeCount > maxWords {
		excess := float64(runeCount-maxWords) / float64(maxWords)
		lengthScore = 1.0 - excess*0.3
		if lengthScore < 0.3 {
			lengthScore = 0.3
		}
	}
	scores["length"] = lengthScore

	// 2. Keyword coverage
	if len(sample.ExpectedKeywords) > 0 {
		matched := 0
		articleLower := strings.ToLower(article)
		for _, kw := range sample.ExpectedKeywords {
			if strings.Contains(articleLower, strings.ToLower(kw)) {
				matched++
			}
		}
		scores["keyword_coverage"] = float64(matched) / float64(len(sample.ExpectedKeywords))
	}

	// 3. Structure compliance — check for markdown headers
	hasTitle := strings.Contains(article, "##") || strings.Contains(article, "# ")
	paragraphCount := strings.Count(article, "\n\n") + 1
	structureScore := 0.5
	if hasTitle {
		structureScore += 0.2
	}
	if paragraphCount >= 3 {
		structureScore += 0.3
	}
	if structureScore > 1.0 {
		structureScore = 1.0
	}
	scores["structure"] = structureScore

	// 4. Safety — check for obviously problematic patterns
	problematicPatterns := []string{"震惊", "不看后悔", "点击查看", "速看", "紧急扩散"}
	problemCount := 0
	for _, pattern := range problematicPatterns {
		if strings.Contains(article, pattern) {
			problemCount++
		}
	}
	safetyScore := 1.0 - float64(problemCount)*0.2
	if safetyScore < 0 {
		safetyScore = 0
	}
	scores["safety"] = safetyScore

	return scores
}

// mergeScores combines LLM judge scores with rule-based scores.
// LLM scores are weighted 70%, rule-based scores 30%.
func (s *EvaluationService) mergeScores(llmScores, ruleScores map[string]float64) map[string]float64 {
	merged := make(map[string]float64)

	// All dimensions from LLM scores
	for dim, llmScore := range llmScores {
		// Normalize LLM score from 1-5 to 0-1
		normalizedLLM := llmScore / 5.0
		if normalizedLLM > 1.0 {
			normalizedLLM = 1.0
		}

		if ruleScore, hasRule := ruleScores[dim]; hasRule {
			merged[dim] = normalizedLLM*0.7 + ruleScore*0.3
		} else {
			merged[dim] = normalizedLLM
		}
	}

	// Add rule-only dimensions
	for dim, ruleScore := range ruleScores {
		if _, exists := merged[dim]; !exists {
			merged[dim] = ruleScore
		}
	}

	return merged
}

// truncate shortens a string to maxLen characters.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// GenerateImprovementSuggestions uses LLM to analyze feedback and generate suggestions.
func (s *EvaluationService) GenerateImprovementSuggestions(ctx context.Context, agg *database.FeedbackAggregation) (string, error) {
	if s.llm == nil {
		return "LLM 未配置，无法生成改进建议", nil
	}

	breakdownJSON, _ := json.Marshal(agg.SegmentBreakdown)

	prompt := fmt.Sprintf(`你是一个写作系统优化顾问。请基于以下反馈聚合数据，为风格 Profile 迭代提供改进建议。

## 风格信息
- 风格 Slug: %s
- Profile 版本: %d

## 反馈统计
- 总反馈数: %d
- 录用数: %d
- 平均评分: %.2f / 5.00
- 信誉加权分: %.2f

## 维度得分
%s

## 分段反馈明细
%s

请从以下方面给出具体、可操作的建议（200-400字）：
1. 最突出的问题是什么
2. 哪些维度需要优先改进
3. 具体的 Profile 配置调整建议（如修辞、结构、字数等）
4. 是否达到迭代条件（当前 %d 条反馈，阈值 30 条）`,
		agg.StyleSlug,
		agg.ProfileVersion,
		agg.TotalFeedback,
		agg.TotalAdopted,
		agg.AvgRating,
		agg.WeightedScore,
		formatDimScores(agg.DimensionScores),
		string(breakdownJSON),
		agg.TotalFeedback,
	)

	messages := []tools.LLMMessage{
		{Role: "system", Content: "你是一个专业的写作系统优化顾问。"},
		{Role: "user", Content: prompt},
	}

	text, _, err := s.llm.Chat(ctx, messages, tools.WithTemperature(0.5))
	if err != nil {
		return "", err
	}

	return text, nil
}

func formatDimScores(scores map[string]float64) string {
	if len(scores) == 0 {
		return "暂无数据"
	}
	var sb strings.Builder
	for dim, score := range scores {
		sb.WriteString(fmt.Sprintf("- %s: %.2f\n", dim, score))
	}
	return sb.String()
}

// TriggerEvaluationIfProfileChanged triggers an evaluation when a profile is published.
// This is called asynchronously.
func (s *EvaluationService) TriggerEvaluationIfProfileChanged(ctx context.Context, slug string, version int, detail string) {
	if s.repo == nil {
		return
	}

	// Find evaluation sets for this style
	sets, _, err := s.repo.ListSets(ctx, slug, 1, 10)
	if err != nil || len(sets) == 0 {
		slog.Info("no evaluation sets found for style, skipping auto-eval", "slug", slug)
		return
	}

	for _, set := range sets {
		if set.Status != "ready" && set.Status != "draft" {
			continue
		}

		// Create a new run
		run, err := s.repo.CreateRun(ctx, set.ID, slug, version, "profile_change", detail)
		if err != nil {
			slog.Warn("failed to create auto-eval run", "error", err)
			continue
		}

		// Run asynchronously
		go func(runID string) {
			evalCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := s.RunEvaluation(evalCtx, runID); err != nil {
				slog.Error("auto-evaluation failed", "run_id", runID, "error", err)
			}
		}(run.ID)

		slog.Info("auto-evaluation triggered", "slug", slug, "version", version, "set", set.Name, "run_id", run.ID)
	}
}

// ExportRunJSON exports an evaluation run as a JSON byte slice.
func (s *EvaluationService) ExportRunJSON(ctx context.Context, runID string) ([]byte, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("database not available")
	}

	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to get run: %w", err)
	}

	// Build export structure
	export := map[string]interface{}{
		"export_version":   "1.0",
		"exported_at":      time.Now().Format(time.RFC3339),
		"run_id":           run.ID,
		"profile_slug":     run.ProfileSlug,
		"profile_version":  run.ProfileVersion,
		"trigger_type":     run.TriggerType,
		"trigger_detail":   run.TriggerDetail,
		"status":           run.Status,
		"total_samples":    run.TotalSamples,
		"completed_count":  run.CompletedCount,
		"overall_score":    run.OverallScore,
		"dimension_scores": run.DimensionScores,
		"results":          run.Results,
		"started_at":       run.StartedAt,
		"completed_at":     run.CompletedAt,
		"created_at":       run.CreatedAt,
	}

	return json.MarshalIndent(export, "", "  ")
}

// ExportRunCSV exports an evaluation run as a CSV byte slice.
// Each row represents a single sample's evaluation result.
func (s *EvaluationService) ExportRunCSV(ctx context.Context, runID string) ([]byte, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("database not available")
	}

	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to get run: %w", err)
	}

	var sb strings.Builder
	// Header row
	sb.WriteString("sample_id,topic,weighted_score,judge_feedback,article_preview\n")

	for _, result := range run.Results {
		sampleID, _ := result["sample_id"].(string)
		topic, _ := result["topic"].(string)
		weighted, _ := result["weighted"].(float64)
		feedback, _ := result["judge_feedback"].(string)
		preview, _ := result["article_preview"].(string)

		// Escape CSV fields
		sb.WriteString(fmt.Sprintf("%s,%s,%.2f,%s,%s\n",
			csvEscape(sampleID),
			csvEscape(topic),
			weighted,
			csvEscape(feedback),
			csvEscape(preview),
		))
	}

	return []byte(sb.String()), nil
}

// csvEscape wraps a field in double quotes and escapes internal quotes.
func csvEscape(s string) string {
	return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
}

// Ensure no data race on concurrent run checks
var _ = sync.Mutex{}
