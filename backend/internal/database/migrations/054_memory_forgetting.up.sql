-- 054: Memory Forgetting — TTL and salience-based decay support
ALTER TABLE user_memories ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
ALTER TABLE user_memories ADD COLUMN IF NOT EXISTS salience_score DECIMAL(3,2) NOT NULL DEFAULT 0.50;
CREATE INDEX IF NOT EXISTS idx_memories_expires ON user_memories (expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_memories_salience ON user_memories (salience_score);
