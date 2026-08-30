# File Index

本索引记录受治理写作平台新增或职责显著变化的文件；已有遗留目录按原项目结构维护。

## Governed runtime

- `backend/internal/writingruntime/`：WritingContract/Plan 执行、typed Artifact 提交、恢复、材料快照、B2 executor adapter、Task12 rollout、telemetry 与 shadow 内容隔离（`shadow://` namespace + TTL 清理）；异步调度异常必须进入可审计 failed 终态。
- `backend/internal/writingstore/`：受治理运行的唯一事实源与事务提交接口；初始 contract/material Artifact 与其 lineage attempt 原子提交，绝不绕过外键。
- `backend/internal/writingquality/`：Candidate / Accepted / Verified 质量门、validator 与降级策略。
- `backend/internal/writingplan/`：IntentPlan、ExecutablePlan、能力注册和静态验证。
- `backend/internal/database/migrations/095_governed_rollout_evidence.*.sql`：在 append-only RunLedger 中持久化 Task12 路由、执行和 shadow 对比证据。
- `backend/internal/database/migrations/096_shadow_content.*.sql`：独立持久化、可过期且回滚受保护的 shadow candidate 正文。
- `backend/internal/database/migrations/097_canonical_content.*.sql`：持久化 canonical Artifact 正文，支持重启恢复、不可变重放和逐次 hash 校验。
- `backend/internal/server/governed_runtime.go`：Task13 生产组合根；统一装配 writingstore、能力注册、调度/恢复、材料正文解析及 off/shadow rollout。
- `backend/internal/server/governed_runtime_test.go`：固定组合失败时的 pre-persistence fail-closed 行为。
- `backend/internal/writingruntime/store_canonical_content.go`、`backend/internal/writingstore/canonical_content.go`：canonical ContentGateway 与唯一事实源实现。
- `backend/internal/engine/steps/post_review_test.go`：固定 required validator 在模型或格式失败时不可自动放行。
- `backend/cmd/writingacceptance/main.go`：通过生产 HTTP API 执行长文、多材料综合和忠实改写验收链路。
- `backend/internal/tools/search.go`、`search_stubs.go`：Task13 OSS 检索能力边界；共享接口保留，但商业 Provider 不注册且显式返回未安装。
- `backend/internal/tools/url_fetcher.go`：两版共享的有界本地网页抓取与正文抽取实现。
- `backend/internal/tools/search_capability_test.go`：验证 OSS 不暴露或假注册付费搜索源。
- `backend/internal/server/readiness.go`、`readiness_test.go`：区分 installed/configured/reachable/ready，并为生产流量提供 fail-closed `/ready`。
- `backend/internal/server/provider_preflight.go`、`provider_preflight_test.go`：显式或 opt-in 的有界 Provider 凭证探测、稳定错误码与脱敏证据。
- `backend/.env.example`、`docker-compose.yml`、`docs/runbook.md`：Task13 双端点健康检查、preflight 开关和回滚运维说明；OSS 示例不含付费搜索凭证。
- `backend/Dockerfile`：OSS 运行镜像不下载、不初始化 Commercial 付费信息源 CLI，仅保留共享运行时依赖。
- `backend/internal/tools/deepseek_config_test.go`：防止空值和占位密钥被误报为 LLM 已配置。
- `backend/internal/mcp/registry.go`、`registry_test.go`：保留失败连接状态、输出无凭证 MCP 快照，并防御 nil 执行上下文。
- `backend/internal/mcp/server.go`、`sse_test.go`：串行化 SSE endpoint/JSON-RPC 响应写入，消除跨 HTTP handler 的 ResponseWriter 数据竞争。
- `backend/internal/writingruntime/store_evidence.go`、`store_evidence_test.go`：将 rollout evidence 严格校验后写入 writingstore 唯一 RunLedger。
- `backend/internal/editorial/role_agent_runner.go`、`role_agent_runner_test.go`：角色执行器在依赖缺失时 fail-closed；受治理终稿节点拥有独立注册的内置工具集，不能因 nil registry 令服务进程崩溃。
- `backend/internal/writingstore/shadow_content.go`、`backend/internal/writingruntime/store_shadow_content.go`：持久化 shadow sink、hash 校验与 policy/run 边界清理。

## Specifications and plans

- `specs/lcp/v1/`：Lumin Content Protocol schema 与纵向场景 fixture。
- `specs/task11-governed-materials/`：Task11 材料、事实源与 B2 契约规格。
- `specs/task12-governed-rollout/`：Task12 三类适配器、shadow、埋点和发布门禁规格。
- `docs/plans/2026-08-29-governed-material-artifacts.md`：Task11 实施计划。
- `docs/plans/2026-08-29-governed-adapter-rollout.md`：Task12 实施计划。
- `docs/plans/2026-08-30-task13-production-wiring-design.md`：Task13 生产接线、版本能力边界、真实 readiness 与 shadow-only 验收设计。
- `docs/plans/2026-08-30-task13-production-wiring.md`：Task13 生产接线、真实健康检查、持久化 evidence/shadow 与 governed runtime composition root 实施计划。
- `docs/releases/2026-08-30-task13-production-wiring-readiness.md`：Task13 权威发布门禁记录，明确区分代码/构建通过与真实凭证、staging、生产流量未通过。
- `frontend/tests/governed-e2e-fixtures.test.ts`：验证三条写作场景在前端投影中保持统一质量状态与 fail-closed 条件。
