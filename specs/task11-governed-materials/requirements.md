# Task11 Governed Materials Requirements

状态：已确认

日期：2026-08-29

## 问题与范围

现有用户材料、知识库检索和旧执行器仍可能以字符串、进程内状态或旧表记录参与写作。Task11 将材料和来源转换为有完整 lineage 的 typed Artifact，并固定 B2 适配契约；Harness、Editorial DAG 和 Engine 旧业务路径不接收 governed 流量。

## 用户故事

1. 作为内容创作者，我希望知道材料是否已完成快照和完整性校验，以便确认文章使用的是哪一版材料。
2. 作为写作运行时，我希望只消费带身份、哈希和来源的 Artifact，以免长上下文或旧状态成为隐式事实源。
3. 作为运维人员，我希望旧执行器在正式放量前通过同一套契约测试，并在越权写入时稳定失败。

## 验收标准

### R1 材料归一化

- 当文本、文件、URL 或知识库材料进入 governed run 时，系统应生成不可变 Material Artifact，记录内容哈希、内容引用、所有者、来源类型、来源标识和快照时间。
- 当检索结果进入 governed run 时，系统应生成 SourcePack、ResearchNote 或 ClaimMap，而不是把结果只拼接进 Prompt。
- 当内容引用的实际字节与声明哈希不一致时，系统应以 `MATERIAL_INTEGRITY_FAILED` 失败关闭。
- 当来源快照无法持久化时，系统应以 `SOURCE_SNAPSHOT_FAILED` 失败关闭。

### R2 单一事实来源

- 当 Executor 完成节点时，Executor 应只返回 provisional `ExecutionResult`。
- 当结果有效时，Orchestrator 应通过 `CompleteNodeAttempt` 在同一事务中提交 Artifact、Attempt 和 RunEvent。
- 当运行恢复时，系统应只读取 writingstore 的 Plan、Attempt、Artifact、Ledger 和 Snapshot。
- 当旧算子尝试直接写文档、质量状态或 canonical Artifact 时，系统应返回 `LEGACY_WRITE_VIOLATION`。

### R3 B2 契约预埋

- 当任意旧算子声明适配能力时，它应提供版本化 Descriptor、身份模型、输入输出类型、权限、预算、取消能力和副作用级别。
- 当 ExecutionRequest 与 Descriptor 或 WritingContract 引用不匹配时，系统应返回稳定的契约错误码。
- 当 ExecutionResult 缺失计量、lineage、provenance 或声明输出时，系统应拒绝提交。
- 当 Task11 构建默认生产注册表时，Harness、Editorial DAG 和 Engine 旧算子应保持不可用。
- 当运行离线契约测试时，三类适配器应能使用 fake operator 验证相同的 conformance suite。

### R4 材料冲突

- 当用户材料与外部来源包含冲突声明时，系统应生成 typed Finding，并保留双方来源引用。
- 当合约的 `conflict_handling=ask_user` 时，冲突应阻止自动接受，并要求用户决策。
- 用户材料应保持最高优先级，但优先级不得删除或隐藏冲突证据。

### R5 用户界面

- 当用户查看材料列表时，界面应区分“素材库可用”“快照待运行创建”和“已具备治理元数据”。
- 当用户从材料发起写作时，前端应传递材料引用，不再把内容预览复制为权威材料正文。
- 当后端尚未返回治理字段时，前端应显示兼容状态，不得声称材料已纳入治理链路。

## 非目标

- Task11 不打开旧 Harness、Editorial DAG 或 Engine 的生产流量。
- Task11 不允许双写 canonical writingstore 与旧 Artifact 表。
- Task11 不完成三条端到端纵向场景；该工作属于 Task12。
- Task11 不把素材库记录本身当作某次 Run 的 Artifact。
