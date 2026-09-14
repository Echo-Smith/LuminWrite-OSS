-- 086: Backfill kb_id into default style profile config
-- The StyleProfile struct now supports a kb_id field that binds a style
-- to a specific knowledge base. The built-in "default" style should be
-- bound to the "default" KB (the article library).
--
-- config is JSONB, so we use jsonb_set to inject kb_id without losing
-- existing fields.

UPDATE style_profiles
SET config = jsonb_set(config, '{kb_id}', '"default"', true),
    updated_at = NOW()
WHERE slug = 'default'
  AND NOT (config ? 'kb_id');

-- Also update profile_versions for default
UPDATE profile_versions
SET config = jsonb_set(config, '{kb_id}', '"default"', true)
WHERE profile_slug = 'default'
  AND NOT (config ? 'kb_id');

-- Update user_style_profiles if any user has copied the default style
UPDATE user_style_profiles
SET config = jsonb_set(config, '{kb_id}', '"default"', true)
WHERE slug = 'default'
  AND NOT (config ? 'kb_id');

-- Update user_style_profile_versions
UPDATE user_style_profile_versions
SET config = jsonb_set(config, '{kb_id}', '"default"', true)
WHERE profile_slug = 'default'
  AND NOT (config ? 'kb_id');
