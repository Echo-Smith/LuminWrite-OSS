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

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
)

type RunRecord struct {
	RunID              string
	DocumentID         string
	ContractID         string
	ContractVersion    int
	ContractHash       string
	BaseVersionID      string
	Status             string
	ApprovalMode       writingkernel.ApprovalMode
	RequestedAssurance writingkernel.AssuranceLevel
	Budget             writingplan.PlanBudget
	Permissions        []writingplan.Permission
	Trace              TraceContext
	CreatedAt          time.Time
}

var validRunStatuses = map[string]bool{
	"draft": true, "contract_confirmed": true, "planning": true,
	"planned": true, "awaiting_approval": true, "running": true,
	"pausing": true, "paused": true, "replanning": true, "failed": true,
	"cancelling": true, "cancelled": true, "completed": true,
}

func (s *Store) CreateRun(ctx context.Context, record RunRecord) error {
	return s.InTransaction(ctx, func(tx *Tx) error { return tx.CreateRun(ctx, record) })
}

func (tx *Tx) CreateRun(ctx context.Context, record RunRecord) error {
	if err := validateID(record.RunID, "run_", "run_id"); err != nil {
		return err
	}
	if err := validateID(record.DocumentID, "doc_", "document_id"); err != nil {
		return err
	}
	if err := validateID(record.ContractID, "ctr_", "contract_id"); err != nil {
		return err
	}
	if record.ContractVersion < 1 || !record.ApprovalMode.Valid() || !record.RequestedAssurance.Valid() {
		return fmt.Errorf("%w: invalid run contract/control fields", ErrInvalidRecord)
	}
	if err := validateHash(record.ContractHash, "contract_hash"); err != nil {
		return err
	}
	if err := record.Trace.validate(); err != nil {
		return err
	}
	if record.Status == "" {
		record.Status = "draft"
	}
	if !validRunStatuses[record.Status] {
		return fmt.Errorf("%w: invalid run status %q", ErrInvalidRecord, record.Status)
	}
	budget, err := marshalJSON(record.Budget, "run budget")
	if err != nil {
		return err
	}
	permissions, err := marshalJSON(record.Permissions, "run permissions")
	if err != nil {
		return err
	}
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	result, err := tx.tx.ExecContext(ctx, `
		INSERT INTO writing_runs (
			run_id, document_id, contract_id, contract_version, contract_hash,
			base_version_id, status, approval_mode, requested_assurance,
			budget, permissions, created_by_type, created_by_id, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14)
		ON CONFLICT (run_id) DO NOTHING
	`, record.RunID, record.DocumentID, record.ContractID, record.ContractVersion,
		record.ContractHash, nullString(record.BaseVersionID), record.Status,
		string(record.ApprovalMode), string(record.RequestedAssurance), budget, permissions,
		string(record.Trace.Actor.Type), nullString(record.Trace.Actor.ID), createdAt)
	if err != nil {
		return fmt.Errorf("create writing run: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect writing run insert: %w", err)
	}
	if inserted == 1 {
		return nil
	}
	var documentID, contractID, contractHash string
	var contractVersion int
	err = tx.tx.QueryRowContext(ctx, `
		SELECT document_id, contract_id, contract_version, contract_hash
		FROM writing_runs WHERE run_id=$1
	`, record.RunID).Scan(&documentID, &contractID, &contractVersion, &contractHash)
	if err != nil {
		return fmt.Errorf("load conflicting run: %w", err)
	}
	if documentID == record.DocumentID && contractID == record.ContractID &&
		contractVersion == record.ContractVersion && contractHash == record.ContractHash {
		return nil
	}
	return fmt.Errorf("%w: run %s already exists with another contract", ErrImmutableConflict, record.RunID)
}

type PlanRecord struct {
	RunID          string
	PlanVersion    int
	Envelope       writingplan.WritingPlanEnvelope
	Budget         writingplan.PlanBudget
	Permissions    []writingplan.Permission
	ApprovalStatus string
	Trace          TraceContext
	CreatedAt      time.Time
}

func (s *Store) PutPlan(ctx context.Context, record PlanRecord) error {
	return s.InTransaction(ctx, func(tx *Tx) error { return tx.PutPlan(ctx, record) })
}

func (tx *Tx) PutPlan(ctx context.Context, record PlanRecord) error {
	if err := validateID(record.RunID, "run_", "run_id"); err != nil {
		return err
	}
	if record.PlanVersion < 1 {
		return fmt.Errorf("%w: plan_version must be at least 1", ErrInvalidRecord)
	}
	if err := record.Envelope.Validate(); err != nil {
		return fmt.Errorf("%w: writing plan: %v", ErrInvalidRecord, err)
	}
	provenance, sources, err := record.Trace.values()
	if err != nil {
		return err
	}
	intent, err := marshalJSON(record.Envelope.IntentPlan, "intent plan")
	if err != nil {
		return err
	}
	executable, err := marshalJSON(record.Envelope.ExecutablePlan, "executable plan")
	if err != nil {
		return err
	}
	strategy, err := marshalJSON(record.Envelope.StrategyDecision, "strategy decision")
	if err != nil {
		return err
	}
	validation, err := marshalJSON(record.Envelope.ExecutablePlan.StaticValidation, "static validation")
	if err != nil {
		return err
	}
	budget, err := marshalJSON(record.Budget, "plan budget")
	if err != nil {
		return err
	}
	permissions, err := marshalJSON(record.Permissions, "plan permissions")
	if err != nil {
		return err
	}
	approvalRequired := record.Envelope.StrategyDecision.ApprovalRequired
	approvalStatus := record.ApprovalStatus
	if approvalStatus == "" {
		if approvalRequired {
			approvalStatus = "pending"
		} else {
			approvalStatus = "not_required"
		}
	}
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	plan := record.Envelope.ExecutablePlan
	result, err := tx.tx.ExecContext(ctx, `
		INSERT INTO writing_run_plans (
			plan_id, run_id, plan_version, contract_id, contract_version,
			schema_version, intent_plan_hash, plan_hash, content_hash,
			trust_level, status, intent_plan, executable_plan, strategy_decision,
			static_validation, static_validation_valid, budget, permissions,
			approval_required, approval_status, provenance, source_refs,
			created_by_type, created_by_id, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
		ON CONFLICT (plan_id, plan_version) DO NOTHING
	`, plan.PlanID, record.RunID, record.PlanVersion,
		record.Envelope.IntentPlan.ContractRef.ID, record.Envelope.IntentPlan.ContractRef.Version,
		record.Envelope.SchemaVersion, record.Envelope.IntentPlan.IntentPlanHash, plan.PlanHash,
		string(plan.TrustLevel), string(plan.Status), intent, executable, strategy, validation,
		plan.StaticValidation.Valid, budget, permissions, approvalRequired, approvalStatus,
		provenance, sources, string(record.Trace.Actor.Type), nullString(record.Trace.Actor.ID), createdAt)
	if err != nil {
		return fmt.Errorf("insert writing plan: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect writing plan insert: %w", err)
	}
	if inserted == 0 {
		var existingRunID, existingHash string
		var immutablePayloadMatches bool
		if err := tx.tx.QueryRowContext(ctx, `
			SELECT run_id, plan_hash,
			       contract_id=$3 AND contract_version=$4 AND schema_version=$5
			         AND intent_plan_hash=$6 AND trust_level=$7
			         AND intent_plan=$8::jsonb AND executable_plan=$9::jsonb
			         AND strategy_decision=$10::jsonb AND static_validation=$11::jsonb
			         AND static_validation_valid=$12 AND budget=$13::jsonb
			         AND permissions=$14::jsonb AND provenance=$15::jsonb
			         AND source_refs=$16::jsonb AND created_by_type=$17
			         AND COALESCE(created_by_id, '')=$18
			FROM writing_run_plans
			WHERE plan_id=$1 AND plan_version=$2
		`, plan.PlanID, record.PlanVersion, record.Envelope.IntentPlan.ContractRef.ID,
			record.Envelope.IntentPlan.ContractRef.Version, record.Envelope.SchemaVersion,
			record.Envelope.IntentPlan.IntentPlanHash, string(plan.TrustLevel), string(intent),
			string(executable), string(strategy), string(validation), plan.StaticValidation.Valid,
			string(budget), string(permissions), string(provenance), string(sources),
			string(record.Trace.Actor.Type), record.Trace.Actor.ID).Scan(
			&existingRunID, &existingHash, &immutablePayloadMatches); err != nil {
			return fmt.Errorf("load conflicting writing plan: %w", err)
		}
		if existingRunID != record.RunID || existingHash != plan.PlanHash || !immutablePayloadMatches {
			return fmt.Errorf("%w: plan %s version %d has different content", ErrImmutableConflict, plan.PlanID, record.PlanVersion)
		}
	}
	return nil
}

func (tx *Tx) ActivatePlan(ctx context.Context, runID, planID string, planVersion int, status string) error {
	if err := validateID(runID, "run_", "run_id"); err != nil {
		return err
	}
	if err := validateID(planID, "plan_", "plan_id"); err != nil {
		return err
	}
	if planVersion < 1 || !validRunStatuses[status] {
		return fmt.Errorf("%w: invalid plan activation version or run status", ErrInvalidRecord)
	}
	result, err := tx.tx.ExecContext(ctx, `
		UPDATE writing_runs
		SET active_plan_id=$1, active_plan_version=$2, status=$3, updated_at=NOW()
		WHERE run_id=$4 AND EXISTS (
			SELECT 1 FROM writing_run_plans p
			WHERE p.run_id=$4 AND p.plan_id=$1 AND p.plan_version=$2
		)
	`, planID, planVersion, status, runID)
	if err != nil {
		return fmt.Errorf("activate writing plan: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect plan activation: %w", err)
	}
	if updated != 1 {
		return ErrNotFound
	}
	return nil
}

type RunEvent struct {
	EventID          string         `json:"event_id"`
	RunID            string         `json:"run_id"`
	Sequence         int64          `json:"sequence"`
	EventType        string         `json:"event_type"`
	OccurredAt       time.Time      `json:"occurred_at"`
	NodeID           string         `json:"node_id,omitempty"`
	Attempt          int            `json:"attempt,omitempty"`
	IdempotencyKey   string         `json:"idempotency_key,omitempty"`
	CausationEventID string         `json:"causation_event_id,omitempty"`
	EntityKind       string         `json:"entity_kind"`
	EntityID         string         `json:"entity_id"`
	Payload          map[string]any `json:"payload"`
	Checksum         string         `json:"checksum"`
	Trace            TraceContext   `json:"trace"`
}

var validRunEventTypes = map[string]bool{
	"run.planned": true, "run.started": true, "run.paused": true,
	"run.resumed": true, "run.cancelled": true, "run.completed": true,
	"run.failed": true, "node.started": true, "node.completed": true,
	"node.failed": true, "artifact.created": true, "quality.updated": true,
	"snapshot.created": true, "run.transitioned": true,
	"document.committed":      true,
	"run.transition_rejected": true, "node.paused": true,
	"node.cancelled": true,
}

var validRunEventEntityKinds = map[string]bool{
	"run": true, "node": true, "artifact": true,
	"document_version": true, "quality_report": true, "snapshot": true,
}

func (tx *Tx) AppendRunEvent(ctx context.Context, event RunEvent) (RunEvent, error) {
	if !validRunEventTypes[event.EventType] {
		return RunEvent{}, fmt.Errorf("%w: unsupported event type %q", ErrInvalidRecord, event.EventType)
	}
	if event.Payload == nil || !validRunEventEntityKinds[event.EntityKind] || strings.TrimSpace(event.EntityID) == "" {
		return RunEvent{}, fmt.Errorf("%w: event entity and payload are required", ErrInvalidRecord)
	}
	provenance, sources, err := event.Trace.values()
	if err != nil {
		return RunEvent{}, err
	}
	nodeScoped := event.EventType == "node.started" || event.EventType == "node.completed" || event.EventType == "node.failed" || event.EventType == "node.paused" || event.EventType == "node.cancelled" || event.EventType == "artifact.created"
	transitionScoped := event.EventType == "run.transitioned" || event.EventType == "run.transition_rejected"
	if nodeScoped {
		expected, err := NodeAttemptKey(event.RunID, event.NodeID, event.Attempt)
		if err != nil {
			return RunEvent{}, err
		}
		if event.IdempotencyKey != expected {
			return RunEvent{}, fmt.Errorf("%w: event idempotency key mismatch", ErrIdempotencyConflict)
		}
	} else if transitionScoped {
		if event.NodeID != "" || event.Attempt != 0 || strings.TrimSpace(event.IdempotencyKey) == "" {
			return RunEvent{}, fmt.Errorf("%w: transition event requires command idempotency only", ErrInvalidRecord)
		}
	} else if event.NodeID != "" || event.Attempt != 0 || event.IdempotencyKey != "" {
		return RunEvent{}, fmt.Errorf("%w: run-scoped event cannot carry node attempt identity", ErrInvalidRecord)
	}

	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return RunEvent{}, fmt.Errorf("marshal event payload: %w", err)
	}
	var previousSequence int64
	err = tx.tx.QueryRowContext(ctx, `
		SELECT last_event_sequence FROM writing_runs WHERE run_id=$1 FOR UPDATE
	`, event.RunID).Scan(&previousSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return RunEvent{}, ErrNotFound
	}
	if err != nil {
		return RunEvent{}, fmt.Errorf("lock run event sequence: %w", err)
	}
	if nodeScoped || transitionScoped {
		var existing RunEvent
		var payloadMatches, causationMatches, traceMatches bool
		err = tx.tx.QueryRowContext(ctx, `
			SELECT event_id, sequence, occurred_at, checksum,
			       payload=$6::jsonb,
			       COALESCE(causation_event_id, '')=$7,
			       provenance=$8::jsonb AND source_refs=$9::jsonb
			         AND actor_type=$10 AND COALESCE(actor_id, '')=$11
			FROM writing_run_events
			WHERE run_id=$1 AND idempotency_key=$2 AND event_type=$3
			  AND entity_kind=$4 AND entity_id=$5
		`, event.RunID, event.IdempotencyKey, event.EventType, event.EntityKind,
			event.EntityID, string(payload), event.CausationEventID,
			string(provenance), string(sources), string(event.Trace.Actor.Type), event.Trace.Actor.ID).Scan(
			&existing.EventID, &existing.Sequence, &existing.OccurredAt,
			&existing.Checksum, &payloadMatches, &causationMatches, &traceMatches)
		if err == nil {
			if !payloadMatches || !causationMatches || !traceMatches || (event.EventID != "" && event.EventID != existing.EventID) {
				return RunEvent{}, fmt.Errorf("%w: node event was replayed with different content", ErrIdempotencyConflict)
			}
			existing.RunID = event.RunID
			existing.EventType = event.EventType
			existing.NodeID = event.NodeID
			existing.Attempt = event.Attempt
			existing.IdempotencyKey = event.IdempotencyKey
			existing.CausationEventID = event.CausationEventID
			existing.EntityKind = event.EntityKind
			existing.EntityID = event.EntityID
			existing.Payload = event.Payload
			existing.Trace = event.Trace
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return RunEvent{}, fmt.Errorf("load idempotent run event: %w", err)
		}
	}
	event.Sequence = previousSequence + 1
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.EventID == "" {
		event.EventID = StableID("evt_", event.RunID, fmt.Sprint(event.Sequence), event.EventType, event.EntityKind, event.EntityID)
	}
	checksumPayload := struct {
		RunID            string         `json:"run_id"`
		Sequence         int64          `json:"sequence"`
		EventType        string         `json:"event_type"`
		OccurredAt       time.Time      `json:"occurred_at"`
		NodeID           string         `json:"node_id,omitempty"`
		Attempt          int            `json:"attempt,omitempty"`
		IdempotencyKey   string         `json:"idempotency_key,omitempty"`
		CausationEventID string         `json:"causation_event_id,omitempty"`
		EntityKind       string         `json:"entity_kind"`
		EntityID         string         `json:"entity_id"`
		Payload          map[string]any `json:"payload"`
	}{event.RunID, event.Sequence, event.EventType, event.OccurredAt, event.NodeID,
		event.Attempt, event.IdempotencyKey, event.CausationEventID,
		event.EntityKind, event.EntityID, event.Payload}
	encoded, err := json.Marshal(checksumPayload)
	if err != nil {
		return RunEvent{}, fmt.Errorf("marshal event checksum payload: %w", err)
	}
	sum := sha256.Sum256(encoded)
	event.Checksum = "sha256:" + hex.EncodeToString(sum[:])
	_, err = tx.tx.ExecContext(ctx, `
		INSERT INTO writing_run_events (
			event_id, run_id, sequence, event_type, occurred_at, node_id,
			attempt, idempotency_key, causation_event_id, entity_kind,
			entity_id, payload, checksum, content_hash, provenance,
			source_refs, actor_type, actor_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13,$14,$15,$16,$17)
	`, event.EventID, event.RunID, event.Sequence, event.EventType, event.OccurredAt,
		nullString(event.NodeID), nullableAttempt(event.Attempt), nullString(event.IdempotencyKey),
		nullString(event.CausationEventID), event.EntityKind, event.EntityID, payload,
		event.Checksum, provenance, sources, string(event.Trace.Actor.Type), nullString(event.Trace.Actor.ID))
	if err != nil {
		return RunEvent{}, fmt.Errorf("append run event: %w", err)
	}
	result, err := tx.tx.ExecContext(ctx, `
		UPDATE writing_runs
		SET last_event_sequence=$1, updated_at=NOW()
		WHERE run_id=$2 AND last_event_sequence=$3
	`, event.Sequence, event.RunID, previousSequence)
	if err != nil {
		return RunEvent{}, fmt.Errorf("advance run event projection: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return RunEvent{}, fmt.Errorf("%w: run event sequence advanced concurrently", ErrConflict)
	}
	return event, nil
}

func nullableAttempt(attempt int) any {
	if attempt == 0 {
		return nil
	}
	return attempt
}
