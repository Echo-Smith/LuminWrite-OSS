-- Migration: yinyue → default (OSS Version)
-- 
-- Purpose: Replace all references to the yinyue style with default
-- as the primary/default style profile.
--
-- Strategy: Complete replacement - OSS does not need to preserve yinyue
-- as a separate editorial style.

BEGIN;

-- 1. Migrate all agent_traces records
UPDATE agent_traces
SET style_slug = 'default'
WHERE style_slug = 'yinyue';

-- 2. Migrate all evaluation data records
UPDATE evaluation_samples
SET style_slug = 'default'
WHERE style_slug = 'yinyue';

UPDATE evaluation_sets
SET style_slug = 'default'
WHERE style_slug = 'yinyue';

-- 3. Rename the style_profiles slug (if it exists)
UPDATE style_profiles
SET slug = 'default',
    name = '默认写作风格',
    description = '通用的默认写作风格',
    updated_at = NOW()
WHERE slug = 'yinyue';

-- 4. Update profile_versions records
UPDATE profile_versions
SET profile_slug = 'default'
WHERE profile_slug = 'yinyue';

-- 5. Update user_style_configs (if table exists)
DO $$
BEGIN
    IF EXISTS (
        SELECT FROM information_schema.tables 
        WHERE table_name = 'user_style_configs'
    ) THEN
        UPDATE user_style_configs
        SET style_slug = 'default'
        WHERE style_slug = 'yinyue';
    END IF;
END $$;

-- 6. Update topics table (if it has style references)
DO $$
BEGIN
    IF EXISTS (
        SELECT FROM information_schema.columns 
        WHERE table_name = 'topics' AND column_name = 'style_slug'
    ) THEN
        UPDATE topics
        SET style_slug = 'default'
        WHERE style_slug = 'yinyue';
    END IF;
END $$;

-- 7. Update any outline_cache entries
DO $$
BEGIN
    IF EXISTS (
        SELECT FROM information_schema.tables 
        WHERE table_name = 'outline_cache'
    ) THEN
        UPDATE outline_cache
        SET style_slug = 'default'
        WHERE style_slug = 'yinyue';
    END IF;
END $$;

COMMIT;

-- Summary:
-- ✅ All agent_traces migrated
-- ✅ All evaluation samples/sets migrated
-- ✅ Style profile renamed to 'default'
-- ✅ All profile versions updated
-- ✅ User configs updated (if table exists)
-- ✅ Topics updated (if column exists)
-- ✅ Outline cache updated (if table exists)
