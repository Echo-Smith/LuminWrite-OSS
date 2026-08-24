-- 069: Redeem codes — 兑换码系统
--
-- admin 可批量生成兑换码，用户输入兑换码获得积分。
-- 替代原来的管理员手动直接充值方式，更标准可控。

CREATE TABLE IF NOT EXISTS redeem_codes (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    code            VARCHAR(32) NOT NULL UNIQUE,           -- 兑换码（大写字母+数字，不含易混淆字符）
    point_amount    DECIMAL(12,2) NOT NULL,                -- 可兑换的积分数
    batch_id        UUID,                                   -- 批次 ID（同批生成的码共享一个 batch_id）
    batch_label     VARCHAR(128),                           -- 批次标签（admin 备注，如"8月活动赠送"）
    status          VARCHAR(16) NOT NULL DEFAULT 'unused',  -- unused | used | disabled | expired
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL, -- 创建者（admin user_id）
    redeemed_by     UUID REFERENCES users(id) ON DELETE SET NULL, -- 兑换者（user_id）
    redeemed_at     TIMESTAMPTZ,                             -- 兑换时间
    expires_at      TIMESTAMPTZ,                             -- 过期时间（NULL = 永不过期）
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_redeem_codes_code ON redeem_codes(code) WHERE status = 'unused';
CREATE INDEX IF NOT EXISTS idx_redeem_codes_batch ON redeem_codes(batch_id);
CREATE INDEX IF NOT EXISTS idx_redeem_codes_status ON redeem_codes(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_redeem_codes_redeemer ON redeem_codes(redeemed_by) WHERE redeemed_by IS NOT NULL;
