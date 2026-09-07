-- 107: Research-review workflow (T02) — persistent research subtasks, the two
-- human confirmation gates, and the CHECK extensions the new path needs
-- (design.md §5/§6). Incremental only: no existing table is rewritten.

-- ── writing_research_tasks ──────────────────────────────────────────────────
-- One row per (run, node, task_key, input_hash) sub-task of a research node
-- (research_discover / research_read). task_key is phase + paper identity or
-- query ordinal; input_hash covers research question, selection policy
-- version, source content hash, model/provider, prompt, reader/parser
-- versions, and block-selection policy (design.md §6). First-version reuse is
-- owner-scoped; there is no cross-tenant global cache.
CREATE TABLE writing_research_tasks (
    id                 BIGSERIAL PRIMARY KEY,
    owner_user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    run_id             VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    node_id            VARCHAR(128) NOT NULL,
    task_key           VARCHAR(256) NOT NULL,
    phase              VARCHAR(64) NOT NULL,
    input_hash         VARCHAR(71) NOT NULL,
    status             VARCHAR(24) NOT NULL DEFAULT 'pending',
    attempt            INTEGER NOT NULL DEFAULT 0,
    lease_owner        VARCHAR(160),
    lease_expires_at   TIMESTAMPTZ,
    output_artifact_id VARCHAR(128),
    output_hash        VARCHAR(71),
    error_code         VARCHAR(128),
    retry_after        TIMESTAMPTZ,
    usage_json         JSONB,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_writing_research_task UNIQUE (run_id, node_id, task_key, input_hash),
    CONSTRAINT chk_writing_research_task_node CHECK (node_id <> '' AND task_key <> '' AND phase <> ''),
    CONSTRAINT chk_writing_research_task_input_hash CHECK (input_hash ~ '^sha256:[0-9a-f]{64}$'),
    -- outcome_unknown means the upstream call's result is unknown (connection
    -- lost after the worker may have accepted it): retrying may re-bill, so
    -- the status is kept inspectable instead of being silently recycled.
    CONSTRAINT chk_writing_research_task_status CHECK (status IN (
        'pending', 'claimed', 'running', 'succeeded', 'failed',
        'outcome_unknown', 'cancelled'
    )),
    CONSTRAINT chk_writing_research_task_attempt CHECK (attempt >= 0),
    -- A leased task always names its worker and a live lease; succeeded tasks
    -- always carry the content hash of their committed output so cache reuse
    -- can verify the artifact before reusing it (design.md §6 fencing).
    CONSTRAINT chk_writing_research_task_lease CHECK (
        status NOT IN ('claimed', 'running') OR
        (lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
    ),
    CONSTRAINT chk_writing_research_task_output CHECK (
        status <> 'succeeded' OR
        (output_artifact_id IS NOT NULL AND output_hash IS NOT NULL
         AND output_hash ~ '^sha256:[0-9a-f]{64}$')
    ),
    CONSTRAINT chk_writing_research_task_usage CHECK (
        usage_json IS NULL OR jsonb_typeof(usage_json) = 'object'
    )
);

CREATE INDEX idx_writing_research_tasks_owner_run ON writing_research_tasks(owner_user_id, run_id, updated_at DESC);
CREATE INDEX idx_writing_research_tasks_node ON writing_research_tasks(run_id, node_id, status);
CREATE INDEX idx_writing_research_tasks_lease ON writing_research_tasks(status, lease_expires_at)
    WHERE status IN ('claimed', 'running');

-- ── writing_gate_decisions ──────────────────────────────────────────────────
-- The persistent waiting-gate record AND its decision (design.md §6): a row
-- is created 'pending' when the orchestrator arrives at the gate node and is
-- flipped to 'approved' inside the atomic decision transaction. One row per
-- (run, node, plan_version) — an outline revision mutates the same row
-- (input_* becomes the revised outline ref, revision increments) and never
-- spawns extra rows, so the UNIQUE below is exactly the one-decision rule.
CREATE TABLE writing_gate_decisions (
    gate_id               VARCHAR(128) NOT NULL,
    run_id                VARCHAR(128) NOT NULL REFERENCES writing_runs(run_id) ON DELETE CASCADE,
    node_id               VARCHAR(128) NOT NULL,
    plan_id               VARCHAR(128) NOT NULL,
    plan_version          INTEGER NOT NULL,
    plan_hash             VARCHAR(71) NOT NULL,
    gate_kind             VARCHAR(32) NOT NULL DEFAULT 'evidence',
    input_artifact_id     VARCHAR(128),
    input_artifact_version INTEGER,
    input_hash            VARCHAR(71),
    revision              INTEGER NOT NULL DEFAULT 1,
    status                VARCHAR(24) NOT NULL DEFAULT 'pending',
    decision              VARCHAR(24),
    decision_artifact_id  VARCHAR(128),
    owner_user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    operation             VARCHAR(32) NOT NULL DEFAULT 'decision',
    idempotency_key       VARCHAR(320),
    request_hash          VARCHAR(71),
    actor_id              VARCHAR(128),
    decided_at            TIMESTAMPTZ,
    -- Outline-revision request identity (decisions carry their own in
    -- idempotency_key/request_hash): the same key+body replay must return the
    -- already-saved revision instead of bumping the gate twice.
    last_revision_key     VARCHAR(320),
    last_revision_hash    VARCHAR(71),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (gate_id),
    CONSTRAINT uk_writing_gate_decision UNIQUE (run_id, node_id, plan_version),
    -- Idempotency keys are scoped to owner + operation (run_id:gate:<gate_id>:
    -- <operation>:<owner_user_id>:<client_key>), so one user's replayed
    -- confirm can never collide with another actor's request.
    CONSTRAINT uk_writing_gate_idempotency UNIQUE (idempotency_key),
    CONSTRAINT uk_writing_gate_revision_key UNIQUE (last_revision_key),
    CONSTRAINT chk_writing_gate_id CHECK (gate_id ~ '^gate_[A-Za-z0-9_-]+$'),
    CONSTRAINT chk_writing_gate_node CHECK (node_id <> '' AND plan_id <> ''),
    CONSTRAINT chk_writing_gate_plan_version CHECK (plan_version >= 1),
    CONSTRAINT chk_writing_gate_plan_hash CHECK (plan_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_writing_gate_kind CHECK (gate_kind IN ('evidence', 'outline')),
    CONSTRAINT chk_writing_gate_revision CHECK (revision >= 1),
    CONSTRAINT chk_writing_gate_status CHECK (status IN ('pending', 'approved')),
    CONSTRAINT chk_writing_gate_decision CHECK (decision IS NULL OR decision = 'approve'),
    -- Only decided rows may carry an idempotency key, a request hash, an
    -- actor, and a decided_at; pending rows must not.
    CONSTRAINT chk_writing_gate_decided_binding CHECK (
        (status = 'approved' AND idempotency_key IS NOT NULL AND request_hash IS NOT NULL
         AND actor_id IS NOT NULL AND decided_at IS NOT NULL AND operation = 'decision')
        OR (status = 'pending' AND idempotency_key IS NULL AND request_hash IS NULL
            AND actor_id IS NULL AND decided_at IS NULL
            AND operation IN ('pending', 'outline_revision'))
    ),
    CONSTRAINT chk_writing_gate_input CHECK (
        (status = 'pending' AND input_artifact_id IS NOT NULL
         AND input_artifact_version >= 1 AND input_hash IS NOT NULL
         AND input_hash ~ '^sha256:[0-9a-f]{64}$')
        OR status = 'approved'
    ),
    CONSTRAINT chk_writing_gate_request_hash CHECK (
        request_hash IS NULL OR request_hash ~ '^sha256:[0-9a-f]{64}$'
    ),
    CONSTRAINT chk_writing_gate_revision_binding CHECK (
        (status = 'pending' AND last_revision_key IS NULL AND last_revision_hash IS NULL)
        OR (status = 'pending' AND last_revision_key IS NOT NULL AND last_revision_hash IS NOT NULL
            AND last_revision_hash ~ '^sha256:[0-9a-f]{64}$')
        OR (status = 'approved' AND last_revision_key IS NULL)
    )
);

CREATE INDEX idx_writing_gate_decisions_run ON writing_gate_decisions(run_id, status);
CREATE INDEX idx_writing_gate_decisions_owner ON writing_gate_decisions(owner_user_id, run_id);

-- ── CHECK extensions for the new contract version, node kind, and events ──
-- writing_contracts.schema_version: allow lcp/1.1 (research_review contracts,
-- design.md §6; the v1.0 rows and their hashes are untouched).
ALTER TABLE writing_contracts
    DROP CONSTRAINT chk_writing_contract_schema;

ALTER TABLE writing_contracts
    ADD CONSTRAINT chk_writing_contract_schema CHECK (schema_version IN ('lcp/1.0', 'lcp/1.1'));

-- writing_node_attempts.node_kind: 'human_gate' is already permitted by
-- migration 092 (chk_writing_attempt_kind), so no change is required here.

-- writing_run_events.event_type: add the research-review event family. The
-- constraint is an enum list (095 rewrote it), so 107 rewrites it again with
-- the three new members appended; existing event rows are unaffected.
ALTER TABLE writing_run_events
    DROP CONSTRAINT chk_writing_event_type,
    DROP CONSTRAINT chk_writing_event_entity;

ALTER TABLE writing_run_events
    ADD CONSTRAINT chk_writing_event_type CHECK (event_type IN (
        'run.planned', 'run.started', 'run.paused', 'run.resumed', 'run.cancelled',
        'run.completed', 'run.failed', 'node.started', 'node.completed', 'node.failed',
        'artifact.created', 'quality.updated', 'snapshot.created', 'document.committed',
        'run.transitioned', 'run.transition_rejected', 'node.paused', 'node.cancelled',
        'runtime.route_decided', 'runtime.execution_observed', 'runtime.shadow_compared',
        'research.progress', 'gate.pending', 'gate.decided'
    )),
    ADD CONSTRAINT chk_writing_event_entity CHECK (entity_kind IN (
        'run', 'node', 'artifact', 'document_version', 'quality_report',
        'snapshot', 'rollout_evidence', 'research_gate'
    ));

-- research.progress / gate.pending / gate.decided are run-scoped ledger
-- events (no node attempt identity): they fall into chk_writing_event_node_
-- attempt's third branch (node_id/attempt/idempotency_key all NULL), which
-- needs no change.

-- writing_artifacts.artifact_type: the server-created evidence_approval
-- decision artifact (contracts.md §2) must be storable. research outline
-- revisions reuse the existing 'outline' type; pack/candidates types land
-- with T05/T06 migrations.
ALTER TABLE writing_artifacts
    DROP CONSTRAINT chk_writing_artifact_type;

ALTER TABLE writing_artifacts
    ADD CONSTRAINT chk_writing_artifact_type CHECK (artifact_type IN (
        'contract', 'materials', 'brief', 'source_pack', 'research_note',
        'claim_map', 'outline', 'section_draft', 'full_draft',
        'review_report', 'revision_set', 'quality_report',
        'evidence_report', 'fact_report', 'evidence_approval'
    ));
