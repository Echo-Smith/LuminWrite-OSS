package writingstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

type RuntimeRun struct {
	RunID               string                   `json:"run_id"`
	DocumentID          string                   `json:"document_id"`
	ContractID          string                   `json:"contract_id"`
	ContractHash        string                   `json:"contract_hash"`
	ContractVersion     int                      `json:"contract_version"`
	BaseVersionID       string                   `json:"base_version_id,omitempty"`
	Status              string                   `json:"status"`
	ActivePlanID        string                   `json:"active_plan_id,omitempty"`
	ActivePlanVersion   int                      `json:"active_plan_version,omitempty"`
	ApprovalMode        string                   `json:"approval_mode"`
	ApprovalStatus      string                   `json:"approval_status,omitempty"`
	Budget              writingplan.PlanBudget   `json:"budget"`
	Permissions         []writingplan.Permission `json:"permissions"`
	LastEventSequence   int64                    `json:"last_event_sequence"`
	LastSnapshotID      string                   `json:"last_snapshot_id,omitempty"`
	LastSnapshotVersion int                      `json:"last_snapshot_version,omitempty"`
}

func (s *Store) LoadRuntimeRun(ctx context.Context, runID string) (RuntimeRun, error) {
	if err := validateID(runID, "run_", "run_id"); err != nil {
		return RuntimeRun{}, err
	}
	var run RuntimeRun
	var baseVersionID, planID, approvalStatus, snapshotID sql.NullString
	var planVersion, snapshotVersion sql.NullInt64
	var budget, permissions []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT r.run_id, r.document_id, r.contract_id, r.contract_version,
		       r.contract_hash, r.base_version_id, r.status, r.active_plan_id, r.active_plan_version,
		       r.approval_mode, COALESCE(p.approval_status, ''), r.budget,
		       r.permissions, r.last_event_sequence, r.last_snapshot_id,
		       r.last_snapshot_version
		FROM writing_runs r
		LEFT JOIN writing_run_plans p ON p.run_id=r.run_id
		 AND p.plan_id=r.active_plan_id AND p.plan_version=r.active_plan_version
		WHERE r.run_id=$1
	`, runID).Scan(&run.RunID, &run.DocumentID, &run.ContractID, &run.ContractVersion,
		&run.ContractHash, &baseVersionID, &run.Status, &planID, &planVersion, &run.ApprovalMode,
		&approvalStatus, &budget, &permissions, &run.LastEventSequence,
		&snapshotID, &snapshotVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeRun{}, ErrNotFound
	}
	if err != nil {
		return RuntimeRun{}, fmt.Errorf("load runtime run: %w", err)
	}
	run.BaseVersionID, run.ActivePlanID, run.ApprovalStatus, run.LastSnapshotID = baseVersionID.String, planID.String, approvalStatus.String, snapshotID.String
	run.ActivePlanVersion, run.LastSnapshotVersion = int(planVersion.Int64), int(snapshotVersion.Int64)
	if err := json.Unmarshal(budget, &run.Budget); err != nil {
		return RuntimeRun{}, fmt.Errorf("decode run budget: %w", err)
	}
	if err := json.Unmarshal(permissions, &run.Permissions); err != nil {
		return RuntimeRun{}, fmt.Errorf("decode run permissions: %w", err)
	}
	return run, nil
}

func (s *Store) LoadActivePlan(ctx context.Context, runID string) (PlanRecord, error) {
	var record PlanRecord
	var envelope writingplan.WritingPlanEnvelope
	var intent, executable, strategy, budget, permissions []byte
	var approvalStatus string
	err := s.db.QueryRowContext(ctx, `
		SELECT p.run_id, p.plan_version, p.schema_version, p.intent_plan,
		       p.executable_plan, p.strategy_decision, p.budget, p.permissions,
		       p.approval_status, p.created_at
		FROM writing_run_plans p JOIN writing_runs r
		  ON r.run_id=p.run_id AND r.active_plan_id=p.plan_id
		 AND r.active_plan_version=p.plan_version
		WHERE p.run_id=$1
	`, runID).Scan(&record.RunID, &record.PlanVersion, &envelope.SchemaVersion,
		&intent, &executable, &strategy, &budget, &permissions,
		&approvalStatus, &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PlanRecord{}, ErrNotFound
	}
	if err != nil {
		return PlanRecord{}, fmt.Errorf("load active writing plan: %w", err)
	}
	if err := json.Unmarshal(intent, &envelope.IntentPlan); err != nil {
		return PlanRecord{}, fmt.Errorf("decode intent plan: %w", err)
	}
	if err := json.Unmarshal(executable, &envelope.ExecutablePlan); err != nil {
		return PlanRecord{}, fmt.Errorf("decode executable plan: %w", err)
	}
	if err := json.Unmarshal(strategy, &envelope.StrategyDecision); err != nil {
		return PlanRecord{}, fmt.Errorf("decode strategy decision: %w", err)
	}
	if err := json.Unmarshal(budget, &record.Budget); err != nil {
		return PlanRecord{}, fmt.Errorf("decode plan budget: %w", err)
	}
	if err := json.Unmarshal(permissions, &record.Permissions); err != nil {
		return PlanRecord{}, fmt.Errorf("decode plan permissions: %w", err)
	}
	record.Envelope, record.ApprovalStatus = envelope, approvalStatus
	return record, nil
}

type RunTransitionCommand struct {
	RunID, IdempotencyKey, ExpectedFrom, RequestedTo string
	RuleAccepted                                     bool
	Cause, ReasonCode, Summary                       string
	OccurredAt                                       time.Time
	Trace                                            TraceContext
}

type RunTransitionResult struct {
	ExpectedFrom, ActualFrom, RequestedTo, EffectiveState string
	Accepted, Replayed                                    bool
	Event                                                 RunEvent
}

func (s *Store) RecordRunTransition(ctx context.Context, command RunTransitionCommand) (RunTransitionResult, error) {
	if err := validateTransitionCommand(command); err != nil {
		return RunTransitionResult{}, err
	}
	var result RunTransitionResult
	err := s.InTransaction(ctx, func(tx *Tx) error {
		var current string
		if err := tx.tx.QueryRowContext(ctx, `SELECT status FROM writing_runs WHERE run_id=$1 FOR UPDATE`, command.RunID).Scan(&current); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("lock run transition: %w", err)
		}

		var existing RunEvent
		var payload []byte
		err := tx.tx.QueryRowContext(ctx, `
			SELECT event_id, sequence, event_type, occurred_at, entity_kind,
			       entity_id, payload, checksum
			FROM writing_run_events WHERE run_id=$1 AND idempotency_key=$2
			  AND event_type IN ('run.transitioned','run.transition_rejected')
		`, command.RunID, command.IdempotencyKey).Scan(&existing.EventID, &existing.Sequence,
			&existing.EventType, &existing.OccurredAt, &existing.EntityKind,
			&existing.EntityID, &payload, &existing.Checksum)
		if err == nil {
			var saved struct {
				ExpectedFrom   string `json:"expected_from"`
				ActualFrom     string `json:"actual_from"`
				RequestedTo    string `json:"requested_to"`
				EffectiveState string `json:"effective_state"`
				Accepted       bool   `json:"accepted"`
			}
			if err := json.Unmarshal(payload, &saved); err != nil {
				return fmt.Errorf("decode prior transition: %w", err)
			}
			if saved.ExpectedFrom != command.ExpectedFrom || saved.RequestedTo != command.RequestedTo {
				return fmt.Errorf("%w: transition command was replayed with different states", ErrIdempotencyConflict)
			}
			existing.RunID, existing.IdempotencyKey, existing.Payload, existing.Trace = command.RunID, command.IdempotencyKey, map[string]any{}, command.Trace
			result = RunTransitionResult{ExpectedFrom: saved.ExpectedFrom, ActualFrom: saved.ActualFrom,
				RequestedTo: saved.RequestedTo, EffectiveState: saved.EffectiveState,
				Accepted: saved.Accepted, Replayed: true, Event: existing}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load prior transition: %w", err)
		}

		accepted := command.RuleAccepted && current == command.ExpectedFrom
		effective, eventType, reason := current, "run.transition_rejected", "stale_or_invalid_transition"
		if accepted {
			effective, eventType, reason = command.RequestedTo, "run.transitioned", command.ReasonCode
			if reason == "" {
				reason = "transition_applied"
			}
		}
		payloadMap := map[string]any{
			"expected_from": command.ExpectedFrom, "actual_from": current,
			"requested_to": command.RequestedTo, "effective_state": effective,
			"accepted": accepted, "cause": command.Cause, "reason_code": reason,
			"summary": command.Summary,
		}
		event, err := tx.AppendRunEvent(ctx, RunEvent{RunID: command.RunID,
			EventType: eventType, IdempotencyKey: command.IdempotencyKey,
			EntityKind: "run", EntityID: command.RunID, Payload: payloadMap,
			OccurredAt: command.OccurredAt, Trace: command.Trace})
		if err != nil {
			return err
		}
		if accepted {
			completedAt := any(nil)
			if effective == "completed" || effective == "cancelled" || effective == "failed" {
				completedAt = event.OccurredAt
			}
			update, err := tx.tx.ExecContext(ctx, `
				UPDATE writing_runs SET status=$1::varchar,
				 started_at=CASE WHEN $1::varchar='running' AND started_at IS NULL THEN $2 ELSE started_at END,
				 completed_at=COALESCE($3, completed_at), updated_at=$2
				WHERE run_id=$4 AND status=$5
			`, effective, event.OccurredAt, completedAt, command.RunID, current)
			if err != nil {
				return fmt.Errorf("advance run state projection: %w", err)
			}
			if rows, _ := update.RowsAffected(); rows != 1 {
				return fmt.Errorf("%w: run state changed concurrently", ErrConflict)
			}
		}
		result = RunTransitionResult{ExpectedFrom: command.ExpectedFrom, ActualFrom: current,
			RequestedTo: command.RequestedTo, EffectiveState: effective, Accepted: accepted, Event: event}
		return nil
	})
	return result, err
}

func runtimeHash(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

type AttemptCompletion struct {
	RunID, NodeID                         string
	Attempt                               int
	Status                                string
	Artifacts                             []ArtifactRecord
	CostUSD                               float64
	InputTokens, OutputTokens, DurationMS int64
	ErrorCode, ErrorMessage               string
	Trace                                 TraceContext
	CompletedAt                           time.Time
}

type RuntimeEvidenceRecord struct {
	EvidenceID string
	RunID      string
	NodeID     string
	Attempt    int
	Kind       string
	Payload    map[string]any
	OccurredAt time.Time
}

func (s *Store) RecordRuntimeEvidence(ctx context.Context, record RuntimeEvidenceRecord) error {
	if s == nil || strings.TrimSpace(record.EvidenceID) == "" || record.Payload == nil {
		return fmt.Errorf("%w: runtime evidence identity and payload are required", ErrInvalidRecord)
	}
	eventType := map[string]string{
		"route_decision":    "runtime.route_decided",
		"execution":         "runtime.execution_observed",
		"shadow_comparison": "runtime.shadow_compared",
	}[record.Kind]
	if eventType == "" {
		return fmt.Errorf("%w: unsupported runtime evidence kind %q", ErrInvalidRecord, record.Kind)
	}
	key, err := NodeAttemptKey(record.RunID, record.NodeID, record.Attempt)
	if err != nil {
		return err
	}
	return s.InTransaction(ctx, func(tx *Tx) error {
		_, err := tx.AppendRunEvent(ctx, RunEvent{EventID: record.EvidenceID, RunID: record.RunID,
			EventType: eventType, NodeID: record.NodeID, Attempt: record.Attempt,
			IdempotencyKey: key, EntityKind: "rollout_evidence", EntityID: record.EvidenceID,
			Payload: record.Payload, OccurredAt: record.OccurredAt,
			Trace: TraceContext{Provenance: map[string]any{"runtime": "governed", "evidence_kind": record.Kind},
				SourceRefs: []string{}, Actor: Actor{Type: ActorPolicy, ID: "writingruntime.rollout"}}})
		return err
	})
}

func (s *Store) StartNodeAttempt(ctx context.Context, attempt NodeAttempt, trace TraceContext) (NodeAttempt, bool, error) {
	var saved NodeAttempt
	var dispatch bool
	err := s.InTransaction(ctx, func(tx *Tx) error {
		var created bool
		var err error
		saved, created, err = tx.EnsureNodeAttempt(ctx, attempt)
		if err != nil {
			return err
		}
		if !created && saved.Status != "pending" && saved.Status != "expired" {
			return nil
		}
		now := time.Now().UTC()
		leaseHash := runtimeHash(saved.IdempotencyKey, "lease")
		result, err := tx.tx.ExecContext(ctx, `
			UPDATE writing_node_attempts SET status='running', lease_owner=$1,
			 lease_token_hash=$2, lease_expires_at=$3, heartbeat_at=$4,
			 started_at=COALESCE(started_at,$4), updated_at=$4
			WHERE idempotency_key=$5 AND status IN ('pending','expired')
		`, "writingruntime", leaseHash, now.Add(time.Duration(attempt.Bounds.TimeoutMS)*time.Millisecond), now, saved.IdempotencyKey)
		if err != nil {
			return fmt.Errorf("start node attempt: %w", err)
		}
		rows, _ := result.RowsAffected()
		dispatch = rows == 1
		if dispatch {
			saved.Status = "running"
			_, err = tx.AppendRunEvent(ctx, RunEvent{RunID: saved.RunID, EventType: "node.started",
				NodeID: saved.NodeID, Attempt: saved.Attempt, IdempotencyKey: saved.IdempotencyKey,
				EntityKind: "node", EntityID: saved.NodeID,
				Payload: map[string]any{"executor_id": saved.ExecutorID}, Trace: trace})
		}
		return err
	})
	return saved, dispatch, err
}

func (s *Store) CompleteNodeAttempt(ctx context.Context, completion AttemptCompletion) error {
	return s.InTransaction(ctx, func(tx *Tx) error {
		key, err := NodeAttemptKey(completion.RunID, completion.NodeID, completion.Attempt)
		if err != nil {
			return err
		}
		if completion.CompletedAt.IsZero() {
			completion.CompletedAt = time.Now().UTC()
		}
		if completion.Status != "succeeded" && completion.Status != "failed" && completion.Status != "paused" && completion.Status != "cancelled" {
			return fmt.Errorf("%w: invalid attempt completion status", ErrInvalidRecord)
		}
		outputIDs := make([]string, 0, len(completion.Artifacts))
		for _, artifact := range completion.Artifacts {
			if artifact.RunID != completion.RunID || artifact.NodeID != completion.NodeID || artifact.Attempt != completion.Attempt {
				return fmt.Errorf("%w: artifact and attempt bindings differ", ErrInvalidRecord)
			}
			if err := tx.PutArtifact(ctx, artifact); err != nil {
				return err
			}
			outputIDs = append(outputIDs, artifact.ArtifactID)
			if _, err := tx.AppendRunEvent(ctx, RunEvent{RunID: completion.RunID,
				EventType: "artifact.created", NodeID: completion.NodeID, Attempt: completion.Attempt,
				IdempotencyKey: key, EntityKind: "artifact", EntityID: artifact.ArtifactID,
				Payload: map[string]any{"version": artifact.Version, "output_key": artifact.OutputKey,
					"artifact_type": artifact.ArtifactType, "content_hash": artifact.ContentHash}, Trace: completion.Trace}); err != nil {
				return err
			}
		}
		outputs, _ := json.Marshal(outputIDs)
		errorDetail, _ := json.Marshal(map[string]any{"message": completion.ErrorMessage})
		result, err := tx.tx.ExecContext(ctx, `
			UPDATE writing_node_attempts SET status=$1::varchar, output_artifact_ids=$2,
			 actual_cost_usd=$3, actual_input_tokens=$4, actual_output_tokens=$5,
			 actual_duration_ms=$6, error_code=$7, error_detail=$8,
			 completed_at=CASE WHEN $1::varchar IN ('succeeded','failed','cancelled') THEN $9 ELSE completed_at END,
			 lease_owner=NULL, lease_token_hash=NULL, lease_expires_at=NULL, updated_at=$9
			WHERE idempotency_key=$10 AND status IN ('running','paused')
		`, completion.Status, outputs, completion.CostUSD, completion.InputTokens,
			completion.OutputTokens, completion.DurationMS, nullString(completion.ErrorCode),
			errorDetail, completion.CompletedAt, key)
		if err != nil {
			return fmt.Errorf("complete node attempt: %w", err)
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			var existing string
			if err := tx.tx.QueryRowContext(ctx, `SELECT status FROM writing_node_attempts WHERE idempotency_key=$1`, key).Scan(&existing); err != nil {
				return ErrNotFound
			}
			if existing == completion.Status {
				return nil
			}
			return fmt.Errorf("%w: attempt is already %s", ErrConflict, existing)
		}
		eventType := map[string]string{"succeeded": "node.completed", "failed": "node.failed", "paused": "node.paused", "cancelled": "node.cancelled"}[completion.Status]
		_, err = tx.AppendRunEvent(ctx, RunEvent{RunID: completion.RunID, EventType: eventType,
			NodeID: completion.NodeID, Attempt: completion.Attempt, IdempotencyKey: key,
			EntityKind: "node", EntityID: completion.NodeID,
			Payload: map[string]any{"status": completion.Status, "error_code": completion.ErrorCode,
				"cost_usd": completion.CostUSD, "output_artifact_ids": outputIDs}, Trace: completion.Trace})
		return err
	})
}

func (s *Store) ListRunAttempts(ctx context.Context, runID string) ([]NodeAttempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT attempt_id, run_id, plan_id, plan_version, node_id, attempt,
		 idempotency_key, node_kind, capability_id, capability_version,
		 executor_id, status, failure_path, input_hash, actual_cost_usd,
		 actual_input_tokens, actual_output_tokens, actual_duration_ms, created_at
		FROM writing_node_attempts WHERE run_id=$1 ORDER BY node_id, attempt
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("list node attempts: %w", err)
	}
	defer rows.Close()
	var result []NodeAttempt
	for rows.Next() {
		var item NodeAttempt
		var kind, failure string
		if err := rows.Scan(&item.AttemptID, &item.RunID, &item.PlanID, &item.PlanVersion,
			&item.NodeID, &item.Attempt, &item.IdempotencyKey, &kind, &item.CapabilityID,
			&item.CapabilityVersion, &item.ExecutorID, &item.Status, &failure,
			&item.InputHash, &item.ActualCostUSD, &item.ActualInputTokens,
			&item.ActualOutputTokens, &item.ActualDurationMS, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.NodeKind, item.FailurePath = writingplan.NodeKind(kind), writingplan.FailurePath(failure)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ListRunArtifacts(ctx context.Context, runID string) ([]ArtifactRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT artifact_id, version, run_id, plan_id, plan_version, node_id,
		 attempt, idempotency_key, output_key, artifact_type, status, content_hash,
		 media_type, content_ref, producer, capability_version, created_at
		FROM writing_artifacts WHERE run_id=$1 AND status <> 'superseded'
		ORDER BY created_at, artifact_id, version
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("list run artifacts: %w", err)
	}
	defer rows.Close()
	var result []ArtifactRecord
	for rows.Next() {
		var item ArtifactRecord
		if err := rows.Scan(&item.ArtifactID, &item.Version, &item.RunID, &item.PlanID,
			&item.PlanVersion, &item.NodeID, &item.Attempt, &item.IdempotencyKey,
			&item.OutputKey, &item.ArtifactType, &item.Status, &item.ContentHash,
			&item.MediaType, &item.ContentRef, &item.Producer,
			&item.CapabilityVersion, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) LoadLatestSnapshot(ctx context.Context, runID string) (SnapshotRecord, error) {
	var snapshot SnapshotRecord
	var manifest []byte
	var base, candidate, quality sql.NullString
	var qualityVersion sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT snapshot_id, snapshot_version, run_id, checkpoint_id, ledger_sequence,
		 plan_id, plan_version, contract_id, contract_version, contract_hash,
		 document_id, base_version_id, candidate_version_id, quality_report_id,
		 quality_report_version, content_hash, snapshot_status, complete,
		 manifest_payload, storage_ref, created_at, persisted_at
		FROM writing_snapshots WHERE run_id=$1
		ORDER BY ledger_sequence DESC LIMIT 1
	`, runID).Scan(&snapshot.SnapshotID, &snapshot.SnapshotVersion, &snapshot.RunID,
		&snapshot.CheckpointID, &snapshot.LedgerSequence, &snapshot.PlanID,
		&snapshot.PlanVersion, &snapshot.ContractID, &snapshot.ContractVersion,
		&snapshot.ContractHash, &snapshot.DocumentID, &base, &candidate, &quality,
		&qualityVersion, &snapshot.ContentHash, &snapshot.Status, &snapshot.Complete,
		&manifest, &snapshot.StorageRef, &snapshot.CreatedAt, &snapshot.PersistedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SnapshotRecord{}, ErrNotFound
	}
	if err != nil {
		return SnapshotRecord{}, fmt.Errorf("load latest snapshot: %w", err)
	}
	snapshot.BaseVersionID, snapshot.CandidateVersionID, snapshot.QualityReportID = base.String, candidate.String, quality.String
	snapshot.QualityReportVersion = int(qualityVersion.Int64)
	if err := json.Unmarshal(manifest, &snapshot.Manifest); err != nil {
		return SnapshotRecord{}, err
	}
	return snapshot, nil
}

func validateTransitionCommand(command RunTransitionCommand) error {
	if err := validateID(command.RunID, "run_", "run_id"); err != nil {
		return err
	}
	if !strings.HasPrefix(command.IdempotencyKey, command.RunID+":transition:") ||
		strings.TrimSpace(command.Cause) == "" || strings.TrimSpace(command.Summary) == "" ||
		!validRunStatuses[command.ExpectedFrom] || !validRunStatuses[command.RequestedTo] {
		return fmt.Errorf("%w: invalid transition command", ErrInvalidRecord)
	}
	return command.Trace.validate()
}
