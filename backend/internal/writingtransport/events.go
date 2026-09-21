// Package writingtransport holds the transport view types of the governed
// writing runtime: the durable event envelope shared by the SSE stream and
// API clients, its typed payloads, and the material-reference request type.
// These types were originally declared in internal/websocket; when the
// legacy WebSocket transport exited the architecture they moved here so the
// governed runtime carries no dependency on a deleted transport package.
package writingtransport

import (
	"errors"
	"strings"
	"time"
)

const WritingProtocolV2 = "lumin-writing.v2"

const (
	MsgWritingRunStatus         = "writing.run.status"
	MsgWritingDocumentDelta     = "writing.document.delta"
	MsgWritingDocumentCommitted = "writing.document.committed"
	MsgWritingNodeStatus        = "writing.node.status"
	MsgWritingArtifactCreated   = "writing.artifact.created"
	MsgWritingQualityUpdated    = "writing.quality.updated"
	MsgWritingLedgerEvent       = "writing.ledger.event"

	// Streaming content events: real-time text/reasoning deltas from engine steps.
	MsgWritingContentDelta   = "writing.content.delta"
	MsgWritingContentDone    = "writing.content.done"
	MsgWritingReasoningDelta = "writing.reasoning.delta"
	MsgWritingNodeProgress   = "writing.node.progress"
)

// WritingEvent is the governed event envelope carried by the SSE stream.
// Sequence is the durable run-ledger position; clients resume from it rather
// than inferring state from transport delivery order.
type WritingEvent struct {
	Protocol  string      `json:"protocol"`
	Type      string      `json:"type"`
	RunID     string      `json:"run_id"`
	Sequence  int64       `json:"sequence"`
	Timestamp time.Time   `json:"timestamp"`
	Status    string      `json:"status"`
	Payload   interface{} `json:"payload"`
}

type WritingRunStatusPayload struct {
	From       string `json:"from,omitempty"`
	To         string `json:"to"`
	ReasonCode string `json:"reason_code,omitempty"`
}

type WritingDocumentDeltaPayload struct {
	DocumentID string `json:"document_id"`
	BlockID    string `json:"block_id,omitempty"`
	Delta      string `json:"delta"`
	Lifecycle  string `json:"lifecycle"`
}

type WritingDocumentCommittedPayload struct {
	DocumentID   string `json:"document_id"`
	VersionID    string `json:"version_id"`
	ContentHash  string `json:"content_hash"`
	QualityState string `json:"quality_state"`
	Lifecycle    string `json:"lifecycle"`
}

type WritingNodeStatusPayload struct {
	NodeID  string `json:"node_id"`
	Attempt int    `json:"attempt"`
	Status  string `json:"status"`
}

type WritingArtifactPayload struct {
	ArtifactID   string `json:"artifact_id"`
	ArtifactType string `json:"artifact_type"`
	ContentHash  string `json:"content_hash"`
	Lifecycle    string `json:"lifecycle"`
}

type WritingQualityPayload struct {
	ReportID          string `json:"report_id"`
	QualityState      string `json:"quality_state"`
	AchievedAssurance string `json:"achieved_assurance"`
}

type WritingLedgerPayload struct {
	EventType  string         `json:"event_type"`
	EntityKind string         `json:"entity_kind"`
	EntityID   string         `json:"entity_id"`
	Data       map[string]any `json:"data"`
}

// WritingContentDeltaPayload carries a streaming text delta from an engine step.
type WritingContentDeltaPayload struct {
	NodeID      string `json:"node_id"`
	Attempt     int    `json:"attempt"`
	Delta       string `json:"delta"`
	Accumulated string `json:"accumulated,omitempty"`
}

// WritingContentDonePayload signals that streaming content is complete.
type WritingContentDonePayload struct {
	NodeID    string `json:"node_id"`
	Attempt   int    `json:"attempt"`
	FinalText string `json:"final_text"`
}

// WritingReasoningDeltaPayload carries a reasoning chain delta.
type WritingReasoningDeltaPayload struct {
	NodeID  string `json:"node_id"`
	Attempt int    `json:"attempt"`
	Delta   string `json:"delta"`
}

// WritingNodeProgressPayload reports step start/complete within a node.
type WritingNodeProgressPayload struct {
	NodeID   string `json:"node_id"`
	Attempt  int    `json:"attempt"`
	StepName string `json:"step_name"`
	Status   string `json:"status"`
}

func (event WritingEvent) Validate() error {
	if event.Protocol != WritingProtocolV2 || !strings.HasPrefix(event.RunID, "run_") || event.Sequence < 1 || event.Timestamp.IsZero() || strings.TrimSpace(event.Status) == "" || event.Payload == nil {
		return errors.New("invalid governed writing event envelope")
	}
	switch payload := event.Payload.(type) {
	case WritingDocumentDeltaPayload:
		if event.Type != MsgWritingDocumentDelta || payload.Lifecycle != "provisional" || !strings.HasPrefix(payload.DocumentID, "doc_") || payload.Delta == "" {
			return errors.New("document delta must remain provisional")
		}
	case WritingDocumentCommittedPayload:
		if event.Type != MsgWritingDocumentCommitted || payload.Lifecycle != "committed" || !strings.HasPrefix(payload.DocumentID, "doc_") || !strings.HasPrefix(payload.VersionID, "ver_") || !strings.HasPrefix(payload.ContentHash, "sha256:") || payload.QualityState == "" {
			return errors.New("invalid committed document event")
		}
	case WritingRunStatusPayload:
		if event.Type != MsgWritingRunStatus || payload.To == "" {
			return errors.New("invalid run status event")
		}
	case WritingNodeStatusPayload:
		if event.Type != MsgWritingNodeStatus || payload.NodeID == "" || payload.Attempt < 1 || payload.Status == "" {
			return errors.New("invalid node status event")
		}
	case WritingArtifactPayload:
		if event.Type != MsgWritingArtifactCreated || payload.ArtifactID == "" || payload.ArtifactType == "" || payload.ContentHash == "" || payload.Lifecycle == "" {
			return errors.New("invalid artifact event")
		}
	case WritingQualityPayload:
		if event.Type != MsgWritingQualityUpdated || payload.ReportID == "" || payload.QualityState == "" || payload.AchievedAssurance == "" {
			return errors.New("invalid quality event")
		}
	case WritingLedgerPayload:
		if event.Type != MsgWritingLedgerEvent || payload.EventType == "" || payload.EntityKind == "" || payload.EntityID == "" || payload.Data == nil {
			return errors.New("invalid ledger event")
		}
	case WritingContentDeltaPayload:
		if event.Type != MsgWritingContentDelta || payload.NodeID == "" || payload.Attempt < 1 || payload.Delta == "" {
			return errors.New("invalid content delta event")
		}
	case WritingContentDonePayload:
		if event.Type != MsgWritingContentDone || payload.NodeID == "" || payload.Attempt < 1 {
			return errors.New("invalid content done event")
		}
	case WritingReasoningDeltaPayload:
		if event.Type != MsgWritingReasoningDelta || payload.NodeID == "" || payload.Attempt < 1 || payload.Delta == "" {
			return errors.New("invalid reasoning delta event")
		}
	case WritingNodeProgressPayload:
		if event.Type != MsgWritingNodeProgress || payload.NodeID == "" || payload.Attempt < 1 || payload.StepName == "" || payload.Status == "" {
			return errors.New("invalid node progress event")
		}
	default:
		return errors.New("unsupported governed writing event payload")
	}
	return nil
}

// MaterialReference names a user material attached to a run-creation request.
type MaterialReference struct {
	MaterialID string `json:"material_id"`
	SourceRef  string `json:"source_ref"`
	Title      string `json:"title,omitempty"`
}
