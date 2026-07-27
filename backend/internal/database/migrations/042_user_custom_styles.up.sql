-- 042_user_custom_styles.up.sql
-- 用户自定义风格：私有风格 + 不可变版本快照 + 审核请求
-- 审核通过后，获批版本快照写入现有 style_profiles / profile_versions

-- ── 1. 用户风格主表（先建，因为 style_profiles 的 ALTER 需要引用它）──
CREATE TABLE IF NOT EXISTS user_style_profiles (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    owner_user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slug            VARCHAR(64) NOT NULL,
    name            VARCHAR(128) NOT NULL,
    description     TEXT,
    status          VARCHAR(16) NOT NULL DEFAULT 'draft',  -- draft | pending_review | approved | rejected
    current_version INTEGER NOT NULL DEFAULT 0,             -- 最新版本号，0 表示尚无已保存版本
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner_user_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_user_style_profiles_owner
    ON user_style_profiles (owner_user_id);
CREATE INDEX IF NOT EXISTS idx_user_style_profiles_status
    ON user_style_profiles (status);

-- ── 2. 不可变版本快照 ──
CREATE TABLE IF NOT EXISTS user_style_profile_versions (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    profile_id  UUID NOT NULL REFERENCES user_style_profiles(id) ON DELETE CASCADE,
    version     INTEGER NOT NULL,
    config      JSONB NOT NULL,
    changelog   TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (profile_id, version)
);

-- ── 3. 审核请求（绑定特定版本快照）──
CREATE TABLE IF NOT EXISTS style_review_requests (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    profile_id           UUID NOT NULL REFERENCES user_style_profiles(id) ON DELETE CASCADE,
    submitted_version_id UUID NOT NULL REFERENCES user_style_profile_versions(id) ON DELETE CASCADE,
    status               VARCHAR(16) NOT NULL DEFAULT 'pending',  -- pending | approved | rejected
    review_note          TEXT,
    reviewed_by          VARCHAR(64),
    reviewed_at          TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_style_review_requests_status
    ON style_review_requests (status);
CREATE INDEX IF NOT EXISTS idx_style_review_requests_profile
    ON style_review_requests (profile_id);

-- ── 4. 扩展 style_profiles：记录社区来源 ──
ALTER TABLE style_profiles
    ADD COLUMN IF NOT EXISTS source_type            VARCHAR(16)  NOT NULL DEFAULT 'builtin',
    ADD COLUMN IF NOT EXISTS source_user_profile_id UUID         REFERENCES user_style_profiles(id),
    ADD COLUMN IF NOT EXISTS author_user_id         UUID         REFERENCES users(id);
