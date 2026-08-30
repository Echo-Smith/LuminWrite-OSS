ALTER TABLE user_style_profiles
    ADD COLUMN IF NOT EXISTS config JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE user_style_profiles AS profile
SET config = latest.config
FROM (
    SELECT DISTINCT ON (profile_id) profile_id, config
    FROM user_style_profile_versions
    ORDER BY profile_id, version DESC
) AS latest
WHERE latest.profile_id = profile.id;

ALTER TABLE user_style_profile_versions
    ADD COLUMN IF NOT EXISTS profile_slug VARCHAR(64);

UPDATE user_style_profile_versions AS version_row
SET profile_slug = profile.slug
FROM user_style_profiles AS profile
WHERE version_row.profile_id = profile.id
  AND version_row.profile_slug IS NULL;

ALTER TABLE user_style_profile_versions
    ALTER COLUMN profile_slug SET NOT NULL;
