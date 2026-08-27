package lcp

import (
	"fmt"
	"regexp"
	"strings"
)

type ErrorCode string

const (
	ErrEmptyDocument        ErrorCode = "EMPTY_DOCUMENT"
	ErrForbiddenHTML        ErrorCode = "FORBIDDEN_HTML"
	ErrForbiddenMDX         ErrorCode = "FORBIDDEN_MDX"
	ErrForbiddenFrontMatter ErrorCode = "FORBIDDEN_FRONT_MATTER"
	ErrSectionHeading       ErrorCode = "SECTION_HEADING_FORBIDDEN"
	ErrUnclosedCodeFence    ErrorCode = "UNCLOSED_CODE_FENCE"
	ErrTableColumnMismatch  ErrorCode = "TABLE_COLUMN_MISMATCH"
	ErrInvalidCitation      ErrorCode = "INVALID_CITATION"
)

type ParseError struct {
	Code    ErrorCode
	Line    int
	Message string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s at line %d: %s", e.Code, e.Line, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type ParseOptions struct {
	DocumentID   string
	VersionID    string
	SectionID    string
	SectionTitle string
	SectionLevel int
	Origin       Origin
}

type Parser struct {
	opts ParseOptions
	buf  strings.Builder
}

func NewParser(opts ParseOptions) *Parser { return &Parser{opts: opts} }

func (p *Parser) Append(delta string) error {
	p.buf.WriteString(delta)
	return nil
}

func (p *Parser) Finalize() (Document, error) {
	return parseSectionBody(p.buf.String(), p.opts)
}

func ParseSectionBody(markdown string, opts ParseOptions) (Document, error) {
	p := NewParser(opts)
	_ = p.Append(markdown)
	return p.Finalize()
}

var (
	headingPattern       = regexp.MustCompile(`^\s{0,3}#{1,6}(?:\s+|$)`)
	setextPattern        = regexp.MustCompile(`^\s{0,3}(?:=+|-+)\s*$`)
	orderedPattern       = regexp.MustCompile(`^\s*\d+[.)]\s+(.+)$`)
	unorderedPattern     = regexp.MustCompile(`^\s*[-+*]\s+(.+)$`)
	htmlPattern          = regexp.MustCompile(`(?i)</?[a-z][^>]*>`)
	mdxExpressionPattern = regexp.MustCompile(`(?m)^\s*(?:import|export)\s+|[{}]`)
	mdxComponentPattern  = regexp.MustCompile(`</?[A-Z][A-Za-z0-9_.:-]*(?:\s|/?>)`)
	strongPattern        = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	emphasisPattern      = regexp.MustCompile(`\*([^*\n]+)\*`)
	linkPattern          = regexp.MustCompile(`\[([^\]\n]+)\]\(([^\s)]+)\)`)
)

func parseSectionBody(markdown string, opts ParseOptions) (Document, error) {
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	markdown = strings.ReplaceAll(markdown, "\r", "\n")
	if strings.TrimSpace(markdown) == "" {
		return Document{}, parseError(ErrEmptyDocument, 0, "section body is empty")
	}
	if strings.HasPrefix(strings.TrimSpace(markdown), "---\n") {
		return Document{}, parseError(ErrForbiddenFrontMatter, 1, "front matter is not part of Lumin Markdown")
	}
	lines := strings.Split(markdown, "\n")
	inFence := false
	previousOutsideLine := ""
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			previousOutsideLine = ""
			continue
		}
		if inFence {
			continue
		}
		if headingPattern.MatchString(line) {
			return Document{}, parseError(ErrSectionHeading, i+1, "section_body cannot contain headings")
		}
		if previousOutsideLine != "" && setextPattern.MatchString(line) {
			return Document{}, parseError(ErrSectionHeading, i+1, "section_body cannot contain Setext headings")
		}
		if mdxExpressionPattern.MatchString(line) || mdxComponentPattern.MatchString(line) {
			return Document{}, parseError(ErrForbiddenMDX, i+1, "MDX is not allowed")
		}
		if htmlPattern.MatchString(line) {
			return Document{}, parseError(ErrForbiddenHTML, i+1, "raw HTML is not allowed")
		}
		previousOutsideLine = strings.TrimSpace(line)
	}
	if inFence {
		return Document{}, parseError(ErrUnclosedCodeFence, len(lines), "code fence has no closing delimiter")
	}

	blocks, err := parseBlocks(lines, opts.Origin)
	if err != nil {
		return Document{}, err
	}
	level := opts.SectionLevel
	if level < 1 || level > 6 {
		level = 1
	}
	root := Node{Type: NodeDocument, Attrs: map[string]any{}, Children: []*Node{
		{Type: NodeSection, Attrs: map[string]any{"section_id": opts.SectionID, "title": opts.SectionTitle, "level": level}, Children: blocks, Origin: opts.Origin},
	}, Origin: opts.Origin}
	doc := Document{DocumentID: opts.DocumentID, VersionID: opts.VersionID, SectionID: opts.SectionID, Root: root}
	if err := doc.Seal(); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func parseBlocks(lines []string, origin Origin) ([]*Node, error) {
	blocks := make([]*Node, 0)
	for i := 0; i < len(lines); {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			start := i
			language := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "```"))
			i++
			code := make([]string, 0)
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				code = append(code, lines[i])
				i++
			}
			if i == len(lines) {
				return nil, parseError(ErrUnclosedCodeFence, start+1, "code fence has no closing delimiter")
			}
			i++
			blocks = append(blocks, block(NodeCodeBlock, origin, map[string]any{"language": language}, strings.Join(code, "\n")))
			continue
		}
		if isTableStart(lines, i) {
			table, next, err := parseTable(lines, i, origin)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, table)
			i = next
			continue
		}
		if match := unorderedPattern.FindStringSubmatch(line); match != nil {
			list, next, err := parseList(lines, i, origin, false)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, list)
			i = next
			continue
		}
		if match := orderedPattern.FindStringSubmatch(line); match != nil {
			list, next, err := parseList(lines, i, origin, true)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, list)
			i = next
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			quoteLines := make([]string, 0)
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				quoteLines = append(quoteLines, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")))
				i++
			}
			children, err := inlineNodes(strings.Join(quoteLines, "\n"), origin)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, container(NodeBlockquote, origin, paragraph(origin, children)))
			continue
		}
		paragraphLines := []string{strings.TrimSpace(line)}
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !startsBlock(lines, i) {
			paragraphLines = append(paragraphLines, strings.TrimSpace(lines[i]))
			i++
		}
		children, err := inlineNodes(strings.Join(paragraphLines, "\n"), origin)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, paragraph(origin, children))
	}
	return blocks, nil
}

func startsBlock(lines []string, i int) bool {
	line := strings.TrimSpace(lines[i])
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, ">") ||
		unorderedPattern.MatchString(lines[i]) || orderedPattern.MatchString(lines[i]) || isTableStart(lines, i)
}

func parseList(lines []string, start int, origin Origin, ordered bool) (*Node, int, error) {
	typeName, pattern := NodeUnorderedList, unorderedPattern
	if ordered {
		typeName, pattern = NodeOrderedList, orderedPattern
	}
	items := make([]*Node, 0)
	i := start
	for i < len(lines) {
		match := pattern.FindStringSubmatch(lines[i])
		if match == nil {
			break
		}
		children, err := inlineNodes(strings.TrimSpace(match[1]), origin)
		if err != nil {
			return nil, i, err
		}
		items = append(items, container(NodeListItem, origin, paragraph(origin, children)))
		i++
	}
	return &Node{Type: typeName, Attrs: map[string]any{}, Children: items, Origin: origin}, i, nil
}

func isTableStart(lines []string, i int) bool {
	return i+1 < len(lines) && strings.Contains(lines[i], "|") && isTableDelimiter(lines[i+1])
}

func isTableDelimiter(line string) bool {
	cells := tableCells(line)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		trimmed := strings.Trim(strings.TrimSpace(cell), ":")
		if len(trimmed) < 3 || strings.Trim(trimmed, "-") != "" {
			return false
		}
	}
	return true
}

func parseTable(lines []string, start int, origin Origin) (*Node, int, error) {
	header := tableCells(lines[start])
	delimiter := tableCells(lines[start+1])
	if len(header) != len(delimiter) {
		return nil, start, parseError(ErrTableColumnMismatch, start+2, "table delimiter width differs from header")
	}
	rows := make([]*Node, 0)
	for i := start; i < len(lines); i++ {
		if i == start+1 {
			continue
		}
		if i > start+1 && (strings.TrimSpace(lines[i]) == "" || !strings.Contains(lines[i], "|")) {
			return &Node{Type: NodeTable, Attrs: map[string]any{}, Children: rows, Origin: origin}, i, nil
		}
		cells := tableCells(lines[i])
		if len(cells) != len(header) {
			return nil, i, parseError(ErrTableColumnMismatch, i+1, "table row width differs from header")
		}
		rowCells := make([]*Node, 0, len(cells))
		for _, cell := range cells {
			inline, err := inlineNodes(strings.TrimSpace(cell), origin)
			if err != nil {
				return nil, i, err
			}
			rowCells = append(rowCells, container(NodeTableCell, origin, paragraph(origin, inline)))
		}
		rows = append(rows, &Node{Type: NodeTableRow, Attrs: map[string]any{}, Children: rowCells, Origin: origin})
		if i == len(lines)-1 {
			return &Node{Type: NodeTable, Attrs: map[string]any{}, Children: rows, Origin: origin}, len(lines), nil
		}
	}
	return &Node{Type: NodeTable, Attrs: map[string]any{}, Children: rows, Origin: origin}, len(lines), nil
}

func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	if len(parts) == 1 && strings.TrimSpace(parts[0]) == "" {
		return nil
	}
	return parts
}

func inlineNodes(text string, origin Origin) ([]*Node, error) {
	nodes := make([]*Node, 0)
	for len(text) > 0 {
		kind, start, end, label, value := nextInline(text)
		if start < 0 {
			nodes = appendText(nodes, text, origin)
			break
		}
		if start > 0 {
			nodes = appendText(nodes, text[:start], origin)
		}
		switch kind {
		case "citation":
			sourceID, err := ParseCitationMarker(text[start:end])
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, &Node{Type: NodeCitation, Attrs: map[string]any{}, Children: []*Node{}, SourceID: sourceID, Origin: origin})
		case "strong", "emphasis":
			children := appendText(nil, label, origin)
			t := NodeStrong
			if kind == "emphasis" {
				t = NodeEmphasis
			}
			nodes = append(nodes, &Node{Type: t, Attrs: map[string]any{}, Children: children, Origin: origin})
		case "link":
			nodes = append(nodes, &Node{Type: NodeLink, Attrs: map[string]any{}, Children: appendText(nil, label, origin), Destination: value, Origin: origin})
		}
		text = text[end:]
	}
	return nodes, nil
}

func nextInline(text string) (kind string, start, end int, label, value string) {
	start = -1
	consider := func(k string, s, e int, l, v string) {
		if s >= 0 && (start < 0 || s < start) {
			kind, start, end, label, value = k, s, e, l, v
		}
	}
	if s := strings.Index(text, "[[cite:"); s >= 0 {
		if close := strings.Index(text[s:], "]]"); close >= 0 {
			consider("citation", s, s+close+2, "", "")
		} else {
			consider("citation", s, len(text), "", "")
		}
	}
	if m := strongPattern.FindStringSubmatchIndex(text); m != nil {
		consider("strong", m[0], m[1], text[m[2]:m[3]], "")
	}
	if m := emphasisPattern.FindStringSubmatchIndex(text); m != nil {
		consider("emphasis", m[0], m[1], text[m[2]:m[3]], "")
	}
	if m := linkPattern.FindStringSubmatchIndex(text); m != nil {
		consider("link", m[0], m[1], text[m[2]:m[3]], text[m[4]:m[5]])
	}
	return
}

func appendText(nodes []*Node, text string, origin Origin) []*Node {
	if text == "" {
		return nodes
	}
	return append(nodes, &Node{Type: NodeText, Attrs: map[string]any{}, Children: []*Node{}, Text: text, Origin: origin})
}

func paragraph(origin Origin, children []*Node) *Node {
	return &Node{Type: NodeParagraph, Attrs: map[string]any{}, Children: children, Origin: origin}
}
func container(t NodeType, origin Origin, children ...*Node) *Node {
	return &Node{Type: t, Attrs: map[string]any{}, Children: children, Origin: origin}
}
func block(t NodeType, origin Origin, attrs map[string]any, text string) *Node {
	return &Node{Type: t, Attrs: attrs, Children: []*Node{}, Text: text, Origin: origin}
}
func parseError(code ErrorCode, line int, message string) error {
	return &ParseError{Code: code, Line: line, Message: message}
}
