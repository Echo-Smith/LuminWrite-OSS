# ProjectMemory M1（项目记忆基础：canon facts + 候选道 + HITL 门）

**日期**：2026-09-02
**范围**：OSS + Commercial 双仓同步；V2.9 主线第一步，落地 `docs/18` 设计的 M1 里程碑。纯存储层与治理语义，不涉及运行时路由、部署或真实流量。

## 设计矫正（对照代码，全文见 docs/18 §18.9）

原设计稿按 roadmap 理想形态写就，落地前对照真实内核矫正了四点：project 是新增一级对象（内核原本只有 `doc_*` + owner）、HITL 用 ActorType 硬门禁（user-only）而非可配置角色、append-only 复用 089 的 `writing_reject_immutable_columns` 触发器、词表版本化在 Go 侧不进 DB CHECK。另补了一条设计稿未预见的语义：**单值/多值谓词二分**。

## 变更

1. **迁移 `099_project_memory`**：
   - `writing_projects`（`prj_*`，owner FK users，active/archived）；
   - `writing_documents` 加 nullable `project_id`（FK，ON DELETE SET NULL）+ 部分索引；
   - `project_memory_candidates`（候选暂存道：batch_id、受控词表版本、扩展谓词警告、staged/committed/rejected）；
   - `project_facts`（有效期区间行：`valid_from`/`valid_to`/`superseded_by`，provenance 走 `source_run_id`+`source_refs`，内容列挂 `writing_reject_immutable_columns` 触发器——仅区间列可变）；partial unique index 保证同一活跃内容三元组唯一（重复提交幂等）。
2. **`internal/projectmemory` 包**（新）：`VocabularyVersion=1` 受控谓词词表（identity/location/possession/goal/state/relationship/style_rule/constraint）+ `x-` 扩展（允许并警告）；`NormalizeSubject`（trim/折叠空白/小写）；`ContentKey`（project+subject+predicate+object+词表版本，relationship 折叠端点对）；`ValidateCandidate`（三元组完整性、词表成员、provenance 必填、as_of 必填，归一化原位完成）；`SingleValued`（单值谓词一个 subject+predicate 只允许一条 active fact，`x-` 扩展默认多值）。
3. **`writingstore/projectmemory.go`**（新）：
   - `CreateProject`/`GetProject`/`SetDocumentProject`；
   - `StageMemoryCandidates`：整批验证整批落库（含无效候选的批次整体拒绝，不产生部分通道），任何 actor 可暂存——这是模型唯一通道；
   - `CommitMemoryCandidate`：**HITL 门禁**（仅 `ActorUser`），内容 hash 幂等（重复提交返回既有 fact 并关闭候选），单值谓词自动区间失效（新状态盖旧状态 `valid_to`+`superseded_by`，回填早于现有 `valid_from` 报 `ErrConflict`），多值谓词共存；
   - `RejectMemoryCandidate`（同样 user-only）、`SupersedeFact`（区间失效唯一写路径，user-only）、`ListActiveFacts`/`GetFact`。
4. **测试**：包级 7 个单测（归一化、扩展警告、provenance、自指 relationship 拒绝、端点折叠、词表完整性）；store 集成测试 `TestProjectMemoryCandidateLifecycleRequiresUserCommit`（整批拒绝、staged 道读取、HITL 拒绝 model actor、幂等 replay、状态变更区间失效、回填冲突、relationship 折叠与共存、subject 过滤、DB 级不可变触发器、缺失后继的 supersede 拒绝、拒绝道清理）。

## 验证（双仓一致，容器 `golang:1.25`）

- fresh DB 全量 `writingstore` 套件（迁移自动应用至 099）+ `projectmemory` 单测：全部通过；`writingruntime`/`writingplan`/`writingkernel` 回归无影响。
- 真实证据库 `writing_agent_evidence` 未触碰（3 条 allowlist 审批原样在库）；临时验证库已删除。
- 双仓 6 个变更文件 `diff` 字节一致。

## 边界

M1 不含 Context Compiler（M3）、entities/terminology/through_line（M2）、claims 晋升（M2）；provenance 不完整的候选在暂存即被拒绝，不存在无出处事实进入 canon 的路径。任何 actor 都能读 canon（`ListActiveFacts`），写 canon 只能经 user actor——HITL 语义由 store 强制而非调用方自觉。
