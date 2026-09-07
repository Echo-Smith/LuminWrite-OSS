package writingstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Human gate persistence (design.md §5/§6). A gate row is created 'pending'
// when the orchestrator arrives at the gate node (in the same transaction as
// the waiting-gate checkpoint and the paused transition) and is flipped to
// 'approved' inside the atomic DecideGate transaction. The run stays paused:
// the approved decision row plus the succeeded gate attempt are the durable
// "resolved, awaiting resume" marker.

var (
	// ErrGateAlreadyDecided maps to 409 GATE_ALREADY_DECIDED: the gate has a
	// persisted decision and this request carried a different idempotency
	// key (a same-key replay returns the stored decision instead).
	ErrGateAlreadyDecided = errors.New("writingstore: gate already decided")
	// ErrStaleGate maps to 409 STALE_GATE: the submitted gate_revision no
	// longer matches the gate row (e.g. a newer outline revision was saved).
	ErrStaleGate = errors.New("writingstore: stale gate revision")
	// ErrGateForbidden marks a decision actor that does not own the run's
	// document. Handlers translate it to 404 so gate existence is not leaked.
	ErrGateForbidden = errors.New("writingstore: gate actor is not the run owner")
)

const (
	GateStatusPending  = "pending"
	GateStatusApproved = "approved"

	GateKindEvidence = "evidence"
	GateKindOutline  = "outline"

	GateDecisionApprove = "approve"

	GateOperationPending  = "pending"
	GateOperationDecision = "decision"
	GateOperationRevision = "outline_revision"
)

// GateRecord is one gate's persistent state. While pending it carries the
// input ref the decision must bind to; after approval it carries the actor,
// idempotency key, request hash, and the decision artifact.
type GateRecord struct {
	GateID               string    `json:"gate_id"`
	RunID                string    `json:"run_id"`
	NodeID               string    `json:"node_id"`
	PlanID               string    `json:"plan_id"`
	PlanVersion          int       `json:"plan_version"`
	PlanHash             string    `json:"plan_hash"`
	GateKind             string    `json:"gate_kind"`
	InputArtifactID      string    `json:"input_artifact_id"`
	InputArtifactVersion int       `json:"input_artifact_version"`
	InputHash            string    `json:"input_hash"`
	Revision             int       `json:"revision"`
	Status               string    `json:"status"`
	Decision             string    `json:"decision,omitempty"`
	DecisionArtifactID   string    `json:"decision_artifact_id,omitempty"`
	OwnerUserID          string    `json:"owner_user_id"`
	ActorID              string    `json:"actor_id,omitempty"`
	IdempotencyKey       string    `json:"-"`
	RequestHash          string    `json:"-"`
	LastRevisionKey      string    `json:"-"`
	LastRevisionHash     string    `json:"-"`
	DecidedAt            time.Time `json:"decided_at,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func (gate GateRecord) validate() error {
	if err := validateID(gate.RunID, "run_", "run_id"); err != nil {
		return err
	}
	if !strings.HasPrefix(gate.GateID, "gate_") || len(gate.GateID) <= len("gate_") {
		return fmt.Errorf("%w: gate_id must use the gate_ prefix", ErrInvalidRecord)
	}
	if strings.TrimSpace(gate.NodeID) == "" || strings.TrimSpace(gate.PlanID) == "" || gate.PlanVersion < 1 {
		return fmt.Errorf("%w: gate node and plan binding are required", ErrInvalidRecord)
	}
	if err := validateHash(gate.PlanHash, "gate plan_hash"); err != nil {
		return err
	}
	switch gate.GateKind {
	case GateKindEvidence, GateKindOutline:
	default:
		return fmt.Errorf("%w: unsupported gate kind %q", ErrInvalidRecord, gate.GateKind)
	}
	if strings.TrimSpace(gate.OwnerUserID) == "" {
		return fmt.Errorf("%w: gate owner_user_id is required", ErrInvalidRecord)
	}
	if gate.Status == GateStatusPending && (gate.Revision < 1 || gate.InputArtifactID == "" ||
		gate.InputArtifactVersion < 1 || validateHash(gate.InputHash, "gate input_hash") != nil) {
		return fmt.Errorf("%w: pending gate requires a revision and an input ref", ErrInvalidRecord)
	}
	return nil
}

const gateColumns = `gate_id, run_id, node_id, plan_id, plan_version, plan_hash, gate_kind,
	input_artifact_id, input_artifact_version, input_hash, revision, status, decision,
	decision_artifact_id, owner_user_id, actor_id, idempotency_key, request_hash,
	last_revision_key, last_revision_hash, decided_at, created_at, updated_at`

func scanGate(row interface{ Scan(dest ...any) error }) (GateRecord, error) {
	var gate GateRecord
	var inputArtifactID, decision, decisionArtifactID, actorID sql.NullString
	var inputVersion sql.NullInt64
	var idempotencyKey, requestHash, lastRevisionKey, lastRevisionHash sql.NullString
	var decidedAt sql.NullTime
	if err := row.Scan(&gate.GateID, &gate.RunID, &gate.NodeID, &gate.PlanID, &gate.PlanVersion,
		&gate.PlanHash, &gate.GateKind, &inputArtifactID, &inputVersion, &gate.InputHash,
		&gate.Revision, &gate.Status, &decision, &decisionArtifactID, &gate.OwnerUserID,
		&actorID, &idempotencyKey, &requestHash, &lastRevisionKey, &lastRevisionHash,
		&decidedAt, &gate.CreatedAt, &gate.UpdatedAt); err != nil {
		return GateRecord{}, err
	}
	gate.InputArtifactID, gate.Decision, gate.DecisionArtifactID = inputArtifactID.String, decision.String, decisionArtifactID.String
	gate.InputArtifactVersion = int(inputVersion.Int64)
	gate.ActorID, gate.IdempotencyKey, gate.RequestHash = actorID.String, idempotencyKey.String, requestHash.String
	gate.LastRevisionKey, gate.LastRevisionHash = lastRevisionKey.String, lastRevisionHash.String
	gate.DecidedAt = decidedAt.Time
	return gate, nil
}

// EnsurePendingGate inserts the waiting-gate row, first-writer-wins on
// (run, node, plan_version). A concurrent arrival or a replay observes the
// existing pending row and must not re-run transitions, events, or
// checkpoints; callers branch on the returned created flag.
func (tx *Tx) EnsurePendingGate(ctx context.Context, gate GateRecord) (GateRecord, bool, error) {
	if err := gate.validate(); err != nil {
		return GateRecord{}, false, err
	}
	result, err := tx.tx.ExecContext(ctx, `
		INSERT INTO writing_gate_decisions (
			gate_id, run_id, node_id, plan_id, plan_version, plan_hash, gate_kind,
			input_artifact_id, input_artifact_version, input_hash, revision, status, operation,
			owner_user_id, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'pending','pending',$12,NOW(),NOW())
		ON CONFLICT (run_id, node_id, plan_version) DO NOTHING
	`, gate.GateID, gate.RunID, gate.NodeID, gate.PlanID, gate.PlanVersion, gate.PlanHash,
		gate.GateKind, gate.InputArtifactID, gate.InputArtifactVersion, gate.InputHash,
		gate.Revision, gate.OwnerUserID)
	if err != nil {
		return GateRecord{}, false, fmt.Errorf("insert pending gate: %w", err)
	}
	if inserted, _ := result.RowsAffected(); inserted == 1 {
		// Reload so callers observe the persisted row (defaults and CHECK
		// normalization included), not the pre-insert struct.
		loaded, err := tx.loadGateByNode(ctx, gate.RunID, gate.NodeID, gate.PlanVersion)
		if err != nil {
			return GateRecord{}, false, err
		}
		return loaded, true, nil
	}
	existing, err := tx.loadGateByNode(ctx, gate.RunID, gate.NodeID, gate.PlanVersion)
	if err != nil {
		return GateRecord{}, false, err
	}
	if existing.GateID != gate.GateID || existing.PlanHash != gate.PlanHash ||
		existing.GateKind != gate.GateKind || existing.InputHash != gate.InputHash {
		return GateRecord{}, false, fmt.Errorf("%w: gate %s was replayed with different binding", ErrImmutableConflict, gate.GateID)
	}
	return existing, false, nil
}

func (tx *Tx) loadGateByNode(ctx context.Context, runID, nodeID string, planVersion int) (GateRecord, error) {
	row := tx.tx.QueryRowContext(ctx, `SELECT `+gateColumns+`
		FROM writing_gate_decisions WHERE run_id=$1 AND node_id=$2 AND plan_version=$3
	`, runID, nodeID, planVersion)
	gate, err := scanGate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GateRecord{}, ErrNotFound
	}
	if err != nil {
		return GateRecord{}, fmt.Errorf("load gate by node: %w", err)
	}
	return gate, nil
}

// HumanGatePause is the atomic gate-arrival payload: the pending gate row,
// the paused transitions, the waiting-gate checkpoint writer, and the
// gate.pending event all commit in one transaction.
type HumanGatePause struct {
	Gate GateRecord
	// Transitions records the run.transitioned audit trail
	// (running→pausing→paused) inside the same transaction.
	Transitions []RunTransitionCommand
	// Checkpoint commits the waiting-gate checkpoint snapshot within the
	// transaction. The runtime builds it; nil skips (not used in production).
	Checkpoint func(*Tx) error
	Trace      TraceContext
}

// PauseAtHumanGate creates the pending gate, applies the paused transitions,
// commits the waiting-gate checkpoint, and appends gate.pending — atomically.
// Re-arrival of an existing pending gate is a no-op returning (existing, false).
func (s *Store) PauseAtHumanGate(ctx context.Context, pause HumanGatePause) (GateRecord, bool, error) {
	var record GateRecord
	var created bool
	err := s.InTransaction(ctx, func(tx *Tx) error {
		// Serialize arrivals against the run projection before the existence
		// check so two dispatches cannot both claim first-writer.
		var sequence int64
		if err := tx.tx.QueryRowContext(ctx,
			`SELECT last_event_sequence FROM writing_runs WHERE run_id=$1 FOR UPDATE`,
			pause.Gate.RunID).Scan(&sequence); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock run for gate arrival: %w", err)
		}
		var ensureErr error
		record, created, ensureErr = tx.EnsurePendingGate(ctx, pause.Gate)
		if ensureErr != nil || !created {
			return ensureErr
		}
		for _, transition := range pause.Transitions {
			if _, err := tx.RecordRunTransition(ctx, transition); err != nil {
				return err
			}
		}
		if pause.Checkpoint != nil {
			if err := pause.Checkpoint(tx); err != nil {
				return err
			}
		}
		_, err := tx.AppendRunEvent(ctx, RunEvent{RunID: record.RunID, EventType: "gate.pending",
			EntityKind: "research_gate", EntityID: record.GateID,
			Payload: map[string]any{"gate_id": record.GateID, "node_id": record.NodeID,
				"gate_kind": record.GateKind, "revision": record.Revision,
				"input_ref": map[string]any{"artifact_id": record.InputArtifactID,
					"version": record.InputArtifactVersion, "content_hash": record.InputHash},
				"status": record.Status}, Trace: pause.Trace})
		return err
	})
	return record, created, err
}

// GetGate loads one gate by id, scoped to its run.
func (s *Store) GetGate(ctx context.Context, runID, gateID string) (GateRecord, error) {
	if err := validateID(runID, "run_", "run_id"); err != nil {
		return GateRecord{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+gateColumns+`
		FROM writing_gate_decisions WHERE gate_id=$1 AND run_id=$2
	`, gateID, runID)
	gate, err := scanGate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GateRecord{}, ErrNotFound
	}
	if err != nil {
		return GateRecord{}, fmt.Errorf("get gate: %w", err)
	}
	return gate, nil
}

// GetRunGate resolves the gate parked on one node of the active plan — the
// resume path uses it to distinguish pending (blocked) from decided.
func (s *Store) GetRunGate(ctx context.Context, runID, nodeID string, planVersion int) (GateRecord, error) {
	if err := validateID(runID, "run_", "run_id"); err != nil {
		return GateRecord{}, err
	}
	var gate GateRecord
	err := s.InTransaction(ctx, func(tx *Tx) error {
		var err error
		gate, err = tx.loadGateByNode(ctx, runID, nodeID, planVersion)
		return err
	})
	return gate, err
}

// ListGatesByRun lists a run's gates (owner-scoped).
func (s *Store) ListGatesByRun(ctx context.Context, runID, ownerUserID string) ([]GateRecord, error) {
	if err := validateID(runID, "run_", "run_id"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+gateColumns+`
		FROM writing_gate_decisions WHERE run_id=$1 AND owner_user_id=$2
		ORDER BY created_at, gate_id
	`, runID, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list gates by run: %w", err)
	}
	defer rows.Close()
	gates := []GateRecord{}
	for rows.Next() {
		gate, err := scanGate(rows)
		if err != nil {
			return nil, err
		}
		gates = append(gates, gate)
	}
	return gates, rows.Err()
}

// ArtifactContentRef is the identity of one artifact version as gates bind
// and decisions verify it.
type ArtifactContentRef struct {
	ArtifactID  string
	Version     int
	ContentHash string
}

// GateDecisionCommand carries the verified decision request. The runtime (or
// the API layer) fills Attempt/Completion and the checkpoint writer so the
// decision, the gate node's succeeded attempt, the waiting-gate checkpoint
// update, and the gate.decided event commit as one database transaction.
type GateDecisionCommand struct {
	RunID             string
	GateID            string
	PlanID            string
	PlanVersion       int
	PlanHash          string
	GateRevision      int
	Input             ArtifactContentRef
	Decision          string
	ActorID           string
	IdempotencyKey    string
	RequestHash       string
	Attempt           NodeAttempt
	AttemptCompletion AttemptCompletion
	AfterDecision     func(*Tx) error
	Trace             TraceContext
}

type GateDecisionResult struct {
	Gate     GateRecord
	Replayed bool
}

// DecideGate runs the core confirmation transaction:
// verify (actor ownership, plan binding, revision, input ref) → write the
// decision on the gate row → persist the gate node's succeeded attempt
// (artifacts + node.completed event) → run the caller's checkpoint update →
// append gate.decided. The run itself stays paused: resumption is a separate
// trigger (API-direct and scan-backstop), never part of the decision.
func (s *Store) DecideGate(ctx context.Context, command GateDecisionCommand) (GateDecisionResult, error) {
	var result GateDecisionResult
	err := s.InTransaction(ctx, func(tx *Tx) error {
		if err := command.validate(); err != nil {
			return err
		}
		row := tx.tx.QueryRowContext(ctx, `SELECT `+gateColumns+`
			FROM writing_gate_decisions WHERE gate_id=$1 AND run_id=$2 FOR UPDATE
		`, command.GateID, command.RunID)
		gate, err := scanGate(row)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock gate for decision: %w", err)
		}
		if gate.OwnerUserID != command.ActorID {
			return ErrGateForbidden
		}
		if gate.Status == GateStatusApproved {
			if gate.IdempotencyKey == command.IdempotencyKey && gate.RequestHash == command.RequestHash {
				result = GateDecisionResult{Gate: gate, Replayed: true}
				return nil
			}
			if gate.IdempotencyKey == command.IdempotencyKey {
				return fmt.Errorf("%w: decision idempotency key replayed with a different body", ErrIdempotencyConflict)
			}
			return ErrGateAlreadyDecided
		}
		if gate.Status != GateStatusPending {
			return fmt.Errorf("%w: gate is %s", ErrConflict, gate.Status)
		}
		if gate.Revision != command.GateRevision {
			return fmt.Errorf("%w: gate revision %d no longer matches (current %d)",
				ErrStaleGate, command.GateRevision, gate.Revision)
		}
		if gate.PlanID != command.PlanID || gate.PlanVersion != command.PlanVersion || gate.PlanHash != command.PlanHash {
			return fmt.Errorf("%w: decision plan binding does not match the gate", ErrConflict)
		}
		if gate.InputArtifactID != command.Input.ArtifactID || gate.InputArtifactVersion != command.Input.Version ||
			gate.InputHash != command.Input.ContentHash {
			return fmt.Errorf("%w: decision input ref does not match the gate", ErrConflict)
		}
		if command.Decision != GateDecisionApprove {
			return fmt.Errorf("%w: decision %q is not supported", ErrInvalidRecord, command.Decision)
		}

		// The gate node's succeeded attempt is the ledger record that lets
		// recovery treat the gate as completed (and stops a later Execute
		// from re-pausing on it).
		completion := command.AttemptCompletion
		completion.RunID, completion.NodeID, completion.Attempt = command.RunID, gate.NodeID, command.Attempt.Attempt
		completion.Status = "succeeded"
		if err := tx.ensureAndCompleteAttempt(ctx, command.Attempt, completion); err != nil {
			return err
		}
		approvalArtifactID := ""
		if len(completion.Artifacts) > 0 {
			approvalArtifactID = completion.Artifacts[0].ArtifactID
		}
		_, err = tx.AppendRunEvent(ctx, RunEvent{RunID: gate.RunID, EventType: "gate.decided",
			EntityKind: "research_gate", EntityID: gate.GateID,
			Payload: map[string]any{"gate_id": gate.GateID, "node_id": gate.NodeID,
				"gate_kind": gate.GateKind, "revision": gate.Revision,
				"decision": GateDecisionApprove, "actor_id": command.ActorID,
				"input_ref": map[string]any{"artifact_id": gate.InputArtifactID,
					"version": gate.InputArtifactVersion, "content_hash": gate.InputHash},
				"status": GateStatusApproved}, Trace: command.Trace})
		if err != nil {
			return err
		}
		update, err := tx.tx.ExecContext(ctx, `
			UPDATE writing_gate_decisions SET status='approved', decision='approve',
			 decision_artifact_id=$1, idempotency_key=$2, request_hash=$3, actor_id=$4,
			 operation='decision', decided_at=$5, last_revision_key=NULL,
			 last_revision_hash=NULL, updated_at=$5
			WHERE gate_id=$6
		`, nullString(approvalArtifactID), command.IdempotencyKey,
			command.RequestHash, command.ActorID, time.Now().UTC(), gate.GateID)
		if err != nil {
			return fmt.Errorf("approve gate: %w", err)
		}
		if updated, _ := update.RowsAffected(); updated != 1 {
			return fmt.Errorf("%w: gate %s changed concurrently", ErrConflict, gate.GateID)
		}
		if command.AfterDecision != nil {
			if err := command.AfterDecision(tx); err != nil {
				return err
			}
		}
		approved, err := tx.loadGateByNode(ctx, gate.RunID, gate.NodeID, gate.PlanVersion)
		if err != nil {
			return err
		}
		result = GateDecisionResult{Gate: approved}
		return nil
	})
	return result, err
}

func (command GateDecisionCommand) validate() error {
	if err := validateID(command.RunID, "run_", "run_id"); err != nil {
		return err
	}
	if !strings.HasPrefix(command.GateID, "gate_") || strings.TrimSpace(command.IdempotencyKey) == "" {
		return fmt.Errorf("%w: gate decision requires a gate id and an idempotency key", ErrInvalidRecord)
	}
	if err := validateHash(command.RequestHash, "request_hash"); err != nil {
		return err
	}
	if err := validateHash(command.PlanHash, "plan_hash"); err != nil {
		return err
	}
	if err := validateHash(command.Input.ContentHash, "input content_hash"); err != nil {
		return err
	}
	if strings.TrimSpace(command.ActorID) == "" || command.PlanVersion < 1 || command.GateRevision < 1 {
		return fmt.Errorf("%w: incomplete gate decision command", ErrInvalidRecord)
	}
	return command.Trace.validate()
}

// GateOutlineRevision persists a user-edited outline for a pending outline
// gate: the gate's input ref becomes the new outline and the revision
// increments, so confirmations submitted against the previous revision fail
// with ErrStaleGate (contracts.md §3).
type GateOutlineRevision struct {
	RunID          string
	GateID         string
	PlanID         string
	PlanVersion    int
	PlanHash       string
	GateRevision   int
	Outline        ArtifactContentRef
	ActorID        string
	IdempotencyKey string
	RequestHash    string
}

type GateOutlineRevisionResult struct {
	Gate     GateRecord
	Replayed bool
}

func (s *Store) SaveOutlineRevision(ctx context.Context, revision GateOutlineRevision) (GateOutlineRevisionResult, error) {
	var result GateOutlineRevisionResult
	err := s.InTransaction(ctx, func(tx *Tx) error {
		if err := validateID(revision.RunID, "run_", "run_id"); err != nil {
			return err
		}
		if strings.TrimSpace(revision.IdempotencyKey) == "" || strings.TrimSpace(revision.ActorID) == "" {
			return fmt.Errorf("%w: outline revision requires actor and idempotency identity", ErrInvalidRecord)
		}
		if err := validateHash(revision.RequestHash, "request_hash"); err != nil {
			return err
		}
		if err := validateHash(revision.Outline.ContentHash, "outline content_hash"); err != nil {
			return err
		}
		row := tx.tx.QueryRowContext(ctx, `SELECT `+gateColumns+`
			FROM writing_gate_decisions WHERE gate_id=$1 AND run_id=$2 FOR UPDATE
		`, revision.GateID, revision.RunID)
		gate, err := scanGate(row)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock gate for outline revision: %w", err)
		}
		if gate.OwnerUserID != revision.ActorID {
			return ErrGateForbidden
		}
		if gate.Status != GateStatusPending {
			return fmt.Errorf("%w: gate is %s; only pending outline gates accept revisions", ErrGateAlreadyDecided, gate.Status)
		}
		if gate.GateKind != GateKindOutline {
			return fmt.Errorf("%w: gate kind %s does not accept outline revisions", ErrInvalidRecord, gate.GateKind)
		}
		// Same key + same request body replays the already-saved revision.
		if gate.LastRevisionKey == revision.IdempotencyKey {
			if gate.LastRevisionHash == revision.RequestHash {
				result = GateOutlineRevisionResult{Gate: gate, Replayed: true}
				return nil
			}
			return fmt.Errorf("%w: outline revision idempotency key replayed with a different body", ErrIdempotencyConflict)
		}
		if gate.Revision != revision.GateRevision {
			return fmt.Errorf("%w: gate revision %d no longer matches (current %d)",
				ErrStaleGate, revision.GateRevision, gate.Revision)
		}
		if gate.PlanID != revision.PlanID || gate.PlanVersion != revision.PlanVersion || gate.PlanHash != revision.PlanHash {
			return fmt.Errorf("%w: outline revision plan binding does not match the gate", ErrConflict)
		}
		updated, err := tx.tx.ExecContext(ctx, `
			UPDATE writing_gate_decisions SET input_artifact_id=$1, input_artifact_version=$2,
			 input_hash=$3, revision=revision+1, operation='outline_revision',
			 last_revision_key=$4, last_revision_hash=$5, updated_at=NOW()
			WHERE gate_id=$6 AND status='pending' AND revision=$7
		`, revision.Outline.ArtifactID, revision.Outline.Version, revision.Outline.ContentHash,
			revision.IdempotencyKey, revision.RequestHash, gate.GateID, gate.Revision)
		if err != nil {
			return fmt.Errorf("save outline revision: %w", err)
		}
		if rows, _ := updated.RowsAffected(); rows != 1 {
			return fmt.Errorf("%w: gate %s changed concurrently", ErrConflict, gate.GateID)
		}
		saved, err := tx.loadGateByNode(ctx, gate.RunID, gate.NodeID, gate.PlanVersion)
		if err != nil {
			return err
		}
		result = GateOutlineRevisionResult{Gate: saved}
		return nil
	})
	return result, err
}

// GateResumableRunIDs backs the worker scan: paused runs whose gates are all
// decided (an approved decision exists and none is pending) are waiting for
// their resume trigger — the durable fallback when the API-direct trigger
// raced a process restart (design.md §5.3).
func (s *Store) GateResumableRunIDs(ctx context.Context, limit int) ([]string, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.run_id FROM writing_runs r
		WHERE r.status='paused'
		  AND EXISTS (SELECT 1 FROM writing_gate_decisions g
		              WHERE g.run_id=r.run_id AND g.status='approved')
		  AND NOT EXISTS (SELECT 1 FROM writing_gate_decisions p
		              WHERE p.run_id=r.run_id AND p.status='pending')
		ORDER BY r.updated_at, r.run_id LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
