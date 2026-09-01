# File Index

本索引记录受治理写作平台新增或职责显著变化的文件；已有遗留目录按原项目结构维护。

## Governed runtime

- `backend/internal/writingruntime/`：WritingContract/Plan 执行、typed Artifact 提交、恢复、材料快照、B2 executor adapter、rollout、telemetry、持久化 shadow sink 适配和 fail-closed allowlist promotion gate。
- `backend/internal/writingstore/`：受治理运行的唯一事实源与事务提交接口；`rollout.go` 提供 shadow body、evidence health 和 append-only approval 的 PostgreSQL 操作。
- `backend/internal/writingquality/`：Candidate / Accepted / Verified 质量门、validator 与降级策略。
- `backend/internal/writingplan/`：IntentPlan、ExecutablePlan、能力注册和静态验证。
- `backend/internal/database/migrations/095_governed_rollout_evidence.*.sql`：在 append-only RunLedger 中持久化 Task12 路由、执行和 shadow 对比证据。
- `backend/internal/database/migrations/096_governance_productionization.*.sql`：独立持久化 shadow body 与 allowlist 审批；存在数据时禁止无保护降级。
- `backend/cmd/governance-gate/`：对 exact allowlist policy 执行只读 evidence 评估或追加限时审批，不改变流量。

## Specifications and plans

- `specs/lcp/v1/`：Lumin Content Protocol schema 与纵向场景 fixture。
- `specs/task11-governed-materials/`：Task11 材料、事实源与 B2 契约规格。
- `specs/task12-governed-rollout/`：Task12 三类适配器、shadow、埋点和发布门禁规格。
- `specs/task13-productionize-governance/`：持久化 evidence/sink、allowlist 晋升门禁、真实模型纵向验收及双仓回归要求/设计/任务。
- `docs/plans/2026-08-29-governed-material-artifacts.md`：Task11 实施计划。
- `docs/plans/2026-08-29-governed-adapter-rollout.md`：Task12 实施计划。
- `docs/plans/2026-09-01-task13-governance-productionization.md`：Task13 工程化实施计划。
- `docs/releases/2026-09-01-task13-governance-productionization.md`：实际验收证据与当前授权边界。
- `docs/runbook.md`、`scripts/run-task13-live-acceptance.sh`：晋升运维清单、评估/审批命令和无凭据入库的真实模型验收入口。
- `frontend/tests/governed-e2e-fixtures.test.ts`：验证三条写作场景在前端投影中保持统一质量状态与 fail-closed 条件。

## Governed writing frontend

- `frontend/src/pages/writing-workspace.tsx`：文档优先工作台编排；组织全屏宽可收起的完整导航抽屉（宽屏默认展开、中小屏默认收起）、独立用户气泡与折叠 Agent 过程流、连续稿纸、文末评价、320–520px 安全范围内可拖拽的桌面详情栏与跨栏浮动输入框。
- `frontend/src/stores/workspace-layout-store.ts`：按用户、设备和文档保存详情与输入框宽度偏好；全局导航开合由工作台按当前断点控制，旧对话总容器偏好不再参与布局。
- `frontend/src/components/document/document-surface.tsx`：无成稿时呈现无纸张边框的写作欢迎态；第一版形成后切换为连续 A4 比例稿纸、可编辑成稿正文和紧随纸张下方的评价区，不渲染运行过程。
- `frontend/src/components/assistant-ui/`：在稿件区域把用户要求呈现为独立气泡，把 Agent 步骤与思考呈现为默认折叠的低强调条目，并抑制正文重复输出；不再使用“写作对话”总容器。
- `frontend/src/components/composer/`：宽/窄两态写作输入，支持整框拖拽上传、锚定 `+` 的独立素材浮层、照片/文件/文本添加、独立可搜索多选的素材库弹窗与自动检索开关；同时保留三种主写作意图、渐进式资料/确认设置，以及按容器宽度压缩并显示“快速 · 经济/Pro”的模型选择控件。
- `frontend/src/index.css`：工作台响应式布局、连续稿纸、独立对话/过程层、透明浮动输入层，以及左右栏共享的 280ms CSS 运动系统与输入框 260ms 宽窄切换。

## Engineering map and current boundary (2026-09-01)

- `backend/internal/writingkernel/`、`lcp/`、`worldstate/`：版本化 WritingContract、Document / Revision、Lumin Content Protocol 解析与章节状态；它们是受治理写作内容模型的核心，不以流式 Markdown 作为权威状态。
- `backend/internal/writingplan/`：将 IntentPlan 编译为经过能力、类型、依赖、预算和失败路径校验的 ExecutablePlan；模板、片段与受限动态编排在此收敛。
- `backend/internal/writingstore/`、`backend/internal/database/migrations/088_plan_json.*.sql` 至 `096_governance_productionization.*.sql`：运行、Artifact、质量状态、文档版本、材料快照、append-only evidence、独立 shadow body 和审批的持久化与事务边界。
- `backend/internal/writingruntime/`：Orchestrator、checkpoint / recovery、旧 Harness / Pipeline / Editorial 适配器、材料完整性、质量门和 telemetry；`rollout*.go`、`shadow_content.go`、`persistent_rollout.go`、`promotion_gate.go` 实现 baseline/shadow 隔离与晋升前置门禁。
- `backend/internal/server/writing_api.go`、`writing_routes_test.go`、`metrics.go`：受治理写作 API、路由完整性测试和低基数运行时指标；legacy WebSocket / workflow 路径仍是兼容入口，并非另一套权威内核。
- `docs/19-governed-writing-runtime.md`：目标架构与不可绕过的协议、质量和提交规则；`docs/releases/2026-09-01-task13-governance-productionization.md`：最新验收证据与授权边界。
- **发布状态**：双版本 local shadow 已完成持久化、晋升门禁和 `deepseek-v4-flash` 三场景本地验收；尚未创建真实发布审批、激活 allowlist subject 或积累生产 shadow 证据，因此 allowlist 仍未放行，percentage / production 均未获授权。
- **仓库边界**：本索引所在 `codex/v2-stabilization-oss` 与商业版对应稳定化分支为当前代码基线；仓库根目录的旧 `writing-agent-v2` 检出停在 2026-08-25，且有大规模未提交整合改动，只可作为工作副本，不可作为发布事实源。
