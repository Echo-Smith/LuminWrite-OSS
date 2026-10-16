/** 手写 SQL。Drizzle 只负责查询；测试默认跑在 PGlite 上。 */
export const migrationSql = `
CREATE TABLE IF NOT EXISTS users (
  id text PRIMARY KEY,
  display_name text NOT NULL,
  token_hash text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  singleton boolean NOT NULL DEFAULT true,
  CONSTRAINT uk_users_singleton UNIQUE (singleton)
);

CREATE TABLE IF NOT EXISTS documents (
  id text PRIMARY KEY,
  owner_id text NOT NULL,
  title text NOT NULL,
  genre text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'draft',
  head_version_id text,
  final_version_id text,
  style_ref text NOT NULL DEFAULT 'pkg:luminwrite/plain-professional@1.0.0',
  tier_ref text NOT NULL DEFAULT 'balanced',
  brief jsonb NOT NULL DEFAULT '{}'::jsonb,
  authored_by_me boolean NOT NULL DEFAULT false,
  created_via text NOT NULL DEFAULT 'ui',
  human_touched boolean NOT NULL DEFAULT false,
  derived_from_document_id text,
  derived_from_version_id text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_doc_derived ON documents (derived_from_document_id);

CREATE TABLE IF NOT EXISTS doc_versions (
  id text PRIMARY KEY,
  document_id text NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  seq integer NOT NULL,
  parent_id text,
  origin text NOT NULL,
  actor_type text NOT NULL,
  actor_id text NOT NULL,
  turn_id text,
  pi_task_id text,
  content jsonb NOT NULL,
  ops jsonb NOT NULL DEFAULT '[]'::jsonb,
  summary text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (document_id, seq)
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_ver_pi_task ON doc_versions (pi_task_id) WHERE pi_task_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS doc_blocks (
  document_id text NOT NULL,
  block_id text NOT NULL,
  type text NOT NULL,
  content_hash text NOT NULL,
  text text NOT NULL,
  order_key text NOT NULL,
  search_text text NOT NULL,
  PRIMARY KEY (document_id, block_id)
);

CREATE TABLE IF NOT EXISTS turns (
  id text PRIMARY KEY,
  document_id text NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  kind text NOT NULL,
  delivery text NOT NULL,
  status text NOT NULL,
  selection jsonb,
  scope text NOT NULL,
  base_version_id text,
  result_version_id text,
  apply_mode text NOT NULL,
  actor_type text NOT NULL,
  actor_id text NOT NULL,
  session_id text NOT NULL,
  idempotency_key text NOT NULL,
  pi_conversation_id text,
  pi_submission_id text,
  instruction text NOT NULL,
  error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (document_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS run_events (
  id text PRIMARY KEY,
  document_id text NOT NULL,
  seq integer NOT NULL,
  turn_id text,
  type text NOT NULL,
  payload jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (document_id, seq)
);

CREATE TABLE IF NOT EXISTS proposals (
  id text PRIMARY KEY,
  document_id text NOT NULL,
  turn_id text,
  base_version_id text NOT NULL,
  status text NOT NULL,
  pi_task_id text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_proposal_pi_task ON proposals (pi_task_id) WHERE pi_task_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS proposal_hunks (
  id text PRIMARY KEY,
  proposal_id text NOT NULL REFERENCES proposals(id) ON DELETE CASCADE,
  op jsonb NOT NULL,
  status text NOT NULL,
  user_edit jsonb
);

CREATE TABLE IF NOT EXISTS sessions (
  id text PRIMARY KEY,
  document_id text NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  kind text NOT NULL,
  base_version_id text NOT NULL REFERENCES doc_versions(id),
  pi_conversation_id text,
  created_by_type text NOT NULL,
  created_by_id text NOT NULL,
  status text NOT NULL,
  model_pin text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_session_main ON sessions (document_id) WHERE kind = 'main';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS model_pin text;

CREATE TABLE IF NOT EXISTS session_reads (
  session_id text NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  document_id text NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  PRIMARY KEY (session_id, document_id)
);

CREATE TABLE IF NOT EXISTS document_dossier (
  document_id text PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE,
  version integer NOT NULL,
  content jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_keys (
  id text PRIMARY KEY,
  user_id text NOT NULL,
  name text NOT NULL,
  prefix text NOT NULL,
  hash text NOT NULL,
  scopes text[] NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  revoked_at timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_api_keys_hash ON api_keys (hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys (user_id);

CREATE TABLE IF NOT EXISTS api_key_policies (
  api_key_id text PRIMARY KEY REFERENCES api_keys(id),
  max_concurrent_tasks integer,
  daily_cost_cap_usd double precision,
  allowed_tiers text[],
  default_apply_mode text NOT NULL DEFAULT 'auto',
  webhook_secret text
);

CREATE TABLE IF NOT EXISTS provider_credentials (
  id text PRIMARY KEY,
  user_id text NOT NULL,
  provider text NOT NULL,
  label text NOT NULL,
  base_url text,
  ciphertext bytea NOT NULL,
  nonce bytea NOT NULL,
  key_version integer NOT NULL,
  fingerprint text NOT NULL,
  status text NOT NULL DEFAULT 'active',
  last_verified_at timestamptz,
  last_error_code text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cred_user ON provider_credentials (user_id, provider);

CREATE TABLE IF NOT EXISTS model_registry (
  id text PRIMARY KEY,
  owner_id text NOT NULL,
  ref text NOT NULL,
  kind text NOT NULL,
  spec jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (owner_id, ref)
);

CREATE TABLE IF NOT EXISTS model_slot_bindings (
  owner_id text NOT NULL,
  slot text NOT NULL,
  target_ref text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (owner_id, slot)
);

CREATE TABLE IF NOT EXISTS usage_ledger (
  id text PRIMARY KEY,
  request_id text NOT NULL,
  model text NOT NULL,
  input_tokens integer NOT NULL,
  output_tokens integer NOT NULL,
  cost_micros integer NOT NULL,
  api_key_id text,
  api_task_id text,
  user_id text,
  credential_id text,
  model_ref text,
  tier_ref text,
  session_id text,
  turn_id text,
  cache_read_tokens integer,
  cache_write_tokens integer,
  price_known boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE usage_ledger ADD COLUMN IF NOT EXISTS user_id text;
ALTER TABLE usage_ledger ADD COLUMN IF NOT EXISTS credential_id text;
ALTER TABLE usage_ledger ADD COLUMN IF NOT EXISTS model_ref text;
ALTER TABLE usage_ledger ADD COLUMN IF NOT EXISTS tier_ref text;
ALTER TABLE usage_ledger ADD COLUMN IF NOT EXISTS session_id text;
ALTER TABLE usage_ledger ADD COLUMN IF NOT EXISTS turn_id text;
ALTER TABLE usage_ledger ADD COLUMN IF NOT EXISTS cache_read_tokens integer;
ALTER TABLE usage_ledger ADD COLUMN IF NOT EXISTS cache_write_tokens integer;
ALTER TABLE usage_ledger ADD COLUMN IF NOT EXISTS price_known boolean NOT NULL DEFAULT true;

CREATE OR REPLACE FUNCTION doc_versions_append_only()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'doc_versions is append-only';
END;
$$;

DROP TRIGGER IF EXISTS doc_versions_no_mutation ON doc_versions;
CREATE TRIGGER doc_versions_no_mutation
BEFORE UPDATE OR DELETE ON doc_versions
FOR EACH ROW EXECUTE FUNCTION doc_versions_append_only();
`;
