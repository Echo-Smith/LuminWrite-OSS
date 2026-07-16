package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// ─── Tencent News Client ──────────────────────────────────

// TencentNewsClient fetches hot topics and search results from Tencent News.
// Uses the public QQ News API endpoints (no API key required).
//
// 文档来源: docs/01-architecture.md — Phase 2 第三方搜索源
type TencentNewsClient struct {
	baseURL string
	timeout time.Duration
	client  *http.Client
}

// NewTencentNewsClient creates a new TencentNewsClient.
func NewTencentNewsClient(baseURL string, timeout time.Duration) *TencentNewsClient {
	if baseURL == "" {
		baseURL = "https://r.inews.qq.com"
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &TencentNewsClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
	}
}

// Search queries Tencent News for the given query and returns search results.
func (c *TencentNewsClient) Search(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	if limit <= 0 {
		limit = 3
	}

	// Use the QQ News search API
	url := fmt.Sprintf("%s/searchQQNews?key=%s&num=%d", c.baseURL, encodeQuery(query), limit)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; LuminBuddy/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tencent news API returned status %d", resp.StatusCode)
	}

	// Parse response — QQ News returns varied formats, try common ones
	var data struct {
		Code int `json:"code"`
		Data struct {
			NewsList []struct {
				Title   string `json:"title"`
				Summary string `json:"summary"`
				URL     string `json:"url"`
				Source  string `json:"source"`
			} `json:"newslist"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		// Try alternative format
		var altData struct {
			Response struct {
				NewsList []struct {
					Title   string `json:"title"`
					Summary string `json:"abstract"`
					URL     string `json:"url"`
					Source  string `json:"source"`
				} `json:"newslist"`
			} `json:"response"`
		}
		if err2 := json.Unmarshal(body, &altData); err2 != nil {
			return nil, fmt.Errorf("failed to parse tencent news response: %w", err)
		}
		for _, item := range altData.Response.NewsList {
			data.Data.NewsList = append(data.Data.NewsList, struct {
				Title   string `json:"title"`
				Summary string `json:"summary"`
				URL     string `json:"url"`
				Source  string `json:"source"`
			}{
				Title:   item.Title,
				Summary: item.Summary,
				URL:     item.URL,
				Source:  item.Source,
			})
		}
	}

	results := make([]engine.SearchResult, 0, limit)
	for _, item := range data.Data.NewsList {
		if item.Title == "" {
			continue
		}
		snippet := item.Title
		if item.Summary != "" {
			snippet += "：" + cleanHTML(item.Summary)
		}
		results = append(results, engine.SearchResult{
			Title:   cleanHTML(item.Title),
			Snippet: snippet,
			URL:     item.URL,
			Source:  "tencent",
		})
		if len(results) >= limit {
			break
		}
	}

	slog.Debug("tencent news search done", "query", query, "count", len(results))
	return results, nil
}

// FetchHotTopics fetches the current hot news list from Tencent News.
// Tries multiple known API endpoints as fallbacks.
func (c *TencentNewsClient) FetchHotTopics(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	if limit <= 0 {
		limit = 20
	}

	// List of known Tencent News hot list endpoints (tried in order)
	endpoints := []string{
		fmt.Sprintf("%s/getQQNewsUnreadList?machine=samsung&nums=%d", c.baseURL, limit),
		fmt.Sprintf("%s/gettop10?nums=%d", c.baseURL, limit),
		fmt.Sprintf("https://api.vvhan.com/api/hotlist/qqNews", ),
	}

	var body []byte
	var usedEndpoint string
	for _, ep := range endpoints {
		req, err := http.NewRequestWithContext(ctx, "GET", ep, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "application/json, text/plain, */*")

		resp, err := c.client.Do(req)
		if err != nil {
			slog.Debug("tencent hot topics endpoint failed", "url", ep, "error", err)
			continue
		}

		body, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			slog.Debug("tencent hot topics endpoint returned non-200", "url", ep, "status", resp.StatusCode)
			continue
		}
		usedEndpoint = ep
		break
	}

	if body == nil {
		return nil, fmt.Errorf("all tencent hot news endpoints failed")
	}

	topics := parseTencentHotTopics(body, limit)
	if len(topics) == 0 {
		// Try vvhan API format (different structure)
		topics = parseVVHanQQNews(body, limit)
	}

	slog.Debug("tencent hot topics fetched", "count", len(topics), "endpoint", usedEndpoint)
	return topics, nil
}

// parseTencentHotTopics parses the standard Tencent News API response format.
func parseTencentHotTopics(body []byte, limit int) []map[string]interface{} {
	var data struct {
		Code int `json:"code"`
		Data struct {
			NewsList []struct {
				Title   string `json:"title"`
				Summary string `json:"summary"`
				URL     string `json:"url"`
				ID      string `json:"id"`
			} `json:"newslist"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}
	if data.Code != 0 && len(data.Data.NewsList) == 0 {
		return nil
	}

	topics := make([]map[string]interface{}, 0, len(data.Data.NewsList))
	for i, item := range data.Data.NewsList {
		if i >= limit {
			break
		}
		topics = append(topics, map[string]interface{}{
			"title":       cleanHTML(item.Title),
			"description": cleanHTML(item.Summary),
			"source":      "tencent",
			"platform":    "qq_news",
			"hot_rank":    i + 1,
			"url":         item.URL,
		})
	}
	return topics
}

// parseVVHanQQNews parses the vvhan.com QQ News hot list API format.
func parseVVHanQQNews(body []byte, limit int) []map[string]interface{} {
	var data struct {
		Success bool `json:"success"`
		Name    string `json:"name"`
		Data    []struct {
			Title  string `json:"title"`
			Mobil  string `json:"mobil"`
			URL    string `json:"url"`
			Hot    string `json:"hot"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}
	if !data.Success || len(data.Data) == 0 {
		return nil
	}
	topics := make([]map[string]interface{}, 0, len(data.Data))
	for i, item := range data.Data {
		if i >= limit {
			break
		}
		topics = append(topics, map[string]interface{}{
			"title":       cleanHTML(item.Title),
			"description": item.Hot,
			"source":      "tencent",
			"platform":    "qq_news",
			"hot_rank":    i + 1,
			"url":         item.URL,
		})
	}
	return topics
}

// ─── Weibo Client ─────────────────────────────────────────

// WeiboClient fetches hot search topics and search results from Weibo.
// Uses the public Weibo AJAX API endpoints.
//
// 文档来源: docs/01-architecture.md — Phase 2 第三方搜索源
type WeiboClient struct {
	baseURL string
	timeout time.Duration
	client  *http.Client
}

// NewWeiboClient creates a new WeiboClient.
func NewWeiboClient(baseURL string, timeout time.Duration) *WeiboClient {
	if baseURL == "" {
		baseURL = "https://weibo.com/ajax"
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &WeiboClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
	}
}

// Search queries Weibo for the given query and returns search results.
func (c *WeiboClient) Search(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	if limit <= 0 {
		limit = 3
	}

	// Weibo search AJAX endpoint
	url := fmt.Sprintf("%s/search/all?q=%s&page=1&num=%d", c.baseURL, encodeQuery(query), limit)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; LuminBuddy/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weibo search API returned status %d", resp.StatusCode)
	}

	// Parse Weibo search response
	var data struct {
		Data struct {
			Notes []struct {
				Title       string `json:"note_title"`
				Text        string `json:"text"`
				URL         string `json:"url"`
				Author      string `json:"user"`
				LikeCount   int    `json:"like_count"`
			} `json:"notes"`
		} `json:"data"`
		Ok int `json:"ok"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to parse weibo search response: %w", err)
	}

	results := make([]engine.SearchResult, 0, limit)
	for _, item := range data.Data.Notes {
		title := item.Title
		if title == "" {
			title = item.Text[:min(len(item.Text), 50)]
		}
		snippet := title
		if item.Text != "" {
			snippet += "：" + cleanHTML(item.Text)
		}
		if item.Author != "" {
			snippet += "（作者：" + item.Author + "）"
		}
		results = append(results, engine.SearchResult{
			Title:   cleanHTML(title),
			Snippet: snippet,
			URL:     item.URL,
			Source:  "weibo",
		})
		if len(results) >= limit {
			break
		}
	}

	slog.Debug("weibo search done", "query", query, "count", len(results))
	return results, nil
}

// FetchHotTopics fetches the current Weibo hot search list.
func (c *WeiboClient) FetchHotTopics(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	if limit <= 0 {
		limit = 20
	}

	// Try the official Weibo AJAX API first, then fall back to vvhan.com
	endpoints := []string{
		fmt.Sprintf("%s/side/hotSearch", c.baseURL),
		"https://api.vvhan.com/api/hotlist/wbHot",
	}

	var body []byte
	var usedEndpoint string
	for _, ep := range endpoints {
		req, err := http.NewRequestWithContext(ctx, "GET", ep, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Referer", "https://weibo.com")

		resp, err := c.client.Do(req)
		if err != nil {
			slog.Debug("weibo hot topics endpoint failed", "url", ep, "error", err)
			continue
		}

		body, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			slog.Debug("weibo hot topics endpoint returned non-200", "url", ep, "status", resp.StatusCode)
			continue
		}
		usedEndpoint = ep
		break
	}

	if body == nil {
		return nil, fmt.Errorf("all weibo hot search endpoints failed")
	}

	// Try official Weibo format first
	topics := parseWeiboHotSearch(body, limit)
	if len(topics) == 0 {
		// Try vvhan.com format
		topics = parseVVHanWeibo(body, limit)
	}

	slog.Debug("weibo hot topics fetched", "count", len(topics), "endpoint", usedEndpoint)
	return topics, nil
}

// parseWeiboHotSearch parses the official Weibo hot search API response.
func parseWeiboHotSearch(body []byte, limit int) []map[string]interface{} {
	var data struct {
		Data struct {
			Realtime []struct {
				Note      string `json:"note"`
				LabelName string `json:"label_name"`
				Num       int    `json:"num"`
				Rank      int    `json:"rank"`
				URL       string `json:"url"`
				Word      string `json:"word"`
			} `json:"realtime"`
		} `json:"data"`
		Ok int `json:"ok"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}
	if data.Ok != 1 && len(data.Data.Realtime) == 0 {
		return nil
	}

	topics := make([]map[string]interface{}, 0, limit)
	for i, item := range data.Data.Realtime {
		if i >= limit {
			break
		}
		title := item.Word
		if title == "" {
			title = item.Note
		}
		rank := item.Rank
		if rank == 0 {
			rank = i + 1
		}
		topics = append(topics, map[string]interface{}{
			"title":       cleanHTML(title),
			"description": cleanHTML(item.Note),
			"source":      "weibo",
			"platform":    "weibo",
			"hot_rank":    rank,
			"hot_count":   item.Num,
			"url":         item.URL,
		})
	}
	return topics
}

// parseVVHanWeibo parses the vvhan.com Weibo hot search API format.
func parseVVHanWeibo(body []byte, limit int) []map[string]interface{} {
	var data struct {
		Success bool `json:"success"`
		Name    string `json:"name"`
		Data    []struct {
			Title  string `json:"title"`
			Hot    string `json:"hot"`
			URL    string `json:"url"`
			Mobil  string `json:"mobil"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}
	if !data.Success || len(data.Data) == 0 {
		return nil
	}
	topics := make([]map[string]interface{}, 0, len(data.Data))
	for i, item := range data.Data {
		if i >= limit {
			break
		}
		topics = append(topics, map[string]interface{}{
			"title":       cleanHTML(item.Title),
			"description": item.Hot,
			"source":      "weibo",
			"platform":    "weibo",
			"hot_rank":    i + 1,
			"hot_count":   0,
			"url":         item.URL,
		})
	}
	return topics
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
