# Task12 Governed Rollout Requirements

状态：已确认

日期：2026-08-29

## 问题与范围

Task11 已建立 MaterialAdapter、typed Artifact、B2 ExecutorAdapter 契约、Orchestrator 唯一提交和 writingstore 单一事实源，但三类旧算子仍只能离线测试。Task12 在不授予旧算子写权限的前提下，接通 Engine Step、Editorial Role 和 Harness Core，并以默认 shadow、显式放量、可回滚的方式完成三条写作纵向场景。

## 用户故事

1. 作为内容创作者，我希望系统能逐步采用更强的旧写作能力，同时文档、质量状态和来源仍保持一致。
2. 作为产品运营者，我希望能按能力、版本和用户范围控制放量，并能立即关闭异常适配器。
3. 作为审计与运维人员，我希望每次路由、材料校验、执行、影子比较和正式提交都可通过统一身份追溯。

## 验收标准

### R1 受治理的三类适配器

- Engine Step、Editorial Role、Harness Core 应只读取同一 `ExecutionRequest` 和 immutable Artifact。
- 三类适配器应只返回 provisional `ExecutionResult`，不得直接写 document、quality、run 或 canonical artifact。
- Harness 接入必须使用抽离出的无会话持久化 Core，不得调用会写 Session/终态的 `Harness.Run`。
- Editorial 接入必须使用无 Store 写入的 Role runner，不得调用会写 Task、Artifact、Decision 和 Event 的 `DAGExecutor`。
- 所有结果必须通过 Task11 的身份、权限、计量、lineage、provenance 和输出类型校验。

### R2 分级发布门禁

- 发布模式必须支持 `off → shadow → allowlist → percentage → enabled`，默认值为 `shadow`。
- 生产放量必须绑定 executor、adapter family、capability、capability version、policy version 和 activation key。
- allowlist 与 percentage 决策必须确定性可复现；策略缺失、无效或身份不匹配时必须 fail-closed 到 baseline。
- kill switch 应立即把候选执行关闭为 `off`，不依赖重新部署。
- 本 Task 不授权生产部署、真实用户放量或旧入口下线。

### R3 Shadow 隔离

- Shadow 必须使用与 baseline 相同的 Contract、Plan、输入 Artifact 和权限快照。
- Shadow 输出只能进入隔离内容区和对比证据，不得进入 canonical Artifact 图、文档版本或质量状态。
- Shadow 失败不得改变 baseline 业务结果；但越权、材料完整性失败和审计存储失败必须记录稳定错误码并触发适配器熔断候选。
- 对比至少记录输出类型完整性、哈希差异、来源覆盖、用量、时延、错误码和验证结果；不得把正文放入指标标签或普通日志。

### R4 Task11 埋点连续性

- 每次路由决策、执行开始/结束、材料完整性校验、影子比较和 `CompleteNodeAttempt` 提交都必须绑定同一 `ExecutionIdentity`。
- 审计记录必须包含 AdapterPolicy、rollout policy 版本、lane（baseline/shadow/candidate）、稳定错误码、ExecutionUsage 和 Artifact hashes。
- 低基数指标不得使用 run_id、user_id、artifact_id、正文或 source URL 作为标签。
- 至少提供 route decision、execution、duration、usage、material integrity、shadow comparison、commit success/failure 和 write-violation 指标。
- 指标接收器故障不得成为第二事实源；关键路由证据无法持久化时，候选不得成为 authoritative lane。

### R5 三条写作纵向场景

- 长文创作应覆盖 Contract → Outline → Section/Full Draft → Style/Contract Validation → Candidate/Accepted/Verified。
- 多材料综合应覆盖 snapshot → source/claim normalization → conflict detection → synthesis → evidence/consistency validation，并验证材料读取失败和来源冲突。
- 忠实改写应覆盖 original snapshot → meaning snapshot → revision set → semantic preservation → style validation；新增事实、核心观点变化和锁定块破坏必须产生 BLOCKER。

### R6 故障、防御与就绪

- 覆盖超时、取消、重复执行、重复提交、候选崩溃、shadow evidence 写入失败、策略变更、材料篡改、validator unavailable、等价 fallback、非等价降级和 canonical commit 失败。
- 同一 idempotency key 的成功提交只能产生一组 canonical Artifact。
- 发布就绪报告必须区分“本地 shadow-ready”“可灰度”“可全量”，不得把历史 WABench shadow 证据当作 governed runtime 放量证据。
- OSS 与商业版共同文件应保持一致，并分别完成后端与前端回归。

## 非目标

- 不在 Task12 内自动部署、打开生产流量或删除旧业务入口。
- 不允许 shadow 结果自动晋升 Candidate、Accepted 或 Verified。
- 不把 Prometheus、日志或对比数据库变成 writingstore 之外的文档事实源。
- 不接入仍拥有持久化副作用的完整 Harness.Run 或 Editorial DAGExecutor。
