-- 084: subscription_plans 增加年付价格，更新套餐定价和额度，移除企业版
--
-- 变更：
-- 1. 新增 price_yearly 列
-- 2. 更新套餐价格：创作者版 ¥19.9/月 ¥199/年，专业版 ¥39.9/月 ¥399/年
-- 3. 更新积分额度：创作者版 2000/月，专业版 5000/月
-- 4. 移除企业版（设为 is_active=FALSE）
-- 5. 清空 features 矩阵（所有套餐功能相同，区别仅在积分额度）

ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS price_yearly DECIMAL(10,2) NOT NULL DEFAULT 0;

-- 更新免费版
UPDATE subscription_plans
SET price_monthly = 0,
    price_yearly = 0,
    point_quota = 500,
    features = '{}'::jsonb,
    is_active = TRUE,
    is_popular = FALSE,
    sort_order = 1,
    updated_at = NOW()
WHERE name = 'free';

-- 更新创作者版
UPDATE subscription_plans
SET price_monthly = 19.9,
    price_yearly = 199,
    point_quota = 2000,
    features = '{}'::jsonb,
    is_active = TRUE,
    is_popular = TRUE,
    sort_order = 2,
    updated_at = NOW()
WHERE name = 'creator';

-- 更新专业版
UPDATE subscription_plans
SET price_monthly = 39.9,
    price_yearly = 399,
    point_quota = 5000,
    features = '{}'::jsonb,
    is_active = TRUE,
    is_popular = FALSE,
    sort_order = 3,
    updated_at = NOW()
WHERE name = 'pro';

-- 停用企业版（不删除，保留历史数据）
UPDATE subscription_plans
SET is_active = FALSE,
    updated_at = NOW()
WHERE name = 'enterprise';
