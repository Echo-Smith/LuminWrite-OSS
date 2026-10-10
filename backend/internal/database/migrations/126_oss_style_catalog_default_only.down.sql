-- Migration: OSS style catalog — first-party editorial styles out (down)
--
-- 回滚范围说明：本迁移的 up 从库中移除第一方编辑风格（印月三谈/申论/
-- 小红书）。这些风格内容有意不进入开源仓库，回滚不会（也不应）重新写入
-- 第一方内容资产——确需恢复时通过 Admin 风格 API（POST /api/v2/admin/styles）
-- 或商业版环境导入，属于内容运营操作而非代码回滚。
--
-- 因此 down 只做安全的反向清理：若 default 行是本迁移插入的（无既有
-- 版本历史可循的边缘情形），删之使其回到迁移前状态；已存在的 default
-- 不受影响。

BEGIN;

-- 若 default 仅有本迁移创建的 Initial seed 版本且无更早历史，视为本迁移
-- 插入的行，删除以回到迁移前状态（幂等：多次执行无副作用）。
DELETE FROM style_profiles
WHERE slug = 'default'
  AND NOT EXISTS (
      SELECT 1 FROM profile_versions
      WHERE profile_slug = 'default' AND changelog <> 'Initial seed'
  );

COMMIT;
