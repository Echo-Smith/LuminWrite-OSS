package writingruntime

import (
	"context"
	"errors"
	"fmt"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

var ErrTransitionProjectionConflict = errors.New("writingruntime: persisted run state differs from transition request")

// PersistentTransitionStore keeps the state ledger and run projection in the
// same writingstore transaction. It also fails closed on stale From values.
type PersistentTransitionStore struct{ Store *writingstore.Store }

func (adapter PersistentTransitionStore) RecordTransition(ctx context.Context, record TransitionRecord) error {
	if adapter.Store == nil {
		return writingstore.ErrNotFound
	}
	result, err := adapter.Store.RecordRunTransition(ctx, writingstore.RunTransitionCommand{
		RunID: record.RunID, IdempotencyKey: record.IdempotencyKey,
		ExpectedFrom: string(record.From), RequestedTo: string(record.To),
		RuleAccepted: record.Accepted, Cause: record.Cause, ReasonCode: record.ReasonCode,
		Summary: record.Summary, OccurredAt: record.OccurredAt,
		Trace: writingstore.TraceContext{Provenance: map[string]any{
			"command_id": record.CommandID, "cause": record.Cause,
		}, SourceRefs: []string{}, Actor: record.Actor},
	})
	if err != nil {
		return err
	}
	if result.Accepted != record.Accepted || result.ActualFrom != string(record.From) || result.EffectiveState != string(record.EffectiveState) {
		return fmt.Errorf("%w: actual=%s effective=%s", ErrTransitionProjectionConflict, result.ActualFrom, result.EffectiveState)
	}
	return nil
}
