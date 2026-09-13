-- 112: 证据升级链路回填（Layer-0 止血④）
--
-- 与商业版 115 号迁移同源（双仓迁移序列独立编号）。
-- 证据边界门控（gate.go）按 evidence_status 过滤注入，但历史上没有任何
-- 链路写入 verified/supported，所有记忆恒为 none/空——写作场景的严格门
-- 把 Tier2/3 静默全拦。代码侧已补升级链路（conflict.go：two-strike →
-- supported，人工录用 → verified）并把默认门控降为"非 conflicted 即注入"。
-- 本迁移按 two-strike 语义回填存量：出现 >=2 次的 active 记忆视为有支撑
-- 证据，使未来重新收紧到 verified 严格门时存量资产不失效。

UPDATE user_memories
SET evidence_status = 'supported'
WHERE status = 'active'
  AND occurrences >= 2
  AND (evidence_status IS NULL OR evidence_status = '' OR evidence_status = 'none');
