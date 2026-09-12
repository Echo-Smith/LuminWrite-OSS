// document_state rendering (docs/18 §18.5.2: 当前文档 AST 相关子树). The
// renderer turns one committed document version into the deterministic
// block body the compiler budgets: header line, then the section outline
// with one truncated line per block, in document order. It is the M5 data
// source behind the draft/quality/finalize document_state contract — a
// document without committed versions still renders a truthful empty state,
// because "nothing written yet" is the current document state, not a
// missing one.
package writingruntime

import (
	"fmt"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/lcp"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// documentStateTextLimit caps one rendered line: the block exists so the
// model orients inside the document, not to carry the prose itself.
const documentStateTextLimit = 80

// renderDocumentState renders the committed version's subtree summary.
func renderDocumentState(version writingstore.StoredDocumentVersion) string {
	header := fmt.Sprintf("文档版本 v%d（%s）", version.Sequence, version.QualityState)
	root := version.Version.Root
	if root == nil || len(root.Children) == 0 {
		return header + "：正文为空"
	}
	lines := []string{header}
	for _, child := range root.Children {
		appendDocumentNode(&lines, child, 1)
	}
	return strings.Join(lines, "\n")
}

// documentStateEmpty is the truthful state of a document record that has
// never committed a version. It is supplied data — the draft capability's
// required document_state is satisfiable from the first run on.
const documentStateEmpty = "文档尚未提交任何版本：正文为空"

func appendDocumentNode(lines *[]string, node *lcp.Node, depth int) {
	if node == nil {
		return
	}
	if node.Type == lcp.NodeSection {
		title, _ := node.Attrs["title"].(string)
		if strings.TrimSpace(title) == "" {
			title = flattenDocumentText(node, documentStateTextLimit)
		}
		*lines = append(*lines, strings.Repeat("#", depth+1)+" "+truncateDocumentText(title))
		for _, block := range node.Children {
			appendDocumentNode(lines, block, depth+1)
		}
		return
	}
	if text := flattenDocumentText(node, documentStateTextLimit); text != "" {
		*lines = append(*lines, "- "+truncateDocumentText(text))
	}
}

// flattenDocumentText concatenates descendant text in document order up to
// limit runes; nested sections contribute their title text only.
func flattenDocumentText(node *lcp.Node, limit int) string {
	var builder strings.Builder
	var walk func(current *lcp.Node)
	walk = func(current *lcp.Node) {
		if current == nil || builder.Len() >= limit {
			return
		}
		if current.Type == lcp.NodeSection {
			title, _ := current.Attrs["title"].(string)
			builder.WriteString(title)
			return
		}
		builder.WriteString(current.Text)
		for _, child := range current.Children {
			walk(child)
			if builder.Len() >= limit {
				return
			}
		}
	}
	walk(node)
	return strings.TrimSpace(builder.String())
}

func truncateDocumentText(text string) string {
	runes := []rune(text)
	if len(runes) <= documentStateTextLimit {
		return text
	}
	return string(runes[:documentStateTextLimit]) + "…"
}
