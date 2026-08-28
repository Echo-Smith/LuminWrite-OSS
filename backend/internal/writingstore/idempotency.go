package writingstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

func NodeAttemptKey(runID, nodeID string, attempt int) (string, error) {
	if err := validateID(runID, "run_", "run_id"); err != nil {
		return "", err
	}
	if strings.TrimSpace(nodeID) == "" || strings.Contains(nodeID, ":") {
		return "", fmt.Errorf("%w: node_id must be nonblank and cannot contain ':'", ErrInvalidRecord)
	}
	if attempt < 1 {
		return "", fmt.Errorf("%w: attempt must be at least 1", ErrInvalidRecord)
	}
	return runID + ":" + nodeID + ":" + strconv.Itoa(attempt), nil
}

func StableID(prefix string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return prefix + hex.EncodeToString(hash.Sum(nil)[:16])
}

type NodeAttempt struct {
	AttemptID         string
	RunID             string
	PlanID            string
	PlanVersion       int
	NodeID            string
	Attempt           int
	IdempotencyKey    string
	NodeKind          writingplan.NodeKind
	CapabilityID      string
	CapabilityVersion string
	ExecutorID        string
	Status            string
	FailurePath       writingplan.FailurePath
	Bounds            writingplan.Bounds
	CheckpointRef     string
	InputHash         string
	InputArtifactIDs  []string
	CreatedAt         time.Time
}

func (tx *Tx) EnsureNodeAttempt(ctx context.Context, attempt NodeAttempt) (NodeAttempt, bool, error) {
	key, err := NodeAttemptKey(attempt.RunID, attempt.NodeID, attempt.Attempt)
	if err != nil {
		return NodeAttempt{}, false, err
	}
	if attempt.IdempotencyKey != "" && attempt.IdempotencyKey != key {
		return NodeAttempt{}, false, fmt.Errorf("%w: supplied node-attempt key differs from deterministic key", ErrIdempotencyConflict)
	}
	attempt.IdempotencyKey = key
	if attempt.AttemptID == "" {
		attempt.AttemptID = StableID("attempt_", key)
	}
	if attempt.PlanVersion < 1 || strings.TrimSpace(attempt.PlanID) == "" || strings.TrimSpace(attempt.CapabilityID) == "" || strings.TrimSpace(attempt.CapabilityVersion) == "" || strings.TrimSpace(attempt.ExecutorID) == "" {
		return NodeAttempt{}, false, fmt.Errorf("%w: incomplete node attempt binding", ErrInvalidRecord)
	}
	if err := validateHash(attempt.InputHash, "input_hash"); err != nil {
		return NodeAttempt{}, false, err
	}
	if !validNodeKind(attempt.NodeKind) || !validFailurePath(attempt.FailurePath) {
		return NodeAttempt{}, false, fmt.Errorf("%w: invalid node kind or failure path", ErrInvalidRecord)
	}
	if attempt.InputArtifactIDs == nil {
		attempt.InputArtifactIDs = []string{}
	}
	if attempt.Status == "" {
		attempt.Status = "pending"
	}
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = time.Now().UTC()
	}
	bounds, err := json.Marshal(attempt.Bounds)
	if err != nil {
		return NodeAttempt{}, false, fmt.Errorf("marshal node bounds: %w", err)
	}
	inputs, err := json.Marshal(attempt.InputArtifactIDs)
	if err != nil {
		return NodeAttempt{}, false, fmt.Errorf("marshal input artifacts: %w", err)
	}
	result, err := tx.tx.ExecContext(ctx, `
		INSERT INTO writing_node_attempts (
			attempt_id, run_id, plan_id, plan_version, node_id, attempt,
			idempotency_key, node_kind, capability_id, capability_version,
			executor_id, status, failure_path, bounds_snapshot, checkpoint_ref,
			input_hash, input_artifact_ids, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$18)
		ON CONFLICT (idempotency_key) DO NOTHING
	`, attempt.AttemptID, attempt.RunID, attempt.PlanID, attempt.PlanVersion,
		attempt.NodeID, attempt.Attempt, attempt.IdempotencyKey, string(attempt.NodeKind),
		attempt.CapabilityID, attempt.CapabilityVersion, attempt.ExecutorID,
		attempt.Status, string(attempt.FailurePath), bounds, nullString(attempt.CheckpointRef),
		attempt.InputHash, inputs, attempt.CreatedAt)
	if err != nil {
		return NodeAttempt{}, false, fmt.Errorf("insert node attempt: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return NodeAttempt{}, false, fmt.Errorf("inspect node attempt insert: %w", err)
	}
	if inserted == 1 {
		return attempt, true, nil
	}

	var existing NodeAttempt
	var kind, failurePath string
	var immutablePayloadMatches bool
	err = tx.tx.QueryRowContext(ctx, `
		SELECT attempt_id, run_id, plan_id, plan_version, node_id, attempt,
		       idempotency_key, node_kind, capability_id, capability_version,
		       executor_id, status, failure_path, input_hash, created_at,
		       bounds_snapshot = $2::jsonb
		         AND input_artifact_ids = $3::jsonb
		         AND COALESCE(checkpoint_ref, '') = $4
		FROM writing_node_attempts WHERE idempotency_key=$1
	`, key, string(bounds), string(inputs), attempt.CheckpointRef).Scan(&existing.AttemptID, &existing.RunID, &existing.PlanID,
		&existing.PlanVersion, &existing.NodeID, &existing.Attempt,
		&existing.IdempotencyKey, &kind, &existing.CapabilityID,
		&existing.CapabilityVersion, &existing.ExecutorID, &existing.Status,
		&failurePath, &existing.InputHash, &existing.CreatedAt, &immutablePayloadMatches)
	if err != nil {
		return NodeAttempt{}, false, fmt.Errorf("load idempotent node attempt: %w", err)
	}
	existing.NodeKind = writingplan.NodeKind(kind)
	existing.FailurePath = writingplan.FailurePath(failurePath)
	if existing.PlanID != attempt.PlanID || existing.PlanVersion != attempt.PlanVersion ||
		existing.NodeKind != attempt.NodeKind || existing.CapabilityID != attempt.CapabilityID ||
		existing.CapabilityVersion != attempt.CapabilityVersion || existing.ExecutorID != attempt.ExecutorID ||
		existing.InputHash != attempt.InputHash || !immutablePayloadMatches {
		return NodeAttempt{}, false, fmt.Errorf("%w: node attempt %s was replayed with different inputs or binding", ErrIdempotencyConflict, key)
	}
	return existing, false, nil
}

func validNodeKind(kind writingplan.NodeKind) bool {
	switch kind {
	case writingplan.NodeSequence, writingplan.NodeParallel, writingplan.NodeMap,
		writingplan.NodeReduce, writingplan.NodeCondition, writingplan.NodeRetry,
		writingplan.NodeRefine, writingplan.NodeHumanGate, writingplan.NodeValidate,
		writingplan.NodeFallback, writingplan.NodeAction:
		return true
	default:
		return false
	}
}

func validFailurePath(path writingplan.FailurePath) bool {
	switch path {
	case writingplan.FailureFail, writingplan.FailurePause, writingplan.FailureFallback, writingplan.FailurePartial:
		return true
	default:
		return false
	}
}
