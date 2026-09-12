# M1.1 — finalize executor 设计（评审稿）

> V3.0 M1 第一增量。目标：让治理运行时能端到端服务最简真实 plan `tpl_fast_v1`（draft→quality→**finalize**）。finalize 是唯一缺 executor 的收尾节点。本文只定契约与数据流，供评审后再实现。

## 21.1 职责与契约

`core.document.finalize`（executor `kernel.document.finalize`，权限 `document.revision`，NodeAction）：
- **输入**（ExecutionRequest.Inputs）：`full_draft`（bytes）、`quality_report`（bytes）。
- **输出**：`revision_set` artifact（content-addressed，经 canonical ContentGateway 落 `writing_artifact_contents`）。
- **副作用**：把 run 文档提交为一个新的 `writing_document_versions` 版本（accepted 态），推进 canonical 文档。
- **幂等**：以 `(run_id, node_id, attempt)` 的 IdempotencyKey 去重；重放返回同一 revision_set，不重复提交版本。

## 21.2 核心难点：文档质量门（已核实，决定设计）

`writing_enforce_document_quality_gate`（091）：提交 `quality_state <> 'candidate_draft'` 的版本，**必须**带 `quality_report_id` + `quality_report_version` + `snapshot_manifest_id` + `snapshot_version`，且交叉校验：
- 该 quality report 的 `document_id` = 版本 document_id、`candidate_version_id` = 新版本 version_id；
- 该 report 绑定的 snapshot 存在且 `snapshot_id/version` 与版本 `snapshot_manifest_id/version` 一致。

**含义**：finalize 不能凭空提交 accepted 版本——它必须复用 governed run 已产出的 **quality report + snapshot 谱系**。这不是 finalize 自己造的，而是 quality 节点 + orchestrator 的 canonical-commit/checkpoint 路径已建立的。所以 finalize 的正确性**依赖 M1 之前把 quality report + snapshot 的落库打通**（见 21.5 依赖）。

## 21.3 数据流（finalize executor 内部）

1. 从 ExecutionRequest.RunID → `store.LoadRuntimeRun` 取 `DocumentID` + `ContractID/Version/Hash` + `BaseVersionID`（run 的基版本）。
2. 读 `full_draft` bytes → 用 `lcp` 解析器构建 `DocumentVersion.Root`（markdown → AST：document/section/paragraph 树）。`VersionID = StableID("ver_", runID, nodeID, attempt)`，`BaseVersionID = run.BaseVersionID`。
3. 读 `quality_report` artifact → 解析出 `report_id/version`；取 run 的 `snapshot_id/version`（orchestrator checkpoint 已建）。
4. 组 `CommitDocumentVersionParams{Version(accepted), ExpectedBaseVersionID, ContractID/Version, Trace(actor=system/policy), QualityReportID/Version, SnapshotManifestID/Version}` → `store.CommitDocumentVersion`。质量门在此校验；不满足则 fail-closed（错误码 `DOCUMENT_QUALITY_GATE`，RetryNever——谱系缺失不会因重跑愈合）。
5. 产 `revision_set` artifact：内容 = 本次提交的 `{base_version_id, version_id, content_hash, applied_revisions}`（`RevisionSet` 语义，供 lineage），经 ContentGateway.Stage 落库，返回其 hash/ref。

## 21.4 关键设计决策（需你确认）

- **D1 actor**：finalize 是 kernel 提交，actor 用 `ActorSystem`（ID=`writingruntime.finalize`）还是 `ActorPolicy`？倾向 System（内核行为，非策略）。
- **D2 quality_state**：finalize 提交 `accepted_draft` 还是 `verified_deliverable`？verified 要求更严（blocker/error=0 + required validators 满足）。M1.1 先 `accepted_draft`（quality 节点产 report 即可），verified 留给 M1.2 validator 齐备后。
- **D3 AST 粒度**：full_draft 是整篇 markdown；M1.1 用 `lcp` 解析成 section/paragraph 树即可（不追求 block 级增量编辑）。
- **D4 snapshot 来源**：snapshot 由 orchestrator 的 checkpoint/commit 路径产（`CommitCheckpoint`）。需确认 finalize 执行时该 run 的 snapshot 已落库且 id/version 可取——若 quality 节点尚未触发 snapshot，finalize 拿不到 → 依赖 M1 的 commit 顺序（见 21.5）。
- **D5 失败语义**：质量门不过 = 上下文/证据缺口，走 fail-closed 拒绝节点（RetryNever），与 M4b 一致；基础设施错误（store 不可用）降级不拒绝。

## 21.5 依赖与顺序（2026-09-04 只读核查后修正）

只读核查结论（三项）：① orchestrator 每节点落的是 **run-progress checkpoint**（`writing_snapshots`，Manifest=进度），**不含质量阶段/quality_report 绑定**（Save 未设 QualityReportID/CandidateVersionID）；② quality 节点只产 `quality_report` **artifact**，`writing_quality_reports` 实体行（含 `candidate_version_id`）**从未被写**（`PutQualityReport` 非测试代码零调用者），`CommitDocumentVersion` 同样零调用者；③ crash 恢复只恢复节点进度，交付级谱系未持久化。

**结论：governed 路径从未实现"文档版本提交 + quality_report 实体落库 + 交付 snapshot 绑定"协议**——而 finalize 质量门强制要求该谱系。因此新增前置：

- **M1.0（前置，先于 M1.1）**：governed 执行路径的交付提交协议——quality 节点完成后，按质量门要求的交叉引用写入 `writing_quality_reports`（candidate_version_id→交付版本）+ 交付 snapshot 绑定；finalize 据此提交 accepted 版本。store 原语（`PutQualityReport`/`CommitDocumentVersion`/`CommitCheckpoint(bundle.QualityReport)`）齐备，缺的是驱动协议。协议形状见 docs/21 §21.8（版本 id 预分配，使 quality report 能引用 finalize 将提交的版本 id）。
- **M1.1**：finalize executor（本文），在 M1.0 谱系就绪后实现。
- 之后 M1.4 端到端服务 `tpl_fast_v1`。

## 21.6 测试计划（实现时）
- DB 门禁：draft+quality+snapshot 谱系齐 → finalize 提交 accepted 版本 + revision_set artifact + 幂等重放不重复提交。
- 质量门 fail-closed：缺 quality_report/snapshot → finalize 拒绝、错误码稳定、不产生半提交版本（事务回滚）。
- AST round-trip：full_draft markdown → Root → 版本 content_hash 稳定。
- 端到端（M1.4）：起后端 + 真实模型跑 `tpl_fast_v1`，finalize 后文档 current_version 前进、revision_set 可查。

## 21.7 决策记录
- **D1 actor = `ActorSystem`（ID `writingruntime.finalize`）**：已定。
- **D2 = `accepted_draft` 先行**：已定；verified 留 M1.2 validator 齐后。
- **M1.0 前置确认成立**（§21.5 核查）：governed 路径缺整个交付提交协议。

## 21.8 M1.0 交付提交协议设计（2026-09-04，只读核查后定稿）

store 侧原语已完备且自洽——`CommitCheckpoint(bundle)` 单事务内完成：snapshot 落库（含 `CandidateVersionID`）→ **quality_report 落库**（`PutQualityReport`：校验 report↔snapshot 绑定一致（`report.SnapshotID/Version/CandidateVersionID` 与 snapshot 相同、`snapshot.QualityReportID` 指回 report）、置 `SnapshotPersisted=true`）→ **document promotion**（UPDATE 版本 `quality_state=accepted_draft` + 回填 `quality_report_id/snapshot_manifest_id`，质量门在此放行）→ snapshot.created 事件。缺的只是 **governed 执行路径的驱动协议**，按依赖顺序两步：

**步骤 A — draft 节点提交 candidate 版本**（M1.0a，随 draft runner）：
draft executor 产出 `full_draft` 后，用 `lcp` 解析为 `DocumentVersion`（`VersionID=StableID("ver_",runID,"node_draft",attempt)`，`BaseVersionID=run.BaseVersionID`），经 `CommitDocumentVersion` 落 `candidate_draft` 版本（质量门对 candidate 无要求）。`CandidateVersionID` = 该版本 id，供后续 report/snapshot 引用。

**步骤 B — quality 节点后提交交付 snapshot + promotion**（M1.0b，随 quality runner / orchestrator）：
quality executor 产出 `quality_report` artifact 并解析出报告统计（blocker/error/...，candidate 版本在先）→ 构造 `CheckpointBundle{Snapshot: {..., CandidateVersionID, QualityReportID/Version, QualityReport: &report}, DocumentPromotion: {QualityState: accepted_draft, AcceptedAt}}` → `CommitCheckpoint`。`validateQualityReport` 的 accepted 门在此校验（blocker/open_error=0、VersionConsistent、SnapshotPersisted）。

**关键约束**：
1. **版本 id 预分配**：quality report 的 `candidate_version_id` 必须指向 draft 已提交的版本 id（步骤 A 的产物）——不是"finalize 将提交的 id"，因为 promotion UPDATE 的是**已存在**的版本行。这比原设想更简单：finalize 不再提交新版本，**步骤 B 的 promotion 已完成质量态推进**。
2. **对 M1.1 的简化**：finalize（ActorSystem）改为**读取已 promoted 的版本 + 产出 `revision_set` artifact**（记录 base→candidate 的变更与引用），不再调用 `CommitDocumentVersion`——提交与质量推进由 A/B 完成，finalize 只做收尾记账。质量门自然满足（版本已是 accepted）。
3. **report↔snapshot 绑定**是构造期的强校验（`CommitCheckpoint` 内），构造错误 fail-fast，不产生半提交。
4. 快照版本推进沿用 `run.LastSnapshotVersion+1`（`PersistentCheckpointRepository.Save` 现有逻辑扩展其 bundle，M1.0b 实现处）。

**实现切片**：M1.0a/b 各配 DB 门禁测试（candidate 版本落库；report+promotion 单事务、门不满足整体回滚）；随后 M1.1 按 §21.3 简化版实现。

## 21.9 M1.1 实现记录（2026-09-04，已交付）

- **`FinalizeRunner`**（`backend/internal/writingruntime/finalize_runner.go`）：按 §21.8 约束 2 的简化版实现——`LoadRuntimeRun` 取 run → `CurrentDocumentVersionID` 读已 promoted 的版本 → 产 revision_set JSON（`document_id`/`base_version_id`/`version_id`/`quality_state`/`finalized_at`，质量报告以 content hash 引用附于 `quality_report_id` 字段）→ 经 canonical ContentGateway 落 `writing_artifact_contents`，artifact 行 `ArtifactType=revision_set`。**不调 `CommitDocumentVersion`、不推进质量态**：提交与晋升由 M1.0 协议 A/B 完成，质量门自然满足。
- **Actor 纪律**：kernel 记账行为，actor = `ActorSystem`（ID `writingruntime.finalize`），D1 兑现。
- **幂等**：revision_set 内容寻址（同 body 同 hash，`ON CONFLICT DO NOTHING`），artifact 入账沿用节点 attempt 台账，重放不重复。
- **验收**：`TestDeliveryProtocolThroughOrchestrator`（真实 PG）扩为 draft→quality→finalize 三节点全链——断言 revision_set artifact 的内容寻址体与交付谱系一致（`version_id`==candidate、`document_id`、`quality_state=accepted_draft`）；`deliveryTestPlan` 支持尾节点类动态追加，为 M1.2+ 复用。双仓 3 文件字节一致；双仓 `-p 1` 全树带 DB 零失败。
- **遗留**：`quality_state` 钉在 `accepted_draft`（D2）；M1.2 validator 齐后随 verified 门一起放开。本实现未含"谱系缺失 fail-closed"独立分支——谱系由 M1.0 协议测试（`TestDeliveryCommitProtocol`）覆盖，finalize 对 `CurrentDocumentVersionID` 失败以 `ErrInvalidExecutionResult` 拒绝节点。
