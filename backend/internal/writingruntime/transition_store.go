// TransitionStore adapter for the governed runtime (V3.0 M0b-2a). The
// writingstore package cannot import writingruntime (writingruntime already
// depends on it), so the TransitionRecord→RunTransitionCommand mapping lives
// here. The recorder delegates to the store's RecordRunTransition, which owns
// the authoritative semantics: idempotent command ledger plus the
// writing_runs.status projection the HTTP layer reads (M1.4 — the earlier
// event-only recorder left the run row stale).
package writingruntime

import (
	"context"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// WritingStoreTransitionRecorder implements TransitionStore over a
// *writingstore.Store.
type WritingStoreTransitionRecorder struct {
	Store *writingstore.Store
}

// Compile-time proof the recorder satisfies the runtime's TransitionStore.
var _ TransitionStore = WritingStoreTransitionRecorder{}

// RecordTransition maps a TransitionRecord onto the store's transition
// command. Rejected transitions are recorded too (audit), matching the
// TransitionStore contract.
func (recorder WritingStoreTransitionRecorder) RecordTransition(ctx context.Context, record TransitionRecord) error {
	_, err := recorder.Store.RecordRunTransition(ctx, writingstore.RunTransitionCommand{
		RunID:          record.RunID,
		IdempotencyKey: record.IdempotencyKey,
		ExpectedFrom:   string(record.From),
		RequestedTo:    string(record.To),
		// The runtime state machine has already decided acceptance; the
		// store re-checks it against the live row (RuleAccepted AND
		// current==ExpectedFrom).
		RuleAccepted: record.Accepted,
		Cause:        record.Cause,
		ReasonCode:   record.ReasonCode,
		Summary:      record.Summary,
		OccurredAt:   record.OccurredAt,
		Trace:        writingstore.TraceContext{Actor: record.Actor, Provenance: map[string]any{}, SourceRefs: []string{}},
	})
	return err
}
