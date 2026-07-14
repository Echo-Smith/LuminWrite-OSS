-- 009: Reputation system and Workbuddy adoption callback

-- User reputation history (tracks reputation changes over time)
CREATE TABLE IF NOT EXISTS user_reputation_history (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL,
    trace_id        VARCHAR(64),
    feedback_id     UUID REFERENCES feedback_segments (id) ON DELETE SET NULL,

    old_reputation  DECIMAL(5,2) NOT NULL DEFAULT 1.00,
    new_reputation  DECIMAL(5,2) NOT NULL DEFAULT 1.00,
    delta           DECIMAL(5,2) NOT NULL DEFAULT 0.00,
    reason          VARCHAR(128) NOT NULL DEFAULT 'feedback', -- feedback | adoption | penalty | bonus

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rep_history_user ON user_reputation_history (user_id);
CREATE INDEX IF NOT EXISTS idx_rep_history_created ON user_reputation_history (created_at DESC);

-- Workbuddy adoption callbacks (external system marks feedback as adopted)
CREATE TABLE IF NOT EXISTS workbuddy_adoptions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    feedback_id     UUID REFERENCES feedback_segments (id) ON DELETE CASCADE,
    trace_id        VARCHAR(64) NOT NULL,
    user_id         UUID,

    source          VARCHAR(64) NOT NULL DEFAULT 'workbuddy', -- workbuddy | manual | auto
    status          VARCHAR(16) NOT NULL DEFAULT 'adopted',   -- adopted | rejected | pending
    adopted_text    TEXT,                                     -- the final adopted text (if any)
    metadata        JSONB DEFAULT '{}',

    adopted_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    adopted_by      VARCHAR(64) NOT NULL DEFAULT 'system',

    CONSTRAINT uk_workbuddy_adoption_feedback UNIQUE (feedback_id)
);

CREATE INDEX IF NOT EXISTS idx_workbuddy_adoptions_trace ON workbuddy_adoptions (trace_id);
CREATE INDEX IF NOT EXISTS idx_workbuddy_adoptions_user ON workbuddy_adoptions (user_id);
CREATE INDEX IF NOT EXISTS idx_workbuddy_adoptions_status ON workbuddy_adoptions (status);

-- Add reputation_recalc_at to feedback_segments for tracking when reputation was last considered
ALTER TABLE feedback_segments ADD COLUMN IF NOT EXISTS reputation_recalc_at TIMESTAMPTZ;

-- Function to update user_reputation in feedback_segments when reputation changes
CREATE OR REPLACE FUNCTION update_user_reputation(
    p_user_id UUID,
    p_new_reputation DECIMAL(5,2)
) RETURNS void AS $$
BEGIN
    UPDATE feedback_segments
    SET user_reputation = p_new_reputation,
        reputation_recalc_at = NOW()
    WHERE user_id = p_user_id;
END;
$$ LANGUAGE plpgsql;
