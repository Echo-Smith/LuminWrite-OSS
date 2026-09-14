-- Reverse: remove kb_id from default style configs
-- We use #- operator to delete the key from JSONB.

UPDATE style_profiles
SET config = config #- '{kb_id}',
    updated_at = NOW()
WHERE slug = 'default';

UPDATE profile_versions
SET config = config #- '{kb_id}'
WHERE profile_slug = 'default';

UPDATE user_style_profiles
SET config = config #- '{kb_id}'
WHERE slug = 'default';

UPDATE user_style_profile_versions
SET config = config #- '{kb_id}'
WHERE profile_slug = 'default';
