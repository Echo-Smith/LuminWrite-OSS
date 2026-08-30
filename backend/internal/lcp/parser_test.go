package lcp

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"testing"
)

func TestFinalizeParsesLuminMarkdownProfile(t *testing.T) {
	input := "" +
		"第一段含 **粗体**、*强调*、[链接](https://example.com) 和证据 [[cite:source_018]]。\n\n" +
		"- 无序一\n- 无序二\n\n" +
		"1. 有序一\n2. 有序二\n\n" +
		"> 这是引用。\n\n" +
		"```go\nfmt.Println(\"ok\")\n```\n\n" +
		"| 名称 | 状态 |\n| --- | --- |\n| LCP | ready |"

	doc, err := ParseSectionBody(input, testOptions())
	if err != nil {
		t.Fatalf("ParseSectionBody() error = %v", err)
	}
	wantTypes := []NodeType{
		NodeDocument, NodeSection, NodeParagraph, NodeText, NodeStrong, NodeText,
		NodeText, NodeEmphasis, NodeText, NodeText, NodeLink, NodeText, NodeText,
		NodeCitation, NodeText, NodeUnorderedList, NodeListItem, NodeParagraph,
		NodeText, NodeListItem, NodeParagraph, NodeText, NodeOrderedList,
		NodeListItem, NodeParagraph, NodeText, NodeListItem, NodeParagraph,
		NodeText, NodeBlockquote, NodeParagraph, NodeText, NodeCodeBlock,
		NodeTable, NodeTableRow, NodeTableCell, NodeParagraph, NodeText,
		NodeTableCell, NodeParagraph, NodeText, NodeTableRow, NodeTableCell,
		NodeParagraph, NodeText, NodeTableCell, NodeParagraph, NodeText,
	}
	if got := flattenTypes(doc.Root); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("node types mismatch\nwant: %#v\ngot:  %#v", wantTypes, got)
	}

	var citation, link *Node
	walk(&doc.Root, func(node *Node) {
		if node.Type == NodeCitation {
			citation = node
		}
		if node.Type == NodeLink {
			link = node
		}
		if IsBlockNode(node.Type) {
			if !regexp.MustCompile(`^blk_[0-9a-f]{24}$`).MatchString(node.BlockID) {
				t.Errorf("block %s has unstable id %q", node.Type, node.BlockID)
			}
			if node.Origin.Kind != OriginModel || node.Origin.Ref != "run_demo/node_writer" {
				t.Errorf("block %s lost origin: %#v", node.Type, node.Origin)
			}
			if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(node.ContentHash) {
				t.Errorf("block %s has invalid content_hash %q", node.Type, node.ContentHash)
			}
		}
	})
	if citation == nil || citation.SourceID != "source_018" {
		t.Fatalf("citation not compiled: %#v", citation)
	}
	if link == nil || link.Destination != "https://example.com" {
		t.Fatalf("link not compiled: %#v", link)
	}
	if doc.ContentHash != doc.Root.ContentHash {
		t.Fatalf("document hash %q != root hash %q", doc.ContentHash, doc.Root.ContentHash)
	}
}

func TestBlockIDsAndHashesAreDeterministic(t *testing.T) {
	first, err := ParseSectionBody("稳定的正文。", testOptions())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseSectionBody("稳定的正文。", testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same input and identity produced different ASTs\nfirst: %#v\nsecond: %#v", first, second)
	}
}

func TestCodeFenceContentIsNotInterpretedAsHTMLMDXOrHeading(t *testing.T) {
	doc, err := ParseSectionBody("```tsx\n# not a heading\n<Card>{value}</Card>\n```", testOptions())
	if err != nil {
		t.Fatalf("code fence content was parsed as document syntax: %v", err)
	}
	if got := doc.Root.Children[0].Children[0].Type; got != NodeCodeBlock {
		t.Fatalf("compiled node type = %q, want %q", got, NodeCodeBlock)
	}
}

func TestValidateRejectsTamperedContentAndInvalidGrammar(t *testing.T) {
	doc, err := ParseSectionBody("不可变正文。", testOptions())
	if err != nil {
		t.Fatal(err)
	}
	doc.Root.Children[0].Children[0].Children[0].Text = "被篡改正文。"
	if err := doc.Validate(); err == nil {
		t.Fatal("Validate() accepted content that no longer matches its hash")
	}

	doc, err = ParseSectionBody("合法正文。", testOptions())
	if err != nil {
		t.Fatal(err)
	}
	doc.Root.Children[0].Children[0].Children = []*Node{{Type: NodeTableRow, Attrs: map[string]any{}, Children: []*Node{}, Origin: testOptions().Origin}}
	if err := doc.Seal(); err == nil {
		t.Fatal("Seal() accepted an invalid paragraph child")
	}
}

func TestFinalizeReturnsStableErrorCodes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		code  ErrorCode
	}{
		{name: "empty", input: " \n\t", code: ErrEmptyDocument},
		{name: "html", input: "<div>raw html</div>", code: ErrForbiddenHTML},
		{name: "mdx export", input: "export const Card = () => null", code: ErrForbiddenMDX},
		{name: "mdx component", input: "<Card title={name} />", code: ErrForbiddenMDX},
		{name: "mdx expression", input: "计算结果是 {1 + 1}", code: ErrForbiddenMDX},
		{name: "heading in section body", input: "## 模型不应生成标题", code: ErrSectionHeading},
		{name: "setext heading in section body", input: "模型不应生成标题\n====", code: ErrSectionHeading},
		{name: "unclosed fence", input: "```go\npackage main", code: ErrUnclosedCodeFence},
		{name: "table width", input: "| A | B |\n| --- | --- |\n| only-one |", code: ErrTableColumnMismatch},
		{name: "bad citation", input: "事实。[[cite:bad source]]", code: ErrInvalidCitation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser := NewParser(testOptions())
			if err := parser.Append(test.input); err != nil {
				t.Fatalf("Append() performed strict parsing early: %v", err)
			}
			_, err := parser.Finalize()
			if err == nil {
				t.Fatal("Finalize() expected error")
			}
			var parseErr *ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("error type = %T, want *ParseError", err)
			}
			if parseErr.Code != test.code {
				t.Fatalf("error code = %q, want %q (error: %v)", parseErr.Code, test.code, err)
			}
		})
	}
}

func TestDocumentIdentityAndCitationIDsMatchWireSchema(t *testing.T) {
	doc, err := ParseSectionBody("事实。[[cite:source_1]]", testOptions())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*Document)
	}{
		{name: "document id", mutate: func(doc *Document) { doc.DocumentID = "bad" }},
		{name: "version id", mutate: func(doc *Document) { doc.VersionID = "bad" }},
		{name: "block id", mutate: func(doc *Document) { doc.Root.Children[0].BlockID = "bad" }},
		{name: "source id", mutate: func(doc *Document) {
			doc.Root.Children[0].Children[0].Children[1].SourceID = "bad source"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clone := cloneDocument(t, doc)
			test.mutate(&clone)
			if err := clone.Validate(); err == nil {
				t.Fatal("Validate() accepted an identity rejected by document-ast.schema.json")
			}
		})
	}
}

func TestVersionHashBindsBlockIdentityAndOrigin(t *testing.T) {
	doc, err := ParseSectionBody("受治理正文。", testOptions())
	if err != nil {
		t.Fatal(err)
	}

	originTampered := cloneDocument(t, doc)
	originTampered.Root.Children[0].Origin.Ref = "run_attacker/node_writer"
	if err := originTampered.Validate(); err == nil {
		t.Fatal("Validate() accepted provenance tampering without a new version hash")
	}

	idTampered := cloneDocument(t, doc)
	idTampered.Root.Children[0].BlockID = "blk_relocated"
	if err := idTampered.Validate(); err == nil {
		t.Fatal("Validate() accepted block identity tampering without a new version hash")
	}
}

func TestAttrsUseWireStableJSONSafeIntegers(t *testing.T) {
	for _, value := range []json.Number{"1.0", "1e3", "9007199254740993"} {
		t.Run(string(value), func(t *testing.T) {
			doc, err := ParseSectionBody("正文。", testOptions())
			if err != nil {
				t.Fatal(err)
			}
			doc.Root.Children[0].Attrs["rank"] = value
			if err := doc.Seal(); err == nil {
				t.Fatalf("Seal() accepted non-canonical numeric attr %q", value)
			}
		})
	}

	doc, err := ParseSectionBody("正文。", testOptions())
	if err != nil {
		t.Fatal(err)
	}
	doc.Root.Children[0].Attrs["rank"] = int64(9007199254740991)
	if err := doc.Seal(); err != nil {
		t.Fatal(err)
	}
	decoded := cloneDocument(t, doc)
	if err := decoded.Validate(); err != nil {
		t.Fatalf("JSON-safe integer did not survive wire round trip: %v", err)
	}
}

func cloneDocument(t *testing.T, doc Document) Document {
	t.Helper()
	payload, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var clone Document
	if err := json.Unmarshal(payload, &clone); err != nil {
		t.Fatal(err)
	}
	clone.SectionID = doc.SectionID
	return clone
}

func testOptions() ParseOptions {
	return ParseOptions{
		DocumentID:   "doc_demo",
		VersionID:    "ver_demo_1",
		SectionID:    "section_intro",
		SectionTitle: "引言",
		SectionLevel: 1,
		Origin:       Origin{Kind: OriginModel, Ref: "run_demo/node_writer"},
	}
}

func flattenTypes(root Node) []NodeType {
	types := make([]NodeType, 0)
	walk(&root, func(node *Node) { types = append(types, node.Type) })
	return types
}

func walk(node *Node, visit func(*Node)) {
	visit(node)
	for _, child := range node.Children {
		walk(child, visit)
	}
}
