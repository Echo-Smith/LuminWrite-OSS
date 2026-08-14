-- Rollback 056: Memory Safety

-- 删除索引
DROP INDEX IF EXISTS idx_memories_source_count;
DROP INDEX IF EXISTS idx_memories_evidence;

-- 删除列
ALTER TABLE user_memories DROP COLUMN IF EXISTS source_count;
ALTER TABLE user_memories DROP COLUMN IF EXISTS evidence_status;