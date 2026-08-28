package websocket

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDocumentDeltaCanNeverClaimCommittedOrVerified(t *testing.T) {
	event := WritingEvent{Protocol: WritingProtocolV2, Type: MsgWritingDocumentDelta,
		RunID: "run_test", Sequence: 7, Timestamp: time.Now().UTC(), Status: "running",
		Payload: WritingDocumentDeltaPayload{DocumentID: "doc_test", BlockID: "blk_intro", Delta: "draft text", Lifecycle: "provisional"}}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(payload)
	if strings.Contains(wire, `"committed"`) || strings.Contains(wire, `"verified"`) {
		t.Fatalf("provisional delta leaked a terminal claim: %s", wire)
	}
	event.Payload = WritingDocumentDeltaPayload{DocumentID: "doc_test", Delta: "draft text", Lifecycle: "committed"}
	if err := event.Validate(); err == nil {
		t.Fatal("delta accepted a committed lifecycle")
	}
}

func TestCommittedDocumentRequiresSeparateTypedEvent(t *testing.T) {
	event := WritingEvent{Protocol: WritingProtocolV2, Type: MsgWritingDocumentCommitted,
		RunID: "run_test", Sequence: 8, Timestamp: time.Now().UTC(), Status: "completed",
		Payload: WritingDocumentCommittedPayload{DocumentID: "doc_test", VersionID: "ver_2",
			ContentHash:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			QualityState: "verified_deliverable", Lifecycle: "committed"}}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWritingEventRequiresDurableSequence(t *testing.T) {
	event := WritingEvent{Protocol: WritingProtocolV2, Type: MsgWritingRunStatus,
		RunID: "run_test", Timestamp: time.Now().UTC(), Status: "running",
		Payload: WritingRunStatusPayload{To: "running"}}
	if err := event.Validate(); err == nil {
		t.Fatal("event without durable sequence was accepted")
	}
}
