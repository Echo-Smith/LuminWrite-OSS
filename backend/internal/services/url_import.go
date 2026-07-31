package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// ─── URL Import Service ───────────────────────────────
// Fetches web pages, extracts clean text content, and imports
// them into the knowledge base. Replaces WeKnora's URL import
// functionality with native Go implementation.

// URLImporter fetches and parses web pages for knowledge import.
type URLImporter struct {
	kbManager *KbManager
	chunker   ChunkConfig
	client    *http.Client
}

// NewURLImporter creates a new URL importer.
func NewURLImporter(kbManager *KbManager, chunkConfig ChunkConfig) *URLImporter {
	return &URLImporter{
		kbManager: kbManager,
		chunker:   chunkConfig,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ImportURL fetches a web page, extracts text, chunks it, and stores
// it in the knowledge base (default KB).
func (u *URLImporter) ImportURL(ctx context.Context, userID, url, title string) (string, error) {
	return u.ImportURLToKB(ctx, userID, "default", url, title)
}

// ImportURLToKB fetches a web page, extracts text, chunks it, and stores
// it in the specified knowledge base.
func (u *URLImporter) ImportURLToKB(ctx context.Context, userID, kbID, url, title string) (string, error) {
	if u.kbManager == nil || !u.kbManager.IsConfigured() {
		return "", fmt.Errorf("knowledge base not configured")
	}

	// Fetch the web page
	content, err := u.fetchAndExtract(ctx, url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}

	if len([]rune(content)) < 50 {
		return "", fmt.Errorf("extracted content too short (len=%d), URL may not contain readable text", len([]rune(content)))
	}

	// Use provided title or extract from page
	if title == "" {
		title = u.extractTitle(content)
		if title == "" {
			title = url
		}
	}

	// Add document to knowledge base
	metadata := map[string]interface{}{
		"source":     "url",
		"source_url": url,
		"imported_at": time.Now().Format(time.RFC3339),
	}

	doc, err := u.kbManager.AddDocumentToKB(ctx, userID, kbID, title, content, "url", metadata)
	if err != nil {
		return "", fmt.Errorf("failed to add document: %w", err)
	}

	// Chunk the content and store chunks
	chunks := ChunkText(content, u.chunker)
	for _, chunk := range chunks {
		_, err := u.kbManager.AddChunk(ctx, doc.ID, userID, chunk.Index, chunk.Title, chunk.Content, map[string]interface{}{
			"start_pos":   chunk.StartPos,
			"end_pos":     chunk.EndPos,
			"source_url":  url,
		})
		if err != nil {
			slog.Warn("failed to add chunk", "index", chunk.Index, "error", err)
		}
	}

	// Update chunk count
	if err := u.kbManager.UpdateChunkCount(ctx, doc.ID, len(chunks)); err != nil {
		slog.Warn("failed to update chunk count", "error", err)
	}

	slog.Info("URL imported", "url", url, "title", title, "chunks", len(chunks), "doc_id", doc.ID)
	return doc.ID, nil
}

// fetchAndExtract fetches a URL and extracts clean text content.
func (u *URLImporter) fetchAndExtract(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := u.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Read with size limit (10MB)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return "", err
	}

	// Detect encoding and convert to UTF-8 if needed
	html := u.decodeBody(body, resp)

	// Extract text from HTML
	text := u.extractTextFromHTML(html)
	return text, nil
}

// decodeBody handles character encoding detection.
func (u *URLImporter) decodeBody(body []byte, resp *http.Response) string {
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))

	// Check charset from Content-Type header
	if strings.Contains(contentType, "gbk") || strings.Contains(contentType, "gb2312") {
		if gbkText, err := decodeGBK(body); err == nil {
			return gbkText
		}
	}

	// Check for charset declaration in HTML meta tag (first 1024 bytes)
	headLen := len(body)
	if headLen > 1024 {
		headLen = 1024
	}
	head := strings.ToLower(string(body[:headLen]))
	if strings.Contains(head, "charset=gbk") || strings.Contains(head, "charset=gb2312") ||
		strings.Contains(head, `charset="gbk`) || strings.Contains(head, `charset="gb2312`) {
		if gbkText, err := decodeGBK(body); err == nil {
			return gbkText
		}
	}

	// Default UTF-8
	text := string(body)

	// Check for replacement characters (indicates decoding failure)
	if strings.Contains(text, "\ufffd") {
		if gbkText, err := decodeGBK(body); err == nil {
			return gbkText
		}
	}

	return text
}

// extractTextFromHTML removes HTML tags and extracts clean text.
func (u *URLImporter) extractTextFromHTML(html string) string {
	// Remove script and style blocks
	scriptRe := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = scriptRe.ReplaceAllString(html, "")

	styleRe := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = styleRe.ReplaceAllString(html, "")

	// Extract title
	title := u.extractTitle(html)

	// Remove all HTML tags
	tagRe := regexp.MustCompile(`<[^>]*>`)
	text := tagRe.ReplaceAllString(html, "")

	// Decode HTML entities
	text = u.decodeHTMLEntities(text)

	// Normalize whitespace
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", " ")
	wsRe := regexp.MustCompile(`[ \t]+`)
	text = wsRe.ReplaceAllString(text, " ")
	multiNlRe := regexp.MustCompile(`\n{3,}`)
	text = multiNlRe.ReplaceAllString(text, "\n\n")

	// Prepend title if found
	if title != "" {
		text = title + "\n\n" + strings.TrimSpace(text)
	}

	return strings.TrimSpace(text)
}

// extractTitle extracts the page title from HTML.
func (u *URLImporter) extractTitle(html string) string {
	// Try <title> tag first
	titleRe := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	if m := titleRe.FindStringSubmatch(html); len(m) > 1 {
		title := strings.TrimSpace(m[1])
		if len([]rune(title)) > 100 {
			runes := []rune(title)
			title = string(runes[:100]) + "..."
		}
		return title
	}

	// Try <h1> tag
	h1Re := regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	if m := h1Re.FindStringSubmatch(html); len(m) > 1 {
		tagRe := regexp.MustCompile(`<[^>]*>`)
		title := strings.TrimSpace(tagRe.ReplaceAllString(m[1], ""))
		if len([]rune(title)) > 100 {
			runes := []rune(title)
			title = string(runes[:100]) + "..."
		}
		return title
	}

	return ""
}

// decodeHTMLEntities decodes common HTML entities.
func (u *URLImporter) decodeHTMLEntities(text string) string {
	replacements := map[string]string{
		"&nbsp;":  " ",
		"&ldquo;": "\u201c",
		"&rdquo;": "\u201d",
		"&mdash;": "\u2014",
		"&hellip;": "\u2026",
		"&amp;":   "&",
		"&lt;":    "<",
		"&gt;":    ">",
		"&quot;":  "\"",
		"&#39;":   "'",
		"&laquo;": "\u00ab",
		"&raquo;": "\u00bb",
		"&trade;": "\u2122",
		"&copy;":  "\u00a9",
		"&reg;":   "\u00ae",
	}
	for k, v := range replacements {
		text = strings.ReplaceAll(text, k, v)
	}
	return text
}

// decodeGBK decodes GBK/GB2312-encoded bytes to UTF-8.
func decodeGBK(body []byte) (string, error) {
	reader := transform.NewReader(bytes.NewReader(body), simplifiedchinese.GBK.NewDecoder())
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return string(body), err
	}
	return string(decoded), nil
}

// Ensure tools import is used
var _ = tools.FormatVectorForPG
