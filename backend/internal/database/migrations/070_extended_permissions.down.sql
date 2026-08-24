-- 070: Remove extended permissions
DELETE FROM role_permissions
WHERE permission_id IN (SELECT id FROM permissions WHERE key IN (
    'billing.manage', 'billing.view', 'sandbox.manage',
    'security.view', 'agent-cards.manage', 'rbac.manage'
));

DELETE FROM permissions WHERE key IN (
    'billing.manage', 'billing.view', 'sandbox.manage',
    'security.view', 'agent-cards.manage', 'rbac.manage'
);
