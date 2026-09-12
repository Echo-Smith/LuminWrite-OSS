# ProjectMemory M2.5（curated 项目状态：术语 / 决策 / 未决问题 / 贯穿线账本）

**日期**：2026-09-02
**范围**：OSS + Commercial 双仓同步；V2.9 主线第三步。纯存储层与治理语义，不涉及运行时路由、部署或真实流量。本里程碑交付 M3 编译器所需的四类 curated 输入，其中贯穿线按非虚构定位改造为领域中立的"未决线索账本"（docs/18 §18.10）。

## 变更

1. **迁移 `101_project_memory_curated`**，四张表全部遵循既定治理模式（任何 actor 暂存候选 → user-only 晋升与生命周期关闭 → 内容列挂 089 `writing_reject_immutable_columns` 触发器）：
   - `project_terminology`（术语指令：term/definition/aliases/forbidden；`(project, term)` 活跃唯一——术语在册时同名条目连暂存都被拒绝，归档释放；编译器优先级高于通用风格规则）；
   - `project_decisions`（已定决策：statement/rationale/decided_at/supersedes；决策永不被编辑，推翻 = 晋升一条 supersedes 后继，且目标必须仍是 active——同一事务内完成关闭，杜绝替换已失效决策）；
   - `project_open_questions`（未决问题账本：open/answered/dropped；提出是机器工作任何 actor 可做，回答/丢弃是 user-only；可链接 settling fact）；
   - `project_threads`（贯穿线账本：label/summary/**resident**/resolved_fact_id；resident 线索进编译器驻留层——永不丢弃、独立预算（M3 落实）；status candidate→active→resolved/archived）。
2. **`internal/projectmemory/curated.go`**（新）：Terminology/Decision/OpenQuestion/Thread 类型；`ValidateTerminology`（term 必填、alias/forbidden ≤ `MaxAliases` 且大小写不敏感去重、三者互不重叠）；`MaxEntityAliases` 更名 `MaxAliases` 供实体/术语共用；决策/问题/线索的最小校验；provenance 必填沿用 M1 规则。
3. **`writingstore/projectmemory_curated.go`**（新，15 个方法）：四类对象各自的 Stage/Promote/Archive/List，加上 `AnswerOpenQuestion`/`DropOpenQuestion`/`ResolveThread` 生命周期关闭，全部 user-only；`PromoteDecision` 的 supersedes 链在事务内校验并关闭目标。

## 实现过程中发现并修正的语义缝隙（测试驱动）

- **AnswerOpenQuestion 漏 SET `status='answered'`**：答案写入了但问题状态未翻转，测试的"答后不可 drop"断言暴露；
- **threads 不可变触发器列清单误含 `resolved_fact_id`**：Resolve 是生命周期关闭不是内容篡改，把该列移出触发器保护（迁移尚未提交，直接修正）；
- **PromoteDecision 的 supersedes UPDATE 占位符错误**（`$2` 未引用 `$1`）被 PG 42P18 暴露。

## 验证（双仓一致，容器 `golang:1.25`）

- `projectmemory` 12 个单测（术语归一化/碰撞/上限、决策/问题/线索最小校验）；`writingstore` 全量套件（fresh DB，迁移自动应用至 101）含 `TestProjectMemoryCuratedStateLifecycle`（术语 stage 期碰撞与归档重注册、决策替换链时序、模型 actor 四处拒绝、空答案拒绝、resident 线索解析链接 fact、DB 级内容不可变 ×2）；`writingruntime`/`writingplan`/`writingkernel` 回归全绿。
- 真实证据库未触碰（3 条 allowlist 审批原样在库）；临时验证库已删除。
- 双仓 8 个变更文件 `diff` 字节一致。

## 边界

四类 curated 对象目前没有与 facts 的自动解析链接（answer/thread 只存 fact id 引用）；编译器驻留层与分块预算在 M3 落实——本里程碑保证的是：这些对象进 canon 的唯一路径同样经过 HITL 门，provenance 完整，内容不可变可审计。
