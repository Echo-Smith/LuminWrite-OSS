package tools

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// ─── Bing China Search Client ───────────────────────────

// BingClient searches Bing China (cn.bing.com) via HTML scraping.
// No API key required. Works in China. Good Chinese content coverage.
//
// This is the most reliable free search source for Chinese content,
// complementing Tavily (US-based, limited Chinese coverage).
type BingClient struct {
	baseURL string
	timeout time.Duration
	client  *http.Client
}

// NewBingClient creates a new BingClient.
func NewBingClient(baseURL string, timeout time.Duration) *BingClient {
	if baseURL == "" {
		baseURL = "https://cn.bing.com"
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &BingClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
	}
}

// Search queries Bing China and returns search results by parsing HTML.
func (c *BingClient) Search(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	if limit <= 0 {
		limit = 3
	}

	// Build Bing China search URL
	// ensearch=0 forces Chinese region results
	// setlang=zh-Hans sets Chinese language
	params := url.Values{}
	params.Set("q", query)
	params.Set("count", fmt.Sprintf("%d", limit))
	params.Set("ensearch", "0")
	params.Set("setlang", "zh-Hans")

	searchURL := fmt.Sprintf("%s/search?%s", c.baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	// Use a realistic browser User-Agent to get proper HTML results
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2MB limit
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bing search returned status %d", resp.StatusCode)
	}

	html := string(body)
	results := parseBingResults(html, limit)

	slog.Debug("bing search done", "query", query, "count", len(results))
	return results, nil
}

// parseBingResults extracts search results from Bing HTML.
// Bing's result structure:
//
//	<li class="b_algo">
//	  <h2><a href="...">Title</a></h2>
//	  <div class="b_caption"><p>Snippet...</p></div>
//	</li>
func parseBingResults(html string, limit int) []engine.SearchResult {
	// Extract each <li class="b_algo"> block
	blockRe := regexp.MustCompile(`(?s)<li[^>]*class="b_algo"[^>]*>(.*?)</li>`)
	blocks := blockRe.FindAllStringSubmatch(html, -1)

	results := make([]engine.SearchResult, 0, len(blocks))
	for _, block := range blocks {
		if len(results) >= limit {
			break
		}
		content := block[1]

		// Extract title and URL from <h2><a href="URL">Title</a></h2>
		titleRe := regexp.MustCompile(`(?s)<h2[^>]*>\s*<a[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
		titleMatch := titleRe.FindStringSubmatch(content)
		if titleMatch == nil {
			continue
		}
		resultURL := titleMatch[1]
		title := cleanHTML(decodeHTMLEntities(titleMatch[2]))
		if title == "" {
			continue
		}

		// Extract snippet from <div class="b_caption"><p>Snippet</p></div>
		// or <p class="b_lineclamp...">Snippet</p>
		snippetRe := regexp.MustCompile(`(?s)<div[^>]*class="b_caption"[^>]*>.*?<p[^>]*>(.*?)</p>`)
		snippetMatch := snippetRe.FindStringSubmatch(content)
		snippet := ""
		if snippetMatch != nil {
			snippet = cleanHTML(decodeHTMLEntities(snippetMatch[1]))
		}

		// Fallback: try <p class="b_lineclamp...">
		if snippet == "" {
			fallbackRe := regexp.MustCompile(`(?s)<p[^>]*class="b_lineclamp[^"]*"[^>]*>(.*?)</p>`)
			fallbackMatch := fallbackRe.FindStringSubmatch(content)
			if fallbackMatch != nil {
				snippet = cleanHTML(decodeHTMLEntities(fallbackMatch[1]))
			}
		}

		if snippet == "" {
			snippet = title
		}

		results = append(results, engine.SearchResult{
			Title:   title,
			Snippet: snippet,
			URL:     resultURL,
			Source:  "bing",
		})
	}

	return results
}

// decodeHTMLEntities decodes common HTML entities.
func decodeHTMLEntities(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&ldquo;", "\u201c")
	s = strings.ReplaceAll(s, "&rdquo;", "\u201d")
	s = strings.ReplaceAll(s, "&mdash;", "\u2014")
	s = strings.ReplaceAll(s, "&hellip;", "\u2026")
	return s
}
