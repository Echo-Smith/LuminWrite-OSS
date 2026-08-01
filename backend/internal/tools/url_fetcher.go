package tools

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ─── URL Content Fetcher ──────────────────────────────────

// URLFetchResult holds the extracted content from a fetched URL.
type URLFetchResult struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	URL     string `json:"url"`
}

// urlFetcher is a simple HTTP client that fetches a URL and extracts
// the main text content from the HTML. It uses regex-based extraction
// to avoid adding heavy dependencies like goquery.
type URLFetcher struct {
	client   *http.Client
	maxBytes int64
}

// NewURLFetcher creates a new URL fetcher with sensible defaults.
func NewURLFetcher() *URLFetcher {
	return &URLFetcher{
		client:   &http.Client{Timeout: 15 * time.Second},
		maxBytes: 2 << 20, // 2MB max response size
	}
}

// FetchContent fetches the URL and extracts the title and main text content.
func (f *URLFetcher) FetchContent(ctx context.Context, url string) (*URLFetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Browser-like headers to avoid being blocked
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("URL returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	html := string(body)

	title := extractHTMLTitle(html)
	content := extractHTMLText(html)

	// Truncate content to a reasonable size for search results
	maxContentRunes := 2000
	if utf8.RuneCountInString(content) > maxContentRunes {
		runes := []rune(content)
		content = string(runes[:maxContentRunes]) + "..."
	}

	if title == "" {
		title = url
	}

	slog.Debug("URL content fetched",
		"url", url,
		"title", title,
		"content_length", utf8.RuneCountInString(content),
	)

	return &URLFetchResult{
		Title:   title,
		Content: content,
		URL:     url,
	}, nil
}

// extractHTMLTitle extracts the <title> tag content from HTML.
func extractHTMLTitle(html string) string {
	re := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	m := re.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return cleanHTML(strings.TrimSpace(m[1]))
}

// extractHTMLText extracts the main text content from HTML,
// stripping scripts, styles, and HTML tags.
func extractHTMLText(html string) string {
	// Remove script and style blocks entirely
	html = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?is)<footer[^>]*>.*?</footer>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?is)<header[^>]*>.*?</header>`).ReplaceAllString(html, "")

	// Try to find <article> or <main> content first
	articleRe := regexp.MustCompile(`(?is)<(?:article|main)[^>]*>(.*?)</(?:article|main)>`)
	if m := articleRe.FindStringSubmatch(html); len(m) > 1 {
		html = m[1]
	}

	// Convert <br>, <p>, <div> to newlines for better text structure
	html = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(html, "\n")
	html = regexp.MustCompile(`(?i)</p>`).ReplaceAllString(html, "\n\n")
	html = regexp.MustCompile(`(?i)</div>`).ReplaceAllString(html, "\n")
	html = regexp.MustCompile(`(?i)</h[1-6]>`).ReplaceAllString(html, "\n\n")

	// Strip all remaining HTML tags
	text := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(html, "")

	// Decode HTML entities
	text = decodeHTMLEntities(text)

	// Clean up whitespace
	text = cleanHTML(text)

	// Collapse multiple blank lines
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}
