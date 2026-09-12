# ProjectMemory M2（claims 佐证道 + 实体候选池）

**日期**：2026-09-02
**范围**：OSS + Commercial 双仓同步；V2.9 主线第二个里程碑。纯存储层与治理语义，不涉及运行时路由、部署或真实流量。

## 产品定位矫正（docs/18 §18.10）

产品定位确认为**非虚构写作**。落地影响：实体类型按非虚构封闭集设计（person/organization/place/concept/term/product/event/work，无角色卡）；through_line 顺延 M2.5 并改造为领域中立的"未决线索账本"（不采纳 NarraCat 的戏剧问题/伏笔语义）；词表 v1 维持现状，领域新谓词走 `x-` 扩展积累后升 v2。

## 变更

1. **迁移 `100_project_memory_m2`**：
   - `project_claims`（佐证道：与 fact 同形三元组 + 可变 status open/supported/promoted/rejected + `promoted_fact_id` 链接；内容列挂不可变触发器——claim 永远不改"说了什么"，只改"什么在支持它"；partial unique index 保证同一项目同一内容只有一条 open/supported claim，佐证不碎片化）；
   - `project_claim_evidence`（证据引用账本：`(claim_id, evidence_hash)` 唯一——同一来源反复录入不膨胀佐证计数）；
   - `project_entities`（出生证明：不可变 identity 列（kind/canonical_name/aliases/description/provenance）+ 可变 status；`(project, kind, canonical_name)` 活跃唯一，归档后可重新注册）。
2. **`internal/projectmemory/claims.go`**（新）：`ClaimSupportThreshold=2`（佐证门槛，仅 advisory 信号，永不自动晋升）；`MaxEntityAliases=12`；封闭实体类型集；`EvidenceHash`（run 引用或排序 source_refs 的幂等引用键）；`ValidateClaim`（复用 M1 候选校验）；`ValidateEntity`（类型封闭集、canonical name 必填、别名 ≤12 且大小写不敏感去重、别名不得等于 canonical name、provenance 必填）。
3. **`writingstore/projectmemory.go` 扩展**：
   - `StageMemoryClaims`：任何 actor 可提出 claim（整批验证整批落库），重复 open 三元组报 `ErrConflict`；提出时的 run 自动成为第一条证据；
   - `CorroborateMemoryClaim`：任何 actor 可佐证（收集来源是机器工作），幂等按 evidence hash；独立引用数达到门槛后 open→supported；
   - `CommitMemoryClaim`：**HITL 门禁（仅 user actor）**；supported 是给用户的参考信号，open claim 也允许直接提交——用户判断优先，佐证只是输入；事实插入复用与候选提交相同的机制（含幂等与单值谓词自动区间失效）；
   - `RejectMemoryClaim`（user-only）；`ListMemoryClaims`；
   - `StageMemoryEntity`（任何 actor，candidate 道）/ `PromoteMemoryEntity`（user-only）/ `ArchiveMemoryEntity`（user-only，释放身份可重注册）/ `ListMemoryEntities`。
4. **重构**：抽取 `insertFactWithSupersede` 供候选提交与 claim 晋升双路复用（单值谓词探活 + 插入），消除两份复制；新增 `marshalStringSlice` 把 nil slice 归一为 `[]`（修复 M1 潜伏的 `jsonb_typeof='array'` 约束与 nil slice `null` 编码冲突，M2 测试暴露）。

## 验证（双仓一致，容器 `golang:1.25`）

- `projectmemory` 10 个单测（含 claim 校验复用、evidence hash 幂等/互异、实体出生证明归一化/别名上限/虚构类型拒绝）；`writingstore` 全量套件（fresh DB，迁移自动应用至 100）含两个新集成测试：claim 佐证晋升全生命周期（重复 staging 冲突、佐证幂等、supported 翻转、model 晋升拒绝、user 晋升、晋升后拒绝新证据、二次晋升冲突、user 拒绝道）与实体候选池（大小写别名归一、活跃身份碰撞、HITL 晋升、归档释放重注册、DB 级 identity 不可变）。
- `writingruntime`/`writingplan`/`writingkernel` 回归无影响；真实证据库未触碰；临时验证库已删除。
- 双仓 6 个变更文件 `diff` 字节一致。

## 边界

claim 佐证不自动晋升 canon（门槛只是信号）；实体不与 fact.subject 做自动解析链接（M3 编译器折叠实体卡时才需要）；terminology/decisions/open_questions/through_line（领域中立版）顺延 M2.5；evidence 衰减/归档（forgetting）属 roadmap §12 独立机制。
