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
// It also filters out common website boilerplate (navigation, footers, ads, etc.)
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

	// ── Filter out website boilerplate line by line ──
	text = cleanBoilerplate(text)

	// Prepend title if found and not already at the top
	if title != "" && !strings.HasPrefix(strings.TrimSpace(text), title) {
		text = title + "\n\n" + strings.TrimSpace(text)
	}

	return strings.TrimSpace(text)
}

// boilerplatePatterns are regex patterns for lines that are almost certainly
// website navigation, footer, or ad content — not article body text.
var boilerplatePatterns = []*regexp.Regexp{
	// Navigation menus: "首页 | 新闻 | 原创 | 议事厅 | 论坛"
	regexp.MustCompile(`(?i)(首页|主页|网站首页)\s*[|｜]\s*(新闻|原创|议事厅|论坛|视频|图片|专题|专栏|博客|微博|微信)`),
	// App download prompts
	regexp.MustCompile(`(?i)立即下载|扫码下载|APP下载|关注微信|扫一扫|二维码`),
	// Contact / copyright / license lines
	regexp.MustCompile(`(?i)联系电话|联系方式|客服电话|新闻热线|投稿邮箱|广告合作|商务合作`),
	regexp.MustCompile(`(?i)增值电信业务经营许可证|ICP[备证]号|京公网安备|互联网新闻信息服务许可证`),
	regexp.MustCompile(`(?i)版权所有|Copyright|All\s+Rights\s+Reserved|©`),
	// Site map / navigation links
	regexp.MustCompile(`(?i)网站地图|关于我们|联系我们|加入我们|招贤纳士|友情链接|站点地图`),
	// Social media follows
	regexp.MustCompile(`(?i)关注|下载客户端|移动端|PC端`),
	// Short navigation-only lines (e.g., "杭网首页 | 新闻 | 原创")
	regexp.MustCompile(`^[\s|｜A-Za-z\x{4e00}-\x{9fff}]{0,30}$`),
}

// cleanBoilerplate removes lines that match common boilerplate patterns.
// It also removes consecutive blank lines that result from the filtering.
func cleanBoilerplate(text string) string {
	lines := strings.Split(text, "\n")
	var kept []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			// Keep blank lines as paragraph separators (will be collapsed later)
			kept = append(kept, "")
			continue
		}

		isBoilerplate := false
		for _, p := range boilerplatePatterns {
			if p.MatchString(trimmed) {
				isBoilerplate = true
				break
			}
		}

		if !isBoilerplate {
			kept = append(kept, line)
		}
	}

	// Collapse multiple consecutive blank lines into one
	result := strings.Join(kept, "\n")
	multiNlRe := regexp.MustCompile(`\n{3,}`)
	result = multiNlRe.ReplaceAllString(result, "\n\n")

	return result
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
