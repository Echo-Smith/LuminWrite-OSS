package writingkernel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var ErrConflicted = errors.New("CONFLICTED")

type RevisionOperation string

const (
	RevisionInsert  RevisionOperation = "insert"
	RevisionReplace RevisionOperation = "replace"
	RevisionDelete  RevisionOperation = "delete"
	RevisionMove    RevisionOperation = "move"
)

type Revision struct {
	Operation      RevisionOperation `json:"operation"`
	TargetBlockID  string            `json:"target_block_id,omitempty"`
	TargetHash     string            `json:"target_hash,omitempty"`
	ParentBlockID  string            `json:"parent_block_id,omitempty"`
	Index          int               `json:"index,omitempty"`
	Node           *DocumentNode     `json:"node,omitempty"`
	Reason         string            `json:"reason"`
	SemanticImpact string            `json:"semantic_impact"`
}

type RevisionSet struct {
	BaseVersion string     `json:"base_version"`
	Revisions   []Revision `json:"revisions"`
}

func (d DocumentVersion) ApplyRevisionSet(set RevisionSet) (DocumentVersion, error) {
	// This method provides an atomic in-memory transformation. Persistence must
	// additionally compare-and-swap VersionID/BaseVersion at commit time; that
	// storage boundary is intentionally outside the domain object.
	if err := d.Validate(); err != nil {
		return DocumentVersion{}, fmt.Errorf("invalid base document: %w", err)
	}
	if set.BaseVersion != d.VersionID {
		return DocumentVersion{}, conflict("base version %q no longer matches %q", set.BaseVersion, d.VersionID)
	}
	if len(set.Revisions) == 0 {
		return DocumentVersion{}, errors.New("revision set must not be empty")
	}
	updated := d.Clone()
	for i, revision := range set.Revisions {
		if revision.TargetBlockID != "" && revision.TargetHash != "" {
			target := findNode(updated.Root, revision.TargetBlockID)
			if target == nil || target.ContentHash != revision.TargetHash {
				return DocumentVersion{}, conflict("target %q changed before revision %d", revision.TargetBlockID, i)
			}
		}
		if err := validateRevision(revision); err != nil {
			return DocumentVersion{}, fmt.Errorf("revision %d: %w", i, err)
		}
		if err := applyRevision(updated.Root, revision); err != nil {
			return DocumentVersion{}, fmt.Errorf("revision %d: %w", i, err)
		}
		// Re-seal after each operation so subsequent target_hash checks observe
		// the result of earlier revisions in the same ordered set.
		sealed, err := updated.WithComputedHashes()
		if err != nil {
			return DocumentVersion{}, fmt.Errorf("revision %d: seal: %w", i, err)
		}
		updated = sealed
	}
	base := d.VersionID
	updated.BaseVersionID = &base
	updated.VersionID = nextVersionID(d.VersionID, updated.ContentHash, set)
	return updated.WithComputedHashes()
}

func validateRevision(r Revision) error {
	if strings.TrimSpace(r.Reason) == "" || strings.TrimSpace(r.SemanticImpact) == "" {
		return errors.New("reason and semantic_impact are required")
	}
	if r.Index < 0 {
		return errors.New("index must not be negative")
	}
	switch r.Operation {
	case RevisionInsert:
		if r.ParentBlockID == "" || r.Node == nil {
			return errors.New("insert requires parent_block_id and node")
		}
	case RevisionReplace:
		if r.TargetBlockID == "" || r.TargetHash == "" || r.Node == nil {
			return errors.New("replace requires target_block_id, target_hash, and node")
		}
	case RevisionDelete:
		if r.TargetBlockID == "" || r.TargetHash == "" {
			return errors.New("delete requires target_block_id and target_hash")
		}
	case RevisionMove:
		if r.TargetBlockID == "" || r.TargetHash == "" || r.ParentBlockID == "" {
			return errors.New("move requires target_block_id, target_hash, and parent_block_id")
		}
	default:
		return fmt.Errorf("unsupported revision operation %q", r.Operation)
	}
	return nil
}

func applyRevision(root *DocumentNode, revision Revision) error {
	switch revision.Operation {
	case RevisionInsert:
		if revision.Node.BlockID != "" && findNode(root, revision.Node.BlockID) != nil {
			return conflict("block_id %q already exists", revision.Node.BlockID)
		}
		parent := findNode(root, revision.ParentBlockID)
		if parent == nil {
			return conflict("parent block %q not found", revision.ParentBlockID)
		}
		return insertChild(parent, revision.Index, cloneNode(revision.Node))
	case RevisionReplace:
		parent, index := findParent(root, revision.TargetBlockID)
		if parent == nil {
			return conflict("target block %q cannot be replaced", revision.TargetBlockID)
		}
		replacement := cloneNode(revision.Node)
		if replacement.BlockID == "" {
			replacement.BlockID = revision.TargetBlockID
		}
		if replacement.BlockID != revision.TargetBlockID && findNode(root, replacement.BlockID) != nil {
			return conflict("replacement block_id %q already exists", replacement.BlockID)
		}
		parent.Children[index] = replacement
		return nil
	case RevisionDelete:
		parent, index := findParent(root, revision.TargetBlockID)
		if parent == nil {
			return conflict("target block %q cannot be deleted", revision.TargetBlockID)
		}
		parent.Children = append(parent.Children[:index], parent.Children[index+1:]...)
		return nil
	case RevisionMove:
		parent, index := findParent(root, revision.TargetBlockID)
		if parent == nil {
			return conflict("target block %q cannot be moved", revision.TargetBlockID)
		}
		target := parent.Children[index]
		if findNode(target, revision.ParentBlockID) != nil {
			return conflict("move would create a cycle")
		}
		parent.Children = append(parent.Children[:index], parent.Children[index+1:]...)
		newParent := findNode(root, revision.ParentBlockID)
		if newParent == nil {
			return conflict("parent block %q not found", revision.ParentBlockID)
		}
		return insertChild(newParent, revision.Index, target)
	default:
		return fmt.Errorf("unsupported revision operation %q", revision.Operation)
	}
}

func insertChild(parent *DocumentNode, index int, child *DocumentNode) error {
	if index > len(parent.Children) {
		return conflict("index %d exceeds parent size %d", index, len(parent.Children))
	}
	parent.Children = append(parent.Children, nil)
	copy(parent.Children[index+1:], parent.Children[index:])
	parent.Children[index] = child
	return nil
}

func findParent(root *DocumentNode, blockID string) (*DocumentNode, int) {
	if root == nil {
		return nil, -1
	}
	for i, child := range root.Children {
		if child.BlockID == blockID {
			return root, i
		}
		if parent, index := findParent(child, blockID); parent != nil {
			return parent, index
		}
	}
	return nil, -1
}

func cloneNode(node *DocumentNode) *DocumentNode {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Attrs = make(map[string]any, len(node.Attrs))
	for key, value := range node.Attrs {
		clone.Attrs[key] = value
	}
	clone.Children = make([]*DocumentNode, len(node.Children))
	for i, child := range node.Children {
		clone.Children[i] = cloneNode(child)
	}
	return &clone
}

func nextVersionID(baseVersion, contentHash string, set RevisionSet) string {
	seed := baseVersion + "\x00" + contentHash
	for _, revision := range set.Revisions {
		seed += "\x00" + string(revision.Operation) + "\x00" + revision.TargetBlockID + "\x00" + revision.ParentBlockID + "\x00" + revision.Reason + "\x00" + revision.SemanticImpact
	}
	sum := sha256.Sum256([]byte(seed))
	return "ver_" + hex.EncodeToString(sum[:12])
}

func conflict(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConflicted, fmt.Sprintf(format, args...))
}
