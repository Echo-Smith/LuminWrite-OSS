// Initial artifact persistence (V3.0 M1.0 wiring): the orchestrator's first
// dispatch captures the run's initial artifacts and must persist them as real
// artifact rows — downstream node outputs record lineage edges to them, and
// the artifact foreign keys require the rows. The initial capture is recorded
// as a synthetic "initial" node attempt so the attempt FK binds; the attempt
// is then completed through the standard path so it never looks in-flight.
package writingstore

import (
	"context"
	"fmt"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

// SaveInitialArtifacts persists the run's initial artifact rows idempotently,
// binding them to a synthetic succeeded "initial" node attempt.
func (s *Store) SaveInitialArtifacts(ctx context.Context, artifacts []ArtifactRecord) error {
	if len(artifacts) == 0 {
		return nil
	}
	first := artifacts[0]
	// 1. Ensure the synthetic initial attempt exists (pending), inside one
	// transaction with the artifact rows so the attempt FK resolves.
	if err := s.InTransaction(ctx, func(tx *Tx) error {
		if _, _, err := tx.EnsureNodeAttempt(ctx, NodeAttempt{RunID: first.RunID,
			PlanID: first.PlanID, PlanVersion: first.PlanVersion, NodeID: "initial",
			Attempt: 1, NodeKind: writingplan.NodeAction, CapabilityID: "kernel.initial.capture",
			CapabilityVersion: "initial", ExecutorID: "kernel.initial.capture",
			Status: "pending", FailurePath: writingplan.FailurePause,
			Bounds:    writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 0, TimeoutMS: 60000},
			InputHash: "sha256:" + "0000000000000000000000000000000000000000000000000000000000000000"}); err != nil {
			return fmt.Errorf("ensure initial attempt: %w", err)
		}
		for _, artifact := range artifacts {
			if err := tx.PutArtifact(ctx, artifact); err != nil {
				return fmt.Errorf("put initial artifact: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	// 2. Complete the synthetic attempt through the standard path (idempotent
	// when already succeeded): completed_at + node.completed event.
	return s.CompleteNodeAttempt(ctx, AttemptCompletion{RunID: first.RunID,
		NodeID: "initial", Attempt: 1, Status: "succeeded",
		Trace: TraceContext{Provenance: map[string]any{"initial": true},
			SourceRefs: []string{}, Actor: Actor{Type: ActorSystem, ID: "writingruntime.initial"}}})
}
