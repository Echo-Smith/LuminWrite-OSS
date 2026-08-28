package server

import (
	"fmt"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/websocket"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

func adaptWritingRunEvent(event writingstore.RunEvent, runStatus string) (websocket.WritingEvent, error) {
	result := websocket.WritingEvent{Protocol: websocket.WritingProtocolV2, RunID: event.RunID, Sequence: event.Sequence, Timestamp: event.OccurredAt, Status: runStatus}
	switch event.EventType {
	case "run.planned", "run.started", "run.paused", "run.resumed", "run.cancelled", "run.completed", "run.failed", "run.transitioned", "run.transition_rejected":
		result.Type = websocket.MsgWritingRunStatus
		result.Payload = websocket.WritingRunStatusPayload{From: stringValue(event.Payload, "actual_from"), To: firstNonblank(stringValue(event.Payload, "effective_state"), stringValue(event.Payload, "to"), runStatus), ReasonCode: stringValue(event.Payload, "reason_code")}
	case "node.started", "node.completed", "node.failed", "node.paused", "node.cancelled":
		result.Type = websocket.MsgWritingNodeStatus
		result.Payload = websocket.WritingNodeStatusPayload{NodeID: event.NodeID, Attempt: event.Attempt, Status: firstNonblank(stringValue(event.Payload, "status"), eventStatus(event.EventType))}
	case "artifact.created":
		result.Type = websocket.MsgWritingArtifactCreated
		result.Payload = websocket.WritingArtifactPayload{ArtifactID: event.EntityID, ArtifactType: stringValue(event.Payload, "artifact_type"), ContentHash: stringValue(event.Payload, "content_hash"), Lifecycle: firstNonblank(stringValue(event.Payload, "lifecycle"), "provisional")}
	case "quality.updated":
		result.Type = websocket.MsgWritingQualityUpdated
		result.Payload = websocket.WritingQualityPayload{ReportID: event.EntityID, QualityState: stringValue(event.Payload, "quality_state"), AchievedAssurance: stringValue(event.Payload, "achieved_assurance")}
	case "document.committed":
		result.Type = websocket.MsgWritingDocumentCommitted
		result.Payload = websocket.WritingDocumentCommittedPayload{DocumentID: stringValue(event.Payload, "document_id"), VersionID: event.EntityID, ContentHash: stringValue(event.Payload, "content_hash"), QualityState: stringValue(event.Payload, "quality_state"), Lifecycle: "committed"}
	default:
		result.Type = websocket.MsgWritingLedgerEvent
		result.Payload = websocket.WritingLedgerPayload{EventType: event.EventType, EntityKind: event.EntityKind, EntityID: event.EntityID, Data: event.Payload}
	}
	if err := result.Validate(); err != nil {
		return websocket.WritingEvent{}, fmt.Errorf("adapt writing event %s: %w", event.EventID, err)
	}
	return result, nil
}

func newProvisionalDocumentDelta(runID, documentID, blockID, delta, status string, sequence int64, occurredAt time.Time) websocket.WritingEvent {
	return websocket.WritingEvent{Protocol: websocket.WritingProtocolV2, Type: websocket.MsgWritingDocumentDelta, RunID: runID, Sequence: sequence, Timestamp: occurredAt.UTC(), Status: status, Payload: websocket.WritingDocumentDeltaPayload{DocumentID: documentID, BlockID: blockID, Delta: delta, Lifecycle: "provisional"}}
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
