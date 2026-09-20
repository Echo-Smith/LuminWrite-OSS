package writingruntime

// EventSink receives real-time execution events from engine steps
// and forwards them to the run event ledger for SSE delivery.
type EventSink interface {
	EmitContentDelta(nodeID string, attempt int, delta string)
	EmitContentDone(nodeID string, attempt int, finalText string)
	EmitReasoningDelta(nodeID string, attempt int, delta string)
	EmitNodeProgress(nodeID string, attempt int, stepName, status string)
}

// NoopEventSink silently discards all events (used when no sink is configured).
type NoopEventSink struct{}

func (NoopEventSink) EmitContentDelta(string, int, string)          {}
func (NoopEventSink) EmitContentDone(string, int, string)           {}
func (NoopEventSink) EmitReasoningDelta(string, int, string)        {}
func (NoopEventSink) EmitNodeProgress(string, int, string, string)  {}
