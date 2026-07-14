-- 012: WebAuthn / Passkey credentials storage

CREATE TABLE IF NOT EXISTS passkey_credentials (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         VARCHAR(64) NOT NULL,          -- links to users.uid or admin
    credential_id   TEXT NOT NULL,                  -- base64url encoded credential ID
    public_key      BYTEA NOT NULL,                 -- COSE public key
    attestation_type VARCHAR(64) NOT NULL DEFAULT 'none',
    aaguid          VARCHAR(64),
    sign_count      BIGINT NOT NULL DEFAULT 0,
    transports      TEXT[] DEFAULT '{}',
    device_type     VARCHAR(32) NOT NULL DEFAULT 'single_device',
    backed_up       BOOLEAN NOT NULL DEFAULT FALSE,
    name            VARCHAR(128),                   -- user-facing label (e.g. "MacBook Touch ID")
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at    TIMESTAMPTZ,

    CONSTRAINT uk_passkey_credential_id UNIQUE (credential_id)
);

CREATE INDEX IF NOT EXISTS idx_passkey_user ON passkey_credentials (user_id);

-- Track WebAuthn challenge sessions (for registration & authentication)
CREATE TABLE IF NOT EXISTS passkey_challenges (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    challenge       TEXT NOT NULL,                  -- base64url encoded challenge
    user_id         VARCHAR(64),                    -- set during registration, NULL for authentication
    purpose         VARCHAR(16) NOT NULL,            -- 'registration' | 'authentication'
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '5 minutes',
    used            BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_passkey_challenge ON passkey_challenges (challenge);
CREATE INDEX IF NOT EXISTS idx_passkey_challenge_expires ON passkey_challenges (expires_at) WHERE used = FALSE;
