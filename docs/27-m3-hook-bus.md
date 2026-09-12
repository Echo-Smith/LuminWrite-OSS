# M3 — Runtime Lifecycle + Hook Bus：设计与实现记录

> V3.0 M3（2026-09-04 实现）。目标（docs/20）：RunCreated→…→RunCompleted 生命周期；telemetry/audit/context/memory/rollout 作 Hook。验收红线：**Hook 只观察/横切，不得实现可绕过的 Contract/Plan/Artifact/Permission/Quality/Commit**。

## 27.1 排查结论（实现前）

- 状态迁移已有 append-only 事件账（`run.transitioned`/`run.transition_rejected`，M0b-2a/M1.4）。
- telemetry 已是 hook 形（`RuntimeTelemetry` 接口，24 个 emit 点、8 个有界 MetricKind），但**没有统一生命周期事件**，各点只报自家 kind。
- attempt/artifact/snapshot 账齐备。缺的是：命名生命周期时刻 + 一个观察者可插的 fan-out 面。

## 27.2 设计决策

- **D1 事件集以 orchestrator 实际拥有的时刻为根**：`run.dispatched → capability.before → artifact.submitted → checkpoint.before → capability.after → run.completed|paused|failed|cancelled`。RunCreated 本身是 API 层事实（CreateRun），不进 runtime bus。
- **D2 kernel 守卫是结构性的**：`RuntimeHook` 合同只收 `LifecycleSnapshot`（值类型：id/class/状态/完成集/成本/时长/artifact **类型**列表），返回 nothing。守卫测试反射钉死快照无 func/chan/store 句柄——hook 能观察、能自存记录、能富化遥测，**永远不能改 run、拒迁移、动 artifact 或实现可绕过的六大内核面**。
- **D3 同步 fan-out + panic 遏制**：注册序确定性分发（审计/测试友好）；单 hook panic 被捕获记日志、后续 hook 照跑——坏 hook 永不失联 run。
- **D4 telemetry 是 bus 的第一个 hook**：`lifecycleTelemetryHook` 把生命周期时刻投影到有界 `MetricLifecycle` 流（status=event、reason=state）——M2 telemetry 面与 M3 bus 一条管线，不是两套。
- **D5 终态分类集中**：`terminalLifecycleEvent(err)` 单点映射 outcome 错误 → 终态事件（completed/cancelled/paused/failed），failNode/finishControl/Execute 尾部声明式调用。

## 27.3 实现切片

- `writingruntime/hooks.go`（新）：`LifecycleEvent` ×9、`LifecycleSnapshot`、`RuntimeHook`/`RuntimeHookFunc`、`hookBus`（register/emit/containment）、`lifecycleTelemetryHook`、`terminalLifecycleEvent`。
- `writingruntime/telemetry.go`：`MetricLifecycle` 常量。
- `writingruntime/dispatch.go`：`lifecycleSnapshot()` 组装器（完成集排序、artifact 类型列表）。
- `writingruntime/orchestrator.go`：`Hooks []RuntimeHook` 字段；Execute 初始化 bus（telemetry 自动注册投影 hook）；六处 emit（dispatched/before/submitted/checkpoint.before/after/终态×4）。
- `writingruntime/hooks_test.go`（新）：bus fan-out + panic 遏制 + nil bus 安全 + 快照守卫 + 终态分类 + **真实 orchestrator 生命周期序列测试**（dispatched→before→submitted→checkpoint.before→after→completed，且遥测投影同 bus 可见）。

## 27.4 验收

- `TestOrchestratorEmitsLifecycleSequence`：真实 orchestrator 夹具上事件序列逐位匹配、快照 run id 正确、`MetricLifecycle` 经同一 bus 到达 metricCapture。
- 守卫测试：快照类型反射扫描无执行面。
- 双仓默认并行 `-count=1` 全树带 DB 零失败。

## 27.5 边界与后续

- memory/context/rollout 的 hook 化是渐进的：它们的现有面（ContextRuntime、RolloutEvidenceStore、RunLedger）已经是具名组件，逐步加 hook 适配器即可，无需再动 orchestrator。
- admin 面（能力清单端点同理）未来可挂 audit hook 持久化生命周期记录——当前 RunLedger 事件已覆盖迁移审计。
