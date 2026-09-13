-- 回滚 112：将本次回填的 supported 恢复为 none。
-- 注意：无法区分"本次回填"与"升级链路之后写入的 supported"，
-- 回滚会把后者一并降级，仅在彻底放弃证据升级设计时使用。

UPDATE user_memories
SET evidence_status = 'none'
WHERE status = 'active'
  AND occurrences >= 2
  AND evidence_status = 'supported';
