package steps

import (
	"strings"
	"unicode/utf8"
)

// ArticleOutputProtocol records how the model's article output was parsed.
// Only the canonical Markdown protocol is considered non-deviated.
type ArticleOutputProtocol string

const (
	ArticleProtocolMarkdown          ArticleOutputProtocol = "markdown"
	ArticleProtocolLegacyJSON        ArticleOutputProtocol = "legacy_json"
	ArticleProtocolShortLineFallback ArticleOutputProtocol = "short_line_fallback"
	ArticleProtocolMissingTitle      ArticleOutputProtocol = "missing_title"
)

// ArticleStreamParserConfig configures a streaming article parser.
type ArticleStreamParserConfig struct {
	ConfirmedTitle string
	MaxPrefixRunes int
	OnTitle        func(string)
	OnBody         func(string)
}

// ParsedArticle is the normalized article produced by ArticleStreamParser.
//
// SectionBody is the compatibility adapter output for LCP's section_body
// content. It deliberately contains only the normalized body: the title and
// any legacy JSON/separator framing are never carried into section_body.
// Keeping this value in the steps package avoids coupling the legacy input
// adapter to the future LCP parser package.
type ParsedArticle struct {
	Title       string
	Body        string
	SectionBody string
	Protocol    ArticleOutputProtocol
}

// ArticleStreamParser normalizes model output into a separate title and body.
// It buffers only the undecided prefix; after the protocol is resolved, later
// deltas are forwarded exactly once.
type ArticleStreamParser struct {
	confirmedTitle string
	maxPrefixRunes int
	onTitle        func(string)
	onBody         func(string)

	prefix        strings.Builder
	raw           strings.Builder
	body          strings.Builder
	title         string
	protocol      ArticleOutputProtocol
	resolved      bool
	trimBodyStart bool
}

// NewArticleStreamParser creates a parser that prefers Markdown headings while
// remaining compatible with the former JSON + separator protocol.
func NewArticleStreamParser(cfg ArticleStreamParserConfig) *ArticleStreamParser {
	limit := cfg.MaxPrefixRunes
	if limit <= 0 {
		limit = TitleCollectCharLimit
	}
	return &ArticleStreamParser{
		confirmedTitle: strings.TrimSpace(cfg.ConfirmedTitle),
		maxPrefixRunes: limit,
		onTitle:        cfg.OnTitle,
		onBody:         cfg.OnBody,
	}
}

// Push consumes one model delta. Each byte is either held in the undecided
// prefix or forwarded to the body callback, never both.
func (p *ArticleStreamParser) Push(delta string) {
	if delta == "" {
		return
	}
	p.raw.WriteString(delta)
	if p.resolved {
		p.emitBody(delta)
		return
	}

	p.prefix.WriteString(delta)
	if p.resolveRecognized(false) {
		return
	}
	if utf8.RuneCountInString(p.prefix.String()) > p.maxPrefixRunes {
		p.resolveFallback(false)
	}
}

// Reset discards all state from an intermediate tool-call round while keeping
// callbacks and the confirmed title for the next round.
func (p *ArticleStreamParser) Reset() {
	p.prefix.Reset()
	p.raw.Reset()
	p.body.Reset()
	p.title = ""
	p.protocol = ""
	p.resolved = false
	p.trimBodyStart = false
}

// Body returns the body emitted so far.
func (p *ArticleStreamParser) Body() string {
	return p.body.String()
}

// SectionBody returns the normalized LCP section_body compatibility output
// accumulated so far. Legacy JSON remains an input-only compatibility format;
// this method never produces or exposes its title/separator envelope.
func (p *ArticleStreamParser) SectionBody() string {
	return p.Body()
}

// Protocol returns the protocol resolved for the current streaming round.
// It is empty while the prefix is still undecided or immediately after Reset.
func (p *ArticleStreamParser) Protocol() ArticleOutputProtocol {
	return p.protocol
}

// Finalize resolves an incomplete final heading or applies a lossless fallback.
// fullText is used only when no streaming delta was observed.
func (p *ArticleStreamParser) Finalize(fullText string) ParsedArticle {
	if p.raw.Len() == 0 && fullText != "" {
		p.raw.WriteString(fullText)
		p.prefix.WriteString(fullText)
	}
	if !p.resolved {
		if !p.resolveRecognized(true) {
			p.resolveFallback(true)
		}
	}
	body := p.SectionBody()
	return ParsedArticle{
		Title:       p.title,
		Body:        body,
		SectionBody: body,
		Protocol:    p.protocol,
	}
}

func (p *ArticleStreamParser) resolveRecognized(final bool) bool {
	buf := p.prefix.String()
	if sepIdx := strings.Index(buf, ArticleSeparator); sepIdx >= 0 {
		prefix := strings.TrimSpace(buf[:sepIdx])
		title, _ := ParseTitleJSON(prefix)
		if title == "" {
			title = p.confirmedTitle
		}
		body := trimLeadingArticleBreaks(buf[sepIdx+len(ArticleSeparator):])
		p.resolve(title, body, ArticleProtocolLegacyJSON)
		return true
	}

	if title, body, ok := splitMarkdownArticle(buf, final); ok {
		p.resolve(title, body, ArticleProtocolMarkdown)
		return true
	}
	return false
}

func (p *ArticleStreamParser) resolveFallback(final bool) {
	buf := p.prefix.String()
	if p.confirmedTitle != "" {
		p.resolve(p.confirmedTitle, buf, ArticleProtocolMissingTitle)
		return
	}
	if title, body, ok := splitShortLineTitle(buf, final); ok {
		p.resolve(title, body, ArticleProtocolShortLineFallback)
		return
	}
	p.resolve("", buf, ArticleProtocolMissingTitle)
}

func (p *ArticleStreamParser) resolve(title, body string, protocol ArticleOutputProtocol) {
	p.title = strings.TrimSpace(title)
	p.protocol = protocol
	p.resolved = true
	p.trimBodyStart = protocol != ArticleProtocolMissingTitle
	p.prefix.Reset()
	if p.title != "" && p.onTitle != nil {
		p.onTitle(p.title)
	}
	p.emitBody(body)
}

func (p *ArticleStreamParser) emitBody(text string) {
	if p.trimBodyStart {
		text = trimLeadingArticleBreaks(text)
		if text != "" {
			p.trimBodyStart = false
		}
	}
	if text == "" {
		return
	}
	p.body.WriteString(text)
	if p.onBody != nil {
		p.onBody(text)
	}
}

func splitMarkdownArticle(text string, final bool) (string, string, bool) {
	for offset := 0; offset <= len(text); {
		relEnd := strings.IndexByte(text[offset:], '\n')
		lineEnd := len(text)
		complete := false
		if relEnd >= 0 {
			lineEnd = offset + relEnd
			complete = true
		} else if !final {
			return "", "", false
		}

		line := strings.TrimSpace(strings.TrimSuffix(text[offset:lineEnd], "\r"))
		if title, ok := markdownHeadingTitle(line); ok {
			bodyStart := lineEnd
			if complete {
				bodyStart++
			}
			return title, trimLeadingArticleBreaks(text[bodyStart:]), true
		}
		if !complete {
			break
		}
		offset = lineEnd + 1
	}
	return "", "", false
}

func markdownHeadingTitle(line string) (string, bool) {
	for _, prefix := range []string{"## ", "# "} {
		if strings.HasPrefix(line, prefix) {
			title := cleanMarkdownTitle(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
			return title, title != ""
		}
	}
	return "", false
}

func splitShortLineTitle(text string, final bool) (string, string, bool) {
	for offset := 0; offset <= len(text); {
		relEnd := strings.IndexByte(text[offset:], '\n')
		lineEnd := len(text)
		complete := false
		if relEnd >= 0 {
			lineEnd = offset + relEnd
			complete = true
		} else if !final {
			return "", "", false
		}

		line := strings.TrimSpace(strings.TrimSuffix(text[offset:lineEnd], "\r"))
		if line != "" {
			if !looksLikeShortTitle(line) {
				return "", "", false
			}
			bodyStart := lineEnd
			if complete {
				bodyStart++
			}
			return cleanMarkdownTitle(strings.Trim(line, "# ")), trimLeadingArticleBreaks(text[bodyStart:]), true
		}
		if !complete {
			break
		}
		offset = lineEnd + 1
	}
	return "", "", false
}

func looksLikeShortTitle(line string) bool {
	if utf8.RuneCountInString(line) > 30 {
		return false
	}
	return !strings.ContainsAny(line, "。.!！?？：:")
}

func trimLeadingArticleBreaks(text string) string {
	return strings.TrimLeft(text, "\r\n")
}
