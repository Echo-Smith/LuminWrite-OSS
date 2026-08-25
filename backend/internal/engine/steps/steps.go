package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── IntentStep ──────────────────────────────────────────

// voiceNormalizationPairs corrects common speech-to-text errors.
// This is pre-processing, not intent classification — kept as regex
// because it's fast, deterministic, and handles ASR artifacts well.
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

type IntentStep struct {
	llm *tools.LLMClient
}

func NewIntentStep(llm *tools.LLMClient) *IntentStep {
	return &IntentStep{llm: llm}
}

func (s *IntentStep) Name() engine.StepName { return engine.StepIntent }
func (s *IntentStep) CanPause() bool         { return false }
func (s *IntentStep) Timeout() time.Duration { return 30 * time.Second }
func (s *IntentStep) Critical() bool         { return true }

func (s *IntentStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	message := execCtx.UserInput

	// Normalize voice input (ASR error correction)
	normalized := message
	for _, pair := range voiceNormalizationPairs {
		normalized = pair.pattern.ReplaceAllString(normalized, pair.replace)
	}

	// Sanitize user input: strip fake system message tags to prevent prompt injection
	// This is lighter-touch than external content sanitization — only fake system tags
	// are removed, the user's actual writing request is preserved.
	normalized = engine.SanitizeUserInput(normalized)

	execCtx.NormalizedInput = normalized

	// Fast path: empty input → chat
	if strings.TrimSpace(normalized) == "" {
		execCtx.TaskIntent = &engine.TaskIntent{
			TaskMode:        "chat",
			Confidence:      0.95,
			Source:          "rules",
			NormalizedInput: normalized,
		}
		return nil
	}

	// Fast path: explicit writing mode override
	if execCtx.Mode == "writing" {
		execCtx.TaskIntent = &engine.TaskIntent{
			TaskMode:        "writing",
			Confidence:      0.95,
			Source:          "rules",
			NormalizedInput: normalized,
		}
		return nil
	}

	// LLM-first intent classification
	if s.llm != nil {
		llmIntent, err := s.classifyWithLLM(ctx, normalized)
		if err == nil && llmIntent != nil {
			execCtx.TaskIntent = llmIntent
			return nil
		}
		slog.Warn("intent LLM classification failed, falling back to rules",
			"error", err,
			"trace_id", execCtx.TraceID,
		)
	}

	// Fallback: simple keyword matching
	taskIntent := s.fallbackClassify(normalized)
	execCtx.TaskIntent = taskIntent
	return nil
}

// fallbackClassify provides a simple rule-based intent classification
// when the LLM is unavailable. This is intentionally minimal — the LLM
// handles all nuanced cases.
func (s *IntentStep) fallbackClassify(normalized string) *engine.TaskIntent {
	taskMode := "chat"

	// Simple keyword checks in priority order
	switch {
	case strings.Contains(normalized, "写一篇") || strings.Contains(normalized, "写稿") ||
		strings.Contains(normalized, "撰写") || strings.Contains(normalized, "基于热搜"):
		taskMode = "writing"
	case strings.Contains(normalized, "润色") || strings.Contains(normalized, "优化") ||
		strings.Contains(normalized, "改写"):
		taskMode = "polish"
	case strings.Contains(normalized, "缩写") || strings.Contains(normalized, "缩短") ||
		strings.Contains(normalized, "精简"):
		taskMode = "shorten"
	case strings.Contains(normalized, "扩写") || strings.Contains(normalized, "扩充") ||
		strings.Contains(normalized, "展开"):
		taskMode = "expand"
	case strings.Contains(normalized, "提炼") || strings.Contains(normalized, "提取观点"):
		taskMode = "extract_points"
	}

	return &engine.TaskIntent{
		TaskMode:        taskMode,
		Confidence:      0.6,
		Source:          "rules",
		NormalizedInput: normalized,
	}
}

func (s *IntentStep) classifyWithLLM(ctx context.Context, message string) (*engine.TaskIntent, error) {
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
	}, tools.WithTemperature(0), tools.WithThinking(false), tools.WithJSONResponse())
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
		parsed.TaskMode = "chat"
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
func (s *QueryPlanStep) Timeout() time.Duration { return 30 * time.Second }
func (s *QueryPlanStep) Critical() bool         { return false }

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

	// Try LLM-based query planning first
	if s.llm != nil {
		task, err := s.planWithLLM(ctx, execCtx)
		if err == nil && task != nil {
			execCtx.WritingTask = task
			execCtx.SearchPlan = s.buildSearchPlan(task.SearchQueries)
			slog.Info("query plan generated by LLM",
				"topic", task.Topic,
				"queries", task.SearchQueries,
				"word_limit", task.WordLimit,
			)
			return nil
		}
		slog.Warn("LLM query planning failed, falling back to regex", "error", err)
	}

	// Fallback: regex-based topic extraction
	topic := extractTopic(execCtx.NormalizedInput)
	execCtx.WritingTask = &engine.WritingTask{
		Topic:              topic,
		PrimarySearchQuery: topic,
		SearchQueries:      []string{topic},
	}

	execCtx.SearchPlan = s.buildSearchPlan([]string{topic})
	return nil
}

// planWithLLM uses the LLM to analyze user input and generate a structured query plan.
func (s *QueryPlanStep) planWithLLM(ctx context.Context, execCtx *engine.ExecutionContext) (*engine.WritingTask, error) {
	systemMsg := `你是一个搜索规划助手。根据用户的写作请求，提取核心话题并生成多个搜索查询。
只返回 JSON，格式如下：
{
  "topic": "核心话题（简洁，如"长鑫存储"）",
  "primary_query": "最佳搜索关键词（如"长鑫存储 国产替代 进展"）",
  "queries": ["查询1", "查询2", "查询3"],
  "word_limit": 0
}
要求：
- topic: 2-10个字，只保留核心实体名
- primary_query: 最可能搜到高质量结果的查询词
- queries: 3-5个不同角度的查询词，覆盖话题的多个方面（如：实体本身、近期进展、政策背景、行业影响）
- word_limit: 从用户输入中提取建议字数，无则填0
- 不要解释，只返回JSON`

	userMsg := fmt.Sprintf("用户请求：\n%s", execCtx.NormalizedInput)

	resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0.1))
	if err != nil {
		return nil, fmt.Errorf("LLM query planning failed: %w", err)
	}

	jsonStr := tools.ExtractJSONObject(resp)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON in LLM response")
	}

	var result struct {
		Topic        string   `json:"topic"`
		PrimaryQuery string   `json:"primary_query"`
		Queries      []string `json:"queries"`
		WordLimit    int      `json:"word_limit"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse LLM query plan: %w", err)
	}

	if result.Topic == "" {
		return nil, fmt.Errorf("LLM returned empty topic")
	}

	if result.PrimaryQuery == "" {
		result.PrimaryQuery = result.Topic
	}
	if len(result.Queries) == 0 {
		result.Queries = []string{result.PrimaryQuery}
	}

	return &engine.WritingTask{
		Topic:              result.Topic,
		PrimarySearchQuery: result.PrimaryQuery,
		SearchQueries:      result.Queries,
		WordLimit:          result.WordLimit,
	}, nil
}

// buildSearchPlan creates search plan entries from multiple queries.
// Distributes queries across available sources for broader coverage.
func (s *QueryPlanStep) buildSearchPlan(queries []string) []engine.SearchPlanEntry {
	sources := []string{"tavily", "zhihu", "local_kb"}
	var plan []engine.SearchPlanEntry
	for i, q := range queries {
		source := sources[i%len(sources)]
		plan = append(plan, engine.SearchPlanEntry{Query: q, Source: source})
	}
	return plan
}

// extractTopic is a minimal fallback for topic extraction when LLM is unavailable.
// It tries to extract content from common bracket formats, falling back to the first line.
func extractTopic(message string) string {
	firstLine := message
	if idx := strings.IndexByte(message, '\n'); idx >= 0 {
		firstLine = message[:idx]
	}
	firstLine = strings.TrimSpace(firstLine)

	// Try bracket formats: 「...」『...」...
	if m := regexp.MustCompile(`[「『"](.+?)[」』"]`).FindStringSubmatch(firstLine); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}

	return firstLine
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
func (s *SearchStep) Timeout() time.Duration { return 45 * time.Second }
func (s *SearchStep) Critical() bool         { return false }

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
	if len(execCtx.SearchPlan) == 0 && execCtx.TopicURL == "" {
		execCtx.SearchResults = []engine.SearchResult{}
		return nil
	}

	// If search results are frozen (e.g., from a pre-computed snapshot for experiments),
	// skip search and use the pre-populated results
	if execCtx.FrozenSearchResults && len(execCtx.SearchResults) > 0 {
		slog.Info("search step: using frozen search results", "count", len(execCtx.SearchResults))
		return nil
	}

	// Check pause before starting search
	if err := execCtx.CheckPause(ctx, emitter, engine.StepSearch); err != nil {
		return err
	}

	var allResults []engine.SearchResult
	seenURLs := make(map[string]bool)

	// ── Fetch topic URL content as primary background source ──
	// If the writing was initiated from a hot topic with a URL, fetch the
	// original article to provide rich event details and narrative context.
	// Uses AnySearch.Extract (5万字 Markdown) first, falls back to URLFetcher.
	if execCtx.TopicURL != "" {
		title, content, source, err := s.search.ExtractURL(ctx, execCtx.TopicURL)
		if err != nil {
			slog.Warn("failed to fetch topic URL content", "url", execCtx.TopicURL, "error", err)
		} else if content != "" {
			allResults = append(allResults, engine.SearchResult{
				Title:    title,
				Snippet:  content,
				URL:      execCtx.TopicURL,
				Source:   "topic_url",
			})
			seenURLs[execCtx.TopicURL] = true
			slog.Info("topic URL content fetched as background",
				"url", execCtx.TopicURL,
				"title", title,
				"content_length", len([]rune(content)),
				"fetcher", source,
			)
		}
	}

	// Use real search client if available
	if s.search != nil && s.search.HasSources() {
		queries := []string{}
		if execCtx.WritingTask != nil {
			if execCtx.WritingTask.PrimarySearchQuery != "" {
				queries = append(queries, execCtx.WritingTask.PrimarySearchQuery)
			}
			for _, q := range execCtx.WritingTask.SearchQueries {
				if q != "" && !containsString(queries, q) {
					queries = append(queries, q)
				}
			}
		}
		if len(queries) == 0 {
			queries = []string{execCtx.UserInput}
		}

		// Distribute maxTotal across queries; ensure at least 8 per query
		// Long-form (万字论文/报告) needs broader per-query coverage
		maxPerQuery := 30 / len(queries)
		if maxPerQuery < 8 {
			maxPerQuery = 8
		}

		// ── Concurrent multi-query search ──
		// Each query runs in its own goroutine; results are deduplicated
		// by URL under a mutex. This reduces search latency from O(N×latency)
		// to O(max_latency) when N queries are issued.
		var (
			mu sync.Mutex
			wg sync.WaitGroup
		)
		for _, q := range queries {
			wg.Add(1)
			go func(query string) {
				defer wg.Done()
				results := s.search.Search(ctx, query, maxPerQuery)

				mu.Lock()
				for _, r := range results {
					if r.URL != "" {
						if seenURLs[r.URL] {
							continue
						}
						seenURLs[r.URL] = true
					}
					allResults = append(allResults, r)
				}
				mu.Unlock()

				slog.Info("search completed", "query", query, "results", len(results), "cumulative", len(allResults))
			}(q)
		}
		wg.Wait()
		// Sanitize all search results before storing to prevent prompt injection
		execCtx.SearchResults = engine.SanitizeSearchResults(allResults)
		return nil
	}

	// Fallback: use LLM to generate mock search results
	if len(allResults) > 0 {
		execCtx.SearchResults = engine.SanitizeSearchResults(allResults)
		return nil
	}
	if execCtx.WritingTask != nil && s.llm != nil {
		results := s.generateMockResults(ctx, execCtx.WritingTask.Topic)
		execCtx.SearchResults = engine.SanitizeSearchResults(results)
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
	}, tools.WithTemperature(0.3), tools.WithThinking(false))
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
func (s *RelevanceStep) Timeout() time.Duration { return 30 * time.Second }
func (s *RelevanceStep) Critical() bool         { return false }

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
		execCtx.SearchResults = tools.DedupSearchResults(execCtx.SearchResults)
	}

	// Stochastic sampling: keep all strong/medium results, randomly sample weak results
	if execCtx.StochasticState != nil {
		if ss, ok := execCtx.StochasticState.(*memory.StochasticState); ok {
			var filtered []engine.SearchResult
			for _, r := range execCtx.SearchResults {
				if r.Relevance == "weak" && !ss.ShouldKeep() {
					continue // 随机丢弃弱相关结果
				}
				filtered = append(filtered, r)
			}
			execCtx.SearchResults = filtered
		}
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

// ─── Semantic Deduplication (pgvector / embedding-based) ──

// deduplicateResultsSemantic removes near-duplicate search results using
// embedding cosine similarity. Falls back to text-based dedup if embedding fails.
//
// 文档来源: docs/02-database-schema.md — pgvector 语义去重
func deduplicateResultsSemantic(ctx context.Context, results []engine.SearchResult, emb *tools.EmbeddingClient) []engine.SearchResult {
	if len(results) <= 1 || emb == nil || !emb.IsConfigured() {
		return tools.DedupSearchResults(results)
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
		return tools.DedupSearchResults(results)
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
	llm     *tools.LLMClient
	profile *profile.StyleProfile
}

func NewOutlineStep(llm *tools.LLMClient) *OutlineStep {
	return &OutlineStep{llm: llm}
}

// NewOutlineStepWithProfile creates an OutlineStep with a style profile.
// When a profile is provided, the outline generation will be guided by
// the profile's structure skeleton (Sections for custom type, or
// Opening/Body/Conclusion for three_part type).
func NewOutlineStepWithProfile(llm *tools.LLMClient, p *profile.StyleProfile) *OutlineStep {
	return &OutlineStep{llm: llm, profile: p}
}

func (s *OutlineStep) Name() engine.StepName { return engine.StepOutline }
func (s *OutlineStep) CanPause() bool         { return true }
func (s *OutlineStep) Timeout() time.Duration { return 60 * time.Second }
func (s *OutlineStep) Critical() bool         { return true }

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

		// Wait for user confirmation (with timeout for user inactivity)
		confirmedData, err := execCtx.WaitForConfirmWithTimeout(ctx, execCtx.ConfirmTimeout)
		if err != nil {
			// Handle confirm timeout: auto-confirm with current outline
			if err == engine.ErrConfirmTimeout {
				slog.Warn("outline confirm timeout, auto-confirming",
					"trace_id", execCtx.TraceID)
				return nil
			}
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

	// 注入风格配置的结构骨架（如果有）
	structureSkeleton := ""
	if s.profile != nil {
		structureSkeleton = s.profile.RenderStructureSkeleton()
	}

	var userMsg string
	if structureSkeleton != "" {
		userMsg = fmt.Sprintf(`话题：%s

%s

请生成提纲，包含标题和3-5个要点。提纲的每个要点的 type 应与上述结构骨架对应。

返回格式：
{
  "title": "文章标题",
  "outline": [
    {"point": "要点内容", "type": "section_type"}
  ]
}

type 字段说明：用于标注该要点的段落角色，由你根据文章体裁自由决定。常见值包括但不限于：opening、argument、conclusion、intro、method、experiment、discussion、abstract 等，也可使用自定义标签
}`, execCtx.WritingTask.Topic, structureSkeleton)
	} else {
		userMsg = fmt.Sprintf(`话题：%s

请生成提纲，包含标题和3-5个要点。

返回格式：
{
  "title": "文章标题",
  "outline": [
    {"point": "要点内容", "type": "section_type"}
  ]
}

type 字段说明：用于标注该要点的段落角色，由你根据文章体裁自由决定。常见值包括但不限于：opening、argument、conclusion、intro、method、experiment、discussion、abstract 等，也可使用自定义标签
}`, execCtx.WritingTask.Topic)
	}

	resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "user", Content: userMsg},
	}, tools.WithInstructions(systemMsg), tools.WithTemperature(temperature), tools.WithThinking(true), tools.WithReasoningEffort("high"))
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
	llm        *tools.LLMClient
	profile    *profile.StyleProfile
	search     *tools.SearchClient      // optional, enables agent loop with tool calls
	kbSearcher tools.KnowledgeSearcher  // optional, enables search_knowledge tool
}

func NewWriteStep(llm *tools.LLMClient) *WriteStep {
	return &WriteStep{llm: llm}
}

// NewWriteStepWithProfile creates a WriteStep with a style profile.
func NewWriteStepWithProfile(llm *tools.LLMClient, p *profile.StyleProfile) *WriteStep {
	return &WriteStep{llm: llm, profile: p}
}

// NewWriteStepWithSearch creates a WriteStep with a search client for agent loop support.
func NewWriteStepWithSearch(llm *tools.LLMClient, p *profile.StyleProfile, search *tools.SearchClient) *WriteStep {
	return &WriteStep{llm: llm, profile: p, search: search}
}

// NewWriteStepWithKB creates a WriteStep with search and knowledge base support.
func NewWriteStepWithKB(llm *tools.LLMClient, p *profile.StyleProfile, search *tools.SearchClient, kb tools.KnowledgeSearcher) *WriteStep {
	return &WriteStep{llm: llm, profile: p, search: search, kbSearcher: kb}
}

func (s *WriteStep) Name() engine.StepName { return engine.StepWrite }
func (s *WriteStep) CanPause() bool         { return true }
func (s *WriteStep) Timeout() time.Duration { return 180 * time.Second }
func (s *WriteStep) Critical() bool         { return true }

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

	// Inject current time to help the model judge timelines (e.g. "published" vs "upcoming")
	systemPrompt += fmt.Sprintf("\n\n当前日期：%s。写作时请确保引用的政策、文件、规划等的时间状态准确（已发布/即将发布/正在征求意见等）。",
		time.Now().Format("2006年1月2日"))

	// In guided mode, append a clarification that the title is provided and must not be changed
	if execCtx.Outline != nil && execCtx.Outline.Title != "" {
		systemPrompt += "\n\n【重要】本次为引导模式写作，文章标题已由用户确认，必须原样使用提供的标题，不得自行创作或修改。核心比喻和修辞手法仅用于正文，不影响标题。"
	}

	// Append prompt injection defense directive to system prompt
	systemPrompt += engine.PromptInjectionDefenseDirective

	// ── Build user prompt via PromptBuilder (section-based assembly) ──
	// v3.0: 动态 Token 预算（借鉴 Codex TokenBudgetContext）
	// 根据全局剩余 Token（MaxTokens - TotalTokens）动态计算 user prompt 配额，
	// 保留预留量给 system prompt + conversation history + LLM response。
	// 如果 MaxTokens 为 0（无限制），则回退到默认 12000 预算。
	remainingGlobal := 0
	if execCtx.MaxTokens > 0 {
		remainingGlobal = execCtx.MaxTokens - execCtx.TotalTokens
	}
	hasOutlineTitle := execCtx.Outline != nil && execCtx.Outline.Title != ""
	outlineTitle := ""
	if hasOutlineTitle {
		outlineTitle = execCtx.Outline.Title
	}

	pb := NewPromptBuilder().
		WithDynamicBudget(remainingGlobal).
		AddTaskPrompt(taskMode, execCtx).
		AddSearchResults(taskMode, execCtx).
		AddOutline(taskMode, execCtx).
		AddStyleConstraints(s.profile, taskMode, hasOutlineTitle, execCtx.WordLimit).
		AddUserMaterials(execCtx.UserMaterials).
		AddMemory(execCtx).
		AddOutputFormat(s.profile, taskMode, outlineTitle, ArticleSeparator)

	// Stream the article
	messages := []tools.LLMMessage{
		{Role: "system", Content: systemPrompt},
	}

	// Inject conversation history (short-term memory) with smart token budget
	if execCtx.ConversationHistory != nil {
		if history, ok := execCtx.ConversationHistory.([]memory.ConversationMessage); ok {
			selection := memory.SelectHistoryForPrompt(history, memory.DefaultHistorySelectionConfig())
			for _, msg := range selection.Messages {
				messages = append(messages, tools.LLMMessage{
					Role:    string(msg.Role),
					Content: msg.Content,
				})
			}
		}
	}

	messages = append(messages, tools.LLMMessage{
		Role: "user", Content: pb.String(),
	})

	// Determine thinking strategy by task mode:
	//   writing → thinking enabled (deep reasoning for article composition)
	//   polish/shorten/expand/extract_points → thinking disabled (mechanical text ops)
	var streamOpts []tools.ChatOption
	if taskMode == "writing" {
		streamOpts = []tools.ChatOption{
			tools.WithThinking(true),
			tools.WithReasoningEffort("high"),
		}
	} else {
		streamOpts = []tools.ChatOption{
			tools.WithThinking(false),
		}
	}

	// Apply stochastic temperature adjustment (from StochasticState)
	if execCtx.StochasticState != nil {
		if ss, ok := execCtx.StochasticState.(*memory.StochasticState); ok {
			adjusted := ss.AdjustedTemperature(0.7) // base temp 0.7 for writing
			streamOpts = append(streamOpts, tools.WithTemperature(adjusted))
		}
	}

	// Determine whether JSON title prefix is expected
	// (true for writing mode with markdown output format)
	useJSONTitle := taskMode == "writing" && (s.profile == nil || s.profile.OutputFormat.UseMarkdown)

	// State machine for JSON title prefix extraction
	var titleBuf strings.Builder
	var bodyBuf strings.Builder // tracks only the body text sent to user (excludes JSON prefix)
	titleCharCount := 0
	titleResolved := false

	// streamBody is a helper that sends delta to user AND accumulates into bodyBuf
	streamBody := func(text string) {
		bodyBuf.WriteString(text)
		emitter.StreamDelta(text)
	}

	// onReasoning forwards thinking content to the frontend for visualization
	// and accumulates it into execCtx.ReasoningContent for persistence.
	onReasoning := func(delta string) {
		emitter.ReasoningDelta(delta)
		execCtx.ReasoningContent += delta
	}

	// onStreamReset is called by the agent loop when an intermediate tool-call
	// round produced content that was optimistically streamed to the user.
	// It resets the title extraction state machine and tells the frontend to
	// discard all streamed text, so the final answer round starts clean.
	onStreamReset := func() {
		slog.Info("agent loop stream reset — discarding intermediate content",
			"trace_id", execCtx.TraceID,
			"title_resolved", titleResolved,
			"body_chars", bodyBuf.Len())
		titleBuf.Reset()
		bodyBuf.Reset()
		titleCharCount = 0
		titleResolved = false
		emitter.StreamReset()
	}

	// The streaming callback handles JSON title prefix extraction and body streaming.
	// It's shared between the normal streaming path and the agent loop's final stream.
	streamCallback := func(delta string) {
		if !titleResolved && useJSONTitle {
			// Still collecting JSON title prefix
			titleBuf.WriteString(delta)
			titleCharCount += len([]rune(delta))
			buf := titleBuf.String()

			// Try to find the separator
			sepIdx := strings.Index(buf, ArticleSeparator)
			if sepIdx >= 0 {
				// Separator found — parse JSON title from everything before it
				jsonPart := strings.TrimSpace(buf[:sepIdx])
				var title string
				if t, ok := ParseTitleJSON(jsonPart); ok {
					title = t
				} else {
					// JSON parse failed — try regex fallback on the prefix
					title = ExtractTitleFromMarkdown(jsonPart)
				}
				if title != "" {
					execCtx.ArticleTitle = title
					emitter.ArticleTitle(title)
				}

				// Content after separator becomes the start of the streamed body
				after := strings.TrimLeft(buf[sepIdx+len(ArticleSeparator):], "\n\r ")
				// Strip duplicate ## title heading if LLM repeated it
				after = StripLeadingTitleHeading(after, title)
				if after != "" {
					streamBody(after)
				}
				titleResolved = true
				titleBuf.Reset()
				return
			}

			// No separator found yet — check character limit for fallback
			if titleCharCount > TitleCollectCharLimit {
				// LLM didn't follow the format — switch to fallback mode
				// Try to extract title from what we've collected
				title := ExtractTitleFromMarkdown(buf)
				if title != "" {
					execCtx.ArticleTitle = title
					emitter.ArticleTitle(title)
				}
				// Output collected content as body (filter out JSON lines)
				bodyPart := FilterJSONLines(buf)
				bodyPart = StripLeadingTitleHeading(bodyPart, title)
				if bodyPart != "" {
					streamBody(bodyPart)
				}
				titleResolved = true
				titleBuf.Reset()
				return
			}
			// Still collecting, don't forward to user
			return
		}

		// Normal streaming (title already resolved or non-writing mode)
		streamBody(delta)

		// Check pause between stream chunks
		if err := execCtx.CheckPause(ctx, emitter, engine.StepWrite); err != nil {
			return
		}
	}

	// P2: Use agent loop (tool calls + thinking) when conditions are met.
	// The model can autonomously search for more context before writing.
	useAgentLoop := ShouldUseAgentLoop(execCtx, s.search)

	var fullText string
	var tokens int
	var err error

	if useAgentLoop {
		slog.Info("using agent loop for writing",
			"trace_id", execCtx.TraceID,
			"search_results", len(execCtx.SearchResults))

		kbID := ""
		if s.profile != nil {
			kbID = s.profile.KbID
		}
		toolExecutor := WritingToolExecutor(s.search, s.kbSearcher, kbID, execCtx.UserID, execCtx.SearchResults)
		fullText, tokens, err = s.llm.ChatWithTools(
			ctx, messages, streamCallback, onReasoning, onStreamReset,
			WritingTools(s.kbSearcher != nil), toolExecutor, streamOpts...,
		)
	} else {
		fullText, tokens, err = s.llm.ChatStreamWithReasoning(
			ctx, messages, streamCallback, onReasoning, streamOpts...,
		)
	}

	if err != nil {
		return fmt.Errorf("article generation failed: %w", err)
	}

	// Determine the final article body text
	articleBody := bodyBuf.String()
	if !useJSONTitle {
		// Non-writing modes: fullText IS the body (no JSON prefix)
		articleBody = fullText
	}

	// Post-stream: if title still not resolved (e.g. very short output), try final fallback
	if !titleResolved && useJSONTitle {
		title := ExtractTitleFromMarkdown(fullText)
		if title != "" {
			execCtx.ArticleTitle = title
			emitter.ArticleTitle(title)
		}
		// If still not resolved, the body is the filtered fullText
		if articleBody == "" {
			articleBody = FilterJSONLines(fullText)
		}
	}

	// For guided mode: always force the outline title, overriding whatever LLM generated
	if execCtx.Outline != nil && execCtx.Outline.Title != "" {
		if execCtx.ArticleTitle != execCtx.Outline.Title {
			slog.Info("overriding LLM title with outline title",
				"trace_id", execCtx.TraceID,
				"llm_title", execCtx.ArticleTitle,
				"outline_title", execCtx.Outline.Title)
			execCtx.ArticleTitle = execCtx.Outline.Title
			emitter.ArticleTitle(execCtx.Outline.Title)
		}
	}

	execCtx.Article = articleBody
	execCtx.TotalTokens += tokens

	// Emit stream done with the clean body text (no JSON prefix)
	emitter.StreamDone(articleBody)

	return nil
}

// ─── PostReviewStep ──────────────────────────────────────

type PostReviewStep struct {
	llm            *tools.LLMClient
	sensitiveCheck engine.SensitiveChecker
	profile        *profile.StyleProfile
	search         *tools.SearchClient  // optional, enables fact-checking via web search
	jiaozhen       *tools.JiaozhenClient // optional, enables rumor fact-checking
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

// NewPostReviewStepWithSearch creates a PostReviewStep with search capability for
// real-time fact-checking during the review.
func NewPostReviewStepWithSearch(llm *tools.LLMClient, sc engine.SensitiveChecker, p *profile.StyleProfile, search *tools.SearchClient) *PostReviewStep {
	return &PostReviewStep{llm: llm, sensitiveCheck: sc, profile: p, search: search}
}

// NewPostReviewStepWithSearchAndJiaozhen creates a PostReviewStep with both web search
// and Jiaozhen fact-checking capabilities.
func NewPostReviewStepWithSearchAndJiaozhen(llm *tools.LLMClient, sc engine.SensitiveChecker, p *profile.StyleProfile, search *tools.SearchClient, jiaozhen *tools.JiaozhenClient) *PostReviewStep {
	return &PostReviewStep{llm: llm, sensitiveCheck: sc, profile: p, search: search, jiaozhen: jiaozhen}
}

func (s *PostReviewStep) Name() engine.StepName { return engine.StepPostReview }
func (s *PostReviewStep) CanPause() bool         { return false }
func (s *PostReviewStep) Timeout() time.Duration { return 60 * time.Second }
func (s *PostReviewStep) Critical() bool         { return false }

// ShouldSkip returns true for chat intent or when review already passed.
// Chat responses don't need review. If review already passed (e.g. after
// a successful AutoFix), the re-review step is skipped in pipeline mode.
func (s *PostReviewStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	if execCtx.TaskIntent != nil && execCtx.TaskIntent.TaskMode == "chat" {
		return true
	}
	// Skip re-review if the article already passed review
	if execCtx.ReviewResult != nil && execCtx.ReviewResult.Passed {
		return true
	}
	return false
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

	// Determine task mode for fact-check gating
	reviewTaskMode := "writing"
	if execCtx.TaskIntent != nil && execCtx.TaskIntent.TaskMode != "" {
		reviewTaskMode = execCtx.TaskIntent.TaskMode
	}

	// Build review prompt — inject profile-specific rules if available.
	// NOTE: Title guidelines are intentionally excluded here — they are handled
	// by the independent reviewTitle() call which receives both title and body.
	// Including title rules in the body-only review causes the LLM to falsely
	// report "missing title" since it cannot see the title in the article body.
	var profileRules strings.Builder
	if s.profile != nil {
		// Unified rendering: fact guard (P0) + rhetoric/word-range/structure (P1)
		profileRules.WriteString(s.profile.RenderReviewCriteria())
	}

	// Inject user feedback memories (Tier 3) as additional review criteria
	if execCtx.MemoryContext != nil {
		if memCtx, ok := execCtx.MemoryContext.(*memory.MemoryContext); ok {
			if guardStr := FormatReviewGuardForPrompt(memCtx); guardStr != "" {
				profileRules.WriteString(guardStr)
			}
		}
	}

	// ── 联网事实核查（可选）──
	// 当搜索客户端可用时，从文章中提取关键事实声明，联网验证，
	// 将验证结果作为额外上下文注入评审 prompt。
	// 这解决了"模型不知道某政策/文件已发布"的问题。
	var factCheckContext string
	if s.search != nil && s.search.HasSources() && reviewTaskMode == "writing" {
		factCheckContext = s.factCheckArticle(ctx, execCtx)
	}

	// 注入当前时间，帮助模型判断时间线（如"已发布"vs"即将发布"）
	currentTime := time.Now().Format("2006年1月2日")
	profileRules.WriteString(fmt.Sprintf("当前日期：%s（请据此判断文章中提及的政策、文件、规划等是已发布还是即将发布）\n", currentTime))

	// 主评审：只评审正文维度（factuality/structure/style/rhetoric/length/safety）。
	// title_quality 由独立的 reviewTitle 环节评审，这样 LLM 不会因看不到标题而误报"缺少标题"，
	// 也不会把正文首句当作标题来评估。
	systemMsg := "你是文章正文质量评审员。只评审正文，不评审标题（标题由独立环节评审）。只返回 JSON。"

	// 构建评审 prompt，可选注入事实核查结果
	factCheckSection := ""
	if factCheckContext != "" {
		factCheckSection = fmt.Sprintf("\n\n联网事实核查结果（仅供参考，请据此修正 factuality 评分）：\n%s\n", factCheckContext)
	}

	userMsg := fmt.Sprintf(`请评审以下文章正文：

%s

评审维度：factuality（事实准确性）、structure（结构合规）、style（风格符合）、rhetoric（修辞运用）、length（篇幅控制）、safety（内容安全）
注意：不要评审 title_quality，标题由独立环节评审；不要报告"缺少标题"。

%s%s
返回格式：
{
  "scores": {"factuality": 0.9, "structure": 0.85, "style": 0.8, "rhetoric": 0.85, "length": 0.9, "safety": 0.95},
  "issues": [{"severity": "high", "type": "fact", "message": "..."}],
  "passed": true
}`, execCtx.Article, profileRules.String(), factCheckSection)

	resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "user", Content: userMsg},
	}, tools.WithInstructions(systemMsg), tools.WithTemperature(0), tools.WithThinking(true), tools.WithReasoningEffort("high"), tools.WithJSONResponse())
	if err != nil {
		// If review fails, pass by default (graceful degradation) but add a warning
		slog.Warn("post review LLM call failed, skipping review (graceful degradation)",
			"trace_id", execCtx.TraceID,
			"error", err)
		execCtx.ReviewResult = &engine.ReviewResult{
			Scores: map[string]float64{},
			Issues: []engine.ReviewIssue{
				{
					Severity: "medium",
					Type:     "review_skipped",
					Message:  "质量评审因服务异常被跳过（已自动放行）",
				},
			},
			Passed: true,
		}
		return nil
	}

	jsonStr := tools.ExtractJSONObject(resp)
	if jsonStr == "" {
		slog.Warn("post review: no JSON in response, skipping review",
			"trace_id", execCtx.TraceID)
		execCtx.ReviewResult = &engine.ReviewResult{
			Scores: map[string]float64{},
			Issues: []engine.ReviewIssue{
				{
					Severity: "medium",
					Type:     "review_skipped",
					Message:  "质量评审因响应格式异常被跳过（已自动放行）",
				},
			},
			Passed: true,
		}
		return nil
	}

	var review engine.ReviewResult
	if err := json.Unmarshal([]byte(jsonStr), &review); err != nil {
		slog.Warn("post review: failed to parse review JSON, skipping review",
			"trace_id", execCtx.TraceID,
			"error", err)
		execCtx.ReviewResult = &engine.ReviewResult{
			Scores: map[string]float64{},
			Issues: []engine.ReviewIssue{
				{
					Severity: "medium",
					Type:     "review_skipped",
					Message:  "质量评审因解析异常被跳过（已自动放行）",
				},
			},
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

	// 独立标题评审：传入标题 + 正文，LLM 能同时看到两者，
	// 评 title_quality（自身质量）+ title_relevance（与正文关联性），
	// 并可选输出 suggested_title 供 AutoFixStep 使用。
	if execCtx.ArticleTitle != "" {
		titleReview, titleErr := s.reviewTitle(ctx, execCtx)
		if titleErr == nil && titleReview != nil {
			// 合并标题评分
			for k, v := range titleReview.Scores {
				review.Scores[k] = v
			}
			// 合并标题问题
			review.Issues = append(review.Issues, titleReview.Issues...)
			// 存储建议标题
			if titleReview.SuggestedTitle != "" {
				review.TitleSuggestion = titleReview.SuggestedTitle
			}
			// 标题有 high 级问题则整体不通过
			for _, issue := range titleReview.Issues {
				if issue.Severity == "high" {
					review.Passed = false
					break
				}
			}
		} else if titleErr != nil {
			slog.Warn("title review failed, falling back to rule-based title check only",
				"trace_id", execCtx.TraceID,
				"error", titleErr)
		}
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

	// Resolve article title: prefer structured ArticleTitle, fall back to Markdown extraction
	articleTitle := execCtx.ArticleTitle
	if articleTitle == "" {
		articleTitle = ExtractTitleFromMarkdown(execCtx.Article)
	}

	// 1. Check title forbidden_patterns (regex)
	if s.profile != nil && len(s.profile.TitleGuidelines.ForbiddenPatterns) > 0 && articleTitle != "" {
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

	// 2. Check title length compliance
	if s.profile != nil && s.profile.TitleGuidelines.Length.Max > 0 && articleTitle != "" {
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
	llm     *tools.LLMClient
	profile *profile.StyleProfile
}

func NewAutoFixStep(llm *tools.LLMClient) *AutoFixStep {
	return &AutoFixStep{llm: llm}
}

// NewAutoFixStepWithProfile creates an AutoFixStep with a style profile for
// title-guideline-aware title regeneration.
func NewAutoFixStepWithProfile(llm *tools.LLMClient, p *profile.StyleProfile) *AutoFixStep {
	return &AutoFixStep{llm: llm, profile: p}
}

func (s *AutoFixStep) Name() engine.StepName { return engine.StepAutoFix }
func (s *AutoFixStep) CanPause() bool         { return false }
func (s *AutoFixStep) Timeout() time.Duration { return 90 * time.Second }
func (s *AutoFixStep) Critical() bool         { return false }

// ShouldSkip returns true for chat intent — chat responses don't need auto-fix.
func (s *AutoFixStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	return execCtx.TaskIntent != nil && execCtx.TaskIntent.TaskMode == "chat"
}

func (s *AutoFixStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if execCtx.ReviewResult == nil || execCtx.ReviewResult.Passed {
		return nil
	}

	// ── Review-Fix loop limit ──
	if execCtx.CheckFixLimit() {
		slog.Warn("max fix attempts reached, skipping auto_fix",
			"trace_id", execCtx.TraceID,
			"attempts", execCtx.FixAttempts,
			"max", execCtx.MaxFixAttempts)
		return nil
	}

	// Increment fix attempts at the start (after limit check) so that
	// even if no fixable issues are found, the counter advances to prevent
	// infinite loops. The counter tracks attempts, not successful fixes.
	execCtx.FixAttempts++

	// Non-fixable types (hard rules, cannot be auto-corrected)
	nonFixableTypes := map[string]bool{
		"sensitive_word":  true,
		"title_forbidden": true, // 禁止模式是硬规则，自动生成的新标题仍可能触碰
	}

	// Separate issues into title-related and body-related.
	// Title issues go through a dedicated title-regeneration branch;
	// body issues go through the existing article-fix branch.
	var titleIssues, bodyIssues []engine.ReviewIssue
	for _, issue := range execCtx.ReviewResult.Issues {
		if nonFixableTypes[issue.Type] {
			continue
		}
		if isTitleIssue(issue.Type) {
			titleIssues = append(titleIssues, issue)
		} else {
			bodyIssues = append(bodyIssues, issue)
		}
	}

	if len(titleIssues) == 0 && len(bodyIssues) == 0 {
		return nil
	}

	if s.llm == nil {
		return nil
	}

	// Check pause before starting fix
	if err := execCtx.CheckPause(ctx, emitter, engine.StepAutoFix); err != nil {
		return err
	}

	fixedSomething := false

	// ── Branch 1: Title fix ──
	if len(titleIssues) > 0 && execCtx.ArticleTitle != "" {
		newTitle, err := s.fixTitle(ctx, execCtx, titleIssues)
		if err == nil && newTitle != "" && newTitle != execCtx.ArticleTitle {
			// Validate new title against profile rules before applying
			if s.isValidTitle(newTitle) {
				slog.Info("auto-fix title applied",
					"trace_id", execCtx.TraceID,
					"old_title", execCtx.ArticleTitle,
					"new_title", newTitle,
					"title_issues", len(titleIssues))
				execCtx.ArticleTitle = newTitle
				emitter.ArticleTitle(newTitle)
				fixedSomething = true
			} else {
				slog.Warn("auto-fix title rejected by validation",
					"trace_id", execCtx.TraceID,
					"proposed_title", newTitle)
			}
		} else if err != nil {
			slog.Warn("auto-fix title failed",
				"trace_id", execCtx.TraceID,
				"error", err)
		}
	}

	// ── Branch 2: Body fix ──
	if len(bodyIssues) > 0 {
		fixedArticle, err := s.fixBody(ctx, execCtx, bodyIssues)
		if err == nil && fixedArticle != "" {
			execCtx.Article = fixedArticle
			fixedSomething = true
			slog.Info("auto-fix body applied",
				"trace_id", execCtx.TraceID,
				"body_issues", len(bodyIssues))
			emitter.StreamDone(fixedArticle)
		} else if err != nil {
			slog.Warn("auto-fix body failed",
				"trace_id", execCtx.TraceID,
				"error", err)
		}
	}

	// Note: we intentionally do NOT set Passed = true here.
	// After auto_fix, a re-review (post_review) will be triggered to verify
	// the fix actually resolved the issues:
	//   - In unified mode: determineNextStep forces re-review after auto_fix
	//   - In pipeline mode: a second PostReviewStep runs after AutoFixStep
	// This prevents "false pass" where AutoFix marks issues as fixed without
	// verifying the fix actually resolved them.
	if fixedSomething {
		slog.Info("auto-fix applied, awaiting re-review to verify",
			"trace_id", execCtx.TraceID,
			"fix_attempts", execCtx.FixAttempts,
			"max_fix_attempts", execCtx.MaxFixAttempts)
	} else {
		slog.Info("auto-fix attempted but nothing fixable",
			"trace_id", execCtx.TraceID,
			"fix_attempts", execCtx.FixAttempts,
			"max_fix_attempts", execCtx.MaxFixAttempts)
	}

	return nil
}

// isTitleIssue returns true if the issue type is related to the article title.
func isTitleIssue(issueType string) bool {
	switch issueType {
	case "title_length", "title_generic", "title_clickbait",
		"title_relevance", "title_quality":
		return true
	}
	return false
}

// isValidTitle checks whether a candidate title satisfies profile rules
// (length range and forbidden patterns). Used to validate LLM-suggested titles.
func (s *AutoFixStep) isValidTitle(title string) bool {
	if title == "" {
		return false
	}
	if s.profile == nil {
		return true
	}
	// Check forbidden patterns
	for _, pattern := range s.profile.TitleGuidelines.ForbiddenPatterns {
		if re, err := regexp.Compile(pattern); err == nil && re.MatchString(title) {
			return false
		}
	}
	// Check length range
	if s.profile.TitleGuidelines.Length.Max > 0 {
		titleLen := len([]rune(title))
		minLen := s.profile.TitleGuidelines.Length.Min
		maxLen := s.profile.TitleGuidelines.Length.Max
		if titleLen < minLen || titleLen > maxLen {
			return false
		}
	}
	return true
}

// fixTitle regenerates the article title.
// It first tries ReviewResult.TitleSuggestion (from the title review LLM) —
// if valid, uses it directly (saves an LLM call).
// Otherwise, calls LLM to generate a new title that satisfies all constraints.
func (s *AutoFixStep) fixTitle(ctx context.Context, execCtx *engine.ExecutionContext, issues []engine.ReviewIssue) (string, error) {
	// Fast path: use the suggestion from title review if it passes validation
	if execCtx.ReviewResult != nil && execCtx.ReviewResult.TitleSuggestion != "" {
		suggested := strings.TrimSpace(execCtx.ReviewResult.TitleSuggestion)
		if s.isValidTitle(suggested) {
			slog.Info("auto-fix using title suggestion from review",
				"trace_id", execCtx.TraceID,
				"suggested", suggested)
			return suggested, nil
		}
	}

	// Build issue list
	issueList := ""
	for i, issue := range issues {
		issueList += fmt.Sprintf("%d. [%s/%s] %s\n", i+1, issue.Severity, issue.Type, issue.Message)
	}

	// Build title constraints (unified rendering)
	var constraints strings.Builder
	if s.profile != nil {
		constraints.WriteString(s.profile.RenderTitleConstraints())
	}

	systemMsg := "你是文章标题修正助手。根据问题列表和正文重新生成一个标题。只输出新标题（纯文本，不要引号、不要 Markdown 标记），不要解释。"
	userMsg := fmt.Sprintf(`原标题：%s

文章正文：
%s

标题存在的问题：
%s

%s
要求：生成一个新标题，必须满足上述所有约束，且能概括正文核心论点。只输出标题本身。`,
		execCtx.ArticleTitle, execCtx.Article, issueList, constraints.String())

	resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0.3), tools.WithThinking(false))
	if err != nil {
		return "", err
	}

	// Clean up the response: strip quotes, markdown markers, newlines
	newTitle := strings.TrimSpace(resp)
	// Strip ASCII quotes/backticks/hash and CJK quotes
	newTitle = strings.Trim(newTitle, "\"'`#")
	newTitle = strings.TrimFunc(newTitle, func(r rune) bool {
		switch r {
		case '\u300c', '\u300d': // 「」
			return true
		case '\u201c', '\u201d': // ""
			return true
		case '\u2018', '\u2019': // ''
			return true
		}
		return false
	})
	// Take only the first line (in case LLM added explanation)
	if idx := strings.Index(newTitle, "\n"); idx > 0 {
		newTitle = strings.TrimSpace(newTitle[:idx])
	}
	return newTitle, nil
}

// fixBody fixes article body issues using the existing approach.
func (s *AutoFixStep) fixBody(ctx context.Context, execCtx *engine.ExecutionContext, issues []engine.ReviewIssue) (string, error) {
	// Build issue list with severity and type context
	issueList := ""
	for i, issue := range issues {
		issueList += fmt.Sprintf("%d. [%s/%s] %s\n", i+1, issue.Severity, issue.Type, issue.Message)
	}

	// Determine low-scoring dimensions for targeted fix (body dimensions only)
	bodyScoreDims := map[string]bool{
		"factuality": true, "structure": true, "style": true,
		"rhetoric": true, "length": true, "safety": true,
	}
	var lowDims []string
	for dim, score := range execCtx.ReviewResult.Scores {
		if score < 0.7 && bodyScoreDims[dim] {
			lowDims = append(lowDims, fmt.Sprintf("%s (%.1f)", dim, score))
		}
	}
	lowDimsStr := "无"
	if len(lowDims) > 0 {
		lowDimsStr = strings.Join(lowDims, ", ")
	}

	// Build fix-specific instructions based on issue types
	var fixInstructions strings.Builder
	fixInstructions.WriteString("修正要求：\n")
	fixInstructions.WriteString("1. 针对每个问题进行精确修正\n")
	fixInstructions.WriteString("2. 保持原文的整体结构和风格\n")
	fixInstructions.WriteString("3. 不要添加或删除段落\n")
	fixInstructions.WriteString("4. 只修正有问题的部分，保持正确部分不变\n")
	fixInstructions.WriteString("5. 输出完整的修正后文章（不要输出标题，只输出正文）\n")

	// Add specific instructions for fact_guard issues
	for _, issue := range issues {
		if issue.Type == "fact_guard" {
			fixInstructions.WriteString("6. 事实红线问题：将违规表述替换为中性描述（如「获得」「参与」等非结果性动词），不得改变事实本身\n")
			break
		}
	}
	// Add specific instructions for length issues
	for _, issue := range issues {
		if issue.Type == "length" {
			fixInstructions.WriteString("7. 篇幅问题：通过删减冗余表述、合并重复论证来调整字数到要求范围内，不得删除核心论点\n")
			break
		}
	}

	systemMsg := "你是文章修正助手。根据问题列表和评分结果修正文章。只输出修正后的完整文章正文（Markdown格式，不要输出标题），不要解释。"
	userMsg := fmt.Sprintf(`原文：
%s

需要修正的问题：
%s

低分维度：
%s

%s`, execCtx.Article, issueList, lowDimsStr, fixInstructions.String())

	resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0.3), tools.WithThinking(false))
	if err != nil {
		return "", err
	}

	if resp == "" {
		return "", nil
	}

	// Strip any leading title heading from the fixed article (title is stored separately)
	fixedArticle := StripLeadingTitleHeading(resp, execCtx.ArticleTitle)
	// Also filter out any JSON prefix lines that might have been included
	fixedArticle = FilterJSONLines(fixedArticle)
	fixedArticle = strings.TrimSpace(fixedArticle)

	return fixedArticle, nil
}

// time import to avoid unused warning
var _ = time.Now

// ─── 独立标题评审 ────────────────────────────────────────

// titleReviewResult 是标题评审 LLM 返回的结构。
type titleReviewResult struct {
	Scores         map[string]float64   `json:"scores"`
	Issues         []engine.ReviewIssue `json:"issues"`
	SuggestedTitle string               `json:"suggested_title,omitempty"`
}

// reviewTitle 对文章标题进行独立评审。
// 传入标题 + 正文，LLM 能同时看到两者，评：
//  1. title_quality：标题自身质量（字数合规、不笼统、非标题党、有信息量）
//  2. title_relevance：标题与正文核心论点的关联性（是否概括主题、是否一致、是否过度拔高）
//
// 并可选输出 suggested_title 供 AutoFixStep 直接使用（省一次 LLM 调用）。
func (s *PostReviewStep) reviewTitle(ctx context.Context, execCtx *engine.ExecutionContext) (*titleReviewResult, error) {
	if execCtx.ArticleTitle == "" {
		return nil, nil
	}

	// 构建标题专属规则
	var titleRules strings.Builder
	if s.profile != nil {
		if s.profile.TitleGuidelines.Length.Max > 0 {
			titleRules.WriteString(fmt.Sprintf("标题字数限制：%d-%d字\n",
				s.profile.TitleGuidelines.Length.Min, s.profile.TitleGuidelines.Length.Max))
		}
		if s.profile.TitleGuidelines.Style != "" {
			titleRules.WriteString(fmt.Sprintf("标题风格要求：%s\n", s.profile.TitleGuidelines.Style))
		}
		if len(s.profile.TitleGuidelines.Examples) > 0 {
			titleRules.WriteString(fmt.Sprintf("标题参考示例：%s\n",
				strings.Join(s.profile.TitleGuidelines.Examples, " / ")))
		}
		if len(s.profile.TitleGuidelines.ForbiddenPatterns) > 0 {
			titleRules.WriteString(fmt.Sprintf("标题禁止模式（正则，标题不得匹配）：%s\n",
				strings.Join(s.profile.TitleGuidelines.ForbiddenPatterns, ", ")))
		}
	}

	systemMsg := "你是文章标题质量评审员。只返回 JSON，不要解释。"
	userMsg := fmt.Sprintf(`请评审以下文章标题：

标题：%s

文章正文：
%s

%s
评审维度：
1. title_quality：标题自身质量（字数是否合规、是否笼统、是否含标题党用词、是否有信息量）
2. title_relevance：标题与正文核心论点的关联性（标题是否概括了正文主题、是否与正文一致、是否过度拔高或偏离正文论点）

issue type 约定：title_length（字数不合规）、title_generic（过于笼统）、title_clickbait（标题党）、title_relevance（与正文关联性弱）、title_quality（综合质量低）

返回格式：
{
  "scores": {"title_quality": 0.85, "title_relevance": 0.8},
  "issues": [{"severity": "medium", "type": "title_generic", "message": "标题过于笼统，缺少具体信息"}],
  "suggested_title": "建议的新标题（如标题无问题可留空字符串）"
}`, execCtx.ArticleTitle, execCtx.Article, titleRules.String())

	resp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "user", Content: userMsg},
	}, tools.WithInstructions(systemMsg), tools.WithTemperature(0), tools.WithThinking(true), tools.WithReasoningEffort("high"), tools.WithJSONResponse())
	if err != nil {
		return nil, err
	}

	jsonStr := tools.ExtractJSONObject(resp)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON in title review response")
	}

	var tr titleReviewResult
	if err := json.Unmarshal([]byte(jsonStr), &tr); err != nil {
		return nil, err
	}
	if tr.Scores == nil {
		tr.Scores = map[string]float64{}
	}
	if tr.Issues == nil {
		tr.Issues = []engine.ReviewIssue{}
	}
	return &tr, nil
}

// factCheckArticle extracts verifiable factual claims from the article,
// searches the web for each, and returns a structured context string
// for the review LLM. This addresses the problem where the model reports
// "not yet published" for policies/documents that have actually been released.
//
// Strategy:
//  1. Ask LLM to extract 4-8 key verifiable claims (policies, data, events)
//  2. Search the web for each claim concurrently
//  3. If Jiaozhen client is available, also run rumor fact-checking on candidate claims
//  4. Format results as structured context
func (s *PostReviewStep) factCheckArticle(ctx context.Context, execCtx *engine.ExecutionContext) string {
	// Step 1: Extract verifiable claims via LLM
	extractSys := "你是事实核查助手。从文章中提取可验证的关键事实声明（如政策名称、数据统计、事件时间等）。只返回 JSON。"
	extractUser := fmt.Sprintf(`从以下文章中提取 4-8 个最关键的可验证事实声明（如政策/文件名称及发布状态、关键数据、重要事件等）。

文章：
%s

当前日期：%s

返回格式：
{
  "claims": [
    {"claim": "事实声明", "query": "用于搜索验证的关键词"}
  ]
}`, execCtx.Article, time.Now().Format("2006年1月2日"))

	extractResp, _, err := s.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: extractSys},
		{Role: "user", Content: extractUser},
	}, tools.WithTemperature(0), tools.WithThinking(false), tools.WithJSONResponse())
	if err != nil {
		slog.Warn("fact-check: claim extraction failed", "error", err, "trace_id", execCtx.TraceID)
		return ""
	}

	jsonStr := tools.ExtractJSONObject(extractResp)
	if jsonStr == "" {
		return ""
	}

	var extracted struct {
		Claims []struct {
			Claim string `json:"claim"`
			Query string `json:"query"`
		} `json:"claims"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
		return ""
	}

	if len(extracted.Claims) == 0 {
		return ""
	}

	slog.Info("fact-check: extracted claims",
		"trace_id", execCtx.TraceID,
		"count", len(extracted.Claims))

	// Step 2: Search for each claim concurrently (web search)
	type claimResult struct {
		Claim     string
		Query     string
		Results   []engine.SearchResult
		Jiaozhen  *tools.JiaozhenResult
	}

	var (
		mu      sync.Mutex
		results []claimResult
		wg      sync.WaitGroup
	)

	for _, c := range extracted.Claims {
		if c.Query == "" {
			continue
		}
		wg.Add(1)
		go func(claim, query string) {
			defer wg.Done()

			// Web search
			sr := s.search.Search(ctx, query, 3)

			// Jiaozhen fact-check (if configured, skip candidate filter for article claims)
			var jr *tools.JiaozhenResult
			if s.jiaozhen != nil && s.jiaozhen.IsConfigured() {
				jr = s.jiaozhen.CheckClaimDirect(ctx, claim)
				if jr.Status != "ok" {
					jr = nil // skip non-successful results
				}
			}

			mu.Lock()
			results = append(results, claimResult{
				Claim:    claim,
				Query:    query,
				Results:  sr,
				Jiaozhen: jr,
			})
			mu.Unlock()
		}(c.Claim, c.Query)
	}
	wg.Wait()

	if len(results) == 0 {
		return ""
	}

	// Step 3: Format results
	var sb strings.Builder
	jiaozhenCount := 0
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("### 事实声明 %d：%s\n", i+1, r.Claim))
		sb.WriteString(fmt.Sprintf("搜索关键词：%s\n", r.Query))

		// Web search results
		if len(r.Results) == 0 {
			sb.WriteString("搜索结果：无匹配结果\n")
		} else {
			sb.WriteString("搜索结果：\n")
			for j, sr := range r.Results {
				sb.WriteString(fmt.Sprintf("  %d. [%s] %s\n     %s\n", j+1, sr.Source, sr.Title, sr.Snippet))
				if j >= 2 {
					break
				}
			}
		}

		// Jiaozhen fact-check result
		if r.Jiaozhen != nil {
			sb.WriteString(fmt.Sprintf("较真查证结果：\n%s\n", r.Jiaozhen.Content))
			jiaozhenCount++
		}

		sb.WriteString("\n")
	}

	slog.Info("fact-check: completed",
		"trace_id", execCtx.TraceID,
		"claims_checked", len(results),
		"jiaozhen_checked", jiaozhenCount)

	return sb.String()
}

// containsString checks if a string exists in a slice (case-sensitive).
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
