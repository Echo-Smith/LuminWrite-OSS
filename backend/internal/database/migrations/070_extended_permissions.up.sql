-- 070: Extended permissions for billing, sandbox, security, agent-cards, and rbac management
-- Adds permission keys that were missing from the initial 064 seed.

INSERT INTO permissions (key, description) VALUES
    ('billing.manage',    'Manage billing — plans, point rates, recharge, redeem codes'),
    ('billing.view',      'View billing — overview, revenue, consumption stats'),
    ('sandbox.manage',    'Manage MCP security sandbox — policies, violations, test'),
    ('security.view',     'View security audit — prompt injection events, interception stats'),
    ('agent-cards.manage','Manage A2A Agent Cards — create, update, publish'),
    ('rbac.manage',       'Manage RBAC — roles, permissions, user role assignments')
ON CONFLICT (key) DO UPDATE SET description = EXCLUDED.description;

-- Grant new permissions to admin role (all permissions)
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r CROSS JOIN permissions p
WHERE r.name = 'admin' AND p.key IN (
    'billing.manage', 'billing.view', 'sandbox.manage',
    'security.view', 'agent-cards.manage', 'rbac.manage'
)
ON CONFLICT DO NOTHING;

-- Grant billing.view to viewer role (read-only access)
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r JOIN permissions p ON p.key = 'billing.view'
WHERE r.name = 'viewer'
ON CONFLICT DO NOTHING;
