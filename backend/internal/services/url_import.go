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

// extractTextFromHTML removes HTML tags and extracts clean article content.
// Uses a whitelist approach: only keeps title, author, and body text paragraphs.
// Everything else (navigation, breadcrumbs, timestamps, related articles, copyright, etc.) is discarded.
func (u *URLImporter) extractTextFromHTML(html string) string {
	// Remove script, style, nav, header, footer, aside blocks
	for _, tag := range []string{"script", "style", "nav", "header", "footer", "aside", "noscript", "iframe"} {
		re := regexp.MustCompile(`(?is)<` + tag + `[^>]*>.*?</` + tag + `>`)
		html = re.ReplaceAllString(html, "")
	}

	// Extract title before removing tags
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

	// Extract only the article content (title, author, body)
	text = extractArticleContent(text)

	// Prepend title if found and not already at the top
	if title != "" && !strings.HasPrefix(strings.TrimSpace(text), title) {
		text = title + "\n\n" + strings.TrimSpace(text)
	}

	return strings.TrimSpace(text)
}

// ─── Article Content Extraction (Whitelist Approach) ──────────────
// Instead of trying to filter out every possible boilerplate pattern (blacklist),
// we use a whitelist: only keep paragraphs that look like actual article body text.
//
// A paragraph is considered "body text" if:
//   - It has more than 100 runes of content
//   - It contains Chinese sentence-ending punctuation (。！？) or is very long (>200 runes)
//   - It does not match known non-body patterns (URLs, timestamps, navigation)
//
// Additionally, we extract the author from "作者：xxx" patterns.
// Trailing noise (image URLs, "/enpproperty-->", numeric garbage) is stripped from body paragraphs.

// authorRe extracts author name from patterns like "作者：杨欢欢、印钰" or "作者:张三"
var authorRe = regexp.MustCompile(`(?:作者[：:]\s*)([^\s编辑]+?)(?:\s*(?:编辑[：:]|$|\n))`)

// sourceAuthorRe handles "来源：杭州网 作者：杨欢欢、印钰 编辑：王帆"
var sourceAuthorRe = regexp.MustCompile(`作者[：:]\s*([^\s]+(?:[、，,]\s*[^\s]+)*)`)

// noiseTrailingRe matches trailing noise attached to the end of body paragraphs:
// - Image URLs: https://xxx.com/pinglun/images/...
// - "/enpproperty-->"
// - Numeric garbage: "91543692025-12-31 15:48:16:0"
// - Mixed article IDs and metadata appended after body text
var noiseTrailingRe = regexp.MustCompile(`(\d{6,}[\s\d\-:]+.*|https?://\S+$|/enpproperty-->.*$|null.*$)`)

// urlLineRe matches lines that are just URLs
var urlLineRe = regexp.MustCompile(`^https?://\S+$`)

// timestampRe matches timestamp patterns like "2025-12-31 15:48:16" or "时间：2025-12-31"
var timestampRe = regexp.MustCompile(`^(时间[：:])?\s*\d{4}[-/]\d{2}[-/]\d{2}.*`)

// urlInTextRe matches URLs embedded within text (not just lines that are entirely URLs)
var urlInTextRe = regexp.MustCompile(`https?://\S+`)

// relatedArticlesRe detects lines that are concatenations of multiple article titles
// (common in "related articles" sections): "标题1 标题2 标题3 标题4"
// These lines have multiple short segments separated by spaces, each looking like a title
var relatedArticlesRe = regexp.MustCompile(`^[^\n]{60,}$`)

// isBodyParagraph checks if a paragraph is actual article body text.
func isBodyParagraph(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	// Must be at least 100 runes
	runes := []rune(trimmed)
	if len(runes) < 100 {
		return false
	}

	// Reject lines that are just URLs
	if urlLineRe.MatchString(trimmed) {
		return false
	}

	// Reject timestamp lines
	if timestampRe.MatchString(trimmed) {
		return false
	}

	// Reject lines containing URLs (URL fragments embedded in text)
	if urlInTextRe.MatchString(trimmed) {
		return false
	}

	// Reject lines that contain mostly navigation characters (|, ｜, spaces)
	nonNavChars := 0
	for _, r := range runes {
		if r != ' ' && r != '|' && r != '｜' && r != '\t' {
			nonNavChars++
		}
	}
	if nonNavChars < len(runes)/2 {
		return false
	}

	// Reject "related articles" lines: multiple short titles concatenated with spaces
	// These typically have no sentence-ending punctuation (。！？) and contain multiple
	// space-separated segments each < 30 chars
	hasSentenceEnd := false
	spaceCount := 0
	shortSegmentCount := 0
	currentSegmentLen := 0
	for _, r := range runes {
		if r == '。' || r == '！' || r == '？' || r == '.' || r == '!' || r == '?' {
			hasSentenceEnd = true
		}
		if r == ' ' {
			spaceCount++
			if currentSegmentLen > 0 && currentSegmentLen < 30 {
				shortSegmentCount++
			}
			currentSegmentLen = 0
		} else {
			currentSegmentLen++
		}
	}
	// If last segment is also short
	if currentSegmentLen > 0 && currentSegmentLen < 30 {
		shortSegmentCount++
	}

	// If no sentence-ending punctuation AND many short segments, it's likely related articles
	if !hasSentenceEnd && shortSegmentCount >= 3 {
		return false
	}

	// If > 200 runes, accept even without sentence-ending punctuation
	// (some body paragraphs might be lists or code)
	if !hasSentenceEnd && len(runes) < 200 {
		return false
	}

	return true
}

// extractAuthor finds the author name in the text.
func extractAuthor(text string) string {
	// Try "来源：xxx 作者：xxx 编辑：xxx" pattern first
	if m := sourceAuthorRe.FindStringSubmatch(text); len(m) > 1 {
		author := strings.TrimSpace(m[1])
		// Clean up: remove trailing "编辑" if captured
		author = regexp.MustCompile(`\s*编辑.*`).ReplaceAllString(author, "")
		if author != "" && len([]rune(author)) <= 50 {
			return author
		}
	}

	// Try simple "作者：xxx" pattern
	if m := authorRe.FindStringSubmatch(text); len(m) > 1 {
		author := strings.TrimSpace(m[1])
		if author != "" && len([]rune(author)) <= 50 {
			return author
		}
	}

	return ""
}

// cleanBodyParagraph removes trailing noise from a body paragraph.
// This handles cases where the body text runs directly into metadata
// (e.g., "精彩的一笔。91543692025-12-31 15:48:16:0杨欢欢...")
func cleanBodyParagraph(text string) string {
	trimmed := strings.TrimSpace(text)

	// Strip "/enpproperty-->" and everything after it
	if idx := strings.Index(trimmed, "/enpproperty"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}

	// Strip "null" and everything after it (common in scraped content)
	if idx := strings.Index(trimmed, "null"); idx >= 0 && idx > len([]rune(trimmed))/2 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}

	// Find the last sentence-ending punctuation and cut after it
	// This removes trailing noise like "91543692025-12-31..."
	runes := []rune(trimmed)
	lastSentenceEnd := -1
	for i, r := range runes {
		if r == '。' || r == '！' || r == '？' || r == '.' || r == '!' || r == '?' {
			lastSentenceEnd = i
		}
	}

	if lastSentenceEnd >= 0 && lastSentenceEnd < len(runes)-1 {
		// Check if there's noise after the last sentence end
		afterEnd := strings.TrimSpace(string(runes[lastSentenceEnd+1:]))
		if afterEnd != "" {
			// Check if the trailing part looks like noise (starts with digits, URLs, etc.)
			if regexp.MustCompile(`^[\d]|^https?://|^null`).MatchString(afterEnd) {
				trimmed = string(runes[:lastSentenceEnd+1])
			}
		}
	}

	return trimmed
}

// extractArticleContent uses a whitelist approach to extract only:
//   - Author (from "作者：xxx" patterns)
//   - Body text paragraphs (> 100 chars with sentence-ending punctuation)
//
// Title and URL are handled separately (stored in DB columns).
// Returns formatted text: "作者：xxx\n\n正文段落1\n\n正文段落2..."
func extractArticleContent(text string) string {
	// Normalize line endings
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	// Extract author before processing body
	author := extractAuthor(text)

	// Split into paragraphs
	paragraphs := strings.Split(text, "\n")

	var bodyParagraphs []string
	for _, para := range paragraphs {
		trimmed := strings.TrimSpace(para)
		if trimmed == "" {
			continue
		}

		if isBodyParagraph(trimmed) {
			// Clean trailing noise from the paragraph
			cleaned := cleanBodyParagraph(trimmed)
			if cleaned != "" && len([]rune(cleaned)) >= 50 {
				bodyParagraphs = append(bodyParagraphs, cleaned)
			}
		}
	}

	if len(bodyParagraphs) == 0 {
		return ""
	}

	// Build the result: author (if found) + body paragraphs
	var parts []string
	if author != "" {
		parts = append(parts, "作者："+author)
	}
	parts = append(parts, bodyParagraphs...)

	return strings.Join(parts, "\n\n")
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
