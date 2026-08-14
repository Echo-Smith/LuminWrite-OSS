-- 056: Memory Safety (P0) — Evidence Status and Source Count
-- 支持证据边界召回（Evidence-Bounded Recall）和认识论安全

-- 添加证据状态列（用于证据边界过滤）
ALTER TABLE user_memories ADD COLUMN IF NOT EXISTS evidence_status VARCHAR(16) NOT NULL DEFAULT 'none';
COMMENT ON COLUMN user_memories.evidence_status IS '证据状态: verified|supported|conflicted|unknown|none，用于证据边界召回';

-- 添加来源计数列（用于评估记忆可信度）
ALTER TABLE user_memories ADD COLUMN IF NOT EXISTS source_count INTEGER NOT NULL DEFAULT 0;
COMMENT ON COLUMN user_memories.source_count IS '支撑来源数量，用于综合评估记忆可信度';

-- 为新列创建索引以支持高效查询
CREATE INDEX IF NOT EXISTS idx_memories_evidence ON user_memories (user_id, evidence_status) WHERE evidence_status != 'none';
CREATE INDEX IF NOT EXISTS idx_memories_source_count ON user_memories (source_count) WHERE source_count > 0;

-- 更新约束，包含新列的状态检查（可选，为未来的完整性增强预留）
-- 当前暂不修改 uk_user_memory 约束，避免破坏现有数据