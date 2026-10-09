-- 127: Installed package registry (download-install model for styles, skills, services).
-- 127: Installed package registry (download-install model for styles, skills, services).
--
-- installed_packages is the registry for the download-install model: 风格库的
-- 风格 / 技能 / 服务以「包」的形式下载安装到固定目录
-- (data/packages/<kind>/<slug>/<version>/)，并在此登记来源与版本。
-- installed_packages is the registry for the download-install model: styles,
-- skills and services install as packages into a fixed directory
-- (data/packages/<kind>/<slug>/<version>/), with source and version recorded
-- here.
--
-- Per kind, the package body lands differently (docs/34):
--   style   — the profile config is applied to user_style_profiles (immutable
--             versions); the package body stays on disk as the reinstall and
--             migration source;
--   skill   — the package body on disk IS the runtime form (SKILL.md + scripts);
--   service — the manifest registers an mcp_servers row (+ api_keys entry for
--             credentials); the body stays on disk.
-- The registry row is the uninstall anchor: removing it removes all three
-- surfaces (directory, registration, and — for styles — the profile row).

CREATE TABLE installed_packages (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind         VARCHAR(16) NOT NULL,
    slug         VARCHAR(64) NOT NULL,
    version      VARCHAR(32) NOT NULL,
    title        VARCHAR(128) NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    source       VARCHAR(16) NOT NULL DEFAULT 'builtin',
    install_path TEXT NOT NULL DEFAULT '',
    manifest     JSONB NOT NULL DEFAULT '{}',
    installed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uk_installed_packages_user_kind_slug UNIQUE (user_id, kind, slug),
    CONSTRAINT chk_installed_packages_kind CHECK (kind IN ('style', 'skill', 'service')),
    CONSTRAINT chk_installed_packages_source CHECK (source IN ('builtin', 'url')),
    CONSTRAINT chk_installed_packages_slug CHECK (slug ~ '^[A-Za-z0-9._-]{1,64}$'),
    CONSTRAINT chk_installed_packages_version CHECK (version ~ '^[A-Za-z0-9._-]{1,32}$'),
    CONSTRAINT chk_installed_packages_manifest CHECK (jsonb_typeof(manifest) = 'object')
);

CREATE INDEX IF NOT EXISTS idx_installed_packages_user ON installed_packages (user_id);
CREATE INDEX IF NOT EXISTS idx_installed_packages_kind ON installed_packages (kind);

COMMENT ON TABLE installed_packages IS 'Download-install package registry (styles/skills/services); one row per (user, kind, slug), uninstall anchor for all three surfaces';
