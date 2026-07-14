package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// SearchClient manages multi-source search with concurrent execution.
type SearchClient struct {
	tavily  *TavilyClient
	zhihu   *ZhihuClient
	ima     *IMAClient
	tencent *TencentNewsClient
	weibo   *WeiboClient
}

// NewSearchClient creates a new search client.
func NewSearchClient(tavilyAPIKey, tavilyEndpoint string, tavilyTimeout time.Duration,
	zhihuEnabled bool, zhihuBaseURL, zhihuAccessSecret string, zhihuTimeout time.Duration,
	imaBaseURL, imaClientID, imaAPIKey, imaKBID string, imaTimeout time.Duration,
	tencentEnabled bool, tencentBaseURL string, tencentTimeout time.Duration,
	weiboEnabled bool, weiboBaseURL string, weiboTimeout time.Duration,
) *SearchClient {
	c := &SearchClient{}

	if tavilyAPIKey != "" {
		c.tavily = NewTavilyClient(tavilyAPIKey, tavilyEndpoint, tavilyTimeout)
	}

	if zhihuEnabled && zhihuAccessSecret != "" {
		c.zhihu = NewZhihuClient(zhihuBaseURL, zhihuAccessSecret, zhihuTimeout)
	}

	if imaClientID != "" && imaAPIKey != "" {
		c.ima = NewIMAClient(imaBaseURL, imaClientID, imaAPIKey, imaKBID, imaTimeout)
	}

	if tencentEnabled {
		c.tencent = NewTencentNewsClient(tencentBaseURL, tencentTimeout)
	}

	if weiboEnabled {
		c.weibo = NewWeiboClient(weiboBaseURL, weiboTimeout)
	}

	return c
}

// HasSources returns true if at least one search source is configured.
func (c *SearchClient) HasSources() bool {
	return c.tavily != nil || c.zhihu != nil || c.ima != nil || c.tencent != nil || c.weibo != nil
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

	if c.ima != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.ima.Search(ctx, query, maxPerSource)
			if err != nil {
				slog.Warn("ima search failed", "error", err, "query", query)
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

	wg.Wait()

	// Truncate to maxTotal
	if len(results) > maxTotal {
		results = results[:maxTotal]
	}

	slog.Info("multi-source search completed",
		"query", query,
		"results", len(results),
		"sources", c.activeSources(),
	)

	return results
}

func (c *SearchClient) activeSources() []string {
	var sources []string
	if c.tavily != nil {
		sources = append(sources, "tavily")
	}
	if c.zhihu != nil {
		sources = append(sources, "zhihu")
	}
	if c.ima != nil {
		sources = append(sources, "ima")
	}
	if c.tencent != nil {
		sources = append(sources, "tencent")
	}
	if c.weibo != nil {
		sources = append(sources, "weibo")
	}
	return sources
}

// FetchHotTopics fetches hot topics from all configured sources (Tencent, Weibo).
func (c *SearchClient) FetchHotTopics(ctx context.Context, limit int) []map[string]interface{} {
	if limit <= 0 {
		limit = 20
	}

	var (
		mu      sync.Mutex
		results []map[string]interface{}
		wg      sync.WaitGroup
	)

	if c.tencent != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			topics, err := c.tencent.FetchHotTopics(ctx, limit)
			if err != nil {
				slog.Warn("tencent hot topics fetch failed", "error", err)
				return
			}
			mu.Lock()
			results = append(results, topics...)
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

	wg.Wait()

	if len(results) > limit {
		results = results[:limit]
	}

	return results
}

// IMAClient returns the IMA client (for knowledge base sync).
func (c *SearchClient) IMAClient() *IMAClient {
	return c.ima
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
	url := fmt.Sprintf("%s/api/v1/content/zhihu_search?Query=%s&Count=%d",
		c.baseURL, encodeQuery(query), limit)

	return c.doZhihuSearch(ctx, url, "知乎搜索")
}

func (c *ZhihuClient) searchGlobal(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	url := fmt.Sprintf("%s/api/v1/content/global_search?Query=%s&Count=%d&SearchDB=all",
		c.baseURL, encodeQuery(query), limit)

	return c.doZhihuSearch(ctx, url, "知乎全网搜索")
}

func (c *ZhihuClient) doZhihuSearch(ctx context.Context, url, source string) ([]engine.SearchResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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

// ─── IMA Client ──────────────────────────────────────────

type IMAClient struct {
	baseURL  string
	clientID string
	apiKey   string
	kbID     string
	timeout  time.Duration
	client   *http.Client
}

func NewIMAClient(baseURL, clientID, apiKey, kbID string, timeout time.Duration) *IMAClient {
	return &IMAClient{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		clientID: clientID,
		apiKey:   apiKey,
		kbID:     kbID,
		timeout:  timeout,
		client:   &http.Client{Timeout: timeout},
	}
}

func (c *IMAClient) Search(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	if c.kbID == "" {
		return nil, fmt.Errorf("IMA KB ID not configured")
	}

	body := map[string]interface{}{
		"knowledge_base_id": c.kbID,
		"query":             query,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := c.baseURL + "/openapi/wiki/v1/search_knowledge"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("ima-openapi-clientid", c.clientID)
	req.Header.Set("ima-openapi-apikey", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Code int `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			InfoList []struct {
				Title            string `json:"title"`
				HighlightContent string `json:"highlight_content"`
				MediaID          string `json:"media_id"`
				URL              string `json:"url"`
			} `json:"info_list"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("IMA API error code %d: %s", result.Code, result.Msg)
	}

	results := make([]engine.SearchResult, 0, len(result.Data.InfoList))
	for _, item := range result.Data.InfoList {
		title := item.Title
		if title == "" {
			title = "无标题"
		}
		snippet := title + "：" + item.HighlightContent
		results = append(results, engine.SearchResult{
			Title:   title,
			Snippet: snippet,
			URL:     item.URL,
			Source:  "ima",
		})
		if len(results) >= limit {
			break
		}
	}

	slog.Debug("ima search done", "query", query, "count", len(results))
	return results, nil
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
