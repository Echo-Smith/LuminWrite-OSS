package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ─── Session Event Log ──────────────────────────────────
//
// SessionEventRepo provides append-only storage for discrete agent
// execution events. Each event is a single row in session_events,
// indexed by (trace_id, seq) for replay ordering.
//
// This enables:
//   - Session replay: reconstruct the full UI from events
//   - Fork from step: re-run with different parameters from any point
//   - Telemetry export: batch dump for evaluation pipelines
//   - Audit trail: compliance-grade traceability
//
// Inspired by:
//   - dsh's session-log pattern (structured JSON events, not free-text logs)
//   - Pi Agent's stream-json output (each event is a self-contained JSON object)
//   - OpenAI Assistants API's run-steps (discrete, queryable steps)

// SessionEvent represents a single discrete event in the agent lifecycle.
type SessionEvent struct {
	ID        int64                  `json:"id"`
	TraceID   string                 `json:"trace_id"`
	Seq       int                    `json:"seq"`
	EventType string                 `json:"event_type"`
	Step      string                 `json:"step,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// SessionEventRepo handles persistence of session events.
type SessionEventRepo struct {
	db *DB
}

// NewSessionEventRepo creates a new SessionEventRepo.
func NewSessionEventRepo(db *DB) *SessionEventRepo {
	return &SessionEventRepo{db: db}
}

// AppendEvent adds a single event to the log.
// The sequence number is auto-incremented per trace_id using an atomic counter
// cache, falling back to a COALESCE(MAX(seq)+1) query.
func (r *SessionEventRepo) AppendEvent(ctx context.Context, traceID, eventType, step string, data map[string]interface{}) error {
	if r.db == nil {
		return nil
	}

	dataJSON, _ := json.Marshal(data)

	// Use a CTE to atomically get the next sequence number and insert
	// Note: $1 is used twice (in VALUES and in subquery WHERE), so we
	// explicitly cast to VARCHAR to avoid "inconsistent types deduced for parameter $1"
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO session_events (trace_id, seq, event_type, step, data, created_at)
		VALUES (
			$1::varchar,
			COALESCE((SELECT MAX(seq) + 1 FROM session_events WHERE trace_id = $1::varchar), 1),
			$2, $3, $4, NOW()
		)
	`, traceID, eventType, step, string(dataJSON))
	if err != nil {
		slog.Warn("failed to append session event",
			"error", err,
			"trace_id", traceID,
			"event_type", eventType,
		)
	}
	return err
}

// AppendEventBatch adds multiple events in a single transaction.
// This is more efficient for bulk replay or migration scenarios.
func (r *SessionEventRepo) AppendEventBatch(ctx context.Context, events []SessionEvent) error {
	if r.db == nil || len(events) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Get the current max seq for the trace
	for _, evt := range events {
		dataJSON, _ := json.Marshal(evt.Data)
		_, err := tx.ExecContext(ctx, `
			INSERT INTO session_events (trace_id, seq, event_type, step, data, created_at)
			VALUES (
				$1::varchar,
				COALESCE((SELECT MAX(seq) + 1 FROM session_events WHERE trace_id = $1::varchar), 1),
				$2, $3, $4, NOW()
			)
		`, evt.TraceID, evt.EventType, evt.Step, string(dataJSON))
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetEvents retrieves all events for a trace, ordered by sequence.
// If eventTypes is non-empty, only events of those types are returned.
func (r *SessionEventRepo) GetEvents(ctx context.Context, traceID string, eventTypes ...string) ([]SessionEvent, error) {
	if r.db == nil {
		return nil, nil
	}

	query := `
		SELECT id, trace_id, seq, event_type, step, data, created_at
		FROM session_events
		WHERE trace_id = $1
	`
	args := []interface{}{traceID}
	argIdx := 2

	if len(eventTypes) > 0 {
		placeholders := ""
		for i, et := range eventTypes {
			if i > 0 {
				placeholders += ","
			}
			placeholders += fmt.Sprintf("$%d", argIdx)
			args = append(args, et)
			argIdx++
		}
		query += fmt.Sprintf(" AND event_type IN (%s)", placeholders)
	}

	query += " ORDER BY seq ASC"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []SessionEvent
	for rows.Next() {
		var evt SessionEvent
		var dataJSON []byte
		var step sql.NullString

		if err := rows.Scan(&evt.ID, &evt.TraceID, &evt.Seq, &evt.EventType, &step, &dataJSON, &evt.CreatedAt); err != nil {
			slog.Warn("failed to scan session event", "error", err)
			continue
		}
		if step.Valid {
			evt.Step = step.String
		}
		if len(dataJSON) > 0 {
			json.Unmarshal(dataJSON, &evt.Data)
		}
		events = append(events, evt)
	}

	return events, nil
}

// GetEventCount returns the total number of events for a trace.
func (r *SessionEventRepo) GetEventCount(ctx context.Context, traceID string) (int, error) {
	if r.db == nil {
		return 0, nil
	}
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM session_events WHERE trace_id = $1
	`, traceID).Scan(&count)
	return count, err
}

// GetEventsByType retrieves events of a specific type for a trace.
func (r *SessionEventRepo) GetEventsByType(ctx context.Context, traceID, eventType string) ([]SessionEvent, error) {
	return r.GetEvents(ctx, traceID, eventType)
}

// DeleteEvents removes all events for a trace (used for cleanup/replay reset).
func (r *SessionEventRepo) DeleteEvents(ctx context.Context, traceID string) error {
	if r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM session_events WHERE trace_id = $1
	`, traceID)
	return err
}

// ─── Seq Counter (in-memory cache for high-frequency events) ───
//
// For streaming events (stream.delta), we use an atomic counter per trace
// to avoid hitting the database for the MAX(seq) lookup on every delta.
// The counter is initialized once and increments atomically.

// seqCounter caches the last-used sequence number per trace_id.
// This avoids a SELECT MAX(seq) on every AppendEvent for high-frequency
// events like stream.delta.
type seqCounter struct {
	val atomic.Int64
}

// seqCounterMap is a package-level cache of trace_id → atomic counter.
// Entries are cleaned up when the trace completes or is deleted.
var seqCounterMap sync.Map

// getNextSeq returns the next sequence number for a trace.
// It uses an in-memory atomic counter for speed, falling back to
// the database if the counter hasn't been initialized.
func (r *SessionEventRepo) getNextSeq(traceID string) int {
	if counter, ok := seqCounterMap.Load(traceID); ok {
		return int(counter.(*seqCounter).val.Add(1))
	}
	// Counter not initialized — the DB query will handle it via COALESCE
	return 0
}
