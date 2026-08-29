# Task12 Governed Rollout Design

状态：已确认

日期：2026-08-29

## 决策摘要

Task12 采用“统一 B2 适配层 + 版本化 rollout policy + shadow isolation + 双层 telemetry”。Baseline 继续负责当前业务结果；候选适配器默认只运行 shadow。任何 authoritative candidate 选择都必须由显式策略授权，且最终结果仍只能由 Governed Orchestrator 提交。

```text
ExecutionRequest + Task11 material snapshot
                    ↓
             RolloutExecutor
        ┌───────────┼────────────┐
        │ off       │ shadow     │ allowlist / percentage / enabled
        ↓           ↓            ↓
     baseline    baseline      candidate adapter
                    + shadow          │
                       ↓              │ provisional result only
              isolated evidence      ↓
                    └──────→ Governed Orchestrator
                                  ↓
                    CompleteNodeAttempt transaction
                                  ↓
                  writingstore Artifact + Ledger
```

## Rollout policy

`AdapterRolloutPolicy` 是不可变、带版本和内容哈希的配置：

- `mode`: off、shadow、allowlist、percentage、enabled；
- `executor_id`、`adapter_family`、`capability_id`、`capability_version`；
- `policy_version`、`activation_key`、`kill_switch`；
- `allow_subjects` 与 0-10000 basis points；
- `effective_at`、`expires_at` 和 operator reason。

路由以 policy binding 和 execution identity 为输入。percentage 使用稳定哈希，不依赖进程随机数。配置错误、过期、绑定不一致或 kill switch 均选择 baseline，并产生稳定 reason code。`enabled` 仍不绕过 Registry、Request、Result、预算或质量门。

## 执行与隔离

`RolloutExecutor` 实现现有 `Executor`：

- off：只执行 baseline；
- shadow：baseline 为 authoritative lane，候选在同一 request 的只读副本上执行；候选内容由 `ShadowContentGateway` 隔离 stage；
- allowlist/percentage/enabled：命中时由候选返回 provisional result，未命中走 baseline；
- 任一 lane 都不能直接提交，Orchestrator 仍是唯一 authoritative writer。

Shadow evidence 只存储比较摘要和不可变引用：request identity、policy hash、lane、输出 manifest/hash、validator summary、usage、duration、error code。正文不进入普通日志或 metric label。Shadow evidence 存储失败不影响 baseline 响应，但该 policy 不得晋升为 candidate-authoritative。

## 三类适配器

### Engine Step

复用 `EngineStepRunner` 的纯返回路径。执行上下文由 Artifact 构建，Emitter 只能发送观察事件，输出经 ContentGateway staged 后成为 typed draft。

### Editorial Role

复用 `EditorialRoleNodeRunner` 与 `RoleAgentRunner.Run` 的返回值，不调用旧 Store 或 DAGExecutor。上游 Artifact 作为 `AgentContext` 输入，输出映射为 SourcePack、Outline、FullDraft、ReviewReport 或 RevisionSet。

### Harness Core

新增 `HarnessCoreInvoker` 边界，只负责意图/工具循环的计算结果，不加载或保存 Session、不写终态、不直接推送 canonical article。适配器把 Core 返回值映射为 typed Artifact。原 `Harness.Run` 继续 fail-closed，直到其全部副作用均迁出。

## Task11 埋点延续

观测分为两层：

1. `RuntimeTelemetry`：低基数 counter/histogram，用 family、executor、capability、mode、lane、status、stable error code 作标签；禁止 identity、用户和内容字段。
2. `RolloutEvidenceStore`：高基数审计记录，保存完整 `ExecutionIdentity`、AdapterPolicy、rollout policy hash、Artifact manifest、ExecutionUsage、比较结果和时间戳。

埋点边界：

- route_decision：策略解析后、执行前；
- material_integrity：Task11 MaterialAdapter 的 snapshot/verify 结果；
- execution_start / execution_complete：每个 lane；
- shadow_comparison：两个 lane 归一化后；
- canonical_commit：调用 `CompleteNodeAttempt` 前后；
- authority_violation：AdapterPolicy 或 legacy writer 越权时。

`ExecutionIdentity` 贯穿审计记录和结构化日志，但不进入低基数指标标签。Attempt/RunLedger 继续记录 canonical 稳定错误码；Telemetry 只是投影，不能反向修改业务状态。

## 质量与晋升

影子输出按同一 validator contract 评估，但评估结果属于 comparison evidence，不改变 canonical QualityReport。放量晋升条件：

- 无 authority violation、材料完整性或未知错误码；
- 输出契约完整率、BLOCKER 率、来源覆盖、semantic preservation、p95 时延和成本均达到预设门槛；
- 样本量和观察窗口满足策略；
- 人工明确授权从 shadow 进入 allowlist/percentage/enabled。

BLOCKER 不可豁免。非等价 fallback 必须降低 assurance，不能以 shadow 优胜为由提升质量状态。

## 回滚

kill switch 优先级最高。回滚只改变后续 route decision，不回写既有文档或审计证据。正在执行的 candidate 在 context 取消后进入稳定错误状态；已完成但未提交的 provisional result 丢弃。canonical commit 一旦成功，只能通过正常 Revision 流程修改。

## 兼容性

`docs/17-task12-shadow-rollout.md` 仅是旧 WABench/V2 外层路径的历史证据。新就绪结论必须来自 governed `ExecutionRequest → RolloutExecutor → CompleteNodeAttempt` 链路。OSS 与商业版共享本设计及核心实现；商业差异只能位于既有扩展点。
