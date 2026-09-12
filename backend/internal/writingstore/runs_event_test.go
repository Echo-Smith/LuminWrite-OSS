package writingstore

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestStoreAppendRunEventProjection verifies the store-level event append the
// V3.0 transition recorder relies on: it advances the last_event_sequence
// projection, dedups by idempotency key, and rejects malformed transition
// events. Uses the real database (skipped without TEST_DATABASE_URL).
func TestStoreAppendRunEventProjection(t *testing.T) {
	if integrationDB == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}
	store, fixture := newIntegrationFixture(t, true)
	ctx := context.Background()
	now := time.Now().UTC()

	event := RunEvent{
		EventID: StableID("evt_", fixture.runID+":transition:cmd-1"), RunID: fixture.runID,
		EventType: "run.transitioned", OccurredAt: now, IdempotencyKey: fixture.runID + ":transition:cmd-1",
		EntityKind: "run", EntityID: fixture.runID,
		Payload: map[string]any{"from": "running", "to": "completed", "accepted": true},
		Trace:   TraceContext{Actor: Actor{Type: ActorPolicy, ID: "test"}, Provenance: map[string]any{}, SourceRefs: []string{}},
	}
	appended, err := store.AppendRunEvent(ctx, event)
	if err != nil {
		t.Fatalf("append transition event: %v", err)
	}
	if appended.Sequence < 1 {
		t.Fatalf("event sequence %d not advanced", appended.Sequence)
	}
	// Projection must equal the ledger max (the deferred constraint trigger
	// would have failed the commit otherwise); re-read to confirm.
	var lastSeq int64
	if err := integrationDB.QueryRowContext(ctx, `SELECT last_event_sequence FROM writing_runs WHERE run_id=$1`, fixture.runID).Scan(&lastSeq); err != nil {
		t.Fatal(err)
	}
	if lastSeq != appended.Sequence {
		t.Fatalf("projection %d != event sequence %d", lastSeq, appended.Sequence)
	}

	// Replay with the same idempotency key is a no-op returning the original.
	replay, err := store.AppendRunEvent(ctx, event)
	if err != nil {
		t.Fatalf("replay errored: %v", err)
	}
	if replay.EventID != appended.EventID || replay.Sequence != appended.Sequence {
		t.Fatalf("replay created a new event: %s/%d vs %s/%d", replay.EventID, replay.Sequence, appended.EventID, appended.Sequence)
	}

	// A transition event must not carry node identity.
	bad := event
	bad.EventID = StableID("evt_", fixture.runID+":transition:cmd-bad")
	bad.IdempotencyKey = fixture.runID + ":transition:cmd-bad"
	bad.NodeID = "node_draft"
	if _, err := store.AppendRunEvent(ctx, bad); err == nil || !strings.Contains(err.Error(), "transition event requires command idempotency only") {
		t.Fatalf("node-scoped transition accepted: %v", err)
	}
}
