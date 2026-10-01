-- 122: Per-user BYOK model keys (个人模型服务).
-- 122: Per-user BYOK model keys.
--
-- user_model_keys —— 用户自带的模型端点与密钥：BYOK（Bring Your Own Key）的
--   存储层。解析语义是「用户配置优先、全局 model_configs 回退、env 兜底」：
--   LLMService.GetClientForUser 先按 (user_id, model_name) 精确匹配，再取该
--   用户 is_default 行，两者皆空才落入全局配置。密钥沿用 model_configs 的
--   内联加密约定（api_key_encrypted，应用层 AES-256-GCM），部署方未配置
--   API_KEY_ENCRYPTION_KEY 时 BYOK 写端点必须拒绝服务（handlers 层红线），
--   不接受明文落库。
--   purpose 列与 model_configs.purpose 同词表（generation/verification/
--   embedding），本期仅入库不参与路由；guest 用户被 rejectGuest 中间件挡在
--   BYOK 之外，user_id 因此始终是 users(id) 的 UUID。
-- user_model_keys -- user-supplied model endpoints and keys: the storage
--   layer for BYOK (Bring Your Own Key). Resolution semantics are
--   "user config first, global model_configs fallback, env as last resort":
--   LLMService.GetClientForUser matches (user_id, model_name) exactly first,
--   then the user's is_default row, and only falls through to the global
--   config when both are empty. Keys follow the model_configs inline
--   encryption convention (api_key_encrypted, application-layer AES-256-GCM);
--   when API_KEY_ENCRYPTION_KEY is unset the BYOK write endpoints must refuse
--   service (handlers-layer red line) — plaintext at rest is not accepted.
--   The purpose column shares model_configs.purpose's vocabulary
--   (generation/verification/embedding) but is storage-only this iteration;
--   guests are kept out of BYOK by the rejectGuest middleware, so user_id is
--   always a users(id) UUID.

CREATE TABLE user_model_keys (
    id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id            UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name               VARCHAR(128) NOT NULL DEFAULT '',
    provider           VARCHAR(64) NOT NULL,
    model_name         VARCHAR(128) NOT NULL,
    base_url           TEXT NOT NULL DEFAULT '',
    api_key_encrypted  TEXT NOT NULL DEFAULT '',
    max_tokens         INT NOT NULL DEFAULT 0,
    temperature        DOUBLE PRECISION NOT NULL DEFAULT 0.7,
    reasoning_effort   VARCHAR(16) NOT NULL DEFAULT '',
    custom_headers     JSONB,
    purpose            VARCHAR(32) NOT NULL DEFAULT 'generation',
    is_default         BOOLEAN NOT NULL DEFAULT FALSE,
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_user_model_keys_user_model UNIQUE (user_id, model_name),
    CONSTRAINT chk_user_model_keys_provider CHECK (provider ~ '^[A-Za-z0-9._-]{1,64}$'),
    CONSTRAINT chk_user_model_keys_model_name CHECK (model_name ~ '^[A-Za-z0-9._/:+-]{1,128}$'),
    CONSTRAINT chk_user_model_keys_purpose CHECK (purpose IN ('generation', 'verification', 'embedding'))
);

-- One default per user needs a partial unique index (Postgres does not allow
-- WHERE clauses on inline table constraints).
CREATE UNIQUE INDEX IF NOT EXISTS uk_user_model_keys_user_default
    ON user_model_keys (user_id) WHERE is_default;

CREATE INDEX IF NOT EXISTS idx_user_model_keys_user
    ON user_model_keys (user_id);

COMMENT ON TABLE user_model_keys IS 'Per-user BYOK model endpoints and encrypted keys; resolution order: user config -> global model_configs -> env fallback';
