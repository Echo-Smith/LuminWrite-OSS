-- 110: Ops patrol runtime config — singleton row for the admin-facing toggle.
-- The worker checks this each round; API and historical alerts stay available
-- when disabled. Seeded enabled so a fresh deployment patrols by default.

CREATE TABLE IF NOT EXISTS admin_patrol_config (
    id         INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    updated_by TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO admin_patrol_config (id, enabled) VALUES (1, TRUE) ON CONFLICT (id) DO NOTHING;
