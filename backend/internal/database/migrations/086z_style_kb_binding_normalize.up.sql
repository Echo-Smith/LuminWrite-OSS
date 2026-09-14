-- 086z: Apply the user-style binding against the canonical relationship and
-- remove the temporary compatibility columns introduced by 085z.

UPDATE user_style_profile_versions AS version_row
SET config = jsonb_set(version_row.config, '{kb_id}', '"default"', true)
FROM user_style_profiles AS profile
WHERE version_row.profile_id = profile.id
  AND profile.slug = 'yinyue'
  AND NOT (version_row.config ? 'kb_id');

ALTER TABLE user_style_profile_versions
    DROP COLUMN IF EXISTS profile_slug;

ALTER TABLE user_style_profiles
    DROP COLUMN IF EXISTS config;
