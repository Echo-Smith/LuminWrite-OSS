# File Index

本索引记录受治理写作平台新增或职责显著变化的文件；已有遗留目录按原项目结构维护。

## Governed runtime

- `backend/internal/writingruntime/`：WritingContract/Plan 执行、typed Artifact 提交、恢复、材料快照、B2 executor adapter、Task12 rollout 与 telemetry。
- `backend/internal/writingstore/`：受治理运行的唯一事实源与事务提交接口。
- `backend/internal/writingquality/`：Candidate / Accepted / Verified 质量门、validator 与降级策略。
- `backend/internal/writingplan/`：IntentPlan、ExecutablePlan、能力注册和静态验证。

## Specifications and plans

- `specs/lcp/v1/`：Lumin Content Protocol schema 与纵向场景 fixture。
- `specs/task11-governed-materials/`：Task11 材料、事实源与 B2 契约规格。
- `specs/task12-governed-rollout/`：Task12 三类适配器、shadow、埋点和发布门禁规格。
- `docs/plans/2026-08-29-governed-material-artifacts.md`：Task11 实施计划。
- `docs/plans/2026-08-29-governed-adapter-rollout.md`：Task12 实施计划。
