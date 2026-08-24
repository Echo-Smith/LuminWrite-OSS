package tools

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// KnowledgeSearcher is the interface for local knowledge base search.
// It replaces the concrete WeKnoraClient, allowing the search pipeline
// to use the local KbManager (in services package) without circular imports.
//
// userID enables per-user KB isolation: when non-empty, the search is scoped
// to documents owned by that user (user_id = userID) plus shared documents
// (user_id IS NULL). When empty, all documents are searched (admin/global scope).
type KnowledgeSearcher interface {
	SearchKB(ctx context.Context, userID, query string, limit int) ([]engine.SearchResult, error)
	// SearchKBInKB searches within a specific knowledge base identified by kbID.
	// When kbID is empty, behaves identically to SearchKB (searches all KBs).
	SearchKBInKB(ctx context.Context, userID, kbID, query string, limit int) ([]engine.SearchResult, error)
}

// SearchClient manages multi-source search with concurrent execution.
type SearchClient struct {
	tavily            *TavilyClient
	zhihu             *ZhihuClient
	tencent           *TencentNewsClient
	tencentCLI        *TencentNewsCLIClient
	weibo             *WeiboClient
	extraHot          *ExtraHotClient
	bing              *BingClient
	anysearch         *AnySearchClient
	credibilityLookup engine.CredibilityLookup // optional: enrich results with source credibility
}

// NewSearchClient creates a new search client.
func NewSearchClient(tavilyAPIKey, tavilyEndpoint string, tavilyTimeout time.Duration,
	zhihuEnabled bool, zhihuBaseURL, zhihuAccessSecret string, zhihuTimeout time.Duration,
	tencentEnabled bool, tencentBaseURL string, tencentTimeout time.Duration,
	weiboEnabled bool, weiboBaseURL string, weiboTimeout time.Duration,
	extraHotEnabled bool, extraHotBaseURL string, extraHotTimeout time.Duration,
	bingEnabled bool, bingBaseURL string, bingTimeout time.Duration,
	tencentCLIPath string, tencentCLITimeout time.Duration,
	anysearchAPIKey, anysearchEndpoint string, anysearchTimeout time.Duration,
) *SearchClient {
	c := &SearchClient{}

	if tavilyAPIKey != "" {
		c.tavily = NewTavilyClient(tavilyAPIKey, tavilyEndpoint, tavilyTimeout)
	}

	if zhihuEnabled && zhihuAccessSecret != "" {
		c.zhihu = NewZhihuClient(zhihuBaseURL, zhihuAccessSecret, zhihuTimeout)
	}

	if tencentEnabled {
		c.tencent = NewTencentNewsClient(tencentBaseURL, tencentTimeout)
	}

	// Always try to init the CLI client (auto-detects binary on PATH)
	c.tencentCLI = NewTencentNewsCLIClient(tencentCLIPath, tencentCLITimeout)

	if weiboEnabled {
		c.weibo = NewWeiboClient(weiboBaseURL, weiboTimeout)
	}

	if extraHotEnabled {
		c.extraHot = NewExtraHotClient(extraHotBaseURL, extraHotTimeout)
	}

	if bingEnabled {
		c.bing = NewBingClient(bingBaseURL, bingTimeout)
	}

	// AnySearch: always init (anonymous tier works without API key)
	c.anysearch = NewAnySearchClient(anysearchAPIKey, anysearchEndpoint, anysearchTimeout)

	return c
}

// SetCredibilityLookup sets an optional credibility lookup provider.
// When set, search results will be enriched with credibility scores
// and sorted by combined relevance × credibility.
func (c *SearchClient) SetCredibilityLookup(lookup engine.CredibilityLookup) {
	c.credibilityLookup = lookup
}

// HasSources returns true if at least one search source is configured.
func (c *SearchClient) HasSources() bool {
	return c.tavily != nil || c.zhihu != nil || c.tencent != nil || c.tencentCLI != nil && c.tencentCLI.IsConfigured() || c.weibo != nil || c.extraHot != nil || c.bing != nil || c.anysearch != nil
}

// Search executes concurrent multi-source search and returns aggregated results.
func (c *SearchClient) Search(ctx context.Context, query string, maxTotal int) []engine.SearchResult {
	searchStart := time.Now()
	defer func() {
		RecordSearchCall(time.Since(searchStart).Nanoseconds(), nil)
	}()

	if maxTotal <= 0 {
		maxTotal = 9
	}

	maxPerSource := 3
	if maxTotal < 9 {
		maxPerSource = maxTotal / 3
		if maxPerSource < 1 {
			maxPerSource = 1
		}
	}

	var (
		mu      sync.Mutex
		results []engine.SearchResult
		wg      sync.WaitGroup
	)

	if c.tavily != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.tavily.Search(ctx, query, maxPerSource)
			if err != nil {
				slog.Warn("tavily search failed", "error", err, "query", query)
				return
			}
			mu.Lock()
			results = append(results, r...)
			mu.Unlock()
		}()
	}

	if c.zhihu != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.zhihu.Search(ctx, query, maxPerSource)
			if err != nil {
				slog.Warn("zhihu search failed", "error", err, "query", query)
				return
			}
			mu.Lock()
			results = append(results, r...)
			mu.Unlock()
		}()
	}

	if c.tencent != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.tencent.Search(ctx, query, maxPerSource)
			if err != nil {
				slog.Warn("tencent news search failed", "error", err, "query", query)
				return
			}
			mu.Lock()
			results = append(results, r...)
			mu.Unlock()
		}()
	}

	// CLI-based news search (more reliable than HTTP scraping)
	if c.tencentCLI != nil && c.tencentCLI.IsConfigured() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.tencentCLI.Search(ctx, query, maxPerSource)
			if err != nil {
				slog.Warn("tencent news CLI search failed", "error", err, "query", query)
				return
			}
			mu.Lock()
			results = append(results, r...)
			mu.Unlock()
		}()
	}

	if c.weibo != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.weibo.Search(ctx, query, maxPerSource)
			if err != nil {
				slog.Warn("weibo search failed", "error", err, "query", query)
				return
			}
			mu.Lock()
			results = append(results, r...)
			mu.Unlock()
		}()
	}

	if c.bing != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.bing.Search(ctx, query, maxPerSource)
			if err != nil {
				slog.Warn("bing search failed", "error", err, "query", query)
				return
			}
			mu.Lock()
			results = append(results, r...)
			mu.Unlock()
		}()
	}

	if c.anysearch != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.anysearch.Search(ctx, query, maxPerSource)
			if err != nil {
				slog.Warn("anysearch failed", "error", err, "query", query)
				return
			}
			mu.Lock()
			results = append(results, r...)
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Enrich with credibility scores if a lookup is configured
	if c.credibilityLookup != nil && len(results) > 0 {
		results = c.enrichWithCredibility(ctx, results)
	}

	// Truncate to maxTotal
	if len(results) > maxTotal {
		results = results[:maxTotal]
	}

	slog.Info("multi-source search completed",
		"query", query,
		"results", len(results),
		"sources", c.activeSources(),
		"credibility_enriched", c.credibilityLookup != nil,
	)

	return results
}

// enrichWithCredibility adds credibility scores to search results and sorts by combined score.
// Results from high-credibility sources are ranked higher.
func (c *SearchClient) enrichWithCredibility(ctx context.Context, results []engine.SearchResult) []engine.SearchResult {
	for i := range results {
		domain := extractDomain(results[i].URL)
		if domain == "" {
			results[i].CredibilityScore = 0.5 // neutral for results without URL
			continue
		}
		score, err := c.credibilityLookup.GetCredibility(ctx, domain)
		if err != nil || score <= 0 {
			results[i].CredibilityScore = 0.5 // default neutral
		} else {
			results[i].CredibilityScore = score
		}
	}

	// Sort by combined score: relevance_score × (0.5 + 0.5 × credibility)
	// This gives a 50% weight to each factor, but credibility acts as a multiplier
	sort.SliceStable(results, func(i, j int) bool {
		scoreI := results[i].Score * (0.5 + 0.5*results[i].CredibilityScore)
		scoreJ := results[j].Score * (0.5 + 0.5*results[j].CredibilityScore)
		// If scores are equal (e.g., both 0), sort by credibility alone
		if scoreI == scoreJ {
			return results[i].CredibilityScore > results[j].CredibilityScore
		}
		return scoreI > scoreJ
	})

	return results
}

// extractDomain extracts the domain from a URL string.
func extractDomain(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	// Handle URLs without scheme
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func (c *SearchClient) activeSources() []string {
	var sources []string
	if c.tavily != nil {
		sources = append(sources, "tavily")
	}
	if c.zhihu != nil {
		sources = append(sources, "zhihu")
	}
	if c.tencent != nil {
		sources = append(sources, "tencent")
	}
	if c.tencentCLI != nil && c.tencentCLI.IsConfigured() {
		sources = append(sources, "tencent-cli")
	}
	if c.weibo != nil {
		sources = append(sources, "weibo")
	}
	if c.bing != nil {
		sources = append(sources, "bing")
	}
	if c.extraHot != nil {
		sources = append(sources, "extra_hot")
	}
	if c.anysearch != nil {
		sources = append(sources, "anysearch")
	}
	return sources
}

// ActiveSources returns the list of configured search source names (public).
func (c *SearchClient) ActiveSources() []string {
	return c.activeSources()
}

// FetchHotTopics fetches hot topics from all configured sources (Tencent, Weibo).
func (c *SearchClient) FetchHotTopics(ctx context.Context, limit int) []map[string]interface{} {
	if limit <= 0 {
		limit = 20
	}

	var (
		mu         sync.Mutex
		results    []map[string]interface{}
		tencentCnt int
		wg         sync.WaitGroup
	)

	if c.tencent != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			topics, err := c.tencent.FetchHotTopics(ctx, limit)
			if err != nil {
				slog.Warn("tencent hot topics fetch failed (HTTP API)", "error", err)
				return
			}
			if len(topics) == 0 {
				slog.Warn("tencent hot topics: HTTP API returned 0 results (API may be deprecated)")
			}
			mu.Lock()
			results = append(results, topics...)
			tencentCnt = len(topics)
			mu.Unlock()
		}()
	}

	if c.weibo != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			topics, err := c.weibo.FetchHotTopics(ctx, limit)
			if err != nil {
				slog.Warn("weibo hot topics fetch failed", "error", err)
				return
			}
			mu.Lock()
			results = append(results, topics...)
			mu.Unlock()
		}()
	}

	if c.extraHot != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			topics, err := c.extraHot.FetchHotTopics(ctx, limit)
			if err != nil {
				slog.Warn("extra hot topics fetch failed", "error", err)
				return
			}
			mu.Lock()
			results = append(results, topics...)
			mu.Unlock()
		}()
	}

	wg.Wait()

	// ── Fallback: if Tencent HTTP API returned nothing, try the CLI ──
	if tencentCnt == 0 && c.tencentCLI != nil && c.tencentCLI.IsConfigured() {
		slog.Info("tencent HTTP API returned no results, trying CLI fallback")
		topics, err := c.tencentCLI.FetchHot(ctx, limit)
		if err != nil {
			slog.Warn("tencent CLI hot fetch also failed", "error", err)
		} else {
			// Normalize CLI output to match the expected format
			normalized := normalizeTencentCLITopics(topics, limit)
			if len(normalized) > 0 {
				slog.Info("tencent CLI hot topics fetched successfully", "count", len(normalized))
				mu.Lock()
				results = append(results, normalized...)
				mu.Unlock()
			}
		}
	}

	// ── Deduplicate by title (case-insensitive) ──
	seen := make(map[string]bool)
	deduped := make([]map[string]interface{}, 0, len(results))
	for _, r := range results {
		title, _ := r["title"].(string)
		key := strings.ToLower(strings.TrimSpace(title))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, r)
	}
	results = deduped

	if len(results) > limit*10 {
		results = results[:limit*10]
	}

	return results
}

// normalizeTencentCLITopics converts the raw CLI output items into the
// standard hot topic format with source="tencent" and platform="qq_news".
func normalizeTencentCLITopics(items []map[string]interface{}, limit int) []map[string]interface{} {
	if len(items) == 0 {
		return nil
	}
	topics := make([]map[string]interface{}, 0, len(items))
	for i, item := range items {
		if i >= limit {
			break
		}
		// Try common field names from CLI output
		title, _ := item["title"].(string)
		if title == "" {
			// Skip items without a title
			continue
		}
		description, _ := item["description"].(string)
		if description == "" {
			description, _ = item["summary"].(string)
		}
		if description == "" {
			description, _ = item["hot"].(string)
		}
		url, _ := item["url"].(string)
		if url == "" {
			url, _ = item["link"].(string)
		}
		hotCount := 0
		if hc, ok := item["hot_count"].(float64); ok {
			hotCount = int(hc)
		}
		topic := map[string]interface{}{
			"title":       cleanHTML(title),
			"description": cleanHTML(description),
			"source":      "tencent",
			"platform":    "qq_news",
			"hot_rank":    i + 1,
			"url":         url,
		}
		if hotCount > 0 {
			topic["hot_count"] = hotCount
		}
		topics = append(topics, topic)
	}
	return topics
}

// ─── Helpers ─────────────────────────────────────────────

func encodeQuery(query string) string {
	return strings.ReplaceAll(query, " ", "%20")
}

func cleanHTML(s string) string {
	// Remove HTML tags
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	// Collapse whitespace
	return strings.TrimSpace(strings.Join(strings.Fields(result.String()), " "))
}

// readBody reads at most limit bytes from r.
func readBody(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit))
}
