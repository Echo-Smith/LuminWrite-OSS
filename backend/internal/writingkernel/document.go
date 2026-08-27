package writingkernel

import (
	"errors"
	"fmt"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/lcp"
)

type DocumentNode = lcp.Node
type NodeType = lcp.NodeType
type Origin = lcp.Origin

const (
	NodeTypeDocument      = lcp.NodeDocument
	NodeTypeSection       = lcp.NodeSection
	NodeTypeParagraph     = lcp.NodeParagraph
	NodeTypeText          = lcp.NodeText
	NodeTypeStrong        = lcp.NodeStrong
	NodeTypeEmphasis      = lcp.NodeEmphasis
	NodeTypeLink          = lcp.NodeLink
	NodeTypeCitation      = lcp.NodeCitation
	NodeTypeOrderedList   = lcp.NodeOrderedList
	NodeTypeUnorderedList = lcp.NodeUnorderedList
	NodeTypeListItem      = lcp.NodeListItem
	NodeTypeBlockquote    = lcp.NodeBlockquote
	NodeTypeCodeBlock     = lcp.NodeCodeBlock
	NodeTypeTable         = lcp.NodeTable
	NodeTypeTableRow      = lcp.NodeTableRow
	NodeTypeTableCell     = lcp.NodeTableCell
	OriginUser            = lcp.OriginUser
	OriginModel           = lcp.OriginModel
	OriginSystem          = lcp.OriginSystem
)

type DocumentVersion struct {
	SchemaVersion string        `json:"schema_version"`
	DocumentID    string        `json:"document_id"`
	VersionID     string        `json:"version_id"`
	BaseVersionID *string       `json:"base_version_id"`
	ContentHash   string        `json:"content_hash"`
	VersionHash   string        `json:"version_hash"`
	Root          *DocumentNode `json:"root"`
}

func (d DocumentVersion) Clone() DocumentVersion {
	clone := d
	if d.BaseVersionID != nil {
		base := *d.BaseVersionID
		clone.BaseVersionID = &base
	}
	clone.Root = cloneNode(d.Root)
	return clone
}

func (d DocumentVersion) WithComputedHashes() (DocumentVersion, error) {
	copy := d.Clone()
	if copy.Root == nil {
		return DocumentVersion{}, errors.New("document root is required")
	}
	if strings.TrimSpace(copy.DocumentID) == "" || strings.TrimSpace(copy.VersionID) == "" {
		return DocumentVersion{}, errors.New("document_id and version_id are required")
	}
	sectionID := firstSectionID(copy.Root)
	if sectionID == "" {
		sectionID = "root"
	}
	lcpDoc := lcp.Document{DocumentID: copy.DocumentID, VersionID: copy.VersionID, BaseVersionID: copy.BaseVersionID, SectionID: sectionID, Root: *copy.Root}
	if err := lcpDoc.Seal(); err != nil {
		return DocumentVersion{}, err
	}
	copy.SchemaVersion = SchemaVersionV1
	copy.Root = &lcpDoc.Root
	copy.ContentHash = lcpDoc.ContentHash
	copy.VersionHash = lcpDoc.VersionHash
	return copy, nil
}

func (d DocumentVersion) Validate() error {
	if d.SchemaVersion != SchemaVersionV1 {
		return fmt.Errorf("schema_version must be %q", SchemaVersionV1)
	}
	if d.Root == nil {
		return errors.New("document root is required")
	}
	if strings.TrimSpace(d.DocumentID) == "" || strings.TrimSpace(d.VersionID) == "" {
		return errors.New("document_id and version_id are required")
	}
	sectionID := firstSectionID(d.Root)
	if sectionID == "" {
		sectionID = "root"
	}
	lcpDoc := lcp.Document{SchemaVersion: lcp.SchemaVersion, DocumentID: d.DocumentID, VersionID: d.VersionID, BaseVersionID: d.BaseVersionID, SectionID: sectionID, ContentHash: d.ContentHash, VersionHash: d.VersionHash, Root: *d.Root}
	if err := lcpDoc.Validate(); err != nil {
		return err
	}
	return nil
}

func (d DocumentVersion) FindBlock(blockID string) (*DocumentNode, error) {
	if d.Root == nil {
		return nil, errors.New("document root is required")
	}
	if node := findNode(d.Root, blockID); node != nil {
		return node, nil
	}
	return nil, fmt.Errorf("block %q not found", blockID)
}

func (d DocumentVersion) BlockHash(blockID string) (string, error) {
	node, err := d.FindBlock(blockID)
	if err != nil {
		return "", err
	}
	if node.ContentHash == "" {
		return "", fmt.Errorf("block %q is not sealed", blockID)
	}
	return node.ContentHash, nil
}

func findNode(node *DocumentNode, blockID string) *DocumentNode {
	if node == nil || blockID == "" {
		return nil
	}
	if node.BlockID == blockID {
		return node
	}
	for _, child := range node.Children {
		if found := findNode(child, blockID); found != nil {
			return found
		}
	}
	return nil
}

func firstSectionID(root *DocumentNode) string {
	if root == nil {
		return ""
	}
	if root.Type == lcp.NodeSection {
		if value, ok := root.Attrs["section_id"].(string); ok {
			return value
		}
	}
	for _, child := range root.Children {
		if id := firstSectionID(child); id != "" {
			return id
		}
	}
	return ""
}
