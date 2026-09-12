# M2 — Thin Orchestrator：设计与实现记录

> V3.0 M2（2026-09-04 实现）。目标（docs/20）：业务动作从 executor adapter 下沉到 capability，orchestrator 只剩 **Resolve→Authorize→Dispatch→Observe→Decide→Transition**；行数下降；kernel 不变量（contract/plan/artifact/permission/quality/snapshot）不可被 capability 绕过。

## 26.1 排查结论（实现前）

主循环逐段盘点后确认：Execute 的骨架**已经是六动词形态**（M0–M1 的纪律积累），真正的"业务动作残留"只有一处——**delivery 协议的 class 硬编码 switch**（`case DeliveryClassDraft / DeliveryClassQuality`）：orchestrator 知道每类节点交付后该干什么，新增交付类必须改 orchestrator。次要问题：主循环 200+ 行单块，动词边界只存在于注释里。

## 26.2 设计决策

- **D1 路由表下沉，非能力下沉**：delivery 的 store 提交是 **kernel 行为**（ActorSystem 纪律，docs/21 §21.7 D1——版本提交/晋升不允许能力侧执行）。M2 的正确动作不是把 store 写面交给 capability，而是把"class→动作"的**路由**从 orchestrator 移到 DeliveryProtocol 自己（`Drive(class, …)` + `deliveryDrivers()` 表）。新增交付类 = 协议内注册一行，orchestrator 零改动。
- **D2 六动词显式化**：`dispatch.go` 从 Execute 原样抽出四个 helper——`authorizeNode`（Resolve+Authorize：manifest/权限/预算/executor）、`prepareDispatch`（Dispatch setup：inputs/request/style/subject/context）、`dispatchAttempt`（Dispatch 执行：计时调用+控制观察）、`observeResult`（Observe 校验：result binding/预算/shadow 泄漏不变量）。**逐字搬移，行为字节不变**；主循环逐动词注释分段。
- **D3 kernel 不变量结构化守卫**：capability 合同面（ExecutionRequest / ExecutionResult / LegacyNodeInput / OutputArtifactDraft）**按类型系统反射守卫**——不得含 func/chan/unsafe/`*writingstore.Store`。能力拿到的是值（hash+ref 的 artifact 草稿），一切提交在 orchestrator。守卫测试 `TestCapabilityContractHasNoWriteSurface` 钉死漂移。
- **D4 未注册 class = no-op**：`Drive` 对未知类零动作（交付语义按类 opt-in）；已知类 + 缺输入走 driver 自身校验 fail-closed，绝不静默跳过。

## 26.3 实现切片

- `writingruntime/dispatch.go`（新，118 行）：六动词 helper + `errAttemptExhausted`。
- `writingruntime/delivery_protocol.go`：`deliveryDriver` 类型 + `deliveryDrivers()` 路由表 + `Drive()` 门面；`CommitDraftCandidate`/`BuildQualityDelivery` 原样保留为 driver 内核。
- `writingruntime/orchestrator.go`：主循环重构为六动词分段（796→749 行，且 delivery switch 移除）；`writingruntime/orchestrator_invariants_test.go`（新）：守卫测试 ×2。

## 26.4 验收

- 离线：writingruntime 包全测试 PASS（含既有 delivery/vertical/orchestrator 回归 + 新守卫测试）。
- 双仓默认并行 `-count=1` 全树带 DB 零失败。
- 行数：orchestrator.go 796→749（且 dispatch.go 承载 118 行动词实现——六动词代码与骨架分离，骨架本身净降约 80 行）。

## 26.5 遗留与边界

- **editorial/harness 面**不在本次范围（其 adapter 已 fail-closed，见 executor_adapters 注释）。
- delivery 路由表当前含两类（draft/quality）；sourced/strict 的 evidence/fact 无交付语义，符合预期。
- M3（Hook Bus）将把 Observe 动词的 telemetry/audit 面进一步插件化——六动词显式化正是它的前置。
