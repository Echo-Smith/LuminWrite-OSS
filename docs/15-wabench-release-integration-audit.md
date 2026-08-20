# WABench V2 发版集成审计

日期：2026-08-19

审计分支：`codex/release-wabench-v2-review`

发版基线：`origin/main` (`23f03cc`)
纳入能力：Task 8 数据层、Task 9 真实 Harness 评测执行层

## 结论

Task 8—9 可以从 `origin/main` 独立集成，不依赖本地 `main` 超前的 9 个未发布提交。两项提交的补丁可干净应用，后端在真实 PostgreSQL 上通过全量串行测试，WABench migration 创建成功。

因此推荐把 WABench 作为独立 PR 审查；不要把本地 `main` 的 9 个提交隐式夹带进该 PR。

## 干净集成提交

原提交：

- `7c9d895`：Task 8，WABench V2 数据层
- `b20bb00`：Task 9，WABench V2 评测执行层

基于 `origin/main` 重放后的审计提交：

- `1f4420f`：Task 8
- `7d2350a`：Task 9

重放只改变父提交和 commit hash，代码补丁未做业务改写。

## 测试证据

- `go test -p 1 -vet=off ./...`：通过；使用真实 PostgreSQL，并启用 `TEST_DATABASE_URL` 与 `WABENCH_TEST_DATABASE_URL`。
- 数据库 migration：记录 60 个版本，最高版本为 `063_wabench_data_layer`；`wabench_runs` 表存在。
- `go test ./...`：被历史 vet 问题阻断，位置为 `backend/internal/agent/tools.go` 的重复字符条件；该问题在 `origin/main` 已存在，并非 Task 8—9 引入。
- 前端按生产构建参数 `npm ci --legacy-peer-deps && npm run build`：通过；Task 8—9 没有前端业务改动。

同一 PostgreSQL 数据库被多个 Go package 并发执行 migration 时，曾触发 `pg_type_typname_nsp_index` 唯一约束冲突。测试使用 `-p 1` 后稳定通过。CI 的数据库集成测试应改为每个 package 独立数据库，或对 migration 加 PostgreSQL advisory lock；不要长期把 `-p 1` 当成唯一解决方案。

## 本地 main 超前 9 个提交的审计

本地 `main` 为 `87f757d`，相对 `origin/main` 超前 9 个提交：

1. `57b013c`：cron 和知识库自动导入修复。应独立 PR，包含运行调度和数据配置变更。
2. `f639f52`：Harness step history 修复，但混入 WebPush 配置与 migration 060，原子性较差。应拆分审查。
3. `b5fb116`：文章版本、前端详情、主题卡片、WebPush 等 23 个文件的大型功能包。应单独发版验证。
4. `28c0d73`：前端缺失 import 修复，依赖 `b5fb116`。
5. `b8c413b`：后端健康检查窗口调整，可单独进入部署 PR。
6. `ee0e888`：恢复 migration 047 并新增 062，方向正确，但需结合已部署环境核验。
7. `c3555d4`：checksum 不一致时自动改写记录且不执行 SQL，存在高风险，不建议按现状发版。
8. `5ed6d67`：API key NULL 扫描修复，可作为小型独立 PR。
9. `87f757d`：移除 WebPush、改为 SSE，但再次改变 migration 060，必须通过新 migration 处理已部署环境。

## 已复现的 migration 结构漂移

在真实 PostgreSQL 上复现了如下升级链：

1. 在 `b5fb116` 执行 migration 060，创建 `push_subscriptions`。
2. 升级到 `b20bb00` 所在的本地完整栈。
3. 迁移器发现 060 checksum 改变，将记录从 `a3425c...` 更新为 `80ff7f...`，日志同时明确显示 migration 未重新执行。
4. 最终 `push_subscriptions` 仍然存在，但 `schema_migrations` 已记录新 checksum。

这证明 checksum 自动更新会制造“迁移记录显示最新、实际数据库结构仍旧”的假一致状态。

推荐修复：

- 恢复严格 checksum 校验，已发布 migration 永不原地修改。
- 保留历史 060 的原始 SQL 和 checksum。
- 新增后续 migration（例如 064）显式删除已废弃的 `push_subscriptions`，并保证幂等。
- staging 升级前备份 `schema_migrations` 与相关表结构，分别验证“全新安装”和“已有 060/061 的升级”两条路径。

## 前端和依赖风险

- 普通 `npm ci` 因 `tw-shimmer@0.4.11` 要求 Tailwind 4、项目仍使用 Tailwind 3 而失败。
- 当前 Dockerfile/CI 使用 `--legacy-peer-deps` 可以构建，但这是兼容性绕行，应建立依赖升级任务。
- `npm audit` 报告 5 个 high severity 漏洞，需单独做依赖与可利用性审查。
- 构建产物主 JS 约 1.0 MB，Vite 报告大 chunk；不阻断 WABench 后端集成，但应纳入前端性能债务。

## 推荐合并顺序

1. 以 `origin/main` 为基线审查并合并本分支的 Task 8—9。
2. 单独修复历史 `go vet` 阻断和数据库测试并发 migration 问题。
3. 将本地 9 个提交拆成 cron、Harness trace、文章版本/UI、部署健康检查、API key、SSE/WebPush migration 等独立 PR。
4. 在纳入 SSE/WebPush 相关提交前，先完成不可变 migration 修复和真实升级演练。
5. Task 9 保持 shadow 模式；正式生产 gate 切换仍由后续任务完成。

## 发版约束

> 2026-08-20 后续：本文记录的 Go vet、ESLint 缺失、npm 依赖风险和大 chunk 问题已在独立工程质量提交中补齐，详见 [`16-engineering-quality-followup.md`](./16-engineering-quality-followup.md)。本文原始审计数字保留作为当时证据。

- 本审计未 push、未部署、未改动 `main`。
- 本分支不包含本地 `main` 的 9 个未发布提交。
- 不应把旧 WebPush migration、checksum 自动吞错逻辑和 WABench 放进同一个未经拆分的 release PR。
