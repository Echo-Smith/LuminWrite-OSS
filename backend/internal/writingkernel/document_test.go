package writingkernel

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testDocument(t *testing.T) DocumentVersion {
	t.Helper()
	doc := DocumentVersion{
		SchemaVersion: SchemaVersionV1,
		DocumentID:    "doc_test",
		VersionID:     "ver_1",
		BaseVersionID: nil,
		Root: &DocumentNode{
			BlockID: "blk_root",
			Type:    NodeTypeDocument,
			Attrs:   map[string]any{},
			Children: []*DocumentNode{
				{
					BlockID: "blk_section",
					Type:    NodeTypeSection,
					Attrs:   map[string]any{"level": 1},
					Children: []*DocumentNode{
						{BlockID: "blk_p1", Type: NodeTypeParagraph, Attrs: map[string]any{}, Children: []*DocumentNode{
							{BlockID: "blk_t1", Type: NodeTypeText, Text: "原始内容", Attrs: map[string]any{}},
						}},
					},
				},
			},
		},
	}
	stampTestOrigin(doc.Root)
	sealed, err := doc.WithComputedHashes()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func stampTestOrigin(node *DocumentNode) {
	if node == nil {
		return
	}
	node.Origin = Origin{Kind: OriginSystem, Ref: "test/fixture"}
	for _, child := range node.Children {
		stampTestOrigin(child)
	}
}

func TestDocumentVersionValidatesAndRoundTrips(t *testing.T) {
	doc := testDocument(t)
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DocumentVersion
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != doc.ContentHash || decoded.VersionHash != doc.VersionHash {
		t.Fatalf("hashes changed during round trip: %#v %#v", decoded, doc)
	}
}

func TestDocumentVersionHashChangesWithAST(t *testing.T) {
	doc := testDocument(t)
	changed := doc.Clone()
	changed.Root.Children[0].Children[0].Children[0].Text = "修改内容"
	changed, err := changed.WithComputedHashes()
	if err != nil {
		t.Fatal(err)
	}
	if changed.ContentHash == doc.ContentHash || changed.VersionHash == doc.VersionHash {
		t.Fatal("AST mutation did not change version hashes")
	}
}

func TestRevisionSetOperationsProduceNewVersionWithoutMutatingBase(t *testing.T) {
	doc := testDocument(t)
	base := doc
	insert := RevisionSet{
		BaseVersion: doc.VersionID,
		Revisions: []Revision{{
			Operation: RevisionInsert, ParentBlockID: "blk_section", Index: 1,
			Node: &DocumentNode{BlockID: "blk_p2", Type: NodeTypeParagraph, Attrs: map[string]any{}, Origin: Origin{Kind: OriginSystem, Ref: "test/revision"}, Children: []*DocumentNode{
				{Type: NodeTypeText, Text: "新增", Attrs: map[string]any{}, Children: []*DocumentNode{}, Origin: Origin{Kind: OriginSystem, Ref: "test/revision"}},
			}},
			Reason: "补充内容", SemanticImpact: "adds_detail",
		}},
	}
	var err error
	doc, err = doc.ApplyRevisionSet(insert)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := doc.BlockHash("blk_t1")
	doc, err = doc.ApplyRevisionSet(RevisionSet{BaseVersion: doc.VersionID, Revisions: []Revision{{
		Operation: RevisionReplace, TargetBlockID: "blk_t1", TargetHash: target,
		Node:   &DocumentNode{BlockID: "blk_t1", Type: NodeTypeText, Text: "替换", Attrs: map[string]any{}, Children: []*DocumentNode{}, Origin: Origin{Kind: OriginSystem, Ref: "test/revision"}},
		Reason: "修订表述", SemanticImpact: "changes_wording",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	target, _ = doc.BlockHash("blk_p2")
	doc, err = doc.ApplyRevisionSet(RevisionSet{BaseVersion: doc.VersionID, Revisions: []Revision{{
		Operation: RevisionMove, TargetBlockID: "blk_p2", TargetHash: target, ParentBlockID: "blk_section", Index: 0,
		Reason: "调整位置", SemanticImpact: "reorders_content",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	target, _ = doc.BlockHash("blk_p1")
	updated, err := doc.ApplyRevisionSet(RevisionSet{BaseVersion: doc.VersionID, Revisions: []Revision{{
		Operation: RevisionDelete, TargetBlockID: "blk_p1", TargetHash: target,
		Reason: "删除重复", SemanticImpact: "removes_content",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.VersionID == base.VersionID || updated.ContentHash == base.ContentHash {
		t.Fatal("revision did not create a new version")
	}
	if _, err := base.FindBlock("blk_p2"); err == nil {
		t.Fatal("base document was mutated")
	}
	if _, err := updated.FindBlock("blk_t1"); err == nil {
		t.Fatal("delete revision did not remove target")
	}
	if _, err := updated.FindBlock("blk_p2"); err != nil {
		t.Fatal(err)
	}
}

func TestRevisionSetConflictsOnVersionOrTargetHash(t *testing.T) {
	doc := testDocument(t)
	hash, err := doc.BlockHash("blk_t1")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		set  RevisionSet
	}{
		{name: "version", set: RevisionSet{BaseVersion: "ver_other", Revisions: []Revision{{Operation: RevisionDelete, TargetBlockID: "blk_t1", TargetHash: hash}}}},
		{name: "target hash", set: RevisionSet{BaseVersion: doc.VersionID, Revisions: []Revision{{Operation: RevisionDelete, TargetBlockID: "blk_t1", TargetHash: "sha256:" + strings.Repeat("a", 64)}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := doc.ApplyRevisionSet(test.set); !errors.Is(err, ErrConflicted) {
				t.Fatalf("expected CONFLICTED, got %v", err)
			}
		})
	}
}

func TestRevisionSetRequiresTargetHashForDestructiveOperations(t *testing.T) {
	doc := testDocument(t)
	_, err := doc.ApplyRevisionSet(RevisionSet{BaseVersion: doc.VersionID, Revisions: []Revision{{
		Operation: RevisionDelete, TargetBlockID: "blk_p1",
		Reason: "删除", SemanticImpact: "removes_content",
	}}})
	if err == nil || errors.Is(err, ErrConflicted) {
		t.Fatalf("missing target_hash error = %v, want validation failure", err)
	}
}
