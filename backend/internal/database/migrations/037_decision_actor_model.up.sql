-- P0-1/P0-2/P0-3: Decision actor model + target statuses + agent run leases

-- ─── 1. Decision actor model ────────────────────────────
-- Replace the ambiguous decided_by/decided_by_type with a structured actor model.
-- actor_type:     human | agent | system
-- actor_user_id:  nullable UUID (required when actor_type = human)
-- actor_role:     nullable text (required when actor_type = agent)
-- actor_label:    text (human-readable: "张三", "研究Agent", "system")

ALTER TABLE editorial_decisions
    ADD COLUMN IF NOT EXISTS actor_type VARCHAR(16) NOT NULL DEFAULT 'system',
    ADD COLUMN IF NOT EXISTS actor_user_id UUID,
    ADD COLUMN IF NOT EXISTS actor_role VARCHAR(32),
    ADD COLUMN IF NOT EXISTS actor_label VARCHAR(128) NOT NULL DEFAULT '';

-- Backfill from existing decided_by_type / decided_by
UPDATE editorial_decisions SET
    actor_type = CASE
        WHEN decided_by_type IN ('human') THEN 'human'
        WHEN decided_by_type IN ('research_agent', 'writing_agent', 'review_agent') THEN 'agent'
        ELSE 'system'
    END,
    actor_user_id = CASE
        WHEN decided_by_type = 'human' AND decided_by ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        THEN decided_by::uuid
        ELSE NULL
    END,
    actor_role = CASE
        WHEN decided_by_type IN ('research_agent', 'writing_agent', 'review_agent')
        THEN decided_by_type
        ELSE NULL
    END,
    actor_label = COALESCE(decided_by, decided_by_type);

-- Constraint: human actors must have actor_user_id
ALTER TABLE editorial_decisions
    DROP CONSTRAINT IF EXISTS chk_actor_human_user_id;
ALTER TABLE editorial_decisions
    ADD CONSTRAINT chk_actor_human_user_id CHECK (
        actor_type != 'human' OR actor_user_id IS NOT NULL
    );

-- Constraint: agent actors must have actor_role
ALTER TABLE editorial_decisions
    DROP CONSTRAINT IF EXISTS chk_actor_agent_role;
ALTER TABLE editorial_decisions
    ADD CONSTRAINT chk_actor_agent_role CHECK (
        actor_type != 'agent' OR actor_role IS NOT NULL
    );

-- ─── 2. Decision target statuses ────────────────────────
-- Store the approve/reject target status directly on the Decision,
-- so ResolveDecision doesn't need a global switch to guess the next state.

ALTER TABLE editorial_decisions
    ADD COLUMN IF NOT EXISTS approve_target_status VARCHAR(32),
    ADD COLUMN IF NOT EXISTS reject_target_status VARCHAR(32);

-- Backfill known decision types with their default target statuses
UPDATE editorial_decisions SET
    approve_target_status = CASE type
        WHEN 'approve_topic' THEN 'research'
        WHEN 'select_angle' THEN 'writing'
        WHEN 'allow_rewrite' THEN 'review'
        WHEN 'publish' THEN 'published'
        WHEN 'accept_review' THEN 'pending_publish'
        WHEN 'escalate' THEN 'research'
        WHEN 'research_complete' THEN 'writing'
        WHEN 'draft_complete' THEN 'review'
        ELSE NULL
    END,
    reject_target_status = CASE type
        WHEN 'approve_topic' THEN 'draft'
        WHEN 'select_angle' THEN 'research'
        WHEN 'allow_rewrite' THEN 'writing'
        WHEN 'publish' THEN 'review'
        WHEN 'accept_review' THEN 'writing'
        WHEN 'escalate' THEN 'pending_approval'
        WHEN 'research_complete' THEN 'pending_approval'
        WHEN 'draft_complete' THEN 'research'
        ELSE NULL
    END
WHERE approve_target_status IS NULL;

-- ─── 3. Agent run leases ────────────────────────────────
-- Prevents concurrent execution of the same agent on the same task.
-- Lease is acquired before starting an agent and released on completion.
-- A stale lease (expired_at < NOW) can be reclaimed.

CREATE TABLE IF NOT EXISTS editorial_agent_leases (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id     UUID NOT NULL REFERENCES editorial_tasks(id) ON DELETE CASCADE,
    agent_role  VARCHAR(32) NOT NULL,  -- research_agent | writing_agent | review_agent
    status      VARCHAR(16) NOT NULL DEFAULT 'active',  -- active | completed | failed | expired
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expired_at  TIMESTAMPTZ NOT NULL,
    released_at TIMESTAMPTZ,
    metadata    JSONB DEFAULT '{}',

    CONSTRAINT uk_active_lease UNIQUE (task_id, agent_role)
);

-- This unique constraint only allows one active lease per task+role.
-- But we need it to be conditional — only for active leases.
-- PostgreSQL doesn't support conditional unique constraints directly,
-- so we use a partial unique index instead.
DROP INDEX IF EXISTS idx_active_lease_unique;
CREATE UNIQUE INDEX idx_active_lease_unique
    ON editorial_agent_leases (task_id, agent_role)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_agent_leases_task ON editorial_agent_leases (task_id);
CREATE INDEX IF NOT EXISTS idx_agent_leases_expired ON editorial_agent_leases (expired_at) WHERE status = 'active';
