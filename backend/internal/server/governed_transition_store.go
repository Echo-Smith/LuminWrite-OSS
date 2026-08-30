package server

import (
	"context"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// governedTransitionStore adapts writingstore.RecordRunTransition to the
// runtime state machine's TransitionStore. State-machine transitions become
// run.transitioned / run.transition_rejected events in the append-only
// RunLedger and keep the writing_runs projection in sync.
type governedTransitionStore struct {
	store *writingstore.Store
}

func (adapter governedTransitionStore) RecordTransition(ctx context.Context, record writingruntime.TransitionRecord) error {
	_, err := adapter.store.RecordRunTransition(ctx, writingstore.RunTransitionCommand{
		RunID: record.RunID, ExpectedFrom: string(record.From), RequestedTo: string(record.To),
		RuleAccepted: record.Accepted, Cause: record.Cause, ReasonCode: record.ReasonCode,
		Summary: record.Summary, IdempotencyKey: record.IdempotencyKey,
		Trace: writingstore.TraceContext{Provenance: map[string]any{"runtime": "governed", "transition": string(record.To)},
			SourceRefs: []string{}, Actor: record.Actor},
		OccurredAt: record.OccurredAt,
	})
	return err
}
