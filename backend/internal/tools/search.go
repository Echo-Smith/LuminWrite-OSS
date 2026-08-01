package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
type KnowledgeSearcher interface {
	SearchKB(ctx context.Context, query string, limit int) ([]engine.SearchResult, error)
}

// SearchClient manages multi-source search with concurrent execution.
type SearchClient struct {
	tavily            *TavilyClient
	zhihu             *ZhihuClient
	kbSearcher        KnowledgeSearcher // local KB search (replaces IMA)
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

// SetKnowledgeSearcher attaches a local knowledge base searcher.
// This replaces the old SetWeKnoraClient and enables in-process hybrid search
// (BM25 + Dense + RRF) directly on the local PostgreSQL.
func (c *SearchClient) SetKnowledgeSearcher(s KnowledgeSearcher) {
	c.kbSearcher = s
	slog.Info("local knowledge base search source enabled")
}

// SetCredibilityLookup sets an optional credibility lookup provider.
// When set, search results will be enriched with credibility scores
// and sorted by combined relevance × credibility.
func (c *SearchClient) SetCredibilityLookup(lookup engine.CredibilityLookup) {
	c.credibilityLookup = lookup
}

// HasSources returns true if at least one search source is configured.
func (c *SearchClient) HasSources() bool {
	return c.tavily != nil || c.zhihu != nil || c.kbSearcher != nil || c.tencent != nil || c.tencentCLI != nil && c.tencentCLI.IsConfigured() || c.weibo != nil || c.extraHot != nil || c.bing != nil || c.anysearch != nil
}

// Search executes concurrent multi-source search and returns aggregated results.
func (c *SearchClient) Search(ctx context.Context, query string, maxTotal int) []engine.SearchResult {
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

	if c.kbSearcher != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.kbSearcher.SearchKB(ctx, query, maxPerSource)
			if err != nil {
				slog.Warn("local KB search failed", "error", err, "query", query)
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
	if c.kbSearcher != nil {
		sources = append(sources, "local_kb")
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

// ─── Tavily Client ───────────────────────────────────────

type TavilyClient struct {
	apiKey   string
	endpoint string
	timeout  time.Duration
	client   *http.Client
}

func NewTavilyClient(apiKey, endpoint string, timeout time.Duration) *TavilyClient {
	return &TavilyClient{
		apiKey:   apiKey,
		endpoint: endpoint,
		timeout:  timeout,
		client:   &http.Client{Timeout: timeout},
	}
}

func (c *TavilyClient) Search(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	body := map[string]interface{}{
		"query":              query,
		"search_depth":       "basic",
		"max_results":        limit,
		"topic":              "general",
		"include_answer":     false,
		"include_raw_content": false,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Results []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
			URL     string `json:"url"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	results := make([]engine.SearchResult, 0, len(result.Results))
	for _, item := range result.Results {
		snippet := item.Title + "：" + item.Content
		if item.Content == "" {
			snippet = item.Title
		}
		results = append(results, engine.SearchResult{
			Title:   item.Title,
			Snippet: snippet,
			URL:     item.URL,
			Source:  "tavily",
		})
	}

	slog.Debug("tavily search done", "query", query, "count", len(results))
	return results, nil
}

// ─── Zhihu Client ────────────────────────────────────────

type ZhihuClient struct {
	baseURL      string
	accessSecret string
	timeout      time.Duration
	client       *http.Client
}

func NewZhihuClient(baseURL, accessSecret string, timeout time.Duration) *ZhihuClient {
	return &ZhihuClient{
		baseURL:      strings.TrimSuffix(baseURL, "/"),
		accessSecret: accessSecret,
		timeout:      timeout,
		client:       &http.Client{Timeout: timeout},
	}
}

func (c *ZhihuClient) Search(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	// Try site search first, then global search
	results, err := c.searchSite(ctx, query, limit)
	if err != nil || len(results) == 0 {
		// Fallback to global search
		globalResults, gErr := c.searchGlobal(ctx, query, limit)
		if gErr != nil {
			if err != nil {
				return nil, fmt.Errorf("site search: %w; global search: %w", err, gErr)
			}
			return nil, gErr
		}
		return globalResults, nil
	}
	return results, nil
}

func (c *ZhihuClient) searchSite(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	u := c.baseURL + "/api/v1/content/zhihu_search?" + url.Values{
		"Query": {query},
		"Count": {fmt.Sprintf("%d", limit)},
	}.Encode()
	return c.doZhihuSearch(ctx, u, "知乎搜索")
}

func (c *ZhihuClient) searchGlobal(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	u := c.baseURL + "/api/v1/content/global_search?" + url.Values{
		"Query":     {query},
		"Count":     {fmt.Sprintf("%d", limit)},
		"SearchDB": {"all"},
	}.Encode()
	return c.doZhihuSearch(ctx, u, "知乎全网搜索")
}

func (c *ZhihuClient) doZhihuSearch(ctx context.Context, searchURL, source string) ([]engine.SearchResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessSecret)
	req.Header.Set("X-Request-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data struct {
		Code int `json:"Code"`
		Data struct {
			Items []struct {
				Title         string `json:"Title"`
				ContentText   string `json:"ContentText"`
				URL           string `json:"Url"`
				AuthorName    string `json:"AuthorName"`
				VoteUpCount   int    `json:"VoteUpCount"`
				ContentType   string `json:"ContentType"`
			} `json:"Items"`
		} `json:"Data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	if data.Code != 0 {
		return nil, fmt.Errorf("zhihu API error code: %d", data.Code)
	}

	results := make([]engine.SearchResult, 0, len(data.Data.Items))
	for _, item := range data.Data.Items {
		title := cleanHTML(item.Title)
		content := cleanHTML(item.ContentText)
		snippet := title
		if content != "" {
			snippet += "：" + content
		}
		meta := []string{source}
		if item.AuthorName != "" {
			meta = append(meta, "作者："+item.AuthorName)
		}
		if item.VoteUpCount > 0 {
			meta = append(meta, fmt.Sprintf("赞同 %d", item.VoteUpCount))
		}
		if item.URL != "" {
			meta = append(meta, item.URL)
		}
		snippet += "（" + strings.Join(meta, "｜") + "）"

		results = append(results, engine.SearchResult{
			Title:   title,
			Snippet: snippet,
			URL:     item.URL,
			Source:  "zhihu",
		})
	}

	slog.Debug("zhihu search done", "source", source, "count", len(results))
	return results, nil
}

// isPlaceholderKey checks if the given string is a common placeholder value
// that indicates the key was not actually configured.
func isPlaceholderKey(s string) bool {
	switch s {
	case "", "your-dashscope-api-key", "your-deepseek-api-key":
		return true
	}
	// Also check for strings that start with "your-" or "placeholder"
	if strings.HasPrefix(s, "your-") || strings.HasPrefix(s, "placeholder") {
		return true
	}
	return false
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
