-- WritingAgentBench Schema v1 data layer.
-- Legacy evaluation_* tables remain untouched and readable during shadow migration.

CREATE TABLE IF NOT EXISTS wabench_suites (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    suite_id            VARCHAR(128) NOT NULL UNIQUE,
    schema_version      VARCHAR(32) NOT NULL DEFAULT 'wabench.v1',
    version             VARCHAR(32) NOT NULL,
    name                VARCHAR(160) NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    partition           VARCHAR(32) NOT NULL,
    visibility          VARCHAR(16) NOT NULL,
    status              VARCHAR(32) NOT NULL DEFAULT 'draft',
    case_count          INTEGER NOT NULL DEFAULT 0 CHECK (case_count >= 0),
    coverage            JSONB NOT NULL DEFAULT '{}',
    privacy             JSONB NOT NULL DEFAULT '{}',
    content_hash        VARCHAR(71),
    legacy_set_id       UUID UNIQUE,
    migration_warnings  JSONB NOT NULL DEFAULT '[]',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wabench_suite_schema CHECK (schema_version = 'wabench.v1'),
    CONSTRAINT chk_wabench_suite_partition CHECK (partition IN (
        'development', 'public_holdout', 'private_holdout', 'red_team', 'live_probe'
    )),
    CONSTRAINT chk_wabench_suite_visibility CHECK (visibility IN ('public', 'private')),
    CONSTRAINT chk_wabench_suite_hash CHECK (content_hash IS NULL OR content_hash ~ '^sha256:[a-f0-9]{64}$')
);
CREATE INDEX IF NOT EXISTS idx_wabench_suites_partition ON wabench_suites (partition, status);

CREATE TABLE IF NOT EXISTS wabench_source_fixtures (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    fixture_id          VARCHAR(128) NOT NULL UNIQUE,
    schema_version      VARCHAR(32) NOT NULL DEFAULT 'wabench.v1',
    source_type         VARCHAR(40) NOT NULL,
    provider            VARCHAR(80),
    source_ref          TEXT,
    title               VARCHAR(300) NOT NULL,
    retrieved_at        TIMESTAMPTZ NOT NULL,
    content_hash        VARCHAR(71) NOT NULL,
    privacy_level       VARCHAR(16) NOT NULL,
    excerpt_storage     VARCHAR(24) NOT NULL DEFAULT 'hash_only',
    excerpt_text        TEXT,
    private_ref         TEXT,
    metadata            JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wabench_fixture_schema CHECK (schema_version = 'wabench.v1'),
    CONSTRAINT chk_wabench_fixture_type CHECK (source_type IN (
        'public_document', 'simulated_knowledge_base', 'enterprise_knowledge_base', 'user_material', 'live_web'
    )),
    CONSTRAINT chk_wabench_fixture_privacy CHECK (privacy_level IN ('public', 'redacted', 'private')),
    CONSTRAINT chk_wabench_fixture_storage CHECK (excerpt_storage IN ('inline_public', 'private_ref', 'hash_only')),
    CONSTRAINT chk_wabench_fixture_storage_payload CHECK (
        (excerpt_storage = 'inline_public' AND excerpt_text IS NOT NULL AND private_ref IS NULL) OR
        (excerpt_storage = 'private_ref' AND excerpt_text IS NULL AND private_ref IS NOT NULL) OR
        (excerpt_storage = 'hash_only' AND excerpt_text IS NULL AND private_ref IS NULL)
    ),
    CONSTRAINT chk_wabench_fixture_hash CHECK (content_hash ~ '^sha256:[a-f0-9]{64}$')
);

CREATE TABLE IF NOT EXISTS wabench_cases (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    case_id             VARCHAR(128) NOT NULL UNIQUE,
    suite_pk            UUID NOT NULL REFERENCES wabench_suites (id) ON DELETE CASCADE,
    schema_version      VARCHAR(32) NOT NULL DEFAULT 'wabench.v1',
    task_type           VARCHAR(24) NOT NULL,
    difficulty          VARCHAR(4) NOT NULL,
    input_storage       VARCHAR(24) NOT NULL DEFAULT 'hash_only',
    input_text          TEXT,
    input_ref           TEXT,
    input_hash          VARCHAR(71) NOT NULL,
    redacted_input_hash VARCHAR(71),
    context             JSONB NOT NULL DEFAULT '{}',
    source_mode         VARCHAR(16) NOT NULL DEFAULT 'none',
    source_fixture_refs TEXT[] NOT NULL DEFAULT '{}',
    expected_behavior   VARCHAR(16) NOT NULL DEFAULT 'answer',
    must_have           TEXT[] NOT NULL DEFAULT '{}',
    must_not_have       TEXT[] NOT NULL DEFAULT '{}',
    hard_gate_ids       TEXT[] NOT NULL DEFAULT '{}',
    rubric_weights      JSONB NOT NULL,
    capability_tags     TEXT[] NOT NULL DEFAULT '{}',
    risk_tags           TEXT[] NOT NULL DEFAULT '{}',
    rule_profile_refs   TEXT[] NOT NULL DEFAULT '{}',
    privacy_level       VARCHAR(16) NOT NULL,
    legacy_sample_id    UUID UNIQUE,
    legacy_score        JSONB,
    migration_warnings  JSONB NOT NULL DEFAULT '[]',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wabench_case_schema CHECK (schema_version = 'wabench.v1'),
    CONSTRAINT chk_wabench_case_task CHECK (task_type IN ('topic', 'writing', 'polish', 'dedupe', 'abnormal')),
    CONSTRAINT chk_wabench_case_difficulty CHECK (difficulty IN ('L1', 'L2', 'L3')),
    CONSTRAINT chk_wabench_case_storage CHECK (input_storage IN ('inline_public', 'private_ref', 'hash_only')),
    CONSTRAINT chk_wabench_case_storage_payload CHECK (
        (input_storage = 'inline_public' AND input_text IS NOT NULL AND input_ref IS NULL AND privacy_level = 'synthetic') OR
        (input_storage = 'private_ref' AND input_text IS NULL AND input_ref IS NOT NULL) OR
        (input_storage = 'hash_only' AND input_text IS NULL AND input_ref IS NULL)
    ),
    CONSTRAINT chk_wabench_case_source CHECK (source_mode IN ('none', 'frozen', 'live')),
    CONSTRAINT chk_wabench_case_behavior CHECK (expected_behavior IN ('answer', 'clarify', 'refuse', 'degrade')),
    CONSTRAINT chk_wabench_case_privacy CHECK (privacy_level IN ('synthetic', 'anonymized', 'private')),
    CONSTRAINT chk_wabench_case_hash CHECK (input_hash ~ '^sha256:[a-f0-9]{64}$'),
    CONSTRAINT chk_wabench_case_redacted_hash CHECK (redacted_input_hash IS NULL OR redacted_input_hash ~ '^sha256:[a-f0-9]{64}$'),
    CONSTRAINT chk_wabench_case_rubric_weights CHECK (
        jsonb_typeof(rubric_weights) = 'object' AND
        rubric_weights ?& ARRAY['taskCompliance', 'sourceFidelity', 'structureReasoning', 'styleConsistency', 'directUsability'] AND
        rubric_weights - ARRAY['taskCompliance', 'sourceFidelity', 'structureReasoning', 'styleConsistency', 'directUsability'] = '{}'::jsonb AND
        (rubric_weights->>'taskCompliance')::INTEGER +
        (rubric_weights->>'sourceFidelity')::INTEGER +
        (rubric_weights->>'structureReasoning')::INTEGER +
        (rubric_weights->>'styleConsistency')::INTEGER +
        (rubric_weights->>'directUsability')::INTEGER = 100
    )
);
CREATE INDEX IF NOT EXISTS idx_wabench_cases_suite ON wabench_cases (suite_pk, task_type, difficulty);
CREATE INDEX IF NOT EXISTS idx_wabench_cases_legacy ON wabench_cases (legacy_sample_id) WHERE legacy_sample_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS wabench_candidates (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    candidate_id        VARCHAR(128) NOT NULL UNIQUE,
    schema_version      VARCHAR(32) NOT NULL DEFAULT 'wabench.v1',
    name                VARCHAR(160) NOT NULL,
    prompt_hash         VARCHAR(71) NOT NULL,
    memory_hash         VARCHAR(71),
    model_manifest      JSONB NOT NULL,
    code_hash           VARCHAR(71) NOT NULL,
    tool_manifest       JSONB NOT NULL DEFAULT '{}',
    feature_flags       JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wabench_candidate_schema CHECK (schema_version = 'wabench.v1'),
    CONSTRAINT chk_wabench_candidate_prompt_hash CHECK (prompt_hash ~ '^sha256:[a-f0-9]{64}$'),
    CONSTRAINT chk_wabench_candidate_memory_hash CHECK (memory_hash IS NULL OR memory_hash ~ '^sha256:[a-f0-9]{64}$'),
    CONSTRAINT chk_wabench_candidate_code_hash CHECK (code_hash ~ '^sha256:[a-f0-9]{64}$')
);

CREATE TABLE IF NOT EXISTS wabench_runs (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    run_id              VARCHAR(128) NOT NULL UNIQUE,
    schema_version      VARCHAR(32) NOT NULL DEFAULT 'wabench.v1',
    suite_pk            UUID NOT NULL REFERENCES wabench_suites (id),
    candidate_pk        UUID NOT NULL REFERENCES wabench_candidates (id),
    adapter_id          VARCHAR(128) NOT NULL,
    runner_version      VARCHAR(64) NOT NULL,
    environment         VARCHAR(32) NOT NULL,
    traffic_type        VARCHAR(16) NOT NULL DEFAULT 'replay',
    evaluation_run_id   VARCHAR(128),
    status              VARCHAR(24) NOT NULL DEFAULT 'pending',
    total_cases         INTEGER NOT NULL DEFAULT 0,
    completed_cases     INTEGER NOT NULL DEFAULT 0,
    failed_cases        INTEGER NOT NULL DEFAULT 0,
    started_at          TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wabench_run_schema CHECK (schema_version = 'wabench.v1'),
    CONSTRAINT chk_wabench_run_traffic CHECK (traffic_type IN ('user', 'smoke', 'replay')),
    CONSTRAINT chk_wabench_run_counts CHECK (total_cases >= 0 AND completed_cases >= 0 AND failed_cases >= 0)
);
CREATE INDEX IF NOT EXISTS idx_wabench_runs_suite ON wabench_runs (suite_pk, created_at DESC);

CREATE TABLE IF NOT EXISTS wabench_outputs (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    output_id           VARCHAR(128) NOT NULL UNIQUE,
    schema_version      VARCHAR(32) NOT NULL DEFAULT 'wabench.v1',
    run_pk              UUID NOT NULL REFERENCES wabench_runs (id) ON DELETE CASCADE,
    case_pk             UUID NOT NULL REFERENCES wabench_cases (id),
    status              VARCHAR(32) NOT NULL,
    output_hash         VARCHAR(71) NOT NULL,
    text_storage        VARCHAR(24) NOT NULL DEFAULT 'hash_only',
    output_text         TEXT,
    private_ref         TEXT,
    failures            JSONB NOT NULL DEFAULT '[]',
    metrics             JSONB NOT NULL DEFAULT '{}',
    routing             JSONB NOT NULL DEFAULT '{}',
    trace_ref           VARCHAR(200),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_wabench_output_run_case UNIQUE (run_pk, case_pk),
    CONSTRAINT chk_wabench_output_schema CHECK (schema_version = 'wabench.v1'),
    CONSTRAINT chk_wabench_output_status CHECK (status IN ('complete', 'partial', 'generation_failed', 'tool_failed', 'capture_missing')),
    CONSTRAINT chk_wabench_output_storage CHECK (text_storage IN ('inline_public', 'private_ref', 'hash_only')),
    CONSTRAINT chk_wabench_output_storage_payload CHECK (
        (text_storage = 'inline_public' AND output_text IS NOT NULL AND private_ref IS NULL) OR
        (text_storage = 'private_ref' AND output_text IS NULL AND private_ref IS NOT NULL) OR
        (text_storage = 'hash_only' AND output_text IS NULL AND private_ref IS NULL)
    ),
    CONSTRAINT chk_wabench_output_hash CHECK (output_hash ~ '^sha256:[a-f0-9]{64}$')
);
CREATE INDEX IF NOT EXISTS idx_wabench_outputs_run ON wabench_outputs (run_pk, status);

CREATE TABLE IF NOT EXISTS wabench_checks (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    output_pk           UUID NOT NULL REFERENCES wabench_outputs (id) ON DELETE CASCADE,
    check_id            VARCHAR(128) NOT NULL,
    status              VARCHAR(24) NOT NULL,
    severity            VARCHAR(16) NOT NULL,
    evidence            JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_wabench_check_output UNIQUE (output_pk, check_id),
    CONSTRAINT chk_wabench_check_status CHECK (status IN ('pass', 'fail', 'unknown', 'not_applicable')),
    CONSTRAINT chk_wabench_check_severity CHECK (severity IN ('info', 'low', 'medium', 'high', 'critical'))
);

CREATE TABLE IF NOT EXISTS wabench_reviews (
    id                      UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    review_id               VARCHAR(128) NOT NULL UNIQUE,
    schema_version          VARCHAR(32) NOT NULL DEFAULT 'wabench.v1',
    output_pk               UUID NOT NULL REFERENCES wabench_outputs (id) ON DELETE CASCADE,
    reviewer_id             VARCHAR(128) NOT NULL,
    reviewer_role           VARCHAR(80) NOT NULL,
    reviewer_type           VARCHAR(16) NOT NULL,
    review_method           VARCHAR(32) NOT NULL,
    label_source            VARCHAR(80) NOT NULL,
    is_blind                BOOLEAN NOT NULL DEFAULT TRUE,
    task_compliance         SMALLINT NOT NULL,
    source_fidelity         SMALLINT NOT NULL,
    structure_reasoning     SMALLINT NOT NULL,
    style_consistency       SMALLINT NOT NULL,
    direct_usability        SMALLINT NOT NULL,
    acceptance_label        VARCHAR(24) NOT NULL DEFAULT 'unknown',
    modification_burden     SMALLINT,
    hard_failure_ids        TEXT[] NOT NULL DEFAULT '{}',
    primary_root_cause      VARCHAR(24),
    secondary_root_causes   TEXT[] NOT NULL DEFAULT '{}',
    evidence                JSONB NOT NULL DEFAULT '{}',
    reviewed_at             TIMESTAMPTZ NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wabench_review_schema CHECK (schema_version = 'wabench.v1'),
    CONSTRAINT chk_wabench_reviewer_type CHECK (reviewer_type IN ('human', 'model', 'rule')),
    CONSTRAINT chk_wabench_review_scores CHECK (
        task_compliance BETWEEN 1 AND 5 AND source_fidelity BETWEEN 1 AND 5 AND
        structure_reasoning BETWEEN 1 AND 5 AND style_consistency BETWEEN 1 AND 5 AND
        direct_usability BETWEEN 1 AND 5
    ),
    CONSTRAINT chk_wabench_review_acceptance CHECK (acceptance_label IN ('direct_use', 'light_edit', 'heavy_edit', 'reject', 'unknown')),
    CONSTRAINT chk_wabench_review_burden CHECK (modification_burden IS NULL OR modification_burden BETWEEN 0 AND 3),
    CONSTRAINT chk_wabench_review_root_cause CHECK (primary_root_cause IS NULL OR primary_root_cause IN (
        'input', 'retrieval', 'prompt', 'memory', 'tool', 'model', 'interaction'
    )),
    CONSTRAINT chk_wabench_review_secondary_root_causes CHECK (secondary_root_causes <@ ARRAY[
        'input', 'retrieval', 'prompt', 'memory', 'tool', 'model', 'interaction'
    ])
);
CREATE INDEX IF NOT EXISTS idx_wabench_reviews_output ON wabench_reviews (output_pk, reviewed_at DESC);

CREATE TABLE IF NOT EXISTS wabench_outcomes (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    event_id            VARCHAR(128) NOT NULL UNIQUE,
    schema_version      VARCHAR(32) NOT NULL DEFAULT 'wabench.v1',
    trace_ref           VARCHAR(200) NOT NULL,
    run_pk              UUID NOT NULL REFERENCES wabench_runs (id) ON DELETE CASCADE,
    case_pk             UUID NOT NULL REFERENCES wabench_cases (id),
    traffic_type        VARCHAR(16) NOT NULL,
    event_type          VARCHAR(24) NOT NULL,
    metadata            JSONB NOT NULL DEFAULT '{}',
    occurred_at         TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wabench_outcome_schema CHECK (schema_version = 'wabench.v1'),
    CONSTRAINT chk_wabench_outcome_traffic CHECK (traffic_type IN ('user', 'smoke', 'replay')),
    CONSTRAINT chk_wabench_outcome_type CHECK (event_type IN (
        'copy', 'download', 'regenerate', 'continue_edit', 'explicit_accept', 'explicit_reject'
    ))
);
CREATE INDEX IF NOT EXISTS idx_wabench_outcomes_trace ON wabench_outcomes (trace_ref, occurred_at);

CREATE TABLE IF NOT EXISTS wabench_gate_decisions (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    decision_id         VARCHAR(128) NOT NULL UNIQUE,
    schema_version      VARCHAR(32) NOT NULL DEFAULT 'wabench.v1',
    run_pk              UUID NOT NULL REFERENCES wabench_runs (id),
    decision            VARCHAR(24) NOT NULL,
    evidence            JSONB NOT NULL DEFAULT '{}',
    exceptions          JSONB NOT NULL DEFAULT '[]',
    rollback_conditions JSONB NOT NULL DEFAULT '[]',
    owner_ref           VARCHAR(128) NOT NULL,
    decided_at          TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_wabench_gate_schema CHECK (schema_version = 'wabench.v1'),
    CONSTRAINT chk_wabench_gate_decision CHECK (decision IN ('pass', 'fail', 'conditional', 'rollback'))
);
