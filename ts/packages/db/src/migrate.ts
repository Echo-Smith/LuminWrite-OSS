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
  status text NOT NULL DEFAULT 'active',
  head_version_id text,
  style_ref text NOT NULL DEFAULT 'pkg:luminwrite/plain-professional@1.0.0',
  tier_ref text NOT NULL DEFAULT 'balanced',
  brief jsonb NOT NULL DEFAULT '{}'::jsonb,
  authored_by_me boolean NOT NULL DEFAULT false,
  created_via text NOT NULL DEFAULT 'ui',
  human_touched boolean NOT NULL DEFAULT false,
  derived_from_document_id text,
  derived_from_version_id text,
  saved_at timestamptz,
  saved_version_id text,
  discarded_at timestamptz,
  kb_excluded boolean NOT NULL DEFAULT false,
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
  api_key_id text,
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
  model_ref text,
  tier_ref text,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (document_id, idempotency_key)
);

ALTER TABLE turns ADD COLUMN IF NOT EXISTS model_ref text;
ALTER TABLE turns ADD COLUMN IF NOT EXISTS tier_ref text;

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
  discarded_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_session_main ON sessions (document_id) WHERE kind = 'main';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS model_pin text;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS discarded_at timestamptz;

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

ALTER TABLE documents ADD COLUMN IF NOT EXISTS saved_at timestamptz;
ALTER TABLE documents ADD COLUMN IF NOT EXISTS saved_version_id text;
ALTER TABLE documents ADD COLUMN IF NOT EXISTS discarded_at timestamptz;
ALTER TABLE documents ADD COLUMN IF NOT EXISTS kb_excluded boolean NOT NULL DEFAULT false;
ALTER TABLE documents DROP COLUMN IF EXISTS final_version_id;
UPDATE documents SET status = 'active' WHERE status IN ('draft', 'final', 'archived');
ALTER TABLE documents ALTER COLUMN status SET DEFAULT 'active';
ALTER TABLE doc_versions ADD COLUMN IF NOT EXISTS api_key_id text;
CREATE INDEX IF NOT EXISTS idx_doc_discarded ON documents (discarded_at) WHERE discarded_at IS NOT NULL;

CREATE OR REPLACE FUNCTION doc_versions_append_only()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  mode text;
BEGIN
  mode := current_setting('lw.version_write', true);
  IF TG_OP = 'DELETE' AND mode = 'purge' THEN
    RETURN OLD;
  END IF;
  IF TG_OP = 'UPDATE' AND mode = 'coalesce' THEN
    IF OLD.origin IS DISTINCT FROM 'manual'
       OR NEW.id IS DISTINCT FROM OLD.id
       OR NEW.document_id IS DISTINCT FROM OLD.document_id
       OR NEW.seq IS DISTINCT FROM OLD.seq
       OR NEW.parent_id IS DISTINCT FROM OLD.parent_id
       OR NEW.origin IS DISTINCT FROM OLD.origin
       OR NEW.actor_type IS DISTINCT FROM OLD.actor_type
       OR NEW.actor_id IS DISTINCT FROM OLD.actor_id
       OR NEW.turn_id IS DISTINCT FROM OLD.turn_id
       OR NEW.pi_task_id IS DISTINCT FROM OLD.pi_task_id
       OR NEW.api_key_id IS DISTINCT FROM OLD.api_key_id
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NOT EXISTS (
         SELECT 1 FROM documents d
         WHERE d.id = OLD.document_id
           AND d.head_version_id = OLD.id
           AND d.saved_at IS NOT NULL
           AND d.saved_version_id IS DISTINCT FROM OLD.id
       ) THEN
      RAISE EXCEPTION 'doc_versions is append-only';
    END IF;
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'doc_versions is append-only';
END;
$$;

DROP TRIGGER IF EXISTS doc_versions_no_mutation ON doc_versions;
CREATE TRIGGER doc_versions_no_mutation
BEFORE UPDATE OR DELETE ON doc_versions
FOR EACH ROW EXECUTE FUNCTION doc_versions_append_only();

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS folders (
  id text PRIMARY KEY,
  owner_id text NOT NULL,
  name text NOT NULL,
  parent_id text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sources (
  id text PRIMARY KEY,
  owner_id text NOT NULL,
  kind text NOT NULL,
  title text NOT NULL,
  url text,
  publisher text,
  published_at timestamptz,
  authority smallint NOT NULL DEFAULT 2,
  folder_id text,
  feed_id text,
  feed_guid text,
  document_id text,
  indexed_version_id text,
  body text NOT NULL,
  revision integer NOT NULL DEFAULT 1,
  content_hash text NOT NULL,
  status text NOT NULL DEFAULT 'pending',
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_source_owner_hash ON sources (owner_id, content_hash) WHERE kind <> 'document';
CREATE UNIQUE INDEX IF NOT EXISTS uk_source_feed_item ON sources (feed_id, feed_guid) WHERE feed_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uk_source_document ON sources (document_id) WHERE kind = 'document';
CREATE INDEX IF NOT EXISTS idx_source_owner ON sources (owner_id);

CREATE TABLE IF NOT EXISTS chunks (
  id text PRIMARY KEY,
  source_id text NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  owner_id text NOT NULL,
  ord integer NOT NULL,
  text text NOT NULL,
  search_text text NOT NULL,
  embedding vector(1024),
  embedding_model text,
  char_start integer NOT NULL,
  char_end integer NOT NULL,
  page integer,
  heading_path text[],
  block_ids text[]
);

CREATE INDEX IF NOT EXISTS idx_chunk_owner ON chunks (owner_id);
CREATE INDEX IF NOT EXISTS idx_chunk_fts ON chunks USING gin (to_tsvector('simple', search_text));
CREATE INDEX IF NOT EXISTS idx_chunk_vec ON chunks USING hnsw (embedding vector_cosine_ops);

CREATE TABLE IF NOT EXISTS feeds (
  id text PRIMARY KEY,
  owner_id text NOT NULL,
  url text NOT NULL,
  folder_id text NOT NULL,
  title text,
  etag text,
  last_modified text,
  full_text boolean NOT NULL DEFAULT false,
  is_hotspot boolean NOT NULL DEFAULT false,
  max_items_per_tick integer NOT NULL DEFAULT 3,
  retain_days integer NOT NULL DEFAULT 180,
  fail_count integer NOT NULL DEFAULT 0,
  last_error text,
  last_fetched_at timestamptz,
  UNIQUE (owner_id, url)
);

CREATE TABLE IF NOT EXISTS evidence (
  id text PRIMARY KEY,
  owner_id text NOT NULL,
  citing_document_id text NOT NULL,
  session_id text,
  turn_id text,
  plan_id text,
  question_id text,
  source_id text NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  source_revision integer NOT NULL,
  chunk_id text,
  cited_version_id text,
  quote text NOT NULL,
  locator jsonb NOT NULL,
  authority smallint NOT NULL,
  citation_status text NOT NULL DEFAULT 'unverified',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_evidence_source ON evidence (source_id);

CREATE TABLE IF NOT EXISTS research_plans (
  id text PRIMARY KEY,
  turn_id text NOT NULL,
  session_id text NOT NULL,
  plan jsonb NOT NULL,
  gaps jsonb NOT NULL DEFAULT '[]'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_research_plan_turn ON research_plans (turn_id);
CREATE INDEX IF NOT EXISTS idx_research_plan_session ON research_plans (session_id);

CREATE TABLE IF NOT EXISTS subagent_runs (
  id text PRIMARY KEY,
  turn_id text NOT NULL,
  parent_id text,
  session_id text NOT NULL,
  pi_conversation_id text,
  pi_task_id text NOT NULL,
  agent_ref text NOT NULL,
  slot text NOT NULL,
  model_ref text,
  input jsonb NOT NULL,
  output jsonb,
  status text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_subagent_run_task ON subagent_runs (pi_task_id);
CREATE INDEX IF NOT EXISTS idx_subagent_run_turn ON subagent_runs (turn_id);

CREATE TABLE IF NOT EXISTS tags (
  id text PRIMARY KEY,
  owner_id text NOT NULL,
  name text NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_tag_owner_name ON tags (owner_id, name);

CREATE TABLE IF NOT EXISTS document_tags (
  document_id text NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  tag_id text NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (document_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_document_tags_tag ON document_tags (tag_id);

CREATE TABLE IF NOT EXISTS api_tasks (
  id text PRIMARY KEY,
  user_id text NOT NULL,
  api_key_id text NOT NULL,
  idempotency_key text NOT NULL,
  kind text NOT NULL,
  state text NOT NULL,
  phase text,
  document_id text,
  session_id text,
  turn_id text,
  result_version_id text,
  proposal_id text,
  needs_input jsonb,
  input jsonb NOT NULL,
  error jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_api_task_idem ON api_tasks (api_key_id, idempotency_key);
CREATE INDEX IF NOT EXISTS idx_api_task_key_state ON api_tasks (api_key_id, state);

CREATE TABLE IF NOT EXISTS api_task_events (
  id text PRIMARY KEY,
  task_id text NOT NULL REFERENCES api_tasks(id) ON DELETE CASCADE,
  seq integer NOT NULL,
  type text NOT NULL,
  payload jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_api_task_event_seq ON api_task_events (task_id, seq);

CREATE TABLE IF NOT EXISTS preferences (
  id text PRIMARY KEY,
  user_id text NOT NULL,
  kind text NOT NULL,
  statement text NOT NULL,
  rule jsonb,
  scope_level text NOT NULL DEFAULT 'global',
  scope_ref text,
  strength text NOT NULL DEFAULT 'prefer',
  status text NOT NULL,
  source text NOT NULL,
  support integer NOT NULL DEFAULT 0,
  contradict integer NOT NULL DEFAULT 0,
  plain_conflict boolean NOT NULL DEFAULT false,
  version integer NOT NULL DEFAULT 1,
  superseded_by text,
  proposed_in_session_id text,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_pref_user_status ON preferences (user_id, status);

CREATE TABLE IF NOT EXISTS preference_audit (
  id text PRIMARY KEY,
  preference_id text NOT NULL,
  user_id text NOT NULL,
  action text NOT NULL,
  actor_type text NOT NULL,
  actor_id text NOT NULL,
  before_text text NOT NULL DEFAULT '',
  after_text text NOT NULL DEFAULT '',
  at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_pref_audit_pref ON preference_audit (preference_id);
`;
