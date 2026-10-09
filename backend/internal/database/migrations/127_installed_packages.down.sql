-- 127 回滚
-- 127 rollback: drop the installed package registry.
--
-- 卸载各行登记的资源（风格 profile / 技能目录 / MCP 服务）由应用层负责，
-- 数据库层只删注册表；磁盘上的 data/packages 目录随应用清理。
-- The application layer owns uninstalling the registered resources (style
-- profiles / skill directories / MCP servers); the migration only drops the
-- registry. On-disk packages under data/packages are cleaned by the app.
DROP TABLE IF EXISTS installed_packages;
