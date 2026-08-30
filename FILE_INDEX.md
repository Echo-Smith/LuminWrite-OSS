# File Index

本索引记录受治理写作平台新增或职责显著变化的文件；已有遗留目录按原项目结构维护。

## Governed runtime

- `backend/internal/writingruntime/`：WritingContract/Plan 执行、typed Artifact 提交、恢复、材料快照、B2 executor adapter、Task12 rollout、telemetry 与 shadow 内容隔离（`shadow://` namespace + TTL 清理）。
- `backend/internal/writingstore/`：受治理运行的唯一事实源与事务提交接口。
- `backend/internal/writingquality/`：Candidate / Accepted / Verified 质量门、validator 与降级策略。
- `backend/internal/writingplan/`：IntentPlan、ExecutablePlan、能力注册和静态验证。
- `backend/internal/database/migrations/095_governed_rollout_evidence.*.sql`：在 append-only RunLedger 中持久化 Task12 路由、执行和 shadow 对比证据。
- `backend/internal/tools/search.go`、`search_stubs.go`：Task13 OSS 检索能力边界；共享接口保留，但商业 Provider 不注册且显式返回未安装。
- `backend/internal/tools/url_fetcher.go`：两版共享的有界本地网页抓取与正文抽取实现。
- `backend/internal/tools/search_capability_test.go`：验证 OSS 不暴露或假注册付费搜索源。

## Specifications and plans

- `specs/lcp/v1/`：Lumin Content Protocol schema 与纵向场景 fixture。
- `specs/task11-governed-materials/`：Task11 材料、事实源与 B2 契约规格。
- `specs/task12-governed-rollout/`：Task12 三类适配器、shadow、埋点和发布门禁规格。
- `docs/plans/2026-08-29-governed-material-artifacts.md`：Task11 实施计划。
- `docs/plans/2026-08-29-governed-adapter-rollout.md`：Task12 实施计划。
- `docs/plans/2026-08-30-task13-production-wiring-design.md`：Task13 生产接线、版本能力边界、真实 readiness 与 shadow-only 验收设计。
- `docs/plans/2026-08-30-task13-production-wiring.md`：Task13 生产接线、真实健康检查、持久化 evidence/shadow 与 governed runtime composition root 实施计划。
- `frontend/tests/governed-e2e-fixtures.test.ts`：验证三条写作场景在前端投影中保持统一质量状态与 fail-closed 条件。
