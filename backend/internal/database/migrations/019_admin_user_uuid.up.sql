-- 019: Seed admin user with a fixed UUID so that admin traces are properly
-- associated and all UUID-based queries (ListTraces, SoftDeleteTrace, etc.)
-- work without special-casing.
--
-- The fixed UUID 00000000-0000-0000-0000-000000000001 uses version nibble 0,
-- so uuid_generate_v4() (version 4) will never collide with it.

-- 1. Handle edge case: someone may have registered a user with uid='admin'
--    through the register endpoint. Reassign their traces to the admin UUID
--    and remove the duplicate to avoid uid unique constraint conflict.
DO $$
DECLARE
    existing_admin_id UUID;
BEGIN
    SELECT id INTO existing_admin_id
    FROM users
    WHERE uid = 'admin' AND id != '00000000-0000-0000-0000-000000000001';

    IF existing_admin_id IS NOT NULL THEN
        -- Move their traces to the admin UUID
        UPDATE agent_traces
        SET user_id = '00000000-0000-0000-0000-000000000001'
        WHERE user_id = existing_admin_id;

        -- Move their feedback segments too
        UPDATE feedback_segments
        SET user_id = '00000000-0000-0000-0000-000000000001'
        WHERE user_id = existing_admin_id;

        -- Delete the duplicate user row
        DELETE FROM users WHERE id = existing_admin_id;
    END IF;
END $$;

-- 2. Insert admin user into users table (idempotent)
INSERT INTO users (id, uid, name, role, created_at, updated_at)
VALUES ('00000000-0000-0000-0000-000000000001', 'admin', 'admin', 'admin', NOW(), NOW())
ON CONFLICT (id) DO UPDATE SET
    uid = EXCLUDED.uid,
    name = EXCLUDED.name,
    role = EXCLUDED.role,
    updated_at = NOW();

-- 3. Reassign existing admin traces (user_id IS NULL) to the admin UUID
--    These are traces created before this migration where CreateTrace
--    skipped non-UUID user IDs.
UPDATE agent_traces
SET user_id = '00000000-0000-0000-0000-000000000001'
WHERE user_id IS NULL;

-- 4. Migrate passkey_credentials: user_id 'admin' -> admin UUID
UPDATE passkey_credentials
SET user_id = '00000000-0000-0000-0000-000000000001'
WHERE user_id = 'admin';

-- 5. Migrate token_usage: user_id 'admin' -> admin UUID
UPDATE token_usage
SET user_id = '00000000-0000-0000-0000-000000000001'
WHERE user_id = 'admin';
