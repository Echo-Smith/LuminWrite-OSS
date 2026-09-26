-- 121: 深度研究试点授权 + 合同草稿持久化幂等重放。
-- 121: Deep-research pilot entitlements + contract-draft replay persistence.
--
-- 两张表共同构成试点的授权与幂等基础设施（wp-pilot-launch）：
-- These two tables form the pilot's authorization and idempotency
-- infrastructure (wp-pilot-launch):
--
-- research_pilot_entitlements —— 试点精确范围审批记录：深度研究（research_review
--   编排）在试点期不对全员开放，只有在本表拥有未过期 (subject_id, scope) 行的
--   用户才能发起 draft/run。draft 端点与治理运行时的 research direct executor
--   双层都按同一口径查这张表（subject policy），查询错误一律 fail closed。
--   expires_at 支持自动到期的临时授权；granted_by/reason 保留人工审批痕迹。
-- research_pilot_entitlements -- precise per-subject approval records for the
--   pilot: deep research (research_review orchestration) is not open to every
--   user during the pilot; only subjects holding an unexpired
--   (subject_id, scope) row here may launch drafts/runs. Both the draft
--   endpoint and the governed runtime's research direct executors consult this
--   table through the same subject policy; a lookup error always fails closed.
--   expires_at supports auto-expiring grants; granted_by/reason keep the
--   manual-approval trail.
--
-- research_contract_drafts —— 合同草稿封存记录：draft 端点按
--   (document_id, input_hash) 持久化封存结果，重复草稿请求原样重放已存行，
--   保证 contract_hash / confirmed_hash 跨秒重试逐字节稳定（挂钟只影响首次
--   封存时的 attribution recorded_at，之后不再进入结果）。
-- research_contract_drafts -- sealed contract-draft records: the draft endpoint
--   persists its sealed output keyed by (document_id, input_hash); repeated
--   identical requests replay the stored row verbatim, keeping contract_hash /
--   confirmed_hash byte-identical across seconds (the wall clock only shapes
--   the first seal's attribution recorded_at, never a later response).

CREATE TABLE research_pilot_entitlements (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    subject_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope      VARCHAR(64) NOT NULL DEFAULT 'research_review',
    granted_by VARCHAR(128) NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    CONSTRAINT uk_research_pilot_subject_scope UNIQUE (subject_id, scope),
    CONSTRAINT chk_research_pilot_scope CHECK (scope ~ '^[A-Za-z0-9._-]{1,64}$')
);

CREATE INDEX IF NOT EXISTS idx_research_pilot_entitlements_scope
    ON research_pilot_entitlements (scope);

CREATE TABLE research_contract_drafts (
    id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    document_id        VARCHAR(128) NOT NULL REFERENCES writing_documents(document_id) ON DELETE CASCADE,
    input_hash         VARCHAR(64) NOT NULL,
    command            JSONB NOT NULL,
    contract           JSONB NOT NULL,
    confirmed_contract JSONB NOT NULL,
    contract_hash      VARCHAR(128) NOT NULL,
    confirmed_hash     VARCHAR(128) NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_research_contract_drafts UNIQUE (document_id, input_hash),
    CONSTRAINT chk_research_contract_drafts_input_hash CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT chk_research_contract_drafts_contract_hash CHECK (contract_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_research_contract_drafts_confirmed_hash CHECK (confirmed_hash ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT chk_research_contract_drafts_command CHECK (jsonb_typeof(command) = 'object'),
    CONSTRAINT chk_research_contract_drafts_contract CHECK (jsonb_typeof(contract) = 'object'),
    CONSTRAINT chk_research_contract_drafts_confirmed CHECK (jsonb_typeof(confirmed_contract) = 'object')
);
