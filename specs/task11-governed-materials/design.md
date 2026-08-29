# Task11 Governed Materials Design

状态：已确认

日期：2026-08-29

## 架构决策

Task11 采用“A 实施、B2 预埋”。素材库继续负责用户资产管理；MaterialAdapter 在运行边界读取并快照材料，产生属于该 Run 的初始 typed Artifact。Governed Orchestrator 是唯一提交者，writingstore 是唯一恢复与产品投影来源。

```text
Material Library / Search Source
              ↓ read-only
        MaterialAdapter
              ↓ staged immutable content
     Initial Artifact Bundle
              ↓
      Governed Orchestrator
              ↓ ExecutionRequest
       ExecutorAdapter contract
              ↓ provisional ExecutionResult
  CompleteNodeAttempt transaction
              ↓
 writingstore Artifact + Ledger + Checkpoint
```

## 材料模型

`MaterialDescriptor` 保存 material_id、owner_id、title、source_kind、source_ref、media_type、版本时间和治理元数据。`MaterialContentSource` 负责按租户读取实际字节，`ArtifactContentGateway` 负责不可变 Stage/Load。

MaterialAdapter 不直接写 writingstore。它输出 `InitialArtifactBundle`：

- `materials`：一次运行选择的 Material 快照清单；
- `source_pack`：带 query、排名、来源和快照引用的检索集合；
- `research_note`：对已快照来源的有出处摘要；
- `claim_map`：结构化声明、证据引用和冲突 Finding。

每项都带 SHA-256、content_ref、source_refs 和 provenance。初始 Artifact 没有 NodeAttempt，因此保持为受控 `InputArtifact`；首个节点的输出通过父引用把 lineage 接入 canonical Artifact 图。

## B2 适配契约

现有 `ExecutionRequest` 与 `ExecutionResult` 保留为 canonical wire model。新增 `ExecutorAdapter` 生命周期和 `AdapterPolicy`：Prepare 只能校验/归一化，Execute 只能调用算子，NormalizeResult 只能形成 provisional result。`AuthorityScope` 明确禁止 document、quality、run 和 artifact authoritative writes。

错误使用 `RuntimeError{Code, RetryClass, Cause}`，稳定码与 HTTP 文案分离。Orchestrator 在 Attempt 账本写入稳定错误码，而不是统一写 `EXECUTION_FAILED`。

Harness、Editorial 和 Engine 在 Task11 只有声明和 fake-backed conformance tests。生产注册需要显式 `TrafficModeEnabled`，默认构造器拒绝旧 adapter；Task12 才能提供开启实现。

## 冲突与质量

冲突检测使用规范化 Claim 的 subject/predicate 键比较不同值。用户材料具有决策优先级，但冲突双方都进入 Finding。`ask_user` 产生 `SOURCE_CONFLICT_REQUIRES_DECISION`；其他策略只能形成 Candidate，不能自行授予 Accepted/Verified。

## 前端

材料 API 增加可选 `governance` 投影。列表展示完整性、来源快照准备度和上次纳入运行时间。从材料开始写作只传 `material_refs`；旧 `user_materials` 字符串仅保留兼容读取，不再由该入口生成。

## 测试策略

- MaterialAdapter：租户隔离、确定性哈希、快照失败、篡改检测、来源 lineage、冲突 Finding。
- Adapter conformance：身份绑定、权限、计量、取消声明、越权能力、输出类型和 lineage。
- Orchestrator：稳定错误码、唯一事务提交、失败时零 Artifact。
- Registry：Task11 默认生产注册表对三类旧 adapter fail-closed。
- Frontend：材料引用传递和治理状态投影。
