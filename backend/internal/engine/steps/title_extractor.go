package steps

import (
	"encoding/json"
	"strings"
)

// ArticleSeparator is the marker that separates the JSON title prefix from the article body.
const ArticleSeparator = "---ARTICLE---"

// TitleCollectCharLimit is the maximum number of runes to collect before giving up
// on finding the separator and falling back to regex extraction.
const TitleCollectCharLimit = 300

// ParseTitleJSON attempts to extract a "title" field from a JSON string.
// It tolerates surrounding text (e.g. "好的，这是标题：{...}").
func ParseTitleJSON(s string) (string, bool) {
	s = strings.TrimSpace(s)

	// Fast path: direct parse
	if title, ok := tryParseTitle(s); ok {
		return title, true
	}

	// Slow path: extract the first {...} substring
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		jsonStr := s[start : end+1]
		if title, ok := tryParseTitle(jsonStr); ok {
			return title, true
		}
	}

	return "", false
}

func tryParseTitle(jsonStr string) (string, bool) {
	var data struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return "", false
	}
	if data.Title == "" {
		return "", false
	}
	return strings.TrimSpace(data.Title), true
}

// ExtractTitleFromMarkdown extracts a title from Markdown text.
// It looks for the first line starting with "## " or "# ".
// Falls back to the first short non-empty line without sentence-ending punctuation.
func ExtractTitleFromMarkdown(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Match ## or # prefix
		if strings.HasPrefix(trimmed, "## ") {
			title := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			return cleanMarkdownTitle(title)
		}
		if strings.HasPrefix(trimmed, "# ") {
			title := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			return cleanMarkdownTitle(title)
		}
		// If the first non-empty line looks like a title (short, no sentence punctuation)
		runes := []rune(trimmed)
		if len(runes) <= 30 && !strings.Contains(trimmed, "。") && !strings.Contains(trimmed, ".") {
			return cleanMarkdownTitle(strings.Trim(trimmed, "# "))
		}
		// First line is too long to be a title — give up
		break
	}
	return ""
}

// cleanMarkdownTitle strips common Markdown formatting markers from a title.
func cleanMarkdownTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "*_`")
	return s
}

// FilterJSONLines removes lines that look like a JSON title prefix from body text.
// Used in fallback mode when the LLM didn't output the separator but did output JSON.
func FilterJSONLines(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip lines that look like {"title":"..."}
		if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, "\"title\"") {
			continue
		}
		// Skip the separator marker if present
		if trimmed == ArticleSeparator {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// StripLeadingTitleHeading removes a leading "## title" line from body text
// if it matches the resolved title (to avoid duplicate titles).
func StripLeadingTitleHeading(body, title string) string {
	if title == "" {
		return body
	}
	body = strings.TrimLeft(body, "\n\r ")
	firstLineEnd := strings.Index(body, "\n")
	if firstLineEnd < 0 {
		return body
	}
	firstLine := strings.TrimSpace(body[:firstLineEnd])
	// Check if first line is a heading containing the title
	if strings.HasPrefix(firstLine, "## ") || strings.HasPrefix(firstLine, "# ") {
		headingContent := strings.TrimSpace(strings.TrimPrefix(
			strings.TrimPrefix(firstLine, "## "), "# "))
		if headingContent == title || strings.Contains(headingContent, title) {
			// Skip the heading line
			return strings.TrimLeft(body[firstLineEnd+1:], "\n\r ")
		}
	}
	return body
}
