package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// TencentNewsCLIClient wraps the `tencent-news-cli search` command for news search.
// This is more reliable than the HTTP scraping approach in TencentNewsClient.
type TencentNewsCLIClient struct {
	cliPath string
	timeout time.Duration
}

// NewTencentNewsCLIClient creates a new CLI-based news search client.
// If cliPath is empty, it auto-detects the CLI.
func NewTencentNewsCLIClient(cliPath string, timeout time.Duration) *TencentNewsCLIClient {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if cliPath == "" {
		cliPath = detectTencentNewsCLI()
	}
	if cliPath != "" {
		slog.Info("tencent-news-cli client initialized", "cli_path", cliPath)
	} else {
		slog.Warn("tencent-news-cli not found, CLI-based news search disabled")
	}
	return &TencentNewsCLIClient{
		cliPath: cliPath,
		timeout: timeout,
	}
}

// IsConfigured returns true if the CLI binary is available.
func (c *TencentNewsCLIClient) IsConfigured() bool {
	return c != nil && c.cliPath != ""
}

// Search executes a news search using `tencent-news-cli search <query> --limit N`.
func (c *TencentNewsCLIClient) Search(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	if c.cliPath == "" {
		return nil, fmt.Errorf("tencent-news-cli not configured")
	}
	if limit <= 0 {
		limit = 5
	}

	args := []string{
		"search",
		query,
		fmt.Sprintf("--limit=%d", limit),
		"--caller=tencent-news_1.1.0",
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctxWithTimeout, c.cliPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tencent-news-cli search failed: %w (output: %s)", err, string(output))
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		return nil, fmt.Errorf("tencent-news-cli returned empty output")
	}

	// Parse the CLI output (text format or JSON)
	results := c.parseOutput(outputStr, query)
	if len(results) == 0 {
		// If parsing fails, treat the whole output as a single text result
		results = []engine.SearchResult{
			{
				Title:   query,
				Snippet: outputStr,
				URL:     "",
				Source:  "tencent-news-cli",
			},
		}
	}

	slog.Debug("tencent-news-cli search done", "query", query, "count", len(results))
	return results, nil
}

// parseOutput tries to parse the CLI output as JSON first, then falls back to text parsing.
// The CLI returns formatted text like:
//
//	【腾讯新闻 - 搜索「关键词」】 2026-07-20 11:00
//
//	1. 标题：xxx
//	   摘要: xxx
//	   来源: 人民网
//	   发布时间: 2026-07-20 10:36:16
//	   链接: https://...
func (c *TencentNewsCLIClient) parseOutput(output, query string) []engine.SearchResult {
	// Try JSON array format first
	var items []struct {
		Title    string `json:"title"`
		Summary  string `json:"summary"`
		URL      string `json:"url"`
		Source   string `json:"source"`
		Time     string `json:"time"`
		Author   string `json:"author"`
	}
	if err := json.Unmarshal([]byte(output), &items); err == nil && len(items) > 0 {
		return c.jsonItemsToResults(items)
	}

	// Try JSON object with data array
	var dataResp struct {
		Data []struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			URL     string `json:"url"`
			Source  string `json:"source"`
			Time    string `json:"time"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &dataResp); err == nil && len(dataResp.Data) > 0 {
		results := make([]engine.SearchResult, 0, len(dataResp.Data))
		for _, item := range dataResp.Data {
			snippet := item.Title
			if item.Summary != "" {
				snippet += "：" + item.Summary
			}
			results = append(results, engine.SearchResult{
				Title:   item.Title,
				Snippet: snippet,
				URL:     item.URL,
				Source:  "tencent-news-cli",
			})
		}
		return results
	}

	// Fall back to text parsing
	return c.parseTextOutput(output)
}

// jsonItemsToResults converts JSON items to SearchResult slice.
func (c *TencentNewsCLIClient) jsonItemsToResults(items []struct {
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	URL      string `json:"url"`
	Source   string `json:"source"`
	Time     string `json:"time"`
	Author   string `json:"author"`
}) []engine.SearchResult {
	results := make([]engine.SearchResult, 0, len(items))
	for _, item := range items {
		snippet := item.Title
		if item.Summary != "" {
			snippet += "：" + item.Summary
		}
		meta := []string{"腾讯新闻"}
		if item.Author != "" {
			meta = append(meta, "来源："+item.Author)
		}
		if item.Time != "" {
			meta = append(meta, "时间："+item.Time)
		}
		if item.URL != "" {
			meta = append(meta, item.URL)
		}
		snippet += "（" + strings.Join(meta, "｜") + "）"
		results = append(results, engine.SearchResult{
			Title:   item.Title,
			Snippet: snippet,
			URL:     item.URL,
			Source:  "tencent-news-cli",
		})
	}
	return results
}

// parseTextOutput parses the formatted text output from tencent-news-cli.
// Expected format:
//
//	【腾讯新闻 - 搜索「关键词」】 2026-07-20 11:00
//
//	1. 标题：xxx
//	   摘要: xxx
//	   来源: 人民网
//	   发布时间: 2026-07-20 10:36:16
//	   链接: https://...
//
//	2. 标题：xxx
//	   ...
var (
	// Match each news block: starts with "N. 标题：" and ends before next block or footer
	newsBlockRe = regexp.MustCompile(`(?m)^\d+\.\s+标题[：:]\s*(.+)$`)
	summaryRe   = regexp.MustCompile(`(?m)^\s+摘要[：:]\s*(.+)$`)
	sourceRe    = regexp.MustCompile(`(?m)^\s+来源[：:]\s*(.+)$`)
	timeRe      = regexp.MustCompile(`(?m)^\s+发布时间[：:]\s*(.+)$`)
	linkRe      = regexp.MustCompile(`^\s+链接[：:]\s*(https?://\S+)$`)
)

func (c *TencentNewsCLIClient) parseTextOutput(output string) []engine.SearchResult {
	lines := strings.Split(output, "\n")

	var results []engine.SearchResult
	var currentTitle, currentSummary, currentSource, currentTime, currentLink string

	flush := func() {
		if currentTitle == "" {
			return
		}
		snippet := currentTitle
		if currentSummary != "" {
			snippet += "：" + currentSummary
		}
		meta := []string{"腾讯新闻"}
		if currentSource != "" {
			meta = append(meta, "来源："+currentSource)
		}
		if currentTime != "" {
			meta = append(meta, "时间："+currentTime)
		}
		if currentLink != "" {
			meta = append(meta, currentLink)
		}
		snippet += "（" + strings.Join(meta, "｜") + "）"

		results = append(results, engine.SearchResult{
			Title:   currentTitle,
			Snippet: snippet,
			URL:     currentLink,
			Source:  "tencent-news-cli",
		})

		currentTitle, currentSummary, currentSource, currentTime, currentLink = "", "", "", "", ""
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Match "N. 标题：xxx"
		if m := newsBlockRe.FindStringSubmatch(line); m != nil {
			flush()
			currentTitle = strings.TrimSpace(m[1])
			continue
		}

		// Match "   摘要: xxx"
		if m := summaryRe.FindStringSubmatch(line); m != nil {
			currentSummary = strings.TrimSpace(m[1])
			continue
		}

		// Match "   来源: xxx"
		if m := sourceRe.FindStringSubmatch(line); m != nil {
			currentSource = strings.TrimSpace(m[1])
			continue
		}

		// Match "   发布时间: xxx"
		if m := timeRe.FindStringSubmatch(line); m != nil {
			currentTime = strings.TrimSpace(m[1])
			continue
		}

		// Match "   链接: https://..."
		if m := linkRe.FindStringSubmatch(trimmed); m != nil {
			currentLink = strings.TrimSpace(m[1])
			continue
		}

		// Skip header/footer lines
		if strings.HasPrefix(trimmed, "【") || strings.HasPrefix(trimmed, "共 ") {
			continue
		}
	}

	flush()

	if len(results) == 0 {
		slog.Debug("tencent-news-cli text parse returned no results", "output_len", len(output))
	}

	return results
}

// FetchHot fetches hot news using `tencent-news-cli hot`.
func (c *TencentNewsCLIClient) FetchHot(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	if c.cliPath == "" {
		return nil, fmt.Errorf("tencent-news-cli not configured")
	}

	args := []string{
		"hot",
		"--caller=tencent-news_1.1.0",
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctxWithTimeout, c.cliPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tencent-news-cli hot failed: %w", err)
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		return nil, fmt.Errorf("empty output")
	}

	// Try to parse as JSON
	var items []map[string]interface{}
	if err := json.Unmarshal([]byte(outputStr), &items); err == nil {
		if limit > 0 && len(items) > limit {
			items = items[:limit]
		}
		return items, nil
	}

	// Fallback: return raw text as single item
	return []map[string]interface{}{
		{"title": "腾讯新闻热榜", "content": outputStr, "source": "tencent-news-cli"},
	}, nil
}
