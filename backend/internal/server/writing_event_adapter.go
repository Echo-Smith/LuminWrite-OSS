package server

import (
	"fmt"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingtransport"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func adaptWritingRunEvent(event writingstore.RunEvent, runStatus string) (writingtransport.WritingEvent, error) {
	result := writingtransport.WritingEvent{Protocol: writingtransport.WritingProtocolV2, RunID: event.RunID, Sequence: event.Sequence, Timestamp: event.OccurredAt, Status: runStatus}
	switch event.EventType {
	case "run.planned", "run.started", "run.paused", "run.resumed", "run.cancelled", "run.completed", "run.failed", "run.transitioned", "run.transition_rejected":
		result.Type = writingtransport.MsgWritingRunStatus
		result.Payload = writingtransport.WritingRunStatusPayload{From: stringValue(event.Payload, "actual_from"), To: firstNonblank(stringValue(event.Payload, "effective_state"), stringValue(event.Payload, "to"), runStatus), ReasonCode: stringValue(event.Payload, "reason_code")}
	case "node.started", "node.completed", "node.failed", "node.paused", "node.cancelled":
		result.Type = writingtransport.MsgWritingNodeStatus
		result.Payload = writingtransport.WritingNodeStatusPayload{NodeID: event.NodeID, Attempt: event.Attempt, Status: firstNonblank(stringValue(event.Payload, "status"), eventStatus(event.EventType))}
	case "artifact.created":
		result.Type = writingtransport.MsgWritingArtifactCreated
		result.Payload = writingtransport.WritingArtifactPayload{ArtifactID: event.EntityID, ArtifactType: stringValue(event.Payload, "artifact_type"), ContentHash: stringValue(event.Payload, "content_hash"), Lifecycle: firstNonblank(stringValue(event.Payload, "lifecycle"), "provisional")}
	case "quality.updated":
		result.Type = writingtransport.MsgWritingQualityUpdated
		result.Payload = writingtransport.WritingQualityPayload{ReportID: event.EntityID, QualityState: stringValue(event.Payload, "quality_state"), AchievedAssurance: stringValue(event.Payload, "achieved_assurance")}
	case "document.committed":
		result.Type = writingtransport.MsgWritingDocumentCommitted
		result.Payload = writingtransport.WritingDocumentCommittedPayload{DocumentID: stringValue(event.Payload, "document_id"), VersionID: event.EntityID, ContentHash: stringValue(event.Payload, "content_hash"), QualityState: stringValue(event.Payload, "quality_state"), Lifecycle: "committed"}
	default:
		result.Type = writingtransport.MsgWritingLedgerEvent
		result.Payload = writingtransport.WritingLedgerPayload{EventType: event.EventType, EntityKind: event.EntityKind, EntityID: event.EntityID, Data: event.Payload}
	}
	if err := result.Validate(); err != nil {
		return writingtransport.WritingEvent{}, fmt.Errorf("adapt writing event %s: %w", event.EventID, err)
	}
	return result, nil
}

func newProvisionalDocumentDelta(runID, documentID, blockID, delta, status string, sequence int64, occurredAt time.Time) writingtransport.WritingEvent {
	return writingtransport.WritingEvent{Protocol: writingtransport.WritingProtocolV2, Type: writingtransport.MsgWritingDocumentDelta, RunID: runID, Sequence: sequence, Timestamp: occurredAt.UTC(), Status: status, Payload: writingtransport.WritingDocumentDeltaPayload{DocumentID: documentID, BlockID: blockID, Delta: delta, Lifecycle: "provisional"}}
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func firstNonblank(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func eventStatus(eventType string) string {
	for index := len(eventType) - 1; index >= 0; index-- {
		if eventType[index] == '.' {
			return eventType[index+1:]
		}
	}
	return eventType
}
