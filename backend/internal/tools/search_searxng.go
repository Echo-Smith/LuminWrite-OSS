package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// SearXNGClient is the open-source edition's self-hostable metasearch
// adapter. SearXNG (https://docs.searxng.org) aggregates Google, Bing,
// DuckDuckGo and dozens of other engines behind one private JSON API —
// no API key, no per-source credentials, and no results leave the
// operator's chosen instance. This gives the OSS edition a working search
// pipeline out of the box while the proprietary sources stay commercial.
//
// Configure with SEARXNG_BASE_URL (e.g. http://searxng:8080 for the bundled
// compose service); the instance must allow the JSON format
// (formats: [html, json] in settings.yml).
type SearXNGClient struct {
	baseURL string
	timeout time.Duration
	http    *http.Client
}

// NewSearXNGClient builds a client for a self-hosted SearXNG instance.
func NewSearXNGClient(baseURL string, timeout time.Duration) *SearXNGClient {
	return &SearXNGClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		timeout: timeout,
		http:    &http.Client{Timeout: timeout},
	}
}

// searxngResponse models the JSON output of SearXNG's /search endpoint.
type searxngResponse struct {
	Results []struct {
		Title   string  `json:"title"`
		URL     string  `json:"url"`
		Content string  `json:"content"`
		Engine  string  `json:"engine"`
		Score   float64 `json:"score"`
	} `json:"results"`
	Answers []string `json:"answers"`
}

// Search queries the SearXNG instance and maps results onto the engine
// contract. Source is marked "searxng:<upstream-engine>" so credibility
// enrichment and tracing see which underlying engine produced each hit.
func (c *SearXNGClient) Search(ctx context.Context, query string, maxResults int) ([]engine.SearchResult, error) {
	if c.baseURL == "" {
		return nil, fmt.Errorf("searxng: base URL is not configured")
	}
	if maxResults <= 0 {
		maxResults = 5
	}
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("language", "zh-CN")
	params.Set("safesearch", "1")
	endpoint := c.baseURL + "/search?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("searxng: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LuminWrite-OSS/1.0 (self-hosted writing workspace)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 403 with HTML body is the classic "JSON format not enabled" reply.
		if resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("searxng: instance returned 403 — enable the json format in settings.yml (formats: [html, json])")
		}
		return nil, fmt.Errorf("searxng: unexpected status %d", resp.StatusCode)
	}

	var decoded searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("searxng: decode response: %w", err)
	}

	results := make([]engine.SearchResult, 0, len(decoded.Results))
	for _, item := range decoded.Results {
		if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.URL) == "" {
			continue
		}
		source := "searxng"
		if item.Engine != "" {
			source = "searxng:" + item.Engine
		}
		results = append(results, engine.SearchResult{
			Title:   item.Title,
			Snippet: item.Content,
			URL:     item.URL,
			Source:  source,
			Score:   item.Score,
		})
		if len(results) >= maxResults {
			break
		}
	}
	return results, nil
}
