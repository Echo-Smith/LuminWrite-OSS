-- 002_style_profiles.up.sql

CREATE TABLE IF NOT EXISTS style_profiles (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    slug            VARCHAR(64) UNIQUE NOT NULL,
    name            VARCHAR(128) NOT NULL,
    description     TEXT,
    version         INTEGER NOT NULL DEFAULT 1,
    status          VARCHAR(16) NOT NULL DEFAULT 'draft',

    config          JSONB NOT NULL DEFAULT '{}',

    rollout_type    VARCHAR(16) NOT NULL DEFAULT 'full',
    whitelist_uids  TEXT[] DEFAULT '{}',
    rollout_percent INTEGER DEFAULT 100,

    published_at    TIMESTAMPTZ,
    published_by    VARCHAR(64),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_style_slug_version UNIQUE (slug, version)
);

CREATE INDEX IF NOT EXISTS idx_style_profiles_slug_status
    ON style_profiles (slug, status) WHERE status = 'published';

CREATE TABLE IF NOT EXISTS profile_versions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    profile_slug    VARCHAR(64) NOT NULL,
    version         INTEGER NOT NULL,
    config          JSONB NOT NULL,
    changelog       TEXT,
    status          VARCHAR(16) NOT NULL DEFAULT 'draft',
    published_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      VARCHAR(64),

    CONSTRAINT fk_profile_versions_slug FOREIGN KEY (profile_slug)
        REFERENCES style_profiles (slug) ON DELETE CASCADE,
    CONSTRAINT uk_profile_versions UNIQUE (profile_slug, version)
);
