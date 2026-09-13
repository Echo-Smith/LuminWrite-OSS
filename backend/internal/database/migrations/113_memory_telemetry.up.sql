-- 113: memory_telemetry 注入遥测（P3）
--
-- 记忆系统三次线上级静默失败（LoadHistory 窗口、NULL scan 吞行、证据
-- 死锁导致 Tier2/3 写作场景注入率恒为零）的根因都是"无注入内容级观测"。
-- 本表只追加、不阻塞主链路：每次记忆门控/显式捕获/会话提取/关闭事件
-- 记一行，供 admin 汇总端点与未来的黄金集/影子对比消费。

CREATE TABLE IF NOT EXISTS memory_telemetry (
    id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id UUID,
    trace_id TEXT,
    conversation_id TEXT,
    intent TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',      -- pipeline | harness | tool | api
    event TEXT NOT NULL,                  -- gate_inject | gate_refusal | explicit_capture | session_extract | dismiss
    injected_count INT NOT NULL DEFAULT 0,
    review_guard_count INT NOT NULL DEFAULT 0,
    injected_ids JSONB,
    tiers JSONB,
    refusal_reason TEXT NOT NULL DEFAULT '',
    latency_ms INT NOT NULL DEFAULT 0,
    metadata JSONB
);

CREATE INDEX IF NOT EXISTS idx_mem_tel_user_created ON memory_telemetry (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mem_tel_event_created ON memory_telemetry (event, created_at DESC);
