-- 009: Rollback reputation system and Workbuddy adoption callback

DROP TABLE IF EXISTS workbuddy_adoptions CASCADE;
DROP TABLE IF EXISTS user_reputation_history CASCADE;
DROP FUNCTION IF EXISTS update_user_reputation(UUID, DECIMAL(5,2));

ALTER TABLE feedback_segments DROP COLUMN IF EXISTS reputation_recalc_at;
