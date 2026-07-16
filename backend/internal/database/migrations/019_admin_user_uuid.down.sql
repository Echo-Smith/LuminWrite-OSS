-- 019 down: Remove admin user and revert associations

-- Revert token_usage
UPDATE token_usage SET user_id = 'admin' WHERE user_id = '00000000-0000-0000-0000-000000000001';

-- Revert passkey_credentials
UPDATE passkey_credentials SET user_id = 'admin' WHERE user_id = '00000000-0000-0000-0000-000000000001';

-- Revert agent_traces (set back to NULL)
UPDATE agent_traces SET user_id = NULL WHERE user_id = '00000000-0000-0000-0000-000000000001';

-- Remove admin user
DELETE FROM users WHERE id = '00000000-0000-0000-0000-000000000001';
