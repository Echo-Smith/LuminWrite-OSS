-- Append-only operator intent. Installing a policy is distinct from activation.
CREATE TABLE writing_runtime_policy_revisions (
 revision BIGSERIAL PRIMARY KEY,
 capability_id TEXT NOT NULL,
 policy_hash VARCHAR(71) NOT NULL CHECK (policy_hash ~ '^sha256:[0-9a-f]{64}$'),
 policy JSONB NOT NULL CHECK (jsonb_typeof(policy)='object'),
 active BOOLEAN NOT NULL DEFAULT FALSE,
 operator_id TEXT NOT NULL CHECK (length(operator_id)>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX writing_runtime_policy_latest ON writing_runtime_policy_revisions(capability_id,revision DESC);
CREATE FUNCTION writing_runtime_policy_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'runtime policy revisions are append-only'; END $$;
CREATE TRIGGER writing_runtime_policy_immutable BEFORE UPDATE OR DELETE ON writing_runtime_policy_revisions
 FOR EACH ROW EXECUTE FUNCTION writing_runtime_policy_immutable();
