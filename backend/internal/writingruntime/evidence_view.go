package writingruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// EvidenceViewItem is a unified view of evidence from all sources.
// It is a deterministic projection of existing artifacts — not a new
// persistence layer. The same inputs always produce the same view.
type EvidenceViewItem struct {
	EvidenceID          string    `json:"evidence_id"`
	ClaimOrTopic        string    `json:"claim_or_topic"`
	SupportingExcerptRef string   `json:"supporting_excerpt_ref,omitempty"`
	SourceRef           string    `json:"source_ref,omitempty"`
	ObservedAt          time.Time `json:"observed_at"`
	Freshness           string    `json:"freshness,omitempty"`           // "as_of_run", "stale", "unknown"
	SupportStrength     string    `json:"support_strength,omitempty"`    // "strong", "moderate", "weak", "unknown"
	ContradictionStatus string    `json:"contradiction_status,omitempty"` // "none", "unresolved", "resolved"
	ContentHash         string    `json:"content_hash"`
}

// EvidenceView is the node-level evidence projection.
type EvidenceView struct {
	NodeID   string             `json:"node_id"`
	Attempt  int                `json:"attempt"`
	Items    []EvidenceViewItem `json:"items"`
	ViewHash string             `json:"view_hash"` // SHA256 of the sorted items
}

// EvidenceViewRenderer produces an EvidenceView from existing artifacts.
// No external dependencies — pure function over provided data.
type EvidenceViewRenderer struct{}

// EvidenceSources holds the raw data that feeds the EvidenceView.
// All fields are optional — missing sources produce empty sections.
type EvidenceSources struct {
	// SourcePack artifacts (from search/retrieval)
	SourcePacks []SourcePackEvidence
	// ClaimMap artifacts (from material processing)
	ClaimMaps []ClaimMapEvidence
	// Runtime evidence records (from rollout/shadow)
	RuntimeEvidence []RuntimeEvidenceItem
	// Material snapshot artifacts
	MaterialSnapshots []MaterialSnapshotEvidence
	// Research evidence packs (if available)
	ResearchPacks []ResearchPackEvidence
}

// SourcePackEvidence is the projection-ready extract from a SourcePack artifact.
type SourcePackEvidence struct {
	Query      string
	Sources    []SourceRecordEvidence
	ContentHash string
	ObservedAt time.Time
}

// SourceRecordEvidence is one source entry from a SourcePack.
type SourceRecordEvidence struct {
	SourceID string
	Title    string
	URL      string
	Excerpt  string
	Score    float64
}

// ClaimMapEvidence is the projection-ready extract from a ClaimMap artifact.
type ClaimMapEvidence struct {
	Claims      []ClaimEvidence
	Findings    []FindingEvidence
	ContentHash string
	ObservedAt  time.Time
}

// ClaimEvidence is one claim entry from a ClaimMap.
type ClaimEvidence struct {
	ClaimID    string
	Subject    string
	Predicate  string
	Value      string
	SourceRefs []string
}

// FindingEvidence is one finding entry from a ClaimMap.
type FindingEvidence struct {
	FindingID string
	Code      string
	Severity  string
	Subject   string
	Predicate string
	ClaimIDs  []string
	SourceRefs []string
}

// RuntimeEvidenceItem is the projection-ready extract from RuntimeEvidence.
type RuntimeEvidenceItem struct {
	EvidenceID  string
	Kind        string
	Lane        string
	PolicyHash  string
	PolicyVersion int
	Status      string
	RecordedAt  time.Time
}

// MaterialSnapshotEvidence is the projection-ready extract from a MaterialSnapshot.
type MaterialSnapshotEvidence struct {
	MaterialID  string
	Title       string
	SourceKind  string
	SourceRef   string
	ContentHash string
	UpdatedAt   time.Time
}

// ResearchPackEvidence is the projection-ready extract from a research evidence pack.
type ResearchPackEvidence struct {
	ClaimID      string
	ClaimText    string
	Kind         string
	PaperID      string
	ReviewStatus string
	EvidenceIDs  []string
	SourceRefs   []string
}

// Render produces a deterministic EvidenceView from the provided artifacts.
// The same inputs always produce the same output (same hash).
func (r *EvidenceViewRenderer) Render(nodeID string, attempt int, sources EvidenceSources) EvidenceView {
	var items []EvidenceViewItem

	// Collect from SourcePacks
	for _, pack := range sources.SourcePacks {
		for _, src := range pack.Sources {
			topic := src.Title
			if topic == "" {
				topic = pack.Query
			}
			item := EvidenceViewItem{
				ClaimOrTopic: topic,
				SourceRef:    src.URL,
				ContentHash:  pack.ContentHash,
				ObservedAt:   pack.ObservedAt,
				Freshness:    "as_of_run",
			}
			if src.Excerpt != "" {
				item.SupportingExcerptRef = src.Excerpt
			}
			items = append(items, item)
		}
		// If the pack has no sources, still record the query as evidence
		if len(pack.Sources) == 0 && pack.Query != "" {
			items = append(items, EvidenceViewItem{
				ClaimOrTopic: pack.Query,
				ContentHash:  pack.ContentHash,
				ObservedAt:   pack.ObservedAt,
				Freshness:    "as_of_run",
			})
		}
	}

	// Collect from ClaimMaps
	for _, cm := range sources.ClaimMaps {
		for _, claim := range cm.Claims {
			claimText := claim.Subject
			if claim.Predicate != "" {
				claimText += " " + claim.Predicate
			}
			if claim.Value != "" {
				claimText += " " + claim.Value
			}
			item := EvidenceViewItem{
				ClaimOrTopic:        claimText,
				ContentHash:         cm.ContentHash,
				ObservedAt:          cm.ObservedAt,
				Freshness:           "as_of_run",
				ContradictionStatus: "none",
			}
			if len(claim.SourceRefs) > 0 {
				item.SourceRef = strings.Join(claim.SourceRefs, "; ")
			}
			items = append(items, item)
		}
		for _, finding := range cm.Findings {
			contradictionStatus := "unresolved"
			if finding.Severity == "info" || finding.Code == "" {
				contradictionStatus = "resolved"
			}
			subject := finding.Subject
			if finding.Predicate != "" {
				subject += " " + finding.Predicate
			}
			item := EvidenceViewItem{
				ClaimOrTopic:        subject,
				ContentHash:         cm.ContentHash,
				ObservedAt:          cm.ObservedAt,
				Freshness:           "as_of_run",
				ContradictionStatus: contradictionStatus,
			}
			if len(finding.SourceRefs) > 0 {
				item.SourceRef = strings.Join(finding.SourceRefs, "; ")
			}
			items = append(items, item)
		}
	}

	// Collect from RuntimeEvidence
	for _, ev := range sources.RuntimeEvidence {
		topic := ev.Kind
		if ev.Lane != "" {
			topic += " (" + ev.Lane + ")"
		}
		item := EvidenceViewItem{
			ClaimOrTopic: topic,
			ContentHash:  ev.PolicyHash,
			ObservedAt:   ev.RecordedAt,
			Freshness:    "as_of_run",
		}
		items = append(items, item)
	}

	// Collect from MaterialSnapshots
	for _, mat := range sources.MaterialSnapshots {
		topic := mat.MaterialID
		if mat.Title != "" {
			topic = mat.Title
		}
		sourceRef := mat.SourceRef
		items = append(items, EvidenceViewItem{
			ClaimOrTopic: topic,
			SourceRef:    sourceRef,
			ContentHash:  mat.ContentHash,
			ObservedAt:   mat.UpdatedAt,
			Freshness:    "as_of_run",
		})
	}

	// Collect from ResearchPacks
	for _, rp := range sources.ResearchPacks {
		supportStrength := "unknown"
		switch rp.ReviewStatus {
		case "approved", "verified":
			supportStrength = "strong"
		case "pending":
			supportStrength = "moderate"
		case "rejected":
			supportStrength = "weak"
		}
		item := EvidenceViewItem{
			ClaimOrTopic:    rp.ClaimText,
			SupportStrength: supportStrength,
			ObservedAt:      time.Time{}, // research packs don't carry a timestamp directly
			Freshness:       "as_of_run",
		}
		if len(rp.SourceRefs) > 0 {
			item.SourceRef = strings.Join(rp.SourceRefs, "; ")
		} else if rp.PaperID != "" {
			item.SourceRef = rp.PaperID
		}
		items = append(items, item)
	}

	// Assign deterministic EvidenceIDs (sha256 of canonical content)
	for i := range items {
		items[i].EvidenceID = deterministicEvidenceID(items[i])
	}

	// Sort by EvidenceID for determinism
	sort.Slice(items, func(i, j int) bool {
		return items[i].EvidenceID < items[j].EvidenceID
	})

	// Compute ViewHash over the sorted items
	viewHash := computeViewHash(nodeID, attempt, items)

	return EvidenceView{
		NodeID:   nodeID,
		Attempt:  attempt,
		Items:    items,
		ViewHash: viewHash,
	}
}

// deterministicEvidenceID computes a stable ID for an evidence item.
// The ID is sha256 of the canonical content fields, so the same content
// always produces the same ID regardless of source ordering.
func deterministicEvidenceID(item EvidenceViewItem) string {
	h := sha256.New()
	h.Write([]byte(item.ClaimOrTopic))
	h.Write([]byte{0})
	h.Write([]byte(item.SourceRef))
	h.Write([]byte{0})
	h.Write([]byte(item.ContentHash))
	h.Write([]byte{0})
	h.Write([]byte(item.ContradictionStatus))
	h.Write([]byte{0})
	h.Write([]byte(item.SupportStrength))
	return "ev_" + hex.EncodeToString(h.Sum(nil))
}

// computeViewHash computes the SHA256 hash over all sorted evidence item IDs
// and their content hashes, ensuring the view is tamper-evident.
func computeViewHash(nodeID string, attempt int, items []EvidenceViewItem) string {
	h := sha256.New()
	h.Write([]byte(nodeID))
	h.Write([]byte{0})
	// Encode attempt as a decimal string for stable hashing
	attemptStr := formatAttempt(attempt)
	h.Write([]byte(attemptStr))
	h.Write([]byte{0})
	for _, item := range items {
		h.Write([]byte(item.EvidenceID))
		h.Write([]byte{0})
		h.Write([]byte(item.ContentHash))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func formatAttempt(attempt int) string {
	if attempt < 10 {
		return string(rune('0' + attempt))
	}
	// For attempts >= 10, use simple decimal conversion
	return fmt.Sprintf("%d", attempt)
}
