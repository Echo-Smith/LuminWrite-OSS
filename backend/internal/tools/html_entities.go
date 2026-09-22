package tools

import "strings"

// decodeHTMLEntities decodes common HTML entities.
//
// Edition note: generic HTML helper required by the shared url_fetcher.go. The
// Commercial edition carries the same helper inside search_bing.go (a
// Commercial-only provider file); the OSS edition keeps it here so the shared
// files stay byte-identical across editions (docs/29 §7).
func decodeHTMLEntities(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&ldquo;", "“")
	s = strings.ReplaceAll(s, "&rdquo;", "”")
	s = strings.ReplaceAll(s, "&mdash;", "—")
	s = strings.ReplaceAll(s, "&hellip;", "…")
	return s
}
