-- 091: Runtime node attempts, append-only event ledger, durable snapshots,
-- and deferred delivery gates.

CREATE TABLE writing_node_attempts (
    attempt_id VARCHAR(160) PRIMARY KEY,
    run_id VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    plan_id VARCHAR(128) NOT NULL,
    plan_version INTEGER NOT NULL,
    node_id VARCHAR(128) NOT NULL,
    attempt INTEGER NOT NULL,
    idempotency_key VARCHAR(320) NOT NULL,
    node_kind VARCHAR(32) NOT NULL,
    capability_id VARCHAR(128) NOT NULL,
    capability_version VARCHAR(64) NOT NULL,
    executor_id VARCHAR(128) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    failure_path VARCHAR(24) NOT NULL,
    bounds_snapshot JSONB NOT NULL,
    checkpoint_ref VARCHAR(256),
    input_hash VARCHAR(71) NOT NULL,
    input_artifact_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    output_artifact_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    lease_owner VARCHAR(160),
    lease_token_hash VARCHAR(71),
    lease_expires_at TIMESTAMPTZ,
    heartbeat_at TIMESTAMPTZ,
    actual_cost_usd NUMERIC(14,6) NOT NULL DEFAULT 0,
    actual_input_tokens BIGINT NOT NULL DEFAULT 0,
    actual_output_tokens BIGINT NOT NULL DEFAULT 0,
    actual_duration_ms BIGINT NOT NULL DEFAULT 0,
    error_code VARCHAR(128),
    error_detail JSONB,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_writing_node_attempt UNIQUE (run_id, node_id, attempt),
    CONSTRAINT uk_writing_node_attempt_binding UNIQUE (run_id, node_id, attempt, idempotency_key),
    CONSTRAINT uk_writing_node_attempt_idempotency UNIQUE (idempotency_key),
    CONSTRAINT fk_writing_node_attempt_plan FOREIGN KEY (run_id, plan_id, plan_version)
        REFERENCES writing_run_plans(run_id, plan_id, plan_version),
    CONSTRAINT chk_writing_attempt_id CHECK (attempt_id ~ '^attempt_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_attempt_number CHECK (attempt >= 1),
    CONSTRAINT chk_writing_attempt_idempotency CHECK (idempotency_key = run_id || ':' || node_id || ':' || attempt::TEXT),
    CONSTRAINT chk_writing_attempt_kind CHECK (node_kind IN ('action', 'map', 'parallel', 'branch', 'validate', 'retry', 'refine')),
    CONSTRAINT chk_writing_attempt_status CHECK (status IN (
        'pending', 'leased', 'running', 'succeeded', 'failed', 'paused', 'cancelled', 'expired'
    )),
    CONSTRAINT chk_writing_attempt_failure_path CHECK (failure_path IN ('fail', 'pause', 'fallback', 'partial')),
    CONSTRAINT chk_writing_attempt_bounds CHECK (jsonb_typeof(bounds_snapshot) = 'object'),
    CONSTRAINT chk_writing_attempt_input_hash CHECK (input_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_attempt_inputs CHECK (jsonb_typeof(input_artifact_ids) = 'array'),
    CONSTRAINT chk_writing_attempt_outputs CHECK (jsonb_typeof(output_artifact_ids) = 'array'),
    CONSTRAINT chk_writing_attempt_lease_hash CHECK (lease_token_hash IS NULL OR lease_token_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_attempt_usage CHECK (
        actual_cost_usd >= 0 AND actual_input_tokens >= 0 AND actual_output_tokens >= 0 AND actual_duration_ms >= 0
    ),
    CONSTRAINT chk_writing_attempt_lease CHECK (
        status NOT IN ('leased', 'running') OR
        (lease_owner IS NOT NULL AND lease_token_hash IS NOT NULL AND lease_expires_at IS NOT NULL)
    ),
    CONSTRAINT chk_writing_attempt_completion CHECK (
        status NOT IN ('succeeded', 'failed', 'cancelled', 'expired') OR completed_at IS NOT NULL
    )
);

CREATE TABLE writing_run_events (
    event_id VARCHAR(128) PRIMARY KEY,
    run_id VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL,
    schema_version VARCHAR(32) NOT NULL DEFAULT 'lcp/1.0',
    event_type VARCHAR(64) NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    node_id VARCHAR(128),
    attempt INTEGER,
    idempotency_key VARCHAR(320),
    causation_event_id VARCHAR(128),
    entity_kind VARCHAR(32) NOT NULL,
    entity_id VARCHAR(160) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    checksum VARCHAR(71) NOT NULL,
    content_hash VARCHAR(71) NOT NULL,
    provenance JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    actor_type VARCHAR(16) NOT NULL,
    actor_id VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_writing_run_event_sequence UNIQUE (run_id, sequence),
    CONSTRAINT uk_writing_run_event_identity UNIQUE (run_id, event_id),
    CONSTRAINT fk_writing_run_event_attempt FOREIGN KEY (run_id, node_id, attempt, idempotency_key)
        REFERENCES writing_node_attempts(run_id, node_id, attempt, idempotency_key)
        DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT fk_writing_run_event_causation FOREIGN KEY (run_id, causation_event_id)
        REFERENCES writing_run_events(run_id, event_id) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT chk_writing_event_id CHECK (event_id ~ '^evt_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_event_sequence CHECK (sequence >= 1),
    CONSTRAINT chk_writing_event_schema CHECK (schema_version = 'lcp/1.0'),
    CONSTRAINT chk_writing_event_type CHECK (event_type IN (
        'run.planned', 'run.started', 'run.paused', 'run.resumed', 'run.cancelled',
        'run.completed', 'run.failed', 'node.started', 'node.completed', 'node.failed',
        'artifact.created', 'quality.updated', 'snapshot.created'
    )),
    CONSTRAINT chk_writing_event_node_attempt CHECK (
        (event_type IN ('node.started', 'node.completed', 'node.failed', 'artifact.created')
            AND node_id IS NOT NULL AND attempt >= 1 AND idempotency_key IS NOT NULL)
        OR
        (event_type NOT IN ('node.started', 'node.completed', 'node.failed', 'artifact.created')
            AND node_id IS NULL AND attempt IS NULL AND idempotency_key IS NULL)
    ),
    CONSTRAINT chk_writing_event_entity CHECK (entity_kind IN (
        'run', 'node', 'artifact', 'document_version', 'quality_report', 'snapshot'
    )),
    CONSTRAINT chk_writing_event_payload CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT chk_writing_event_checksum CHECK (checksum ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_event_content_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_event_hash_binding CHECK (checksum = content_hash),
    CONSTRAINT chk_writing_event_provenance CHECK (jsonb_typeof(provenance) = 'object'),
    CONSTRAINT chk_writing_event_sources CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_writing_event_actor CHECK (actor_type IN (
        'user', 'system', 'model', 'worker', 'validator', 'policy', 'capability'
    ))
);

CREATE UNIQUE INDEX uk_writing_run_event_idempotency
    ON writing_run_events(run_id, idempotency_key, event_type, entity_kind, entity_id)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE writing_snapshots (
    snapshot_id VARCHAR(128) NOT NULL,
    snapshot_version INTEGER NOT NULL,
    run_id VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    checkpoint_id VARCHAR(128) NOT NULL,
    ledger_sequence BIGINT NOT NULL,
    plan_id VARCHAR(128) NOT NULL,
    plan_version INTEGER NOT NULL,
    contract_id VARCHAR(128) NOT NULL,
    contract_version INTEGER NOT NULL,
    contract_hash VARCHAR(71) NOT NULL,
    document_id VARCHAR(128) NOT NULL,
    base_version_id VARCHAR(128),
    candidate_version_id VARCHAR(128),
    quality_report_id VARCHAR(128),
    quality_report_version INTEGER,
    schema_version VARCHAR(32) NOT NULL DEFAULT 'lcp/1.0',
    content_hash VARCHAR(71) NOT NULL,
    snapshot_status VARCHAR(24) NOT NULL DEFAULT 'pending',
    complete BOOLEAN NOT NULL DEFAULT FALSE,
    manifest_payload JSONB NOT NULL,
    storage_ref TEXT NOT NULL,
    provenance JSONB NOT NULL,
    source_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by_type VARCHAR(16) NOT NULL,
    created_by_id VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    persisted_at TIMESTAMPTZ,

    PRIMARY KEY (snapshot_id, snapshot_version),
    CONSTRAINT uk_writing_snapshot_identity UNIQUE (run_id, snapshot_id, snapshot_version),
    CONSTRAINT uk_writing_snapshot_checkpoint UNIQUE (run_id, checkpoint_id, snapshot_version),
    CONSTRAINT uk_writing_snapshot_ledger_position UNIQUE (run_id, ledger_sequence, snapshot_version),
    CONSTRAINT fk_writing_snapshot_event FOREIGN KEY (run_id, ledger_sequence)
        REFERENCES writing_run_events(run_id, sequence) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT fk_writing_snapshot_plan FOREIGN KEY (run_id, plan_id, plan_version)
        REFERENCES writing_run_plans(run_id, plan_id, plan_version),
    CONSTRAINT fk_writing_snapshot_contract FOREIGN KEY (run_id, contract_id, contract_version, contract_hash)
        REFERENCES writing_runs(run_id, contract_id, contract_version, contract_hash),
    CONSTRAINT fk_writing_snapshot_document FOREIGN KEY (run_id, document_id)
        REFERENCES writing_runs(run_id, document_id),
    CONSTRAINT fk_writing_snapshot_base_version FOREIGN KEY (document_id, base_version_id)
        REFERENCES writing_document_versions(document_id, version_id) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT fk_writing_snapshot_candidate_version FOREIGN KEY (document_id, candidate_version_id)
        REFERENCES writing_document_versions(document_id, version_id) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT chk_writing_snapshot_id CHECK (snapshot_id ~ '^snap_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_snapshot_version CHECK (snapshot_version >= 1),
    CONSTRAINT chk_writing_snapshot_checkpoint CHECK (checkpoint_id ~ '^[A-Za-z0-9][A-Za-z0-9._-]*$'),
    CONSTRAINT chk_writing_snapshot_sequence CHECK (ledger_sequence >= 1),
    CONSTRAINT chk_writing_snapshot_quality_ref CHECK (
        (quality_report_id IS NULL AND quality_report_version IS NULL)
        OR (quality_report_id IS NOT NULL AND quality_report_version >= 1)
    ),
    CONSTRAINT chk_writing_snapshot_schema CHECK (schema_version = 'lcp/1.0'),
    CONSTRAINT chk_writing_snapshot_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_snapshot_status CHECK (snapshot_status IN ('pending', 'persisted', 'failed', 'superseded')),
    CONSTRAINT chk_writing_snapshot_manifest CHECK (jsonb_typeof(manifest_payload) = 'object'),
    CONSTRAINT chk_writing_snapshot_provenance CHECK (jsonb_typeof(provenance) = 'object'),
    CONSTRAINT chk_writing_snapshot_sources CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_writing_snapshot_actor CHECK (created_by_type IN (
        'user', 'system', 'model', 'worker', 'validator', 'policy', 'capability'
    )),
    CONSTRAINT chk_writing_snapshot_complete CHECK (NOT complete OR snapshot_status = 'persisted'),
    CONSTRAINT chk_writing_snapshot_persisted CHECK (
        (snapshot_status = 'persisted' AND persisted_at IS NOT NULL) OR snapshot_status <> 'persisted'
    )
);

ALTER TABLE writing_artifacts
    ADD CONSTRAINT fk_writing_artifact_attempt
    FOREIGN KEY (run_id, node_id, attempt, idempotency_key)
    REFERENCES writing_node_attempts(run_id, node_id, attempt, idempotency_key)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE writing_snapshots
    ADD CONSTRAINT fk_writing_snapshot_quality
    FOREIGN KEY (run_id, quality_report_id, quality_report_version)
    REFERENCES writing_quality_reports(run_id, report_id, report_version)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE writing_quality_reports
    ADD CONSTRAINT fk_writing_quality_snapshot
    FOREIGN KEY (run_id, snapshot_manifest_id, snapshot_version)
    REFERENCES writing_snapshots(run_id, snapshot_id, snapshot_version)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE writing_document_versions
    ADD CONSTRAINT fk_writing_document_version_snapshot
    FOREIGN KEY (snapshot_manifest_id, snapshot_version)
    REFERENCES writing_snapshots(snapshot_id, snapshot_version)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE writing_artifacts
    ADD CONSTRAINT fk_writing_artifact_snapshot
    FOREIGN KEY (run_id, snapshot_manifest_id, snapshot_version)
    REFERENCES writing_snapshots(run_id, snapshot_id, snapshot_version)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE writing_runs
    ADD CONSTRAINT fk_writing_run_last_snapshot
    FOREIGN KEY (run_id, last_snapshot_id, last_snapshot_version)
    REFERENCES writing_snapshots(run_id, snapshot_id, snapshot_version)
    DEFERRABLE INITIALLY DEFERRED;

CREATE FUNCTION writing_reject_run_event_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' AND NOT EXISTS (
        SELECT 1 FROM writing_runs WHERE run_id = OLD.run_id
    ) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'writing_run_events is append-only; % is forbidden', TG_OP
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

CREATE TRIGGER trg_writing_run_events_append_only
BEFORE UPDATE OR DELETE ON writing_run_events
FOR EACH ROW EXECUTE FUNCTION writing_reject_run_event_mutation();

CREATE TRIGGER trg_writing_snapshots_immutable
BEFORE UPDATE ON writing_snapshots
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'snapshot_id', 'snapshot_version', 'run_id', 'checkpoint_id', 'ledger_sequence',
    'plan_id', 'plan_version', 'contract_id', 'contract_version', 'contract_hash',
    'document_id', 'base_version_id', 'candidate_version_id', 'quality_report_id',
    'quality_report_version', 'schema_version', 'content_hash', 'snapshot_status',
    'complete', 'manifest_payload', 'storage_ref', 'provenance', 'source_refs',
    'created_by_type', 'created_by_id', 'created_at', 'persisted_at'
);

CREATE FUNCTION writing_enforce_quality_delivery_gate()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    snapshot_row writing_snapshots%ROWTYPE;
    requested_rank INTEGER;
    achieved_rank INTEGER;
BEGIN
    IF NEW.quality_state = 'candidate_draft' THEN
        RETURN NULL;
    END IF;

    SELECT * INTO snapshot_row
    FROM writing_snapshots
    WHERE run_id = NEW.run_id
      AND snapshot_id = NEW.snapshot_manifest_id
      AND snapshot_version = NEW.snapshot_version;

    IF NOT FOUND
       OR snapshot_row.snapshot_status <> 'persisted'
       OR NOT snapshot_row.complete
       OR snapshot_row.persisted_at IS NULL
       OR snapshot_row.quality_report_id IS DISTINCT FROM NEW.report_id
       OR snapshot_row.quality_report_version IS DISTINCT FROM NEW.report_version
       OR snapshot_row.document_id <> NEW.document_id
       OR snapshot_row.candidate_version_id IS DISTINCT FROM NEW.candidate_version_id THEN
        RAISE EXCEPTION 'quality report % v% lacks a complete, mutually-bound snapshot', NEW.report_id, NEW.report_version
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    requested_rank := CASE NEW.requested_assurance WHEN 'flexible' THEN 1 WHEN 'standard' THEN 2 WHEN 'sourced' THEN 3 WHEN 'strict' THEN 4 END;
    achieved_rank := CASE NEW.achieved_assurance WHEN 'flexible' THEN 1 WHEN 'standard' THEN 2 WHEN 'sourced' THEN 3 WHEN 'strict' THEN 4 END;

    IF NEW.blocker_count <> 0 OR NEW.open_error_count <> 0 OR NOT NEW.version_consistent
       OR NOT NEW.assurance_satisfied OR achieved_rank < requested_rank THEN
        RAISE EXCEPTION 'quality report % v% does not satisfy accepted-draft gates', NEW.report_id, NEW.report_version
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;

    IF NEW.quality_state = 'verified_deliverable' THEN
        IF NOT NEW.required_validators_satisfied
           OR NEW.validated_version_id IS NULL
           OR NEW.committed_version_id IS NULL
           OR NEW.validated_version_id <> NEW.committed_version_id
           OR snapshot_row.candidate_version_id IS DISTINCT FROM NEW.committed_version_id
           OR NEW.waived_error_count <> 0
           OR EXISTS (
                SELECT 1 FROM writing_decisions d
                WHERE d.run_id = NEW.run_id
                  AND d.quality_report_id = NEW.report_id
                  AND d.quality_report_version = NEW.report_version
                  AND d.decision_type = 'waiver'
                  AND d.waiver_severity = 'ERROR'
                  AND d.status IN ('recorded', 'approved')
           ) THEN
            RAISE EXCEPTION 'quality report % v% does not satisfy verified-deliverable gates', NEW.report_id, NEW.report_version
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
    END IF;

    RETURN NULL;
END;
$$;

CREATE FUNCTION writing_enforce_snapshot_quality_binding()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.quality_report_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM writing_quality_reports q
        WHERE q.run_id = NEW.run_id
          AND q.report_id = NEW.quality_report_id
          AND q.report_version = NEW.quality_report_version
          AND q.snapshot_manifest_id = NEW.snapshot_id
          AND q.snapshot_version = NEW.snapshot_version
          AND q.document_id = NEW.document_id
          AND q.candidate_version_id IS NOT DISTINCT FROM NEW.candidate_version_id
    ) THEN
        RAISE EXCEPTION 'snapshot % v% is not mutually bound to its quality report', NEW.snapshot_id, NEW.snapshot_version
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE FUNCTION writing_enforce_document_quality_gate()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.quality_report_id IS NULL AND NEW.snapshot_manifest_id IS NULL THEN
        IF NEW.quality_state <> 'candidate_draft' THEN
            RAISE EXCEPTION 'document version % requires quality report and snapshot', NEW.version_id
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
        RETURN NULL;
    END IF;

    IF NEW.quality_report_id IS NULL OR NEW.snapshot_manifest_id IS NULL OR NOT EXISTS (
        SELECT 1
        FROM writing_quality_reports q
        JOIN writing_snapshots s
          ON s.run_id = q.run_id
         AND s.snapshot_id = q.snapshot_manifest_id
         AND s.snapshot_version = q.snapshot_version
        WHERE q.report_id = NEW.quality_report_id
          AND q.report_version = NEW.quality_report_version
          AND q.document_id = NEW.document_id
          AND q.candidate_version_id = NEW.version_id
          AND s.snapshot_id = NEW.snapshot_manifest_id
          AND s.snapshot_version = NEW.snapshot_version
          AND s.document_id = NEW.document_id
          AND s.candidate_version_id = NEW.version_id
          AND s.snapshot_status = 'persisted'
          AND s.complete
          AND s.persisted_at IS NOT NULL
          AND (NEW.quality_state = 'candidate_draft' OR q.quality_state IN (NEW.quality_state, 'verified_deliverable'))
    ) THEN
        RAISE EXCEPTION 'document version % has an inconsistent quality/snapshot binding', NEW.version_id
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE FUNCTION writing_enforce_artifact_commit_gate()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.status = 'committed' AND NOT EXISTS (
        SELECT 1 FROM writing_snapshots s
        WHERE s.run_id = NEW.run_id
          AND s.snapshot_id = NEW.snapshot_manifest_id
          AND s.snapshot_version = NEW.snapshot_version
          AND s.snapshot_status = 'persisted'
          AND s.complete
          AND s.persisted_at IS NOT NULL
          AND (NEW.quality_report_id IS NULL OR (
              s.quality_report_id = NEW.quality_report_id
              AND s.quality_report_version = NEW.quality_report_version
          ))
    ) THEN
        RAISE EXCEPTION 'committed artifact % v% lacks a complete snapshot', NEW.artifact_id, NEW.version
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE FUNCTION writing_enforce_run_projection()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_run_id VARCHAR(128);
    projected_sequence BIGINT;
    ledger_sequence BIGINT;
BEGIN
    target_run_id := NEW.run_id;
    SELECT r.last_event_sequence, COALESCE(MAX(e.sequence), 0)
      INTO projected_sequence, ledger_sequence
    FROM writing_runs r
    LEFT JOIN writing_run_events e ON e.run_id = r.run_id
    WHERE r.run_id = target_run_id
    GROUP BY r.last_event_sequence;

    IF FOUND AND projected_sequence <> ledger_sequence THEN
        RAISE EXCEPTION 'run % event projection % does not match ledger %', target_run_id, projected_sequence, ledger_sequence
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE FUNCTION writing_enforce_completed_run_gate()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.status = 'completed' AND NOT EXISTS (
        SELECT 1 FROM writing_snapshots s
        WHERE s.run_id = NEW.run_id
          AND s.snapshot_id = NEW.last_snapshot_id
          AND s.snapshot_version = NEW.last_snapshot_version
          AND s.snapshot_status = 'persisted'
          AND s.complete
          AND s.persisted_at IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'completed run % lacks a complete last snapshot', NEW.run_id
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER trg_writing_quality_delivery_gate
AFTER INSERT OR UPDATE ON writing_quality_reports
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION writing_enforce_quality_delivery_gate();

CREATE CONSTRAINT TRIGGER trg_writing_snapshot_quality_binding
AFTER INSERT OR UPDATE ON writing_snapshots
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION writing_enforce_snapshot_quality_binding();

CREATE CONSTRAINT TRIGGER trg_writing_document_quality_gate
AFTER INSERT OR UPDATE ON writing_document_versions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION writing_enforce_document_quality_gate();

CREATE CONSTRAINT TRIGGER trg_writing_artifact_commit_gate
AFTER INSERT OR UPDATE ON writing_artifacts
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION writing_enforce_artifact_commit_gate();

CREATE CONSTRAINT TRIGGER trg_writing_run_projection_from_event
AFTER INSERT ON writing_run_events
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION writing_enforce_run_projection();

CREATE CONSTRAINT TRIGGER trg_writing_run_projection_from_run
AFTER INSERT OR UPDATE OF last_event_sequence ON writing_runs
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION writing_enforce_run_projection();

CREATE CONSTRAINT TRIGGER trg_writing_completed_run_gate
AFTER INSERT OR UPDATE ON writing_runs
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION writing_enforce_completed_run_gate();

CREATE INDEX idx_writing_attempts_ready ON writing_node_attempts(run_id, status, created_at)
    WHERE status IN ('pending', 'paused', 'expired');
CREATE INDEX idx_writing_attempts_lease ON writing_node_attempts(status, lease_expires_at)
    WHERE status IN ('leased', 'running');
CREATE INDEX idx_writing_run_events_run_sequence ON writing_run_events(run_id, sequence DESC);
CREATE INDEX idx_writing_run_events_entity ON writing_run_events(entity_kind, entity_id, occurred_at);
CREATE INDEX idx_writing_snapshots_run_sequence ON writing_snapshots(run_id, ledger_sequence DESC);
CREATE INDEX idx_writing_snapshots_content_hash ON writing_snapshots(content_hash);
