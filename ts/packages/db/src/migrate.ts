/** 手写 SQL。Drizzle 只负责查询；测试默认跑在 PGlite 上。 */
export const migrationSql = `
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
  created_at timestamptz NOT NULL DEFAULT now()
);

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

CREATE TABLE IF NOT EXISTS usage_ledger (
  id text PRIMARY KEY,
  request_id text NOT NULL,
  model text NOT NULL,
  input_tokens integer NOT NULL,
  output_tokens integer NOT NULL,
  cost_micros integer NOT NULL,
  api_key_id text,
  api_task_id text,
  created_at timestamptz NOT NULL DEFAULT now()
);

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
