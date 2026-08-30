ALTER TABLE user_style_profile_versions
    DROP COLUMN IF EXISTS profile_slug;

ALTER TABLE user_style_profiles
    DROP COLUMN IF EXISTS config;
