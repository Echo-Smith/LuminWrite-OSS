package lcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
)

const SchemaVersion = "lcp/1.0"

type NodeType string

const (
	NodeDocument      NodeType = "document"
	NodeSection       NodeType = "section"
	NodeParagraph     NodeType = "paragraph"
	NodeText          NodeType = "text"
	NodeStrong        NodeType = "strong"
	NodeEmphasis      NodeType = "emphasis"
	NodeLink          NodeType = "link"
	NodeCitation      NodeType = "citation"
	NodeOrderedList   NodeType = "ordered_list"
	NodeUnorderedList NodeType = "unordered_list"
	NodeListItem      NodeType = "list_item"
	NodeBlockquote    NodeType = "blockquote"
	NodeCodeBlock     NodeType = "code_block"
	NodeTable         NodeType = "table"
	NodeTableRow      NodeType = "table_row"
	NodeTableCell     NodeType = "table_cell"
)

type OriginKind string

const (
	OriginUser   OriginKind = "user"
	OriginModel  OriginKind = "model"
	OriginSystem OriginKind = "system"
)

type Origin struct {
	Kind OriginKind `json:"kind"`
	Ref  string     `json:"ref"`
}

type Node struct {
	BlockID     string         `json:"block_id,omitempty"`
	Type        NodeType       `json:"type"`
	Attrs       map[string]any `json:"attrs"`
	Children    []*Node        `json:"children"`
	Text        string         `json:"text,omitempty"`
	Destination string         `json:"destination,omitempty"`
	SourceID    string         `json:"source_id,omitempty"`
	Origin      Origin         `json:"origin"`
	ContentHash string         `json:"content_hash,omitempty"`
}

type Document struct {
	SchemaVersion string  `json:"schema_version"`
	DocumentID    string  `json:"document_id"`
	VersionID     string  `json:"version_id"`
	BaseVersionID *string `json:"base_version_id"`
	ContentHash   string  `json:"content_hash"`
	VersionHash   string  `json:"version_hash"`
	Root          Node    `json:"root"`
	SectionID     string  `json:"-"`
}

var (
	hashPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	documentIDPattern = regexp.MustCompile(`^doc_[A-Za-z0-9_-]+$`)
	versionIDPattern  = regexp.MustCompile(`^ver_[A-Za-z0-9_-]+$`)
	blockIDPattern    = regexp.MustCompile(`^blk_[A-Za-z0-9_-]+$`)
	sourceIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	attrKeyPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

func IsBlockNode(t NodeType) bool {
	switch t {
	case NodeDocument, NodeSection, NodeParagraph, NodeOrderedList, NodeUnorderedList,
		NodeListItem, NodeBlockquote, NodeCodeBlock, NodeTable, NodeTableRow, NodeTableCell:
		return true
	default:
		return false
	}
}

func (d *Document) Seal() error {
	if !documentIDPattern.MatchString(d.DocumentID) || !versionIDPattern.MatchString(d.VersionID) || strings.TrimSpace(d.SectionID) == "" {
		return errors.New("document_id, version_id, and section_id must use canonical identities")
	}
	if d.BaseVersionID != nil && !versionIDPattern.MatchString(*d.BaseVersionID) {
		return errors.New("base_version_id must use the ver_ prefix")
	}
	if err := sealNode(&d.Root, d.DocumentID+"/"+d.SectionID, "0"); err != nil {
		return err
	}
	d.SchemaVersion = SchemaVersion
	d.ContentHash = d.Root.ContentHash
	versionHashValue, err := versionHash(d.DocumentID, d.VersionID, d.BaseVersionID, d.ContentHash, &d.Root)
	if err != nil {
		return err
	}
	d.VersionHash = versionHashValue
	return d.Validate()
}

func (d Document) Validate() error {
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %q", SchemaVersion)
	}
	if d.Root.Type != NodeDocument {
		return errors.New("root node must be document")
	}
	if !documentIDPattern.MatchString(d.DocumentID) || !versionIDPattern.MatchString(d.VersionID) {
		return errors.New("document_id or version_id is not canonical")
	}
	if d.BaseVersionID != nil && !versionIDPattern.MatchString(*d.BaseVersionID) {
		return errors.New("base_version_id is not canonical")
	}
	seen := make(map[string]struct{})
	if err := validateNode(&d.Root, seen); err != nil {
		return err
	}
	if d.ContentHash != d.Root.ContentHash || !hashPattern.MatchString(d.ContentHash) {
		return errors.New("document content_hash does not match sealed root")
	}
	expectedVersionHash, err := versionHash(d.DocumentID, d.VersionID, d.BaseVersionID, d.ContentHash, &d.Root)
	if err != nil {
		return err
	}
	if d.VersionHash != expectedVersionHash {
		return errors.New("document version_hash does not match version identity and content")
	}
	return nil
}

func sealNode(node *Node, identity, path string) error {
	if node == nil {
		return errors.New("nil AST node")
	}
	if node.Attrs == nil {
		node.Attrs = map[string]any{}
	}
	if node.Children == nil {
		node.Children = []*Node{}
	}
	for i, child := range node.Children {
		if err := sealNode(child, identity, fmt.Sprintf("%s.%d", path, i)); err != nil {
			return err
		}
	}
	nodeHash, err := computeNodeHash(node)
	if err != nil {
		return err
	}
	node.ContentHash = nodeHash
	if IsBlockNode(node.Type) && node.BlockID == "" {
		idSeed := []byte(identity + "\x00" + path + "\x00" + string(node.Type) + "\x00" + node.ContentHash)
		sum := sha256.Sum256(idSeed)
		node.BlockID = "blk_" + hex.EncodeToString(sum[:12])
	}
	return nil
}

func computeNodeHash(node *Node) (string, error) {
	childHashes := make([]string, len(node.Children))
	for i, child := range node.Children {
		if child == nil {
			return "", errors.New("nil AST child")
		}
		childHashes[i] = child.ContentHash
	}
	payload := struct {
		Type        NodeType       `json:"type"`
		Attrs       map[string]any `json:"attrs"`
		Children    []string       `json:"children"`
		Text        string         `json:"text,omitempty"`
		Destination string         `json:"destination,omitempty"`
		SourceID    string         `json:"source_id,omitempty"`
	}{node.Type, node.Attrs, childHashes, node.Text, node.Destination, node.SourceID}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal %s node: %w", node.Type, err)
	}
	return digest(raw), nil
}

func validateNode(node *Node, seen map[string]struct{}) error {
	if node == nil {
		return errors.New("nil AST node")
	}
	if !validNodeType(node.Type) {
		return fmt.Errorf("unsupported node type %q", node.Type)
	}
	if node.Attrs == nil || node.Children == nil {
		return fmt.Errorf("node %s must contain attrs and children", node.Type)
	}
	if err := validateAttrs(node.Attrs); err != nil {
		return fmt.Errorf("node %s attrs: %w", node.Type, err)
	}
	if !validOrigin(node.Origin) || !hashPattern.MatchString(node.ContentHash) {
		return fmt.Errorf("node %s requires valid origin and content_hash", node.Type)
	}
	if IsBlockNode(node.Type) {
		if !blockIDPattern.MatchString(node.BlockID) {
			return fmt.Errorf("block node %s is not sealed", node.Type)
		}
		if _, exists := seen[node.BlockID]; exists {
			return fmt.Errorf("duplicate block_id %q", node.BlockID)
		}
		seen[node.BlockID] = struct{}{}
	}
	if node.Type == NodeText && node.Text == "" {
		return errors.New("text node must not be empty")
	}
	if node.Type == NodeLink && !validLinkDestination(node.Destination) {
		return errors.New("link node requires a safe http, https, mailto, fragment, or relative destination")
	}
	if node.Type == NodeCitation && !sourceIDPattern.MatchString(node.SourceID) {
		return errors.New("citation node requires a canonical source_id")
	}
	if err := validateShape(node); err != nil {
		return err
	}
	for _, child := range node.Children {
		if err := validateNode(child, seen); err != nil {
			return err
		}
	}
	expectedHash, err := computeNodeHash(node)
	if err != nil {
		return err
	}
	if node.ContentHash != expectedHash {
		return fmt.Errorf("content_hash mismatch for %s node", node.Type)
	}
	return nil
}

func validateShape(node *Node) error {
	if len(node.Children) == 0 {
		switch node.Type {
		case NodeDocument, NodeCodeBlock, NodeText, NodeCitation:
			return nil
		default:
			return fmt.Errorf("%s node requires children", node.Type)
		}
	}
	allowed := func(types ...NodeType) bool {
		for _, child := range node.Children {
			matched := false
			for _, nodeType := range types {
				if child != nil && child.Type == nodeType {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		return true
	}
	valid := false
	switch node.Type {
	case NodeDocument:
		valid = allowed(NodeSection)
	case NodeSection:
		valid = allowed(NodeParagraph, NodeOrderedList, NodeUnorderedList, NodeBlockquote, NodeCodeBlock, NodeTable)
	case NodeParagraph, NodeStrong, NodeEmphasis, NodeLink:
		valid = allowed(NodeText, NodeStrong, NodeEmphasis, NodeLink, NodeCitation)
	case NodeOrderedList, NodeUnorderedList:
		valid = allowed(NodeListItem)
	case NodeListItem, NodeBlockquote:
		valid = allowed(NodeParagraph, NodeOrderedList, NodeUnorderedList, NodeBlockquote, NodeCodeBlock, NodeTable)
	case NodeTable:
		valid = allowed(NodeTableRow)
	case NodeTableRow:
		valid = allowed(NodeTableCell)
	case NodeTableCell:
		valid = allowed(NodeParagraph)
	case NodeCodeBlock, NodeText, NodeCitation:
		valid = false
	}
	if !valid {
		return fmt.Errorf("%s node contains an invalid child type", node.Type)
	}
	return nil
}

func validNodeType(t NodeType) bool {
	switch t {
	case NodeDocument, NodeSection, NodeParagraph, NodeText, NodeStrong, NodeEmphasis,
		NodeLink, NodeCitation, NodeOrderedList, NodeUnorderedList, NodeListItem,
		NodeBlockquote, NodeCodeBlock, NodeTable, NodeTableRow, NodeTableCell:
		return true
	default:
		return false
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func versionHash(documentID, versionID string, baseVersionID *string, contentHash string, root *Node) (string, error) {
	base := ""
	if baseVersionID != nil {
		base = *baseVersionID
	}
	rootJSON, err := json.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("marshal AST identity and provenance: %w", err)
	}
	astHash := digest(rootJSON)
	return digest([]byte(documentID + "\x00" + versionID + "\x00" + base + "\x00" + contentHash + "\x00" + astHash)), nil
}

func validOrigin(origin Origin) bool {
	if strings.TrimSpace(origin.Ref) == "" {
		return false
	}
	switch origin.Kind {
	case OriginUser, OriginModel, OriginSystem:
		return true
	default:
		return false
	}
}

func validateAttrs(attrs map[string]any) error {
	for key, value := range attrs {
		if !attrKeyPattern.MatchString(key) {
			return fmt.Errorf("invalid key %q", key)
		}
		switch typed := value.(type) {
		case nil, string, bool:
		case int:
			if !safeInteger(float64(typed)) {
				return fmt.Errorf("%q exceeds the JSON-safe integer range", key)
			}
		case int8, int16, int32:
		case int64:
			if typed < -9007199254740991 || typed > 9007199254740991 {
				return fmt.Errorf("%q exceeds the JSON-safe integer range", key)
			}
		case uint:
			if uint64(typed) > 9007199254740991 {
				return fmt.Errorf("%q exceeds the JSON-safe integer range", key)
			}
		case uint64:
			if typed > 9007199254740991 {
				return fmt.Errorf("%q exceeds the JSON-safe integer range", key)
			}
		case uint8, uint16, uint32:
		case json.Number:
			integer, err := typed.Int64()
			if err != nil || integer < -9007199254740991 || integer > 9007199254740991 || typed.String() != fmt.Sprintf("%d", integer) {
				return fmt.Errorf("%q must use canonical JSON-safe integer notation", key)
			}
		case float32:
			if !safeInteger(float64(typed)) {
				return fmt.Errorf("%q must be a JSON-safe integer", key)
			}
		case float64:
			if !safeInteger(typed) {
				return fmt.Errorf("%q must be a JSON-safe integer", key)
			}
		default:
			return fmt.Errorf("%q must be a scalar JSON value", key)
		}
	}
	return nil
}

func safeInteger(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value && math.Abs(value) <= 9007199254740991
}

func validLinkDestination(destination string) bool {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return false
	}
	if strings.HasPrefix(destination, "#") || strings.HasPrefix(destination, "/") || strings.HasPrefix(destination, "./") || strings.HasPrefix(destination, "../") {
		return true
	}
	parsed, err := url.Parse(destination)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return true
	default:
		return false
	}
}
