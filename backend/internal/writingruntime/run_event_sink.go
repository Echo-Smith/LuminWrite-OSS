package writingruntime

import (
	"context"
	"log/slog"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// RunEventLedger is the store surface the event sink needs.
// *writingstore.Store implements it.
type RunEventLedger interface {
	AppendRunEvent(ctx context.Context, event writingstore.RunEvent) (writingstore.RunEvent, error)
}

// RunEventSink writes streaming events to the run event ledger.
type RunEventSink struct {
	ledger RunEventLedger
	runID  string
	trace  writingstore.TraceContext
}

// NewRunEventSink creates a sink bound to a specific run.
func NewRunEventSink(ledger RunEventLedger, runID string, trace writingstore.TraceContext) *RunEventSink {
	return &RunEventSink{ledger: ledger, runID: runID, trace: trace}
}

func (s *RunEventSink) EmitContentDelta(nodeID string, attempt int, delta string) {
	s.appendEvent("writing.content.delta", nodeID, attempt, map[string]any{
		"delta": delta,
	})
}

func (s *RunEventSink) EmitContentDone(nodeID string, attempt int, finalText string) {
	s.appendEvent("writing.content.done", nodeID, attempt, map[string]any{
		"final_text": finalText,
	})
}

func (s *RunEventSink) EmitReasoningDelta(nodeID string, attempt int, delta string) {
	s.appendEvent("writing.reasoning.delta", nodeID, attempt, map[string]any{
		"delta": delta,
	})
}

func (s *RunEventSink) EmitNodeProgress(nodeID string, attempt int, stepName, status string) {
	s.appendEvent("writing.node.progress", nodeID, attempt, map[string]any{
		"step_name": stepName,
		"status":    status,
	})
}

func (s *RunEventSink) appendEvent(eventType, nodeID string, attempt int, payload map[string]any) {
	ctx := context.Background()
	_, err := s.ledger.AppendRunEvent(ctx, writingstore.RunEvent{
		RunID:       s.runID,
		EventType:   eventType,
		NodeID:      nodeID,
		Attempt:     attempt,
		EntityKind:  "node",
		EntityID:    nodeID,
		Payload:     payload,
		Trace:       s.trace,
	})
	if err != nil {
		slog.Warn("event sink: failed to append event", "type", eventType, "node_id", nodeID, "error", err)
	}
}
