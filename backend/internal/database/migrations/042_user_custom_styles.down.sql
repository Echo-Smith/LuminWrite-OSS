-- 042_user_custom_styles.down.sql

DROP TABLE IF EXISTS style_review_requests;
DROP TABLE IF EXISTS user_style_profile_versions;
DROP TABLE IF EXISTS user_style_profiles;

ALTER TABLE style_profiles
    DROP COLUMN IF EXISTS author_user_id,
    DROP COLUMN IF EXISTS source_user_profile_id,
    DROP COLUMN IF EXISTS source_type;
