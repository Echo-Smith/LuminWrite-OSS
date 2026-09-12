# M1.4 — 挂载 + 执行触发 + 端到端：设计与实现记录

> V3.0 M1 收官增量（2026-09-04 实现）。目标：把治理运行时真正挂上生产写路径——`WRITING_RUNTIME_MODE` 门控挂载 controller、per-capability runner 工厂接真实引擎客户端、批准后幂等触发执行、并以真实 HTTP 服务 + 一次性 PG（+ 可选真实模型）端到端验证。

## 24.1 实现切片

- **`server/governed_runners.go`**（新）：`governedRunnerFactory`（每 capability 构造 LegacyNodeRunner：draft=`NewWriteStepWithKB`（StepEnv profile + KbID 可达）、quality=`NewPostReviewStepWithSearchAndJiaozhen`（sensitive/jiaozhen 复用 legacy 装配）、outline=`governedOutlineStep` 包装（见 §24.2）、research=`SequentialGroup` 四步链（QueryPlan→Search→Relevance→Compress，M0b-2b 兑现）、evidence/fact=`ValidatorRunner`、finalize=`FinalizeRunner`）；`mountGovernedRuntime`（mode 门控组装 + 写回 `api.controller/trigger/capabilities`）；`governedInitialProvider`（run 契约 → 内容寻址 initial artifact）；`governedRunTrigger`（每 run 每进程幂等，后台执行，失败由 orchestrator 落账）。
- **触发双点**：`CreateRun`（计划无需审批 → planned 即触发）+ `ApproveRun`（审批后触发）。审批 transition 仍是状态权威，触发器只把"可调度态"变成执行。
- **`writingplan.CapabilityRegistry.Activate`**（新 seam）：把 default 目录中已声明的能力绑定 executor 并置 available——治理组合是唯一有权"供服"能力的主体。
- **registry 统一**（关键缝修复）：mount 后把 runtime 的可执行 registry 写回 `persistentWritingAPI.capabilities`，CompilePlan/CreateRun 的校验与 orchestrator 的 dispatch 看到同一目录（原 default 目录 declared-only，所有编译都会 T4）。
- **`WritingStoreTransitionRecorder` 重写**：从"仅 append 事件"改为委托 `Store.RecordRunTransition`——恢复幂等命令账 + `writing_runs.status` 投影（e2e 发现：HTTP 层读状态行，事件-only 会让 run 永远停在 planned）。
- **`StoreContextSource` 修序**：`document_state` 移到 project 分支之前——它是文档属性不是 project 属性，无 project 的文档也能满足 required document_state（e2e 发现）。

## 24.2 遗留语义缝的发现与修复（e2e 驱动）

| 发现 | 修复 |
|---|---|
| mount 后 CompilePlan 仍用 declared-only 目录 → 全部 T4 | registry 写回（§24.1） |
| Transition 只写事件 → run 状态行停滞 planned | recorder 委托 RecordRunTransition |
| outline guided-only + WritingTask nil panic + confirm 阻塞 | `governedOutlineStep`：Mode=guided + ConfirmTimeout=1s（legacy 超时自动确认）+ 从契约派生 WritingTask |
| draft required document_state 缺失（无 project 文档） | document_state 解析移出 project 分支 |

## 24.3 验收（`TestGovernedRunEndToEndThroughHTTP`）

真实 `Server.New`（一次性 PG，dbtest 私有库）+ 真实 chi 路由 + JWT：

- **离线（无模型 env）**：建文档→契约 put/confirm→编译计划（T1 outline_first）→创建 run（自动触发）→真实执行到首个模型步失败→治理性 pause（human recovery，`unsafe_retry` 纪律）→状态行正确推进。PASS。
- **live（TASK13_LLM_* env，凭据仅环境变量注入）**：同流程，真实模型 deepseek-v4-flash 跑完 outline→draft→quality→finalize 四节点——run `completed`、revision_set=1、文档 `accepted_draft`（promotion 生效）。PASS（约 107s）。
- 双仓 10 文件字节一致；双仓默认并行 `-count=1` 全树带 DB 零失败。

## 24.4 部署语义

- `WRITING_RUNTIME_MODE=off`（默认）：零行为变化，writing API 与 pre-V3.0 相同（RuntimeUnavailable on control routes）。
- `shadow`：governed run 走 baseline（权威产物）+ shadow candidate（隔离证据），rollout 晋升仍走 V2.8 门控；本增量不构成 allowlist/percentage/production 授权。
- 真实部署（1panel 灰度）只需：设 `WRITING_RUNTIME_MODE=shadow` + 模型 env；前端按 writing API 路由对接。

## 24.5 遗留

- M1.5 Capability Registry 统一（五套面收敛）——本增量的 Activate seam 是它的前奏。
- shadow comparison 的 evidence 聚合看板（`context_pressure` 同级）未接 UI。
- human recovery 的 resume 语义（`Orchestrator.Resume` 已接 controller）待真实前端走查。
