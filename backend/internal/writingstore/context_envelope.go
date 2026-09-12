package writingstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/contextcompiler"
)

// ContextEnvelopeRecord is one persisted compiled envelope: the hash pins the
// exact bytes the model saw, the metadata records what the compiler had to
// trim or could not supply.
type ContextEnvelopeRecord struct {
	EnvelopeID      string                       `json:"envelope_id"`
	RunID           string                       `json:"run_id"`
	NodeID          string                       `json:"node_id"`
	Attempt         int                          `json:"attempt"`
	ProjectID       string                       `json:"project_id,omitempty"`
	CompilerVersion int                          `json:"compiler_version"`
	EnvelopeHash    string                       `json:"envelope_hash"`
	Payload         json.RawMessage              `json:"payload"`
	Missing         []contextcompiler.Missing    `json:"missing,omitempty"`
	Trimmed         []contextcompiler.Trimmed    `json:"trimmed,omitempty"`
	Diagnostics     []contextcompiler.Diagnostic `json:"diagnostics,omitempty"`
	CreatedAt       time.Time                    `json:"created_at"`
}

// SaveContextEnvelope persists one compiled envelope. The (run, node,
// attempt, hash) unique index makes re-saving the same compilation a no-op:
// replaying a node attempt cannot duplicate envelopes, but a genuinely
// different compilation for the same attempt (a compiler version bump or
// different inputs) is retained as a separate row.
func (s *Store) SaveContextEnvelope(ctx context.Context, record ContextEnvelopeRecord) error {
	if err := validateID(record.EnvelopeID, "env_", "envelope_id"); err != nil {
		return err
	}
	if err := validateID(record.RunID, "run_", "run_id"); err != nil {
		return err
	}
	if strings.TrimSpace(record.NodeID) == "" || record.Attempt < 1 {
		return fmt.Errorf("%w: node id and positive attempt are required", ErrInvalidRecord)
	}
	if err := validateHash(record.EnvelopeHash, "envelope_hash"); err != nil {
		return err
	}
	if record.CompilerVersion < 1 {
		return fmt.Errorf("%w: compiler version is required", ErrInvalidRecord)
	}
	if len(record.Payload) == 0 {
		return fmt.Errorf("%w: payload is required", ErrInvalidRecord)
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	var projectID any
	if record.ProjectID != "" {
		projectID = record.ProjectID
	}
	missing, err := json.Marshal(record.Missing)
	if err != nil {
		return err
	}
	trimmed, err := json.Marshal(record.Trimmed)
	if err != nil {
		return err
	}
	diagnostics, err := json.Marshal(record.Diagnostics)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO project_context_envelopes
		(envelope_id, run_id, node_id, attempt, project_id, compiler_version, envelope_hash, payload, missing, trimmed, diagnostics, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,
		 COALESCE($9::jsonb, '[]'::jsonb),
		 COALESCE($10::jsonb, '[]'::jsonb),
		 COALESCE($11::jsonb, '[]'::jsonb),
		 $12)
		ON CONFLICT (run_id, node_id, attempt, envelope_hash) DO NOTHING
	`, record.EnvelopeID, record.RunID, record.NodeID, record.Attempt, projectID,
		record.CompilerVersion, record.EnvelopeHash, []byte(record.Payload),
		jsonbArrayArg(missing), jsonbArrayArg(trimmed), jsonbArrayArg(diagnostics), record.CreatedAt)
	if err != nil {
		return fmt.Errorf("save context envelope: %w", err)
	}
	return nil
}

// jsonbArrayArg passes an encoded array as SQL NULL when it marshaled to
// null, so the column's COALESCE can normalize it to an empty JSON array —
// a jsonb null is valid jsonb and would defeat COALESCE.
func jsonbArrayArg(encoded []byte) any {
	if len(encoded) == 0 || string(encoded) == "null" {
		return nil
	}
	return encoded
}

// ListContextEnvelopes returns the envelopes of one node attempt, newest
// first.
func (s *Store) ListContextEnvelopes(ctx context.Context, runID, nodeID string, attempt int) ([]ContextEnvelopeRecord, error) {
	if err := validateID(runID, "run_", "run_id"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(nodeID) == "" || attempt < 1 {
		return nil, fmt.Errorf("%w: node id and positive attempt are required", ErrInvalidRecord)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT envelope_id, run_id, node_id, attempt, COALESCE(project_id,''), compiler_version, envelope_hash,
		 payload, missing, trimmed, diagnostics, created_at
		FROM project_context_envelopes
		WHERE run_id=$1 AND node_id=$2 AND attempt=$3
		ORDER BY created_at DESC, envelope_id
	`, runID, nodeID, attempt)
	if err != nil {
		return nil, fmt.Errorf("list context envelopes: %w", err)
	}
	defer rows.Close()
	envelopes := []ContextEnvelopeRecord{}
	for rows.Next() {
		var record ContextEnvelopeRecord
		var projectID sql.NullString
		var missing, trimmed, diagnostics []byte
		if err := rows.Scan(&record.EnvelopeID, &record.RunID, &record.NodeID, &record.Attempt, &projectID,
			&record.CompilerVersion, &record.EnvelopeHash, &record.Payload, &missing, &trimmed,
			&diagnostics, &record.CreatedAt); err != nil {
			return nil, err
		}
		record.ProjectID = projectID.String
		if err := json.Unmarshal(missing, &record.Missing); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(trimmed, &record.Trimmed); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(diagnostics, &record.Diagnostics); err != nil {
			return nil, err
		}
		envelopes = append(envelopes, record)
	}
	return envelopes, rows.Err()
}

// GetContextEnvelope loads one envelope by id.
func (s *Store) GetContextEnvelope(ctx context.Context, envelopeID string) (ContextEnvelopeRecord, error) {
	var record ContextEnvelopeRecord
	var projectID sql.NullString
	var missing, trimmed, diagnostics []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT envelope_id, run_id, node_id, attempt, COALESCE(project_id,''), compiler_version, envelope_hash,
		 payload, missing, trimmed, diagnostics, created_at
		FROM project_context_envelopes WHERE envelope_id=$1
	`, envelopeID).Scan(&record.EnvelopeID, &record.RunID, &record.NodeID, &record.Attempt, &projectID,
		&record.CompilerVersion, &record.EnvelopeHash, &record.Payload, &missing, &trimmed,
		&diagnostics, &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextEnvelopeRecord{}, ErrNotFound
	}
	if err != nil {
		return ContextEnvelopeRecord{}, fmt.Errorf("get context envelope: %w", err)
	}
	record.ProjectID = projectID.String
	if err := json.Unmarshal(missing, &record.Missing); err != nil {
		return ContextEnvelopeRecord{}, err
	}
	if err := json.Unmarshal(trimmed, &record.Trimmed); err != nil {
		return ContextEnvelopeRecord{}, err
	}
	if err := json.Unmarshal(diagnostics, &record.Diagnostics); err != nil {
		return ContextEnvelopeRecord{}, err
	}
	return record, nil
}
