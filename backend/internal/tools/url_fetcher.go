package tools

import (
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// URLFetchResult holds normalized content fetched by the shared local crawler.
type URLFetchResult struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	URL     string `json:"url"`
}

// URLFetcher is a bounded generic crawler shared by both editions.
type URLFetcher struct {
	client   *http.Client
	maxBytes int64
}

func NewURLFetcher() *URLFetcher {
	return &URLFetcher{client: &http.Client{Timeout: 15 * time.Second}, maxBytes: 2 << 20}
}

func (f *URLFetcher) FetchContent(ctx context.Context, targetURL string) (*URLFetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; LuminBuddyCrawler/2.0)")
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
	rawHTML := string(body)
	title := extractHTMLTitle(rawHTML)
	content := extractHTMLText(rawHTML)
	if utf8.RuneCountInString(content) > 8000 {
		runes := []rune(content)
		content = string(runes[:8000]) + "..."
	}
	if title == "" {
		title = targetURL
	}
	slog.Debug("URL content fetched", "url", targetURL, "title", title, "content_length", utf8.RuneCountInString(content))
	return &URLFetchResult{Title: title, Content: content, URL: targetURL}, nil
}

func extractHTMLTitle(rawHTML string) string {
	match := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`).FindStringSubmatch(rawHTML)
	if len(match) < 2 {
		return ""
	}
	return cleanHTML(strings.TrimSpace(html.UnescapeString(match[1])))
}

func extractHTMLText(rawHTML string) string {
	for _, pattern := range []string{
		`(?is)<script[^>]*>.*?</script>`,
		`(?is)<style[^>]*>.*?</style>`,
		`(?is)<nav[^>]*>.*?</nav>`,
		`(?is)<footer[^>]*>.*?</footer>`,
		`(?is)<header[^>]*>.*?</header>`,
	} {
		rawHTML = regexp.MustCompile(pattern).ReplaceAllString(rawHTML, "")
	}
	article := regexp.MustCompile(`(?is)<(?:article|main)[^>]*>(.*?)</(?:article|main)>`).FindStringSubmatch(rawHTML)
	if len(article) > 1 {
		rawHTML = article[1]
	}
	rawHTML = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(rawHTML, "\n")
	rawHTML = regexp.MustCompile(`(?i)</p>`).ReplaceAllString(rawHTML, "\n\n")
	rawHTML = regexp.MustCompile(`(?i)</(?:div|h[1-6])>`).ReplaceAllString(rawHTML, "\n")
	text := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(rawHTML, "")
	text = cleanHTML(html.UnescapeString(text))
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
