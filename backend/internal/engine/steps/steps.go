package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── IntentStep ──────────────────────────────────────────

var voiceNormalizationPairs = []struct {
	pattern *regexp.Regexp
	replace string
}{
	{regexp.MustCompile(`寄予热搜`), "基于热搜"},
	{regexp.MustCompile(`给予热搜`), "基于热搜"},
	{regexp.MustCompile(`基于热手`), "基于热搜"},
	{regexp.MustCompile(`热收`), "热搜"},
	{regexp.MustCompile(`斜稿`), "写稿"},
	{regexp.MustCompile(`血稿`), "写稿"},
	{regexp.MustCompile(`携稿`), "写稿"},
	{regexp.MustCompile(`协稿`), "写稿"},
	{regexp.MustCompile(`斜一篇`), "写一篇"},
	{regexp.MustCompile(`斜一份`), "写一份"},
	{regexp.MustCompile(`平乱`), "评论"},
}

var intentLabels = []string{"chat", "writing", "polish", "shorten", "expand", "extract_points"}

var (
	reExtractPoints = regexp.MustCompile(`提炼核心观点|提取核心观点|核心观点|观点提炼`)
	reShorten       = regexp.MustCompile(`缩写|缩短|压缩|简写|精简到|压到|控制到|摘要`)
	reExpand        = regexp.MustCompile(`扩写|扩充|展开|补充论证|丰富|拓展`)
	rePolish        = regexp.MustCompile(`润色|修改|优化|改写|完善|提升|调整|润饰|打磨`)
	reWriting       = regexp.MustCompile(`写一篇|写一份|写篇|写份|写稿|撰写|拟写|拟稿|撰稿|撰文|成文|出稿|命题|为题|题为`)
)

type IntentStep struct {
	llm *tools.LLMClient
}

func NewIntentStep(llm *tools.LLMClient) *IntentStep {
	return &IntentStep{llm: llm}
}

func (s *IntentStep) Name() engine.StepName { return engine.StepIntent }
func (s *IntentStep) CanPause() bool         { return false }

func (s *IntentStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	message := execCtx.UserInput

	// Normalize voice input
	normalized := message
	for _, pair := range voiceNormalizationPairs {
		normalized = pair.pattern.ReplaceAllString(normalized, pair.replace)
	}
	execCtx.NormalizedInput = normalized

	// Rule-based scoring
	scores := make(map[string]int)
	for _, label := range intentLabels {
		scores[label] = 0
	}

	compact := strings.ReplaceAll(normalized, " ", "")
	compact = strings.TrimSpace(compact)

	if compact == "" {
		scores["chat"] += 80
	}

	if execCtx.Mode == "writing" {
		scores["writing"] += 120
	}

	if reExtractPoints.MatchString(normalized) {
		scores["extract_points"] += 92
	}
	if reShorten.MatchString(normalized) {
		scores["shorten"] += 88
	}
	if reExpand.MatchString(normalized) {
		scores["expand"] += 84
	}
	if rePolish.MatchString(normalized) {
		scores["polish"] += 68
	}
	if reWriting.MatchString(normalized) {
		scores["writing"] += 95
	}

	// Determine top intent
	topLabel := "chat"
	topScore := 0
	for _, label := range intentLabels {
		if scores[label] > topScore {
			topScore = scores[label]
			topLabel = label
		}
	}

	// Calculate confidence
	confidence := 0.35
	if topScore > 0 {
		confidence = 0.48 + float64(topScore)/160*0.42
		if confidence > 0.98 {
			confidence = 0.98
		}
	}

	taskIntent := &engine.TaskIntent{
		TaskMode:        topLabel,
		Confidence:      confidence,
		Source:          "rules",
		NormalizedInput: normalized,
	}

	// Use LLM for low-confidence cases
	if confidence < 0.78 && s.llm != nil {
		llmIntent, err := s.classifyWithLLM(ctx, normalized, topLabel)
		if err == nil && llmIntent != nil {
			taskIntent = llmIntent
		}
	}

	execCtx.TaskIntent = taskIntent
	return nil
}

func (s *IntentStep) classifyWithLLM(ctx context.Context, message, fallback string) (*engine.TaskIntent, error) {
	systemMsg := "你是中文写作产品的意图分类器。只返回 JSON，不要解释。taskMode 只能是 chat、writing、polish、shorten、expand、extract_points。confidence 是 0.35-0.96 的数字。"
	userMsg := fmt.Sprintf(`请判断用户本轮真实意图，尤其要容忍语音识别错字。

规则提示：
- 写稿、写文章、写评论、基于热搜/热榜评价某事 => writing
- 润色、优化、调整表达、二次打磨已有文章 => polish
- 缩写、压缩、精简到更短 => shorten
- 扩写、展开、补充论证 => expand
- 提炼核心观点、提取观点 => extract_points
- 普通问答/闲聊 => chat

用户输入：
%s

返回格式：
{"taskMode":"writing","confidence":0.9,"reason":"..."}`, message)

	resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0))
	if err != nil {
		return nil, err
	}

	jsonStr := tools.ExtractJSONObject(resp)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON in response")
	}

	var parsed struct {
		TaskMode   string  `json:"taskMode"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, err
	}

	// Validate taskMode
	valid := false
	for _, label := range intentLabels {
		if parsed.TaskMode == label {
			valid = true
			break
		}
	}
	if !valid {
		parsed.TaskMode = fallback
	}

	if parsed.Confidence < 0.35 {
		parsed.Confidence = 0.72
	}
	if parsed.Confidence > 0.96 {
		parsed.Confidence = 0.96
	}

	return &engine.TaskIntent{
		TaskMode:        parsed.TaskMode,
		Confidence:      parsed.Confidence,
		Source:          "llm",
		NormalizedInput: message,
	}, nil
}

// ─── QueryPlanStep ───────────────────────────────────────

type QueryPlanStep struct {
	llm *tools.LLMClient
}

func NewQueryPlanStep(llm *tools.LLMClient) *QueryPlanStep {
	return &QueryPlanStep{llm: llm}
}

func (s *QueryPlanStep) Name() engine.StepName { return engine.StepQueryPlan }
func (s *QueryPlanStep) CanPause() bool         { return false }

// ShouldSkip returns true for intents that don't need search/query planning.
// chat: conversational, no writing pipeline
// polish: operates on existing text, no search
// shorten: operates on existing text, no search
// expand: operates on existing text, no search
// extract_points: operates on existing text, no search
func (s *QueryPlanStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	if execCtx.TaskIntent == nil {
		return false
	}
	switch execCtx.TaskIntent.TaskMode {
	case "chat", "polish", "shorten", "expand", "extract_points":
		return true
	}
	return false
}

func (s *QueryPlanStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if execCtx.TaskIntent == nil {
		return fmt.Errorf("task intent not set")
	}

	// Skip search for chat mode
	if execCtx.TaskIntent.TaskMode == "chat" {
		execCtx.SearchPlan = []engine.SearchPlanEntry{}
		return nil
	}

	// For polish mode, skip search
	if execCtx.TaskIntent.TaskMode == "polish" {
		execCtx.SearchPlan = []engine.SearchPlanEntry{}
		return nil
	}

	// Extract topic from input
	topic := extractTopic(execCtx.NormalizedInput)
	execCtx.WritingTask = &engine.WritingTask{
		Topic:              topic,
		PrimarySearchQuery: topic,
		SearchQueries:      []string{topic},
	}

	// Plan search across sources
	execCtx.SearchPlan = []engine.SearchPlanEntry{
		{Query: topic, Source: "tavily"},
		{Query: topic, Source: "zhihu"},
		{Query: topic, Source: "ima"},
	}

	return nil
}

func extractTopic(message string) string {
	// Remove common prefixes
	topic := message
	prefixes := []string{
		"请基于热搜写一篇关于", "基于热搜写一篇关于",
		"请写一篇关于", "写一篇关于",
		"请写一篇", "写一篇",
		"请帮我写一篇关于", "帮我写一篇关于",
		"帮我写一篇", "请帮我写一篇",
		"写一篇评论关于", "写一篇评论",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(topic, prefix) {
			topic = strings.TrimPrefix(topic, prefix)
			break
		}
	}

	// Remove common suffixes
	suffixes := []string{"的评论", "的文章", "评论", "文章", "的时评", "时评"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(topic, suffix) {
			topic = strings.TrimSuffix(topic, suffix)
			break
		}
	}

	topic = strings.TrimSpace(topic)
	if topic == "" {
		topic = message
	}
	return topic
}

// ─── SearchStep ──────────────────────────────────────────

type SearchStep struct {
	llm    *tools.LLMClient
	search *tools.SearchClient
}

func NewSearchStep(llm *tools.LLMClient, search *tools.SearchClient) *SearchStep {
	return &SearchStep{llm: llm, search: search}
}

func (s *SearchStep) Name() engine.StepName { return engine.StepSearch }
func (s *SearchStep) CanPause() bool         { return true }

// ShouldSkip returns true for intents that don't need search.
// Same set as QueryPlanStep — if no query plan was created, no search is needed.
func (s *SearchStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	if execCtx.TaskIntent == nil {
		return false
	}
	switch execCtx.TaskIntent.TaskMode {
	case "chat", "polish", "shorten", "expand", "extract_points":
		return true
	}
	return false
}

func (s *SearchStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if len(execCtx.SearchPlan) == 0 {
		execCtx.SearchResults = []engine.SearchResult{}
		return nil
	}

	// Check pause before starting search
	if err := execCtx.CheckPause(ctx, emitter, engine.StepSearch); err != nil {
		return err
	}

	// Use real search client if available
	if s.search != nil && s.search.HasSources() {
		query := ""
		if execCtx.WritingTask != nil {
			query = execCtx.WritingTask.PrimarySearchQuery
			if query == "" && len(execCtx.WritingTask.SearchQueries) > 0 {
				query = execCtx.WritingTask.SearchQueries[0]
			}
		}
		if query == "" {
			query = execCtx.UserInput
		}

		results := s.search.Search(ctx, query, 9)
		execCtx.SearchResults = results
		return nil
	}

	// Fallback: use LLM to generate mock search results
	if execCtx.WritingTask != nil && s.llm != nil {
		results := s.generateMockResults(ctx, execCtx.WritingTask.Topic)
		execCtx.SearchResults = results
	}

	return nil
}

func (s *SearchStep) generateMockResults(ctx context.Context, topic string) []engine.SearchResult {
	// Generate search context using LLM
	systemMsg := "你是搜索结果摘要生成器。根据话题生成3-5条相关搜索结果摘要，每条包含标题和摘要。只返回 JSON 数组。"
	userMsg := fmt.Sprintf(`话题：%s

返回格式：
[{"title":"...","snippet":"...","source":"web"}]`, topic)

	resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0.3))
	if err != nil {
		return []engine.SearchResult{}
	}

	jsonStr := tools.ExtractJSONObject(resp)
	if jsonStr == "" {
		// Try array extraction
		start := strings.Index(resp, "[")
		end := strings.LastIndex(resp, "]")
		if start >= 0 && end > start {
			jsonStr = resp[start : end+1]
		}
	}
	if jsonStr == "" {
		return []engine.SearchResult{}
	}

	// Handle array vs object
	var results []engine.SearchResult
	if strings.HasPrefix(strings.TrimSpace(jsonStr), "[") {
		if err := json.Unmarshal([]byte(jsonStr), &results); err != nil {
			return []engine.SearchResult{}
		}
	} else {
		var obj struct {
			Results []engine.SearchResult `json:"results"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
			return []engine.SearchResult{}
		}
		results = obj.Results
	}

	// Ensure source is set — mark as LLM-generated, NOT real search
	for i := range results {
		results[i].Source = "llm_generated"
		results[i].IsMock = true
	}

	return results
}

// ─── RelevanceStep ───────────────────────────────────────

type RelevanceStep struct {
	embedding *tools.EmbeddingClient // optional, for semantic dedup
}

func NewRelevanceStep() *RelevanceStep { return &RelevanceStep{} }

// NewRelevanceStepWithEmbedding creates a RelevanceStep with semantic dedup support.
func NewRelevanceStepWithEmbedding(emb *tools.EmbeddingClient) *RelevanceStep {
	return &RelevanceStep{embedding: emb}
}

func (s *RelevanceStep) Name() engine.StepName { return engine.StepRelevance }
func (s *RelevanceStep) CanPause() bool         { return false }

// ShouldSkip returns true for intents that don't produce search results.
func (s *RelevanceStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	if execCtx.TaskIntent == nil {
		return false
	}
	switch execCtx.TaskIntent.TaskMode {
	case "chat", "polish", "shorten", "expand", "extract_points":
		return true
	}
	return false
}

func (s *RelevanceStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if len(execCtx.SearchResults) == 0 {
		return nil
	}

	// Extract keywords from the writing task topic for relevance scoring
	topic := ""
	if execCtx.WritingTask != nil {
		topic = execCtx.WritingTask.Topic
	}
	topicKeywords := extractKeywords(topic)

	// Score each search result
	for i := range execCtx.SearchResults {
		result := &execCtx.SearchResults[i]
		score := computeRelevanceScore(topic, topicKeywords, result)

		// Assign relevance level based on score
		switch {
		case score >= 0.7:
			result.Relevance = "strong"
		case score >= 0.4:
			result.Relevance = "medium"
		case score >= 0.2:
			result.Relevance = "weak"
		default:
			result.Relevance = "weak"
		}
		result.Score = score
	}

	// Deduplicate search results — use semantic dedup if embedding client is available
	if s.embedding != nil && s.embedding.IsConfigured() {
		execCtx.SearchResults = deduplicateResultsSemantic(ctx, execCtx.SearchResults, s.embedding)
	} else {
		execCtx.SearchResults = deduplicateResults(execCtx.SearchResults)
	}

	return nil
}

// extractKeywords splits a Chinese/English topic into keywords.
func extractKeywords(topic string) []string {
	if topic == "" {
		return nil
	}
	// Simple approach: split by spaces and common Chinese delimiters
	delimiters := " ，,。.、；;！!？?/\\()（）[]【】"
	fields := strings.FieldsFunc(topic, func(r rune) bool {
		return strings.ContainsRune(delimiters, r)
	})

	var keywords []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if len([]rune(f)) >= 2 {
			keywords = append(keywords, strings.ToLower(f))
		}
	}
	return keywords
}

// computeRelevanceScore calculates a 0-1 relevance score for a search result.
func computeRelevanceScore(topic string, keywords []string, result *engine.SearchResult) float64 {
	if topic == "" {
		return 0.5 // default medium when no topic
	}

	text := strings.ToLower(result.Title + " " + result.Snippet)
	topicLower := strings.ToLower(topic)

	// 1. Exact topic match in title (highest signal)
	titleLower := strings.ToLower(result.Title)
	if strings.Contains(titleLower, topicLower) {
		return 0.9
	}

	// 2. Keyword overlap score
	if len(keywords) == 0 {
		return 0.4
	}

	matched := 0
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			matched++
		}
	}
	keywordScore := float64(matched) / float64(len(keywords))

	// 3. Title keyword match (weighted higher)
	titleMatched := 0
	for _, kw := range keywords {
		if strings.Contains(titleLower, kw) {
			titleMatched++
		}
	}
	titleScore := float64(titleMatched) / float64(len(keywords))

	// Weighted combination: title match (50%) + content match (50%)
	finalScore := titleScore*0.5 + keywordScore*0.5

	// Boost if both title and content have matches
	if titleMatched > 0 && matched > 0 {
		finalScore += 0.1
	}

	if finalScore > 1.0 {
		finalScore = 1.0
	}

	return finalScore
}

// deduplicateResults removes near-duplicate search results based on title similarity.
func deduplicateResults(results []engine.SearchResult) []engine.SearchResult {
	if len(results) <= 1 {
		return results
	}

	var deduped []engine.SearchResult
	for _, r := range results {
		isDup := false
		for j := range deduped {
			if isSimilarTitle(r.Title, deduped[j].Title) {
				isDup = true
				// Keep the one with higher score
				if r.Score > deduped[j].Score {
					deduped[j] = r
				}
				break
			}
		}
		if !isDup {
			deduped = append(deduped, r)
		}
	}

	return deduped
}

// isSimilarTitle checks if two titles are similar enough to be duplicates.
// Uses a simple character overlap ratio (Jaccard-like similarity).
func isSimilarTitle(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))

	// Exact match
	if a == b {
		return true
	}

	// One contains the other (for truncated titles)
	if len(a) > 10 && strings.Contains(b, a) {
		return true
	}
	if len(b) > 10 && strings.Contains(a, b) {
		return true
	}

	// Character-level Jaccard similarity for Chinese text
	setA := make(map[rune]bool)
	for _, r := range a {
		setA[r] = true
	}
	setB := make(map[rune]bool)
	for _, r := range b {
		setB[r] = true
	}

	intersection := 0
	for r := range setA {
		if setB[r] {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection

	if union == 0 {
		return false
	}

	similarity := float64(intersection) / float64(union)
	return similarity > 0.7
}

// ─── Semantic Deduplication (pgvector / embedding-based) ──

// deduplicateResultsSemantic removes near-duplicate search results using
// embedding cosine similarity. Falls back to text-based dedup if embedding fails.
//
// 文档来源: docs/02-database-schema.md — pgvector 语义去重
func deduplicateResultsSemantic(ctx context.Context, results []engine.SearchResult, emb *tools.EmbeddingClient) []engine.SearchResult {
	if len(results) <= 1 || emb == nil || !emb.IsConfigured() {
		return deduplicateResults(results)
	}

	// Build texts for embedding (title + snippet)
	texts := make([]string, len(results))
	for i, r := range results {
		texts[i] = r.Title + " " + r.Snippet
		if len(texts[i]) > 500 {
			texts[i] = texts[i][:500] // truncate to avoid excessive token usage
		}
	}

	// Generate embeddings
	embeddings, _, err := emb.Embed(ctx, texts)
	if err != nil || len(embeddings) != len(results) {
		slog.Warn("semantic dedup: embedding failed, falling back to text dedup", "error", err)
		return deduplicateResults(results)
	}

	// Semantic similarity threshold (cosine similarity)
	const semanticThreshold = 0.85

	var deduped []engine.SearchResult
	dedupedEmbeddings := make([][]float64, 0, len(results))

	for i, r := range results {
		isDup := false
		for j := range deduped {
			sim := cosineSimilarity(embeddings[i], dedupedEmbeddings[j])
			if sim >= semanticThreshold {
				isDup = true
				// Keep the one with higher score
				if r.Score > deduped[j].Score {
					deduped[j] = r
					dedupedEmbeddings[j] = embeddings[i]
				}
				break
			}
		}
		if !isDup {
			deduped = append(deduped, r)
			dedupedEmbeddings = append(dedupedEmbeddings, embeddings[i])
		}
	}

	slog.Debug("semantic dedup completed",
		"input", len(results),
		"output", len(deduped),
		"removed", len(results)-len(deduped))

	return deduped
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// ─── OutlineStep (guided mode) ───────────────────────────

type OutlineStep struct {
	llm *tools.LLMClient
}

func NewOutlineStep(llm *tools.LLMClient) *OutlineStep {
	return &OutlineStep{llm: llm}
}

func (s *OutlineStep) Name() engine.StepName { return engine.StepOutline }
func (s *OutlineStep) CanPause() bool         { return true }

// ShouldSkip returns true for intents that don't need an outline.
// Also skips when WritingTask is nil (e.g. when QueryPlanStep was skipped).
func (s *OutlineStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	if execCtx.TaskIntent == nil {
		return false
	}
	switch execCtx.TaskIntent.TaskMode {
	case "chat", "polish", "shorten", "expand", "extract_points":
		return true
	}
	if execCtx.WritingTask == nil {
		return true
	}
	return false
}

func (s *OutlineStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	// Only for guided mode
	if execCtx.Mode != "guided" {
		return nil
	}

	if s.llm == nil {
		return fmt.Errorf("LLM client not available")
	}

	for attempt := 0; attempt < 5; attempt++ {
		// Generate outline using LLM (higher temperature on regenerate for variety)
		temp := 0.3 + float64(attempt)*0.15
		if temp > 0.8 {
			temp = 0.8
		}

		outline, err := s.generateOutline(ctx, execCtx, temp)
		if err != nil {
			return err
		}

		execCtx.Outline = outline

		// Emit await_input and wait for user confirmation
		emitter.AwaitInput(engine.StepOutline, outline, []string{"confirm", "edit", "regenerate"}, attempt+1, 5)

		// Wait for user confirmation
		confirmedData, err := execCtx.WaitForConfirm(ctx)
		if err != nil {
			return err
		}

		// Check if user requested regeneration
		if confirmedData != nil {
			if action, ok := confirmedData["action"].(string); ok && action == "regenerate" {
				continue // loop back to generate a new outline
			}

			// User provided modified/confirmed data — update the outline
			if title, ok := confirmedData["title"].(string); ok {
				outline.Title = title
			}
			if outlineArr, ok := confirmedData["outline"].([]interface{}); ok {
				items := make([]engine.OutlineItem, 0, len(outlineArr))
				for _, item := range outlineArr {
					if m, ok := item.(map[string]interface{}); ok {
						items = append(items, engine.OutlineItem{
							Point: getString(m, "point"),
							Type:  getString(m, "type"),
						})
					}
				}
				if len(items) > 0 {
					outline.Outline = items
				}
			}
			execCtx.Outline = outline
		}

		return nil
	}

	return fmt.Errorf("outline regeneration limit exceeded")
}

// generateOutline calls the LLM to produce an outline for the current topic.
func (s *OutlineStep) generateOutline(ctx context.Context, execCtx *engine.ExecutionContext, temperature float64) (*engine.OutlineData, error) {
	systemMsg := "你是写作提纲生成器。根据话题和素材生成文章提纲。只返回 JSON。"
	userMsg := fmt.Sprintf(`话题：%s

请生成提纲，包含标题和3-5个要点。

返回格式：
{
  "title": "文章标题",
  "outline": [
    {"point": "要点内容", "type": "opening"},
    {"point": "要点内容", "type": "argument"},
    {"point": "要点内容", "type": "conclusion"}
  ]
}`, execCtx.WritingTask.Topic)

	resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(temperature))
	if err != nil {
		return nil, fmt.Errorf("outline generation failed: %w", err)
	}

	jsonStr := tools.ExtractJSONObject(resp)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON in outline response")
	}

	var outline engine.OutlineData
	if err := json.Unmarshal([]byte(jsonStr), &outline); err != nil {
		return nil, fmt.Errorf("failed to parse outline: %w", err)
	}

	return &outline, nil
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// ─── WriteStep ───────────────────────────────────────────

type WriteStep struct {
	llm     *tools.LLMClient
	profile *profile.StyleProfile
}

func NewWriteStep(llm *tools.LLMClient) *WriteStep {
	return &WriteStep{llm: llm}
}

// NewWriteStepWithProfile creates a WriteStep with a style profile.
func NewWriteStepWithProfile(llm *tools.LLMClient, p *profile.StyleProfile) *WriteStep {
	return &WriteStep{llm: llm, profile: p}
}

func (s *WriteStep) Name() engine.StepName { return engine.StepWrite }
func (s *WriteStep) CanPause() bool         { return true }

// ShouldSkip returns true for chat intent — chat is handled by ChatStep.
func (s *WriteStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	return execCtx.TaskIntent != nil && execCtx.TaskIntent.TaskMode == "chat"
}

func (s *WriteStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if s.llm == nil {
		return fmt.Errorf("LLM client not available")
	}

	// Determine task mode
	taskMode := "writing"
	if execCtx.TaskIntent != nil && execCtx.TaskIntent.TaskMode != "" {
		taskMode = execCtx.TaskIntent.TaskMode
	}

	// Build system prompt — use profile's system prompt if available
	systemPrompt := "你是一个专业的中文写作助手。请根据用户的要求生成高质量的文章。输出 Markdown 格式，以 ## 开头作为标题。"
	if s.profile != nil && s.profile.SystemPrompt != "" {
		systemPrompt = s.profile.SystemPrompt
	}

	// Build user prompt — differentiated by task mode
	var promptBuilder strings.Builder
	s.buildTaskPrompt(&promptBuilder, taskMode, execCtx)

	// Add search results as context (only for writing mode — other modes operate on existing text)
	if taskMode == "writing" && len(execCtx.SearchResults) > 0 {
		hasMockResults := false
		for _, result := range execCtx.SearchResults {
			if result.IsMock {
				hasMockResults = true
				break
			}
		}
		if hasMockResults {
			promptBuilder.WriteString("\n⚠️ 参考素材（注意：以下为 AI 生成的背景参考，非真实搜索结果，使用前请核实事实）：\n")
		} else {
			promptBuilder.WriteString("\n参考素材：\n")
		}
		for i, result := range execCtx.SearchResults {
			promptBuilder.WriteString(fmt.Sprintf("%d. %s\n   %s\n\n", i+1, result.Title, result.Snippet))
		}
	}

	// Add outline if available (guided mode, writing only)
	if taskMode == "writing" && execCtx.Outline != nil {
		promptBuilder.WriteString(fmt.Sprintf("\n标题：%s\n", execCtx.Outline.Title))
		promptBuilder.WriteString("提纲：\n")
		for i, item := range execCtx.Outline.Outline {
			promptBuilder.WriteString(fmt.Sprintf("%d. %s (%s)\n", i+1, item.Point, item.Type))
		}
	}

	// Add word limit — use length_profiles per task type if available, fall back to word_range
	s.appendWordLimit(&promptBuilder, taskMode, execCtx)

	// Add structure requirements from profile (writing only)
	if taskMode == "writing" && s.profile != nil && s.profile.Structure.Type != "" {
		promptBuilder.WriteString(fmt.Sprintf("\n结构要求：%s", s.profile.Structure.Opening))
		if s.profile.Structure.Body != "" {
			promptBuilder.WriteString(fmt.Sprintf(" → %s", s.profile.Structure.Body))
		}
		if s.profile.Structure.Conclusion != "" {
			promptBuilder.WriteString(fmt.Sprintf(" → %s", s.profile.Structure.Conclusion))
		}
		promptBuilder.WriteString("\n")
		if s.profile.Structure.ArgumentPattern != "" {
			promptBuilder.WriteString(fmt.Sprintf("论证模式：%s\n", s.profile.Structure.ArgumentPattern))
		}
	}

	// Add rhetoric requirements from profile (writing only)
	if taskMode == "writing" && s.profile != nil {
		var rhetoricParts []string
		if s.profile.Rhetoric.RequiredMetaphor && s.profile.Rhetoric.MetaphorDescription != "" {
			rhetoricParts = append(rhetoricParts, "核心比喻: "+s.profile.Rhetoric.MetaphorDescription)
		}
		if s.profile.Rhetoric.RequiredParallelism {
			rhetoricParts = append(rhetoricParts, "必须使用排比")
		}
		if s.profile.Rhetoric.RequiredRhetoricalQuestion {
			rhetoricParts = append(rhetoricParts, "必须使用设问")
		}
		if len(rhetoricParts) > 0 {
			promptBuilder.WriteString(fmt.Sprintf("\n修辞要求：%s\n", strings.Join(rhetoricParts, "；")))
		}
	}

	// Add title guidelines from profile
	if s.profile != nil && len(s.profile.TitleGuidelines.ForbiddenPatterns) > 0 {
		promptBuilder.WriteString(fmt.Sprintf("\n标题禁止模式（正则）：%s\n", strings.Join(s.profile.TitleGuidelines.ForbiddenPatterns, ", ")))
	}
	if s.profile != nil && s.profile.TitleGuidelines.Style != "" {
		promptBuilder.WriteString(fmt.Sprintf("标题风格要求：%s\n", s.profile.TitleGuidelines.Style))
	}
	if s.profile != nil && len(s.profile.TitleGuidelines.Examples) > 0 {
		promptBuilder.WriteString(fmt.Sprintf("标题参考示例：%s\n", strings.Join(s.profile.TitleGuidelines.Examples, " / ")))
	}

	// Add fact guard requirements from profile
	if s.profile != nil && len(s.profile.FactGuard.ForbiddenResults) > 0 {
		promptBuilder.WriteString(fmt.Sprintf("\n事实红线——禁止使用以下表述（已完成事件不得用结果性动词）：%s\n", strings.Join(s.profile.FactGuard.ForbiddenResults, ", ")))
	}
	if s.profile != nil && len(s.profile.FactGuard.FutureTenseRequired) > 0 {
		promptBuilder.WriteString(fmt.Sprintf("事实红线——未发生事件须使用以下时态标记：%s\n", strings.Join(s.profile.FactGuard.FutureTenseRequired, ", ")))
	}
	if s.profile != nil && s.profile.FactGuard.UserMaterialPriority {
		promptBuilder.WriteString("事实红线——用户提供的素材优先于 AI 检索结果，如有冲突以用户素材为准\n")
	}

	// Add value orientation keywords from profile
	if taskMode == "writing" && s.profile != nil && len(s.profile.ValueOrientation.Keywords) > 0 {
		promptBuilder.WriteString(fmt.Sprintf("\n价值导向关键词（适当融入）：%s\n", strings.Join(s.profile.ValueOrientation.Keywords, ", ")))
	}

	// Add user materials if provided
	if len(execCtx.UserMaterials) > 0 {
		promptBuilder.WriteString("\n用户提供的素材：\n")
		for i, mat := range execCtx.UserMaterials {
			promptBuilder.WriteString(fmt.Sprintf("%d. %s\n\n", i+1, mat))
		}
		promptBuilder.WriteString("（用户素材优先级高于 AI 检索结果）\n")
	}

	// Add output format requirements
	if s.profile != nil && s.profile.OutputFormat.UseMarkdown {
		promptBuilder.WriteString(fmt.Sprintf("\n输出格式：Markdown，标题以 %s 开头\n", s.profile.OutputFormat.TitlePrefix))
	}

	// Stream the article
	messages := []tools.LLMMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: promptBuilder.String()},
	}

	fullText, tokens, err := s.llm.ChatStream(ctx, messages, func(delta string) {
		emitter.StreamDelta(delta)
		// Check pause between stream chunks
		if err := execCtx.CheckPause(ctx, emitter, engine.StepWrite); err != nil {
			return // stream will continue, but pause was handled
		}
	})
	if err != nil {
		return fmt.Errorf("article generation failed: %w", err)
	}

	execCtx.Article = fullText
	execCtx.TotalTokens += tokens

	// Emit stream done
	emitter.StreamDone(fullText)

	return nil
}

// buildTaskPrompt constructs the core prompt instruction differentiated by task mode.
func (s *WriteStep) buildTaskPrompt(b *strings.Builder, taskMode string, execCtx *engine.ExecutionContext) {
	normalizedInput := execCtx.NormalizedInput
	if normalizedInput == "" {
		normalizedInput = execCtx.UserInput
	}

	switch taskMode {
	case "polish":
		b.WriteString("请对以下文章进行润色优化。保持原文的核心观点和结构不变，重点优化语言表达、修辞效果和行文流畅度。\n\n")
		b.WriteString("原文：\n")
		b.WriteString(normalizedInput)
		b.WriteString("\n\n")
		b.WriteString("润色要求：\n")
		b.WriteString("1. 保持原文的核心论点和逻辑结构\n")
		b.WriteString("2. 优化遣词造句，提升表达力\n")
		b.WriteString("3. 补充必要的修辞手法（如适用风格 Profile 中的要求）\n")
		b.WriteString("4. 修正语病、冗余和不流畅之处\n")
		b.WriteString("5. 标题如有需要可微调，但不可改变主旨\n\n")

	case "shorten":
		b.WriteString("请将以下文章缩短到指定字数范围内。保持核心观点和关键论证完整，删除冗余表述和重复论证。\n\n")
		b.WriteString("原文：\n")
		b.WriteString(normalizedInput)
		b.WriteString("\n\n")
		b.WriteString("缩写要求：\n")
		b.WriteString("1. 保留原文的核心论点和主要论据\n")
		b.WriteString("2. 删除冗余表述、重复论证和过度展开\n")
		b.WriteString("3. 保持文章的逻辑连贯性\n")
		b.WriteString("4. 保持原文的风格和语气\n")
		b.WriteString("5. 标题保持不变或微调\n\n")

	case "expand":
		b.WriteString("请将以下文章扩充到指定字数范围内。在不改变核心观点的前提下，增加论证深度、补充论据和细节。\n\n")
		b.WriteString("原文：\n")
		b.WriteString(normalizedInput)
		b.WriteString("\n\n")
		b.WriteString("扩写要求：\n")
		b.WriteString("1. 保持原文的核心论点和结构框架\n")
		b.WriteString("2. 增加论证深度，补充具体论据和案例\n")
		b.WriteString("3. 丰富修辞手法，增强表达力\n")
		b.WriteString("4. 保持原文的风格和语气\n")
		b.WriteString("5. 扩充内容须与主题紧密相关，不可偏题\n\n")

	case "extract_points":
		b.WriteString("请从以下文章中提炼核心观点，以结构化的方式输出。\n\n")
		b.WriteString("原文：\n")
		b.WriteString(normalizedInput)
		b.WriteString("\n\n")
		b.WriteString("提取要求：\n")
		b.WriteString("1. 提炼 3-5 个核心观点，按重要性排序\n")
		b.WriteString("2. 每个观点用一句话概括，附简要说明\n")
		b.WriteString("3. 保持原文的价值立场，不添加新观点\n")
		b.WriteString("4. 输出格式：\n")
		b.WriteString("## 核心观点\n\n")
		b.WriteString("1. **观点一**：概括（说明）\n")
		b.WriteString("2. **观点二**：概括（说明）\n")
		b.WriteString("3. **观点三**：概括（说明）\n\n")

	default: // writing
		topic := execCtx.UserInput
		if execCtx.WritingTask != nil && execCtx.WritingTask.Topic != "" {
			topic = execCtx.WritingTask.Topic
		}
		b.WriteString("请写一篇文章。\n\n")
		b.WriteString(fmt.Sprintf("话题：%s\n\n", topic))
	}
}

// appendWordLimit adds word limit instructions, preferring length_profiles per task type.
func (s *WriteStep) appendWordLimit(b *strings.Builder, taskMode string, execCtx *engine.ExecutionContext) {
	// Try length_profiles first (per-task-type word ranges)
	if s.profile != nil && s.profile.LengthProfiles != nil {
		var key string
		switch taskMode {
		case "polish", "shorten", "expand":
			// Use polish_short or polish_long based on current length
			if execCtx.WordLimit > 0 && execCtx.WordLimit <= 600 {
				key = "polish_short"
			} else {
				key = "polish_long"
			}
		case "writing":
			key = "writing"
		}

		if wr, ok := s.profile.LengthProfiles[key]; ok && wr.Max > 0 {
			b.WriteString(fmt.Sprintf("\n字数要求：%d-%d字\n", wr.Min, wr.Max))
			if wr.HardLimit {
				b.WriteString("（字数限制为硬性要求，超出范围将不合格）\n")
			}
			return
		}
	}

	// Fall back to profile's global word_range
	if s.profile != nil && s.profile.WordRange.Max > 0 {
		b.WriteString(fmt.Sprintf("\n字数要求：%d-%d字\n", s.profile.WordRange.Min, s.profile.WordRange.Max))
		if s.profile.WordRange.HardLimit {
			b.WriteString("（字数限制为硬性要求，超出范围将不合格）\n")
		}
		return
	}

	// Fall back to execCtx.WordLimit
	if execCtx.WordLimit > 0 {
		b.WriteString(fmt.Sprintf("\n字数要求：约%d字\n", execCtx.WordLimit))
	}
}

// ─── PostReviewStep ──────────────────────────────────────

type PostReviewStep struct {
	llm             *tools.LLMClient
	sensitiveCheck  engine.SensitiveChecker
	profile         *profile.StyleProfile
}

func NewPostReviewStep(llm *tools.LLMClient) *PostReviewStep {
	return &PostReviewStep{llm: llm}
}

// NewPostReviewStepWithSensitiveCheck creates a PostReviewStep with sensitive word checking.
func NewPostReviewStepWithSensitiveCheck(llm *tools.LLMClient, sc engine.SensitiveChecker) *PostReviewStep {
	return &PostReviewStep{llm: llm, sensitiveCheck: sc}
}

// NewPostReviewStepWithProfile creates a PostReviewStep with a style profile for
// fact_guard / title_guidelines enforcement.
func NewPostReviewStepWithProfile(llm *tools.LLMClient, sc engine.SensitiveChecker, p *profile.StyleProfile) *PostReviewStep {
	return &PostReviewStep{llm: llm, sensitiveCheck: sc, profile: p}
}

func (s *PostReviewStep) Name() engine.StepName { return engine.StepPostReview }
func (s *PostReviewStep) CanPause() bool         { return false }

// ShouldSkip returns true for chat intent — chat responses don't need review.
func (s *PostReviewStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	return execCtx.TaskIntent != nil && execCtx.TaskIntent.TaskMode == "chat"
}

func (s *PostReviewStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if s.llm == nil || execCtx.Article == "" {
		// No article to review
		execCtx.ReviewResult = &engine.ReviewResult{
			Scores: map[string]float64{},
			Issues: []engine.ReviewIssue{},
			Passed: true,
		}
		return nil
	}

	// Check pause before starting review
	if err := execCtx.CheckPause(ctx, emitter, engine.StepPostReview); err != nil {
		return err
	}

	// Build review prompt — inject profile-specific rules if available
	var profileRules strings.Builder
	if s.profile != nil {
		// Title guidelines
		if len(s.profile.TitleGuidelines.ForbiddenPatterns) > 0 {
			profileRules.WriteString(fmt.Sprintf("\n标题禁止模式（正则，必须检查标题是否匹配）：%s\n", strings.Join(s.profile.TitleGuidelines.ForbiddenPatterns, ", ")))
		}
		if s.profile.TitleGuidelines.Length.Max > 0 {
			profileRules.WriteString(fmt.Sprintf("标题字数限制：%d-%d字\n", s.profile.TitleGuidelines.Length.Min, s.profile.TitleGuidelines.Length.Max))
		}

		// Fact guard
		if len(s.profile.FactGuard.ForbiddenResults) > 0 {
			profileRules.WriteString(fmt.Sprintf("事实红线——以下表述禁止出现在文章中（已完成事件不得用结果性动词）：%s\n", strings.Join(s.profile.FactGuard.ForbiddenResults, ", ")))
		}
		if len(s.profile.FactGuard.FutureTenseRequired) > 0 {
			profileRules.WriteString(fmt.Sprintf("事实红线——未发生事件须使用以下时态标记：%s\n", strings.Join(s.profile.FactGuard.FutureTenseRequired, ", ")))
		}

		// Rhetoric requirements
		var rhetoricParts []string
		if s.profile.Rhetoric.RequiredMetaphor {
			rhetoricParts = append(rhetoricParts, "核心比喻")
		}
		if s.profile.Rhetoric.RequiredParallelism {
			rhetoricParts = append(rhetoricParts, "排比")
		}
		if s.profile.Rhetoric.RequiredRhetoricalQuestion {
			rhetoricParts = append(rhetoricParts, "设问")
		}
		if len(rhetoricParts) > 0 {
			profileRules.WriteString(fmt.Sprintf("修辞要求——必须包含：%s\n", strings.Join(rhetoricParts, "、")))
		}

		// Word range
		if s.profile.WordRange.Max > 0 {
			profileRules.WriteString(fmt.Sprintf("字数范围：%d-%d字\n", s.profile.WordRange.Min, s.profile.WordRange.Max))
		}

		// Structure
		if s.profile.Structure.Type != "" {
			profileRules.WriteString(fmt.Sprintf("结构类型：%s（%s → %s → %s）\n", s.profile.Structure.Type, s.profile.Structure.Opening, s.profile.Structure.Body, s.profile.Structure.Conclusion))
		}
	}

	systemMsg := "你是文章质量评审员。对文章进行多维度评分和问题检测。只返回 JSON。"
	userMsg := fmt.Sprintf(`请评审以下文章：

%s

评审维度：factuality（事实准确性）、structure（结构合规）、style（风格符合）、rhetoric（修辞运用）、length（篇幅控制）、title_quality（标题质量）、safety（内容安全）

%s
返回格式：
{
  "scores": {"factuality": 0.9, "structure": 0.85, ...},
  "issues": [{"severity": "high", "type": "fact", "message": "..."}],
  "passed": true
}`, execCtx.Article, profileRules.String())

	resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0))
	if err != nil {
		// If review fails, pass by default
		execCtx.ReviewResult = &engine.ReviewResult{
			Scores: map[string]float64{},
			Issues: []engine.ReviewIssue{},
			Passed: true,
		}
		return nil
	}

	jsonStr := tools.ExtractJSONObject(resp)
	if jsonStr == "" {
		execCtx.ReviewResult = &engine.ReviewResult{
			Scores: map[string]float64{},
			Issues: []engine.ReviewIssue{},
			Passed: true,
		}
		return nil
	}

	var review engine.ReviewResult
	if err := json.Unmarshal([]byte(jsonStr), &review); err != nil {
		execCtx.ReviewResult = &engine.ReviewResult{
			Scores: map[string]float64{},
			Issues: []engine.ReviewIssue{},
			Passed: true,
		}
		return nil
	}

	if review.Scores == nil {
		review.Scores = map[string]float64{}
	}
	if review.Issues == nil {
		review.Issues = []engine.ReviewIssue{}
	}

	// Run sensitive word check on the article
	if s.sensitiveCheck != nil {
		scResult := s.sensitiveCheck.Check(ctx, execCtx.Article)
		if scResult != nil && len(scResult.Hits) > 0 {
			// Add sensitive hits as review issues
			for _, hit := range scResult.Hits {
				severity := hit.Severity
				if severity == "" {
					severity = "medium"
				}
				// Escalate block actions to high severity
				if hit.Action == "block" {
					severity = "high"
				}
				review.Issues = append(review.Issues, engine.ReviewIssue{
					Severity: severity,
					Type:     "sensitive_word",
					Message:  fmt.Sprintf("敏感词「%s」（类别: %s, 动作: %s, 出现 %d 次）", hit.Word, hit.Category, hit.Action, hit.Count),
				})
			}

			// If any blocking-level sensitive word is found, mark as not passed
			if !scResult.Passed {
				review.Passed = false
				// Override safety score
				review.Scores["safety"] = 0.1
				slog.Warn("sensitive word check blocked article",
					"trace_id", execCtx.TraceID,
					"hits", len(scResult.Hits),
					"summary", scResult.Summary)
			} else {
				// Adjust safety score based on warnings
				if currentScore, ok := review.Scores["safety"]; ok {
					deduction := 0.0
					for _, hit := range scResult.Hits {
						switch hit.Severity {
						case "high":
							deduction += 0.15
						case "medium":
							deduction += 0.08
						default:
							deduction += 0.03
						}
					}
					review.Scores["safety"] = currentScore - deduction
					if review.Scores["safety"] < 0 {
						review.Scores["safety"] = 0
					}
				}
			}
		}
	}

	// Rule-based checks using profile rules (deterministic, independent of LLM)

	// 1. Check title forbidden_patterns (regex)
	if s.profile != nil && len(s.profile.TitleGuidelines.ForbiddenPatterns) > 0 {
		// Extract title from article (first ## or # line)
		articleTitle := ""
		for _, line := range strings.Split(execCtx.Article, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "# ") {
				articleTitle = strings.TrimPrefix(strings.TrimPrefix(trimmed, "## "), "# ")
				articleTitle = strings.TrimSpace(articleTitle)
				break
			}
		}
		if articleTitle != "" {
			for _, pattern := range s.profile.TitleGuidelines.ForbiddenPatterns {
				if re, err := regexp.Compile(pattern); err == nil && re.MatchString(articleTitle) {
					review.Issues = append(review.Issues, engine.ReviewIssue{
						Severity: "high",
						Type:     "title_forbidden",
						Message:  fmt.Sprintf("标题「%s」匹配禁止模式「%s」", articleTitle, pattern),
					})
					review.Passed = false
					if score, ok := review.Scores["title_quality"]; ok {
						review.Scores["title_quality"] = score * 0.3
					} else {
						review.Scores["title_quality"] = 0.3
					}
					slog.Warn("title forbidden pattern matched",
						"trace_id", execCtx.TraceID,
						"title", articleTitle, "pattern", pattern)
				}
			}
		}
	}

	// 2. Check title length compliance
	if s.profile != nil && s.profile.TitleGuidelines.Length.Max > 0 {
		articleTitle := ""
		for _, line := range strings.Split(execCtx.Article, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "# ") {
				articleTitle = strings.TrimPrefix(strings.TrimPrefix(trimmed, "## "), "# ")
				articleTitle = strings.TrimSpace(articleTitle)
				break
			}
		}
		if articleTitle != "" {
			titleLen := len([]rune(articleTitle))
			minLen := s.profile.TitleGuidelines.Length.Min
			maxLen := s.profile.TitleGuidelines.Length.Max
			if titleLen < minLen || titleLen > maxLen {
				review.Issues = append(review.Issues, engine.ReviewIssue{
					Severity: "medium",
					Type:     "title_length",
					Message:  fmt.Sprintf("标题长度 %d 字，要求 %d-%d 字", titleLen, minLen, maxLen),
				})
			}
		}
	}

	// 3. Check fact_guard forbidden_results (string matching)
	if s.profile != nil && len(s.profile.FactGuard.ForbiddenResults) > 0 {
		articleLower := strings.ToLower(execCtx.Article)
		for _, forbidden := range s.profile.FactGuard.ForbiddenResults {
			if strings.Contains(articleLower, strings.ToLower(forbidden)) {
				review.Issues = append(review.Issues, engine.ReviewIssue{
					Severity: "high",
					Type:     "fact_guard",
					Message:  fmt.Sprintf("文章包含禁止表述「%s」（事实红线：已完成事件不得用结果性动词）", forbidden),
				})
				review.Passed = false
				if score, ok := review.Scores["factuality"]; ok {
					review.Scores["factuality"] = score * 0.5
				} else {
					review.Scores["factuality"] = 0.5
				}
				slog.Warn("fact guard forbidden result detected",
					"trace_id", execCtx.TraceID,
					"forbidden_phrase", forbidden)
			}
		}
	}

	// 4. Check word count compliance
	if s.profile != nil && s.profile.WordRange.Max > 0 {
		wordCount := len([]rune(execCtx.Article))
		minWords := s.profile.WordRange.Min
		maxWords := s.profile.WordRange.Max
		if wordCount < minWords || wordCount > maxWords {
			severity := "medium"
			if s.profile.WordRange.HardLimit {
				severity = "high"
				review.Passed = false
			}
			review.Issues = append(review.Issues, engine.ReviewIssue{
				Severity: severity,
				Type:     "length",
				Message:  fmt.Sprintf("字数 %d，要求 %d-%d 字", wordCount, minWords, maxWords),
			})
		}
	}

	execCtx.ReviewResult = &review
	return nil
}

// ─── AutoFixStep ─────────────────────────────────────────

type AutoFixStep struct {
	llm *tools.LLMClient
}

func NewAutoFixStep(llm *tools.LLMClient) *AutoFixStep {
	return &AutoFixStep{llm: llm}
}

func (s *AutoFixStep) Name() engine.StepName { return engine.StepAutoFix }
func (s *AutoFixStep) CanPause() bool         { return false }

// ShouldSkip returns true for chat intent — chat responses don't need auto-fix.
func (s *AutoFixStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	return execCtx.TaskIntent != nil && execCtx.TaskIntent.TaskMode == "chat"
}

func (s *AutoFixStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if execCtx.ReviewResult == nil || execCtx.ReviewResult.Passed {
		return nil
	}

	// Filter fixable issues (low and medium severity — not high)
	var fixableIssues []engine.ReviewIssue
	for _, issue := range execCtx.ReviewResult.Issues {
		if issue.Severity == "low" || issue.Severity == "medium" {
			fixableIssues = append(fixableIssues, issue)
		}
	}

	if len(fixableIssues) == 0 {
		return nil
	}

	if s.llm == nil {
		return nil
	}

	// Check pause before starting fix
	if err := execCtx.CheckPause(ctx, emitter, engine.StepAutoFix); err != nil {
		return err
	}

	// Build issue list with severity and type context
	issueList := ""
	for i, issue := range fixableIssues {
		issueList += fmt.Sprintf("%d. [%s/%s] %s\n", i+1, issue.Severity, issue.Type, issue.Message)
	}

	// Determine low-scoring dimensions for targeted fix
	var lowDims []string
	for dim, score := range execCtx.ReviewResult.Scores {
		if score < 0.7 {
			lowDims = append(lowDims, fmt.Sprintf("%s (%.1f)", dim, score))
		}
	}
	lowDimsStr := "无"
	if len(lowDims) > 0 {
		lowDimsStr = strings.Join(lowDims, ", ")
	}

	systemMsg := "你是文章修正助手。根据问题列表和评分结果修正文章。只输出修正后的完整文章（Markdown格式），不要解释。"
	userMsg := fmt.Sprintf(`原文：
%s

需要修正的问题：
%s

低分维度：
%s

修正要求：
1. 针对每个问题进行精确修正
2. 保持原文的整体结构和风格
3. 不要添加或删除段落
4. 只修正有问题的部分，保持正确部分不变
5. 输出完整的修正后文章`, execCtx.Article, issueList, lowDimsStr)

	resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0.3))
	if err != nil {
		return nil // Auto-fix failure is non-fatal
	}

	if resp != "" {
		execCtx.Article = resp
		// Mark as passed after fix
		execCtx.ReviewResult.Passed = true

		// Emit the fixed article
		emitter.StreamDone(resp)
	}

	return nil
}

// time import to avoid unused warning
var _ = time.Now
