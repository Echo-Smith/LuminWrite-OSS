-- Rollback: default → yinyue (OSS Version)
--
-- Purpose: Revert the migration from yinyue to default
--
-- WARNING: This rollback reverts ALL default records back to yinyue.
-- Use immediately after migration if issues are detected.

BEGIN;

-- 1. Revert agent_traces records
UPDATE agent_traces
SET style_slug = 'yinyue'
WHERE style_slug = 'default';

-- 2. Revert evaluation data records
UPDATE evaluation_samples
SET style_slug = 'yinyue'
WHERE style_slug = 'default';

UPDATE evaluation_sets
SET style_slug = 'yinyue'
WHERE style_slug = 'default';

-- 3. Revert the style_profiles slug
UPDATE style_profiles
SET slug = 'yinyue',
    name = '印月三谈',
    description = '植根于时评专栏的深度评论风格',
    updated_at = NOW()
WHERE slug = 'default';

-- 4. Revert profile_versions records
UPDATE profile_versions
SET profile_slug = 'yinyue'
WHERE profile_slug = 'default';

-- 5. Revert user_style_configs (if table exists)
DO $$
BEGIN
    IF EXISTS (
        SELECT FROM information_schema.tables 
        WHERE table_name = 'user_style_configs'
    ) THEN
        UPDATE user_style_configs
        SET style_slug = 'yinyue'
        WHERE style_slug = 'default';
    END IF;
END $$;

-- 6. Revert topics table (if it has style references)
DO $$
BEGIN
    IF EXISTS (
        SELECT FROM information_schema.columns 
        WHERE table_name = 'topics' AND column_name = 'style_slug'
    ) THEN
        UPDATE topics
        SET style_slug = 'yinyue'
        WHERE style_slug = 'default';
    END IF;
END $$;

-- 7. Revert outline_cache entries
DO $$
BEGIN
    IF EXISTS (
        SELECT FROM information_schema.tables 
        WHERE table_name = 'outline_cache'
    ) THEN
        UPDATE outline_cache
        SET style_slug = 'yinyue'
        WHERE style_slug = 'default';
    END IF;
END $$;

COMMIT;

-- Summary:
-- ⚠️  WARNING: All 'default' records have been reverted to 'yinyue'
-- ✅ All agent_traces reverted
-- ✅ All evaluation samples/sets reverted
-- ✅ Style profile renamed back to 'yinyue'
-- ✅ All profile versions reverted
-- ✅ User configs reverted (if table exists)
-- ✅ Topics reverted (if column exists)
-- ✅ Outline cache reverted (if table exists)
