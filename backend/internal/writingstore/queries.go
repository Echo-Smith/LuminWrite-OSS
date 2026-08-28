package writingstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

func (s *Store) GetDocument(ctx context.Context, documentID string) (DocumentRecord, error) {
	if err := validateID(documentID, "doc_", "document_id"); err != nil {
		return DocumentRecord{}, err
	}
	var record DocumentRecord
	var metadata []byte
	var currentID sql.NullString
	var actorType string
	var actorID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT document_id, owner_user_id::text, title, status, current_version,
		       current_version_id, metadata, created_by_type, created_by_id,
		       created_at, updated_at
		FROM writing_documents WHERE document_id=$1
	`, documentID).Scan(&record.DocumentID, &record.OwnerUserID, &record.Title,
		&record.Status, &record.CurrentVersion, &currentID, &metadata,
		&actorType, &actorID, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DocumentRecord{}, ErrNotFound
	}
	if err != nil {
		return DocumentRecord{}, fmt.Errorf("get writing document: %w", err)
	}
	if err := json.Unmarshal(metadata, &record.Metadata); err != nil {
		return DocumentRecord{}, fmt.Errorf("decode document metadata: %w", err)
	}
	record.CurrentVersionID = currentID.String
	record.Actor = Actor{Type: ActorType(actorType), ID: actorID.String}
	return record, nil
}

func (s *Store) ListDocumentVersions(ctx context.Context, documentID string) ([]StoredDocumentVersion, error) {
	if err := validateID(documentID, "doc_", "document_id"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT version_id FROM writing_document_versions
		WHERE document_id=$1 ORDER BY version ASC
	`, documentID)
	if err != nil {
		return nil, fmt.Errorf("list document versions: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan document version: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list document versions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close document versions: %w", err)
	}
	result := make([]StoredDocumentVersion, 0, len(ids))
	for _, id := range ids {
		version, err := s.GetDocumentVersion(ctx, documentID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, version)
	}
	return result, nil
}

func (s *Store) CreateRunWithPlan(ctx context.Context, run RunRecord, plan PlanRecord, activeStatus string) error {
	if run.RunID != plan.RunID {
		return fmt.Errorf("%w: run and plan identities differ", ErrInvalidRecord)
	}
	if !validRunStatuses[activeStatus] {
		return fmt.Errorf("%w: invalid active run status", ErrInvalidRecord)
	}
	// The schema forbids an awaiting/running projection without an active plan.
	// Insert the run as planned, then attach the immutable plan and publish the
	// requested active status in the same transaction.
	run.Status = "planned"
	return s.InTransaction(ctx, func(tx *Tx) error {
		if err := tx.CreateRun(ctx, run); err != nil {
			return err
		}
		if err := tx.PutPlan(ctx, plan); err != nil {
			return err
		}
		return tx.ActivatePlan(ctx, run.RunID, plan.Envelope.ExecutablePlan.PlanID, plan.PlanVersion, activeStatus)
	})
}

func (s *Store) ListRunEvents(ctx context.Context, runID string, afterSequence int64, limit int) ([]RunEvent, error) {
	if err := validateID(runID, "run_", "run_id"); err != nil {
		return nil, err
	}
	if afterSequence < 0 {
		return nil, fmt.Errorf("%w: event sequence cannot be negative", ErrInvalidRecord)
	}
	if limit < 1 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, sequence, event_type, occurred_at, node_id, attempt,
		       idempotency_key, causation_event_id, entity_kind, entity_id,
		       payload, checksum, provenance, source_refs, actor_type, actor_id
		FROM writing_run_events
		WHERE run_id=$1 AND sequence>$2 ORDER BY sequence ASC LIMIT $3
	`, runID, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	defer rows.Close()
	result := make([]RunEvent, 0)
	for rows.Next() {
		var event RunEvent
		var nodeID, key, cause, actorID sql.NullString
		var attempt sql.NullInt64
		var payload, provenance, sources []byte
		var actorType string
		if err := rows.Scan(&event.EventID, &event.Sequence, &event.EventType,
			&event.OccurredAt, &nodeID, &attempt, &key, &cause, &event.EntityKind,
			&event.EntityID, &payload, &event.Checksum, &provenance, &sources,
			&actorType, &actorID); err != nil {
			return nil, fmt.Errorf("scan run event: %w", err)
		}
		event.RunID, event.NodeID, event.Attempt = runID, nodeID.String, int(attempt.Int64)
		event.IdempotencyKey, event.CausationEventID = key.String, cause.String
		event.Trace.Actor = Actor{Type: ActorType(actorType), ID: actorID.String}
		if err := json.Unmarshal(payload, &event.Payload); err != nil {
			return nil, fmt.Errorf("decode run event payload: %w", err)
		}
		if err := json.Unmarshal(provenance, &event.Trace.Provenance); err != nil {
			return nil, fmt.Errorf("decode run event provenance: %w", err)
		}
		if err := json.Unmarshal(sources, &event.Trace.SourceRefs); err != nil {
			return nil, fmt.Errorf("decode run event sources: %w", err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	return result, nil
}

func (s *Store) GetLatestQualityReport(ctx context.Context, documentID string) (QualityReportRecord, error) {
	if err := validateID(documentID, "doc_", "document_id"); err != nil {
		return QualityReportRecord{}, err
	}
	var record QualityReportRecord
	var payload, provenance, sources []byte
	var validated, committed, snapshot, actorID sql.NullString
	var snapshotVersion sql.NullInt64
	var actorType string
	err := s.db.QueryRowContext(ctx, `
		SELECT report_id, report_version, run_id, plan_id, plan_version,
		       document_id, candidate_version_id, validated_version_id,
		       committed_version_id, content_hash, requested_assurance,
		       achieved_assurance, assurance_satisfied, quality_state,
		       version_consistent, required_validators_satisfied, blocker_count,
		       error_count, open_error_count, waived_error_count, warning_count,
		       report_payload, snapshot_manifest_id, snapshot_version,
		       snapshot_persisted, provenance, source_refs, created_by_type,
		       created_by_id, created_at
		FROM writing_quality_reports WHERE document_id=$1
		ORDER BY created_at DESC, report_version DESC LIMIT 1
	`, documentID).Scan(&record.ReportID, &record.ReportVersion, &record.RunID,
		&record.PlanID, &record.PlanVersion, &record.DocumentID,
		&record.CandidateVersionID, &validated, &committed, &record.ContentHash,
		&record.RequestedAssurance, &record.AchievedAssurance, &record.AssuranceSatisfied,
		&record.QualityState, &record.VersionConsistent, &record.RequiredValidatorsSatisfied,
		&record.BlockerCount, &record.ErrorCount, &record.OpenErrorCount,
		&record.WaivedErrorCount, &record.WarningCount, &payload, &snapshot,
		&snapshotVersion, &record.SnapshotPersisted, &provenance, &sources,
		&actorType, &actorID, &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return QualityReportRecord{}, ErrNotFound
	}
	if err != nil {
		return QualityReportRecord{}, fmt.Errorf("get latest quality report: %w", err)
	}
	record.ValidatedVersionID, record.CommittedVersionID = validated.String, committed.String
	record.SnapshotID, record.SnapshotVersion = snapshot.String, int(snapshotVersion.Int64)
	record.Trace.Actor = Actor{Type: ActorType(actorType), ID: actorID.String}
	if err := json.Unmarshal(payload, &record.Payload); err != nil {
		return QualityReportRecord{}, fmt.Errorf("decode quality report: %w", err)
	}
	if err := json.Unmarshal(provenance, &record.Trace.Provenance); err != nil {
		return QualityReportRecord{}, fmt.Errorf("decode quality provenance: %w", err)
	}
	if err := json.Unmarshal(sources, &record.Trace.SourceRefs); err != nil {
		return QualityReportRecord{}, fmt.Errorf("decode quality sources: %w", err)
	}
	return record, nil
}

type PlanApprovalCommand struct {
	RunID, PlanID, PlanHash, IdempotencyKey string
	PlanVersion                             int
	Permissions                             []writingplan.Permission
	Actor                                   Actor
	OccurredAt                              time.Time
}

func (s *Store) ApprovePlan(ctx context.Context, command PlanApprovalCommand) error {
	if err := validateID(command.RunID, "run_", "run_id"); err != nil {
		return err
	}
	if err := validateID(command.PlanID, "plan_", "plan_id"); err != nil {
		return err
	}
	if command.PlanVersion < 1 || command.IdempotencyKey == "" || command.Permissions == nil {
		return fmt.Errorf("%w: incomplete plan approval scope", ErrInvalidRecord)
	}
	if err := validateHash(command.PlanHash, "plan_hash"); err != nil {
		return err
	}
	if err := command.Actor.Validate(); err != nil {
		return err
	}
	command.Permissions = append([]writingplan.Permission(nil), command.Permissions...)
	sort.Slice(command.Permissions, func(i, j int) bool { return command.Permissions[i] < command.Permissions[j] })
	return s.InTransaction(ctx, func(tx *Tx) error {
		var actualHash, approvalStatus, documentID string
		var approvalRequired bool
		var budgetJSON, permissionsJSON []byte
		err := tx.tx.QueryRowContext(ctx, `
			SELECT p.plan_hash, p.approval_required, p.approval_status,
			       p.budget, p.permissions, r.document_id
			FROM writing_run_plans p JOIN writing_runs r ON r.run_id=p.run_id
			WHERE p.run_id=$1 AND p.plan_id=$2 AND p.plan_version=$3
			  AND r.active_plan_id=p.plan_id AND r.active_plan_version=p.plan_version
			FOR UPDATE OF p
		`, command.RunID, command.PlanID, command.PlanVersion).Scan(&actualHash,
			&approvalRequired, &approvalStatus, &budgetJSON, &permissionsJSON, &documentID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock plan approval: %w", err)
		}
		var persistedPermissions []writingplan.Permission
		if err := json.Unmarshal(permissionsJSON, &persistedPermissions); err != nil {
			return fmt.Errorf("decode plan approval permissions: %w", err)
		}
		if actualHash != command.PlanHash || !samePermissions(persistedPermissions, command.Permissions) {
			return fmt.Errorf("%w: approval scope differs from active plan", ErrConflict)
		}
		if !approvalRequired || approvalStatus == "not_required" {
			return fmt.Errorf("%w: plan does not require approval", ErrInvalidRecord)
		}
		now := command.OccurredAt
		if now.IsZero() {
			now = time.Now().UTC()
		}
		decisionID := StableID("decision_", command.RunID, command.IdempotencyKey)
		payload := map[string]any{"plan_id": command.PlanID, "plan_version": command.PlanVersion, "plan_hash": command.PlanHash, "permissions": command.Permissions}
		payloadJSON, _ := json.Marshal(payload)
		contentHash := runtimeHash(string(payloadJSON))
		provenance, sources, err := (TraceContext{Provenance: map[string]any{"approval_scope": "exact_active_plan"}, SourceRefs: []string{}, Actor: command.Actor}).values()
		if err != nil {
			return err
		}
		result, err := tx.tx.ExecContext(ctx, `
			INSERT INTO writing_decisions (
				decision_id, decision_version, run_id, plan_id, plan_version,
				document_id, schema_version, decision_type, status, reason_code,
				summary, decision_payload, plan_hash, budget_snapshot,
				permission_snapshot, idempotency_key, content_hash, provenance,
				source_refs, created_by_type, created_by_id, created_at
			) VALUES ($1,1,$2,$3,$4,$5,'lcp/1.0','plan_approval','approved',
			          'user_approved','user approved the exact active plan',$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			ON CONFLICT (idempotency_key) DO NOTHING
		`, decisionID, command.RunID, command.PlanID, command.PlanVersion,
			documentID, payloadJSON, command.PlanHash, budgetJSON, permissionsJSON,
			command.IdempotencyKey, contentHash, provenance, sources,
			string(command.Actor.Type), nullString(command.Actor.ID), now)
		if err != nil {
			return fmt.Errorf("record plan approval: %w", err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect plan approval: %w", err)
		}
		if inserted == 0 {
			var existingRunID, existingPlanID, existingPlanHash string
			var existingPlanVersion int
			var payloadMatches, permissionsMatch bool
			err := tx.tx.QueryRowContext(ctx, `
				SELECT run_id, plan_id, plan_version, plan_hash,
				       decision_payload=$2::jsonb, permission_snapshot=$3::jsonb
				FROM writing_decisions WHERE idempotency_key=$1
			`, command.IdempotencyKey, string(payloadJSON), string(permissionsJSON)).Scan(
				&existingRunID, &existingPlanID, &existingPlanVersion, &existingPlanHash,
				&payloadMatches, &permissionsMatch)
			if err != nil {
				return fmt.Errorf("load idempotent plan approval: %w", err)
			}
			if existingRunID != command.RunID || existingPlanID != command.PlanID ||
				existingPlanVersion != command.PlanVersion || existingPlanHash != command.PlanHash ||
				!payloadMatches || !permissionsMatch {
				return fmt.Errorf("%w: approval idempotency key is already used", ErrIdempotencyConflict)
			}
		}
		result, err = tx.tx.ExecContext(ctx, `
			UPDATE writing_run_plans SET approval_status='approved',
			 approved_by_type=$1, approved_by_id=$2, approved_at=$3
			WHERE run_id=$4 AND plan_id=$5 AND plan_version=$6
		`, string(command.Actor.Type), nullString(command.Actor.ID), now,
			command.RunID, command.PlanID, command.PlanVersion)
		if err != nil {
			return fmt.Errorf("approve writing plan: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect writing plan approval: %w", err)
		}
		if updated != 1 {
			return ErrNotFound
		}
		return nil
	})
}

func samePermissions(left, right []writingplan.Permission) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := append([]writingplan.Permission(nil), left...), append([]writingplan.Permission(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i] < leftCopy[j] })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i] < rightCopy[j] })
	for i := range leftCopy {
		if leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
}
