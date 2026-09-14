-- 086: Backfill kb_id into yinyue style profile config
-- The StyleProfile struct now supports a kb_id field that binds a style
-- to a specific knowledge base. The built-in "yinyue" style should be
-- bound to the "default" KB (the 印月三谈 article library).
--
-- config is JSONB, so we use jsonb_set to inject kb_id without losing
-- existing fields.

UPDATE style_profiles
SET config = jsonb_set(config, '{kb_id}', '"default"', true),
    updated_at = NOW()
WHERE slug = 'yinyue'
  AND NOT (config ? 'kb_id');

-- Also update profile_versions for yinyue
UPDATE profile_versions
SET config = jsonb_set(config, '{kb_id}', '"default"', true)
WHERE profile_slug = 'yinyue'
  AND NOT (config ? 'kb_id');

-- Update user_style_profiles if any user has copied the yinyue style
UPDATE user_style_profiles
SET config = jsonb_set(config, '{kb_id}', '"default"', true)
WHERE slug = 'yinyue'
  AND NOT (config ? 'kb_id');

-- Update user_style_profile_versions
UPDATE user_style_profile_versions
SET config = jsonb_set(config, '{kb_id}', '"default"', true)
WHERE profile_slug = 'yinyue'
  AND NOT (config ? 'kb_id');
