package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// ─── AnySearch Client ─────────────────────────────────────
// https://www.anysearch.com/docs#search-api
// POST https://api.anysearch.com/v1/search
// Auth: Bearer token (optional — anonymous tier available)

const AnySearchDefaultEndpoint = "https://api.anysearch.com/v1/search"
const AnySearchDefaultTimeout = 15 * time.Second

type AnySearchClient struct {
	apiKey   string
	endpoint string
	timeout  time.Duration
	client   *http.Client
}

func NewAnySearchClient(apiKey, endpoint string, timeout time.Duration) *AnySearchClient {
	if endpoint == "" {
		endpoint = AnySearchDefaultEndpoint
	}
	if timeout <= 0 {
		timeout = AnySearchDefaultTimeout
	}
	return &AnySearchClient{
		apiKey:   apiKey,
		endpoint: endpoint,
		timeout:  timeout,
		client:   &http.Client{Timeout: timeout},
	}
}

func (c *AnySearchClient) Search(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	body := map[string]interface{}{
		"query":       query,
		"max_results": limit,
		"zone":        "cn",
		"language":    "zh-CN",
		"format":      "json",
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
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anysearch API returned status %d", resp.StatusCode)
	}

	var result struct {
		Code int `json:"code"`
		Data struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Snippet string `json:"snippet"`
				Content string `json:"content"`
			} `json:"results"`
			Metadata struct {
				TotalResults  int `json:"total_results"`
				SearchTimeMs  int `json:"search_time_ms"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("anysearch API error code: %d", result.Code)
	}

	results := make([]engine.SearchResult, 0, len(result.Data.Results))
	for _, item := range result.Data.Results {
		snippet := item.Snippet
		if snippet == "" && item.Content != "" {
			// Truncate content to ~300 chars for snippet
			content := item.Content
			if len([]rune(content)) > 300 {
				content = string([]rune(content)[:300]) + "..."
			}
			snippet = content
		}
		if snippet == "" {
			snippet = item.Title
		}
		results = append(results, engine.SearchResult{
			Title:   item.Title,
			Snippet: snippet,
			URL:     item.URL,
			Source:  "anysearch",
		})
	}

	slog.Debug("anysearch done", "query", query, "count", len(results))
	return results, nil
}
