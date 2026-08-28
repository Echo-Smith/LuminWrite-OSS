-- 090: Immutable writing outputs, quality evidence, and governed decisions.

CREATE TABLE writing_artifacts (
    artifact_id VARCHAR(128) NOT NULL,
    version INTEGER NOT NULL,
    run_id VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    plan_id VARCHAR(128) NOT NULL,
    plan_version INTEGER NOT NULL,
    node_id VARCHAR(128) NOT NULL,
    attempt INTEGER NOT NULL,
    idempotency_key VARCHAR(320) NOT NULL,
    output_key VARCHAR(128) NOT NULL,
    schema_version VARCHAR(32) NOT NULL DEFAULT 'lcp/1.0',
    artifact_type VARCHAR(64) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'provisional',
    content_hash VARCHAR(71) NOT NULL,
    media_type VARCHAR(128) NOT NULL,
    content_ref TEXT NOT NULL,
    parent_artifact_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    quality_report_id VARCHAR(128),
    quality_report_version INTEGER,
    snapshot_manifest_id VARCHAR(128),
    snapshot_version INTEGER,
    producer VARCHAR(128) NOT NULL,
    capability_version VARCHAR(64) NOT NULL,
    input_hashes JSONB NOT NULL DEFAULT '[]'::jsonb,
    model_ref VARCHAR(256),
    prompt_template_ref VARCHAR(256),
    provenance JSONB NOT NULL,
    source_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by_type VARCHAR(16) NOT NULL,
    created_by_id VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    committed_at TIMESTAMPTZ,
    superseded_at TIMESTAMPTZ,

    PRIMARY KEY (artifact_id, version),
    CONSTRAINT uk_writing_artifact_identity UNIQUE (run_id, artifact_id, version),
    CONSTRAINT uk_writing_artifact_attempt_output UNIQUE (run_id, node_id, attempt, output_key, version),
    CONSTRAINT uk_writing_artifact_idempotency_output UNIQUE (idempotency_key, output_key, version),
    CONSTRAINT fk_writing_artifact_plan FOREIGN KEY (run_id, plan_id, plan_version)
        REFERENCES writing_run_plans(run_id, plan_id, plan_version),
    CONSTRAINT chk_writing_artifact_id CHECK (artifact_id ~ '^art_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_artifact_version CHECK (version >= 1),
    CONSTRAINT chk_writing_artifact_attempt CHECK (attempt >= 1),
    CONSTRAINT chk_writing_artifact_idempotency CHECK (idempotency_key = run_id || ':' || node_id || ':' || attempt::TEXT),
    CONSTRAINT chk_writing_artifact_output_key CHECK (output_key ~ '^[A-Za-z0-9][A-Za-z0-9._-]*$'),
    CONSTRAINT chk_writing_artifact_schema CHECK (schema_version = 'lcp/1.0'),
    CONSTRAINT chk_writing_artifact_type CHECK (artifact_type IN (
        'brief', 'source_pack', 'research_note', 'claim_map', 'outline',
        'section_draft', 'full_draft', 'review_report', 'revision_set', 'quality_report'
    )),
    CONSTRAINT chk_writing_artifact_status CHECK (status IN (
        'provisional', 'generated', 'parsed', 'validated', 'committed', 'superseded'
    )),
    CONSTRAINT chk_writing_artifact_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_artifact_media_type CHECK (media_type IN ('application/json', 'text/markdown', 'text/plain')),
    CONSTRAINT chk_writing_artifact_parents CHECK (jsonb_typeof(parent_artifact_ids) = 'array'),
    CONSTRAINT chk_writing_artifact_input_hashes CHECK (jsonb_typeof(input_hashes) = 'array'),
    CONSTRAINT chk_writing_artifact_quality_ref CHECK (
        (quality_report_id IS NULL AND quality_report_version IS NULL)
        OR (quality_report_id IS NOT NULL AND quality_report_version >= 1)
    ),
    CONSTRAINT chk_writing_artifact_snapshot_ref CHECK (
        (snapshot_manifest_id IS NULL AND snapshot_version IS NULL)
        OR (snapshot_manifest_id IS NOT NULL AND snapshot_version >= 1)
    ),
    CONSTRAINT chk_writing_artifact_provenance CHECK (jsonb_typeof(provenance) = 'object'),
    CONSTRAINT chk_writing_artifact_sources CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_writing_artifact_actor CHECK (created_by_type IN (
        'user', 'system', 'model', 'worker', 'validator', 'policy', 'capability'
    )),
    CONSTRAINT chk_writing_artifact_commit CHECK (
        (status = 'committed' AND committed_at IS NOT NULL
            AND snapshot_manifest_id IS NOT NULL AND snapshot_version IS NOT NULL)
        OR status <> 'committed'
    )
);

CREATE TABLE writing_artifact_edges (
    run_id VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    child_artifact_id VARCHAR(128) NOT NULL,
    child_artifact_version INTEGER NOT NULL,
    parent_artifact_id VARCHAR(128) NOT NULL,
    parent_artifact_version INTEGER NOT NULL,
    relation VARCHAR(24) NOT NULL DEFAULT 'derived_from',
    ordinal INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (run_id, child_artifact_id, child_artifact_version, parent_artifact_id, parent_artifact_version, relation),
    CONSTRAINT fk_writing_artifact_edge_child FOREIGN KEY (run_id, child_artifact_id, child_artifact_version)
        REFERENCES writing_artifacts(run_id, artifact_id, version) ON DELETE CASCADE,
    CONSTRAINT fk_writing_artifact_edge_parent FOREIGN KEY (run_id, parent_artifact_id, parent_artifact_version)
        REFERENCES writing_artifacts(run_id, artifact_id, version),
    CONSTRAINT chk_writing_artifact_edge_relation CHECK (relation IN ('derived_from', 'validated_by', 'revised_from', 'assembled_from')),
    CONSTRAINT chk_writing_artifact_edge_ordinal CHECK (ordinal >= 0),
    CONSTRAINT chk_writing_artifact_edge_not_self CHECK (
        child_artifact_id <> parent_artifact_id OR child_artifact_version <> parent_artifact_version
    )
);

CREATE FUNCTION writing_reject_artifact_edge_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' AND NOT EXISTS (
        SELECT 1 FROM writing_runs WHERE run_id = OLD.run_id
    ) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'writing_artifact_edges is immutable; % is forbidden', TG_OP
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

CREATE TRIGGER trg_writing_artifact_edges_immutable
BEFORE UPDATE OR DELETE ON writing_artifact_edges
FOR EACH ROW EXECUTE FUNCTION writing_reject_artifact_edge_mutation();

CREATE TABLE writing_quality_reports (
    report_id VARCHAR(128) NOT NULL,
    report_version INTEGER NOT NULL,
    run_id VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    plan_id VARCHAR(128) NOT NULL,
    plan_version INTEGER NOT NULL,
    document_id VARCHAR(128) NOT NULL REFERENCES writing_documents(document_id) ON DELETE CASCADE,
    candidate_version_id VARCHAR(128) NOT NULL,
    validated_version_id VARCHAR(128),
    committed_version_id VARCHAR(128),
    schema_version VARCHAR(32) NOT NULL DEFAULT 'lcp/1.0',
    content_hash VARCHAR(71) NOT NULL,
    requested_assurance VARCHAR(16) NOT NULL,
    achieved_assurance VARCHAR(16) NOT NULL,
    assurance_satisfied BOOLEAN NOT NULL DEFAULT FALSE,
    quality_state VARCHAR(32) NOT NULL DEFAULT 'candidate_draft',
    version_consistent BOOLEAN NOT NULL DEFAULT FALSE,
    required_validators_satisfied BOOLEAN NOT NULL DEFAULT FALSE,
    blocker_count INTEGER NOT NULL DEFAULT 0,
    error_count INTEGER NOT NULL DEFAULT 0,
    open_error_count INTEGER NOT NULL DEFAULT 0,
    waived_error_count INTEGER NOT NULL DEFAULT 0,
    warning_count INTEGER NOT NULL DEFAULT 0,
    report_payload JSONB NOT NULL,
    snapshot_manifest_id VARCHAR(128),
    snapshot_version INTEGER,
    snapshot_persisted BOOLEAN NOT NULL DEFAULT FALSE,
    provenance JSONB NOT NULL,
    source_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by_type VARCHAR(16) NOT NULL,
    created_by_id VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (report_id, report_version),
    CONSTRAINT uk_writing_quality_identity UNIQUE (run_id, report_id, report_version),
    CONSTRAINT uk_writing_quality_run_version_hash UNIQUE (run_id, candidate_version_id, content_hash),
    CONSTRAINT fk_writing_quality_plan FOREIGN KEY (run_id, plan_id, plan_version)
        REFERENCES writing_run_plans(run_id, plan_id, plan_version),
    CONSTRAINT fk_writing_quality_document FOREIGN KEY (run_id, document_id)
        REFERENCES writing_runs(run_id, document_id),
    CONSTRAINT fk_writing_quality_candidate FOREIGN KEY (document_id, candidate_version_id)
        REFERENCES writing_document_versions(document_id, version_id),
    CONSTRAINT fk_writing_quality_validated FOREIGN KEY (document_id, validated_version_id)
        REFERENCES writing_document_versions(document_id, version_id),
    CONSTRAINT fk_writing_quality_committed FOREIGN KEY (document_id, committed_version_id)
        REFERENCES writing_document_versions(document_id, version_id),
    CONSTRAINT chk_writing_quality_report_id CHECK (report_id ~ '^qr_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_quality_report_version CHECK (report_version >= 1),
    CONSTRAINT chk_writing_quality_schema CHECK (schema_version = 'lcp/1.0'),
    CONSTRAINT chk_writing_quality_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_quality_requested CHECK (requested_assurance IN ('flexible', 'standard', 'sourced', 'strict')),
    CONSTRAINT chk_writing_quality_achieved CHECK (achieved_assurance IN ('flexible', 'standard', 'sourced', 'strict')),
    CONSTRAINT chk_writing_quality_state CHECK (quality_state IN (
        'candidate_draft', 'accepted_draft', 'verified_deliverable'
    )),
    CONSTRAINT chk_writing_quality_counts CHECK (
        blocker_count >= 0 AND error_count >= 0 AND open_error_count >= 0
        AND waived_error_count >= 0 AND warning_count >= 0
        AND open_error_count + waived_error_count <= error_count
    ),
    CONSTRAINT chk_writing_quality_snapshot_ref CHECK (
        (snapshot_manifest_id IS NULL AND snapshot_version IS NULL)
        OR (snapshot_manifest_id IS NOT NULL AND snapshot_version >= 1)
    ),
    CONSTRAINT chk_writing_quality_payload CHECK (jsonb_typeof(report_payload) = 'object'),
    CONSTRAINT chk_writing_quality_provenance CHECK (jsonb_typeof(provenance) = 'object'),
    CONSTRAINT chk_writing_quality_sources CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_writing_quality_actor CHECK (created_by_type IN (
        'user', 'system', 'model', 'worker', 'validator', 'policy', 'capability'
    )),
    CONSTRAINT chk_writing_quality_accepted CHECK (
        quality_state = 'candidate_draft' OR (blocker_count = 0 AND open_error_count = 0)
    ),
    CONSTRAINT chk_writing_quality_verified_shape CHECK (
        quality_state <> 'verified_deliverable' OR (
            blocker_count = 0 AND open_error_count = 0 AND waived_error_count = 0
            AND assurance_satisfied AND version_consistent AND required_validators_satisfied
            AND validated_version_id IS NOT NULL AND committed_version_id IS NOT NULL
            AND validated_version_id = committed_version_id
            AND snapshot_manifest_id IS NOT NULL AND snapshot_version IS NOT NULL
        )
    )
);

CREATE TABLE writing_decisions (
    decision_id VARCHAR(128) NOT NULL,
    decision_version INTEGER NOT NULL,
    run_id VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    plan_id VARCHAR(128),
    plan_version INTEGER,
    document_id VARCHAR(128),
    document_version_id VARCHAR(128),
    quality_report_id VARCHAR(128),
    quality_report_version INTEGER,
    finding_id VARCHAR(128),
    waiver_severity VARCHAR(16),
    schema_version VARCHAR(32) NOT NULL DEFAULT 'lcp/1.0',
    decision_type VARCHAR(32) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'recorded',
    reason_code VARCHAR(128) NOT NULL,
    summary TEXT NOT NULL,
    decision_payload JSONB NOT NULL,
    plan_hash VARCHAR(71),
    budget_snapshot JSONB,
    permission_snapshot JSONB,
    idempotency_key VARCHAR(320) NOT NULL UNIQUE,
    content_hash VARCHAR(71) NOT NULL,
    provenance JSONB NOT NULL,
    source_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_by_type VARCHAR(16) NOT NULL,
    created_by_id VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,

    PRIMARY KEY (decision_id, decision_version),
    CONSTRAINT uk_writing_decision_identity UNIQUE (run_id, decision_id, decision_version),
    CONSTRAINT fk_writing_decision_plan FOREIGN KEY (run_id, plan_id, plan_version)
        REFERENCES writing_run_plans(run_id, plan_id, plan_version),
    CONSTRAINT fk_writing_decision_document FOREIGN KEY (run_id, document_id)
        REFERENCES writing_runs(run_id, document_id),
    CONSTRAINT fk_writing_decision_version FOREIGN KEY (document_id, document_version_id)
        REFERENCES writing_document_versions(document_id, version_id),
    CONSTRAINT fk_writing_decision_quality FOREIGN KEY (run_id, quality_report_id, quality_report_version)
        REFERENCES writing_quality_reports(run_id, report_id, report_version) DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT chk_writing_decision_id CHECK (decision_id ~ '^decision_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_decision_version_number CHECK (decision_version >= 1),
    CONSTRAINT chk_writing_decision_schema CHECK (schema_version = 'lcp/1.0'),
    CONSTRAINT chk_writing_decision_type CHECK (decision_type IN (
        'strategy', 'plan_approval', 'user_control', 'conflict_resolution', 'waiver', 'degradation', 'acceptance'
    )),
    CONSTRAINT chk_writing_decision_status CHECK (status IN ('recorded', 'approved', 'rejected', 'expired', 'revoked')),
    CONSTRAINT chk_writing_decision_payload CHECK (jsonb_typeof(decision_payload) = 'object'),
    CONSTRAINT chk_writing_decision_hash CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_decision_plan_hash CHECK (plan_hash IS NULL OR plan_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_decision_provenance CHECK (jsonb_typeof(provenance) = 'object'),
    CONSTRAINT chk_writing_decision_sources CHECK (jsonb_typeof(source_refs) = 'array'),
    CONSTRAINT chk_writing_decision_actor CHECK (created_by_type IN (
        'user', 'system', 'model', 'worker', 'validator', 'policy', 'capability'
    )),
    CONSTRAINT chk_writing_decision_plan_scope CHECK (
        (plan_id IS NULL AND plan_version IS NULL) OR (plan_id IS NOT NULL AND plan_version >= 1)
    ),
    CONSTRAINT chk_writing_decision_version_scope CHECK (document_version_id IS NULL OR document_id IS NOT NULL),
    CONSTRAINT chk_writing_decision_quality_scope CHECK (
        (quality_report_id IS NULL AND quality_report_version IS NULL)
        OR (quality_report_id IS NOT NULL AND quality_report_version >= 1)
    ),
    CONSTRAINT chk_writing_decision_waiver_binding CHECK (
        decision_type <> 'waiver' OR (
            quality_report_id IS NOT NULL AND quality_report_version IS NOT NULL
            AND finding_id IS NOT NULL AND waiver_severity IN ('ERROR', 'WARNING')
        )
    ),
    CONSTRAINT chk_writing_decision_no_blocker_waiver CHECK (waiver_severity IS DISTINCT FROM 'BLOCKER'),
    CONSTRAINT chk_writing_decision_approval_binding CHECK (
        decision_type <> 'plan_approval' OR (
            plan_id IS NOT NULL AND plan_version IS NOT NULL AND plan_hash IS NOT NULL
            AND budget_snapshot IS NOT NULL AND jsonb_typeof(budget_snapshot) = 'object'
            AND permission_snapshot IS NOT NULL AND jsonb_typeof(permission_snapshot) = 'array'
        )
    )
);

ALTER TABLE writing_document_versions
    ADD CONSTRAINT fk_writing_document_version_quality
    FOREIGN KEY (quality_report_id, quality_report_version)
    REFERENCES writing_quality_reports(report_id, report_version)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE writing_artifacts
    ADD CONSTRAINT fk_writing_artifact_quality
    FOREIGN KEY (run_id, quality_report_id, quality_report_version)
    REFERENCES writing_quality_reports(run_id, report_id, report_version)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TRIGGER trg_writing_artifacts_immutable
BEFORE UPDATE ON writing_artifacts
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'artifact_id', 'version', 'run_id', 'plan_id', 'plan_version', 'node_id',
    'attempt', 'idempotency_key', 'output_key', 'schema_version', 'artifact_type',
    'content_hash', 'media_type', 'content_ref', 'parent_artifact_ids', 'producer',
    'capability_version', 'input_hashes', 'model_ref', 'prompt_template_ref',
    'provenance', 'source_refs', 'created_by_type', 'created_by_id', 'created_at'
);

CREATE TRIGGER trg_writing_quality_reports_immutable
BEFORE UPDATE ON writing_quality_reports
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'report_id', 'report_version', 'run_id', 'plan_id', 'plan_version',
    'document_id', 'candidate_version_id', 'validated_version_id',
    'committed_version_id', 'schema_version', 'content_hash',
    'requested_assurance', 'achieved_assurance', 'assurance_satisfied',
    'quality_state', 'version_consistent', 'required_validators_satisfied',
    'blocker_count', 'error_count', 'open_error_count', 'waived_error_count',
    'warning_count', 'report_payload', 'snapshot_manifest_id', 'snapshot_version',
    'provenance', 'source_refs', 'created_by_type', 'created_by_id', 'created_at'
);

CREATE TRIGGER trg_writing_decisions_immutable
BEFORE UPDATE ON writing_decisions
FOR EACH ROW EXECUTE FUNCTION writing_reject_immutable_columns(
    'decision_id', 'decision_version', 'run_id', 'plan_id', 'plan_version',
    'document_id', 'document_version_id', 'quality_report_id',
    'quality_report_version', 'finding_id', 'waiver_severity', 'schema_version',
    'decision_type', 'reason_code', 'summary', 'decision_payload', 'plan_hash',
    'budget_snapshot', 'permission_snapshot', 'idempotency_key', 'content_hash',
    'provenance', 'source_refs', 'created_by_type', 'created_by_id', 'created_at'
);

CREATE INDEX idx_writing_artifacts_run_node ON writing_artifacts(run_id, node_id, attempt);
CREATE INDEX idx_writing_artifacts_content_hash ON writing_artifacts(content_hash);
CREATE INDEX idx_writing_artifacts_status ON writing_artifacts(run_id, status, created_at);
CREATE INDEX idx_writing_artifact_edges_parent ON writing_artifact_edges(run_id, parent_artifact_id, parent_artifact_version);
CREATE INDEX idx_writing_quality_run_created ON writing_quality_reports(run_id, created_at DESC);
CREATE INDEX idx_writing_quality_document_version ON writing_quality_reports(document_id, candidate_version_id);
CREATE INDEX idx_writing_decisions_run_created ON writing_decisions(run_id, created_at DESC);
CREATE INDEX idx_writing_decisions_pending ON writing_decisions(run_id, decision_type, status)
    WHERE status IN ('recorded', 'approved');
