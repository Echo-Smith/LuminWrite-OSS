-- 064: RBAC — Roles, Permissions, and User-Role Assignments
-- Implements fine-grained permission control beyond the simple role column on users.

-- ─── permissions ──────────────────────────────────────
-- Catalog of all granted permissions. Permissions are string keys like
-- "style.publish", "kb.manage", "user.manage", etc.
CREATE TABLE IF NOT EXISTS permissions (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    key         VARCHAR(128) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── roles ─────────────────────────────────────────────
-- Named roles (beyond the simple "user"/"admin"/"guest" on users.role).
-- Roles group a set of permissions.
CREATE TABLE IF NOT EXISTS roles (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name        VARCHAR(64) NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    is_system   BOOLEAN NOT NULL DEFAULT FALSE,  -- system roles cannot be deleted
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── role_permissions ─────────────────────────────────
-- Many-to-many: which permissions belong to which role.
CREATE TABLE IF NOT EXISTS role_permissions (
    role_id       UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

-- ─── user_roles ───────────────────────────────────────
-- Many-to-many: which roles are assigned to which users.
-- A user can have multiple roles; their effective permissions are the union.
CREATE TABLE IF NOT EXISTS user_roles (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id    UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    assigned_by VARCHAR(64) NOT NULL DEFAULT '',
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, role_id)
);

CREATE INDEX IF NOT EXISTS idx_user_roles_user ON user_roles(user_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_role ON user_roles(role_id);

-- ─── seed system roles ────────────────────────────────
INSERT INTO roles (name, description, is_system) VALUES
    ('admin',    'Full system access — all permissions', TRUE),
    ('editor',   'Editorial workflow management — review, approve, publish styles', TRUE),
    ('writer',   'Registered writer — create articles, manage own sessions', TRUE),
    ('viewer',   'Read-only access — view articles and evaluation results', TRUE)
ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description, updated_at = NOW();

-- ─── seed core permissions ────────────────────────────
INSERT INTO permissions (key, description) VALUES
    ('style.create',       'Create new style profiles'),
    ('style.publish',      'Publish style profiles to production'),
    ('style.archive',      'Archive style profiles'),
    ('style.review',       'Review and approve/reject community style submissions'),
    ('kb.manage',          'Manage knowledge base — import, rechunk, generate embeddings'),
    ('kb.view',            'View knowledge base contents'),
    ('user.manage',        'Manage users — assign roles, disable accounts'),
    ('user.view',           'View user list and profiles'),
    ('model.manage',       'Manage LLM model configurations'),
    ('apikey.manage',      'Manage API keys'),
    ('eval.run',           'Run evaluation suites'),
    ('eval.view',          'View evaluation results'),
    ('redteam.run',        'Run red-team security evaluation'),
    ('redteam.view',       'View red-team reports'),
    ('cron.manage',        'Manage scheduled cron jobs'),
    ('mcp.manage',         'Manage MCP server configurations'),
    ('sensitive.manage',   'Manage sensitive word lists and config'),
    ('editorial.manage',  'Manage editorial tasks and decisions'),
    ('editorial.view',     'View editorial board'),
    ('audit.view',         'View security audit logs'),
    ('evolution.manage',   'Manage self-evolution candidates'),
    ('wabench.manage',     'Manage WritingAgentBench evaluation center'),
    ('session.delete',     'Delete any user session (not just own)'),
    ('agent.start',        'Start writing agent sessions')
ON CONFLICT (key) DO UPDATE SET description = EXCLUDED.description;

-- ─── assign all permissions to admin role ─────────────
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r CROSS JOIN permissions p
WHERE r.name = 'admin'
ON CONFLICT DO NOTHING;

-- ─── assign writer permissions to writer role ─────────
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r JOIN permissions p ON p.key IN (
    'agent.start', 'kb.view', 'eval.view', 'editorial.view'
)
WHERE r.name = 'writer'
ON CONFLICT DO NOTHING;

-- ─── assign editor permissions to editor role ─────────
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r JOIN permissions p ON p.key IN (
    'style.create', 'style.publish', 'style.archive', 'style.review',
    'kb.view', 'eval.run', 'eval.view', 'redteam.view',
    'editorial.manage', 'editorial.view', 'audit.view', 'wabench.manage'
)
WHERE r.name = 'editor'
ON CONFLICT DO NOTHING;

-- ─── assign viewer permissions to viewer role ────────
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r JOIN permissions p ON p.key IN (
    'kb.view', 'eval.view', 'editorial.view', 'redteam.view'
)
WHERE r.name = 'viewer'
ON CONFLICT DO NOTHING;
