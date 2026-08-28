# Governed Writing Runtime Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在 LuminBuddy V2 OSS 与商业版从第一条新请求开始启用统一的 WritingContract、类型化 RunPlan、LCP/Document AST、Artifact、质量门禁和完整快照运行时，覆盖长文创作、多材料综合和忠实改写。

**Architecture:** 以独立 writingkernel 领域包作为共同内核，使用确定性策略编译器把用户意图和系统建议降低为可验证的 ExecutablePlan。API 负责合约、文档、审批和投影，Worker 负责长任务执行；所有结果通过 LCP 编译为 Document AST，经质量门禁后才提交版本。现有 Harness、Pipeline、Editorial 只作为 Executor 适配器接入。

**Tech Stack:** Go 1.25、Chi、pgx/PostgreSQL、Redis 任务唤醒、WebSocket 运行事件、React/TypeScript/Vite、Zustand、JSON Schema、受限 Markdown parser、Docker verification gates。

**Target repositories:**

- OSS: /Users/marshecho/Codex/luminbuddy/writing-agent-v2-oss
- Commercial: /Users/marshecho/Codex/luminbuddy/writing-agent-v2-commercial

两版从 codex/v2-stabilization-oss 与 codex/v2-stabilization-commercial 的干净提交开始。共同内核、Schema、前端和测试文件必须逐字一致；商业差异只放在 capability、policy、validator、connector 和治理页面。

**Execution status (2026-08-27):**

- [x] Task 1：治理运行时设计基线与兼容边界已在双版本分支提交。
- [x] Task 2：LCP v1 Schema、fixtures、WritingContract 领域对象、严格解码和优先级测试已完成。
- [x] Task 3：Document AST、受限 Markdown/LCP parser、RevisionSet 与旧 ArticleStreamParser 适配已完成。
- [x] Task 4：类型化 Writing Plan IR、Capability Registry、T1–T4 策略编译与静态验证已完成。
- [x] Task 5：治理数据库、不可变账本、版本化 Artifact/Quality/Snapshot 与延迟质量门禁已完成。
- [x] Task 6：事务 Repository、精确幂等、单调事件账本与 Snapshot 原子提交已完成。
- [x] Task 7a：治理运行状态转换图、终态约束与非法转换审计契约已完成。
- [x] Task 7b：Executor typed input/output、能力版本绑定、候选 Artifact 与完整 lineage 校验已完成。
- [x] Task 7：ExecutorRegistry、事务状态事件、依赖调度、预算/权限/审批门禁、暂停/取消/同 Plan 恢复、完整检查点和受限旧执行链适配已完成；Harness/DAG 继续 fail closed，直至其权威写副作用被拆出。

---

## Task 1: 固定设计基线与共享协议

**Files:**

- Create: docs/19-governed-writing-runtime.md
- Create: docs/plans/2026-08-27-governed-writing-runtime-implementation.md
- Modify: README.md
- Modify: FILE_INDEX.md（若存在）

**Step 1: 固定两个仓库基线**

在两个目标仓库运行：

    git status --short --untracked-files=all
    git rev-parse --short HEAD
    git log -1 --oneline

Expected: 稳定分支无未提交代码；若存在本地差异，先记录并停止，不覆盖。

**Step 2: 同步设计文档**

将已确认的架构设计同步为 docs/19-governed-writing-runtime.md。必须包含 Contract、动态计划、LCP、AST、Artifact、三级质量状态、降级、快照、前端四区域布局和 OSS/Commercial 边界。

**Step 3: 添加协议索引**

README 和 FILE_INDEX 只新增新内核入口说明；声明旧 agent/workflow 协议仅用于历史回放或明确兼容入口，新请求不得绕过 governed runtime。

**Step 4: 分别提交**

    git add docs/19-governed-writing-runtime.md README.md FILE_INDEX.md
    git commit -m "docs: define governed runtime baseline"

两版提交内容必须可用 cmp 校验。

## Task 2: 定义 LCP v1 Schema 和 Go 领域值对象

**Files:**

- Create: specs/lcp/v1/writing-contract.schema.json
- Create: specs/lcp/v1/writing-plan.schema.json
- Create: specs/lcp/v1/artifact.schema.json
- Create: specs/lcp/v1/document-ast.schema.json
- Create: specs/lcp/v1/quality-report.schema.json
- Create: specs/lcp/v1/run-event.schema.json
- Create: specs/lcp/v1/snapshot-manifest.schema.json
- Create: specs/lcp/v1/fixtures/*.json
- Create: backend/internal/writingkernel/enums.go
- Create: backend/internal/writingkernel/contract.go
- Create: backend/internal/writingkernel/contract_test.go

**Step 1: 写失败测试**

测试 task_mode、orchestration_mode、assurance_level、approval_mode 的枚举；用户 source=user 的选择不可被 system recommendation 覆盖；合约版本、hash 或关键字段缺失时 Validate 必须失败。

    cd backend
    go test ./internal/writingkernel -run Contract -count=1

Expected: FAIL because package and canonical types do not exist.

**Step 2: 实现领域对象**

定义 WritingContract、ContractVersion、ExecutionControl、MaterialPolicy、EvidencePolicy、DeliverySpec、Inference 和 SourceAttribution。领域包只依赖标准库，不导入数据库、HTTP、模型 SDK 或旧 Agent 包。

**Step 3: 添加 fixtures 和 round-trip 测试**

每个 Schema 至少一份 valid 和 invalid fixture；测试 JSON marshal/unmarshal 不丢失显式用户控制、来源和版本字段。

**Step 4: 运行并提交**

    go test ./internal/writingkernel -count=1
    git add specs/lcp/v1 backend/internal/writingkernel
    git commit -m "feat: add writing contract domain schema"

## Task 3: 实现 Document AST、LCP 编译和 RevisionSet

**Files:**

- Create: backend/internal/lcp/ast.go
- Create: backend/internal/lcp/parser.go
- Create: backend/internal/lcp/parser_test.go
- Create: backend/internal/lcp/citation.go
- Create: backend/internal/lcp/citation_test.go
- Create: backend/internal/writingkernel/document.go
- Create: backend/internal/writingkernel/revision.go
- Create: backend/internal/writingkernel/document_test.go
- Modify: backend/internal/engine/steps/article_stream_parser.go
- Modify: backend/internal/engine/steps/article_stream_parser_test.go

**Step 1: 写失败测试**

覆盖段落、列表、引用、代码、表格、链接、受控引用标记、禁止 HTML/MDX、章节作用域禁止 heading、未闭合代码块、列数不一致和空文档。

    go test ./internal/lcp -count=1

Expected: FAIL because AST and parser do not exist.

**Step 2: 实现最小 AST**

支持 document、section、paragraph、text、strong、emphasis、link、citation、ordered/unordered list、blockquote、code_block、table、table_row、table_cell。块节点必须有稳定 block_id、origin 和 content_hash。

**Step 3: 实现 Lumin Markdown profile**

流式阶段允许 provisional buffer；Finalize 才做严格解析和 Schema 验证。章节标题、层级和 ID 由 ContentTask/RunPlan 提供，模型默认只输出章节正文。禁止语法返回稳定错误码。

**Step 4: 实现引用和 RevisionSet**

将 [[cite:source_id]] 编译为 typed citation node。RevisionSet 支持 insert、replace、delete、move，携带 base_version、目标 hash、修改原因和语义影响；版本冲突返回 CONFLICTED，禁止覆盖用户编辑。

**Step 5: 兼容现有 ArticleStreamParser**

旧 parser 只作为 legacy JSON/Markdown 输入适配器，输出 LCP section_body；不继续扩展旧 JSON 生成协议。保持既有 ArticleStreamParser 测试通过。

**Step 6: 运行并提交**

    go test ./internal/lcp ./internal/writingkernel ./internal/engine/steps -count=1
    git add backend/internal/lcp backend/internal/writingkernel backend/internal/engine/steps/article_stream_parser.go backend/internal/engine/steps/article_stream_parser_test.go
    git commit -m "feat: add LCP compiler and document revisions"

## Task 4: 实现类型化 Writing Plan IR 与动态编译

**Files:**

- Create: backend/internal/writingplan/ir.go
- Create: backend/internal/writingplan/ir_test.go
- Create: backend/internal/writingplan/capability.go
- Create: backend/internal/writingplan/capability_test.go
- Create: backend/internal/writingplan/compiler.go
- Create: backend/internal/writingplan/compiler_test.go
- Create: backend/internal/writingplan/templates.go
- Create: backend/internal/writingplan/templates_test.go

**Step 1: 写失败的静态验证测试**

覆盖未知 executor、缺失输入、循环依赖、无界 Retry/Refine、超预算、用户策略被替换、缺少最终交付物、缺少必需 Validator 和未声明权限。

    go test ./internal/writingplan -run 'Plan|Compiler|Capability' -count=1

Expected: FAIL because IR and compiler do not exist.

**Step 2: 实现 Plan IR**

支持 Sequence、Parallel、Map、Reduce、Condition、Retry、Refine、HumanGate、Validate 和 Fallback；所有循环、并行、重试和额外预算必须有上限。

**Step 3: 实现 Capability Registry**

Capability Manifest 声明输入/输出类型、权限、流式能力、成本、延迟、证据支持、声音保护和版本。首期使用进程内注册表，不建设能力市场。

**Step 4: 实现 IntentPlan → ExecutablePlan**

LLM 只能提出 capability_class 和语义步骤；确定性编译器解析 executor、补齐验证节点、计算预算和失败路径。模板为 T1，片段组合为 T2，原子能力动态拓扑为 T3，缺失能力为 T4。

**Step 5: 实现 StrategyDecision**

记录候选方案、选择来源、用户覆盖、原因、预测成本/延迟/置信度和 fallback。用户明确选择优先，只有 orchestration_mode=auto 才允许系统选择。

**Step 6: 运行并提交**

    go test ./internal/writingplan -count=1
    git add backend/internal/writingplan
    git commit -m "feat: add typed writing plan compiler"

## Task 5: 创建治理运行时数据库 Schema

**Files:**

- Create: backend/internal/database/migrations/089_writing_kernel_core.up.sql
- Create: backend/internal/database/migrations/089_writing_kernel_core.down.sql
- Create: backend/internal/database/migrations/090_writing_artifacts_quality.up.sql
- Create: backend/internal/database/migrations/090_writing_artifacts_quality.down.sql
- Create: backend/internal/database/migrations/091_writing_run_ledger.up.sql
- Create: backend/internal/database/migrations/091_writing_run_ledger.down.sql
- Modify: backend/internal/database/migrator.go
- Modify: backend/internal/database/migrator_test.go

**Step 1: 写迁移结构测试**

验证 up/down 文件存在、表名、主键、外键、唯一约束、hash/version 字段、索引以及 down 的父子表删除顺序。

    go test ./internal/database -run Migration -count=1

Expected: FAIL until 089–091 are present.

**Step 2: 创建 canonical 表**

    writing_contracts
    writing_documents
    writing_document_versions
    writing_runs
    writing_run_plans
    writing_artifacts
    writing_quality_reports
    writing_decisions
    writing_run_events
    writing_snapshots

另建 `writing_artifact_edges` 保存可约束的 Artifact lineage，`writing_node_attempts` 保存执行、租约、幂等键、成本和失败信息；二者属于治理辅助账本，不改变十张 canonical 对象表的定义。

新 governed run 不以 agent_traces 或 editorial_tasks 作为事实来源；旧表仅用于历史兼容和回放。

**Step 3: 实现版本与快照字段**

Contract、Plan、DocumentVersion、Artifact、QualityReport 和 SnapshotManifest 都保存 schema version、content hash、provenance、来源引用和 actor。RunEvent 使用 (run_id, sequence) 唯一约束并保持 append-only；跨聚合引用使用带 run/document/version 的组合外键。Accepted Draft 与 Verified Deliverable 在事务提交时验证 QualityReport、DocumentVersion 与完整持久化 Snapshot 的闭环，BLOCKER 不可豁免。

**Step 4: 临时 PostgreSQL 验证**

执行 up/down/up，检查 089–091 可重复应用、外键不悬空、快照和 Artifact 可被完整回溯。

**Step 5: 运行并提交**

    go test ./internal/database -run 'Migration|Writing' -count=1
    git add backend/internal/database/migrations backend/internal/database/migrator.go backend/internal/database/migrator_test.go
    git commit -m "feat: add governed writing runtime schema"

## Task 6: 实现事务存储、事件账本和 Snapshot 原子性

**Files:**

- Create: backend/internal/writingstore/store.go
- Create: backend/internal/writingstore/contracts.go
- Create: backend/internal/writingstore/documents.go
- Create: backend/internal/writingstore/runs.go
- Create: backend/internal/writingstore/artifacts.go
- Create: backend/internal/writingstore/quality.go
- Create: backend/internal/writingstore/snapshots.go
- Create: backend/internal/writingstore/idempotency.go
- Create: backend/internal/writingstore/store_test.go
- Create: backend/internal/database/migrations/092_writing_node_kind_alignment.up.sql
- Create: backend/internal/database/migrations/092_writing_node_kind_alignment.down.sql
- Modify: backend/internal/database/migrator_test.go

**Step 1: 写存储契约测试**

验证合约不可原地覆盖、文档版本乐观锁、重复 NodeAttempt 幂等、事件 sequence 单调、Artifact hash 不可变、BLOCKER 禁止正式提交、Snapshot 未保存禁止 Verified。

**Step 2: 实现 Repository**

Repository 返回领域对象，不泄漏 pgx rows 到 writingkernel。每个写操作接受 context 和 actor，并支持显式事务。

**Step 3: 实现 ledger transaction**

事件追加、当前 Run 投影和 Snapshot position 在同一事务中提交。队列只唤醒 Worker；丢消息后从数据库重新发现待执行节点。

**Step 4: 实现幂等和冲突**

使用 run_id:node_id:attempt 作为 idempotency key；文档提交检查 base_version 和 content hash；冲突返回领域错误。

**Step 5: 运行并提交**

    go test ./internal/writingstore -count=1
    git add backend/internal/writingstore
    git commit -m "feat: add transactional writing runtime store"

## Task 7: 实现 Runtime 状态机、Orchestrator 和 Executor 适配器

**Files:**

- Create: backend/internal/writingruntime/state.go
- Create: backend/internal/writingruntime/state_test.go
- Create: backend/internal/writingruntime/orchestrator.go
- Create: backend/internal/writingruntime/orchestrator_test.go
- Create: backend/internal/writingruntime/executor.go
- Create: backend/internal/writingruntime/executor_test.go
- Create: backend/internal/writingruntime/checkpoint.go
- Create: backend/internal/writingruntime/recovery.go
- Create: backend/internal/writingruntime/executor_adapters.go
- Modify: backend/internal/engine/context.go
- Modify: backend/internal/editorial/artifact_recorder.go

**Step 1: 写状态转换测试**

覆盖 DRAFT、CONTRACT_CONFIRMED、PLANNING、PLANNED、AWAITING_APPROVAL、RUNNING、PAUSING、PAUSED、REPLANNING、FAILED、CANCELLING、CANCELLED、COMPLETED。非法转换必须拒绝并写事件。

**Step 2: 实现 Orchestrator**

按依赖找到可执行节点，检查预算、权限、审批和取消状态，创建 NodeAttempt，调用 Executor，接收 Artifact，执行 Validator，再推进、重试、重规划或暂停。

**Step 3: 实现暂停/中止/恢复**

停止新节点调度，尝试取消当前调用，保存有效 Artifact 和 Checkpoint；恢复沿用同一 Plan 版本；不可安全重试的节点进入人工处理。

**Step 4: 接入旧 Executor**

Harness、Pipeline、Editorial 只能通过适配器实现 typed input/output；不得直接写 DocumentVersion、QualityState 或 Run 状态。ExecutionContext 只作为兼容运行上下文。

**Step 5: 运行并提交**

    go test ./internal/writingruntime ./internal/engine ./internal/editorial -count=1
    git add backend/internal/writingruntime backend/internal/engine/context.go backend/internal/editorial/artifact_recorder.go
    git commit -m "feat: add governed writing runtime orchestrator"

## Task 8: 实现质量门禁、三级状态和验证器降级

**Files:**

- Create: backend/internal/writingquality/types.go
- Create: backend/internal/writingquality/registry.go
- Create: backend/internal/writingquality/registry_test.go
- Create: backend/internal/writingquality/gates.go
- Create: backend/internal/writingquality/gates_test.go
- Create: backend/internal/writingquality/validators.go
- Create: backend/internal/writingquality/validators_test.go
- Modify: backend/internal/writingkernel/quality.go

**Step 1: 写状态门禁测试**

断言 Candidate 可以包含未验收内容；BLOCKER 无 waiver 且阻止 Accepted/Verified；ERROR 可经 DecisionRecord 成为 Accepted 但不能 Verified；achieved_assurance 低于 requested_assurance 阻止原合约 Verified；验证对象版本不等于提交版本时结果无效。

**Step 2: 实现验证器注册表**

验证器声明 criticality、degradation_policy、输入/输出类型、版本和等价 fallback。首期实现 AST integrity、required sections、contract consistency 和 semantic preservation 接口。

**Step 3: 实现降级矩阵**

AST、版本、Artifact hash 和必须语义保持 fail closed；可读性和风格可 skip with warning；非等价证据验证降级必须降低 achieved_assurance。

**Step 4: 实现 QualityReport**

Finding 包含稳定 code、severity、block/claim/source 定位、规则版本、解释、修复范围和 validator 状态。普通摘要与审计报告只做投影，不重复计算质量结论。

**Step 5: 运行并提交**

    go test ./internal/writingquality ./internal/writingkernel -count=1
    git add backend/internal/writingquality backend/internal/writingkernel/quality.go
    git commit -m "feat: add writing quality gates"

## Task 9: 暴露 V2 API、WebSocket 事件和权限边界

**Files:**

- Create: backend/internal/server/handlers_writing_contract.go
- Create: backend/internal/server/handlers_writing_document.go
- Create: backend/internal/server/handlers_writing_run.go
- Create: backend/internal/server/handlers_writing_quality.go
- Create: backend/internal/server/writing_routes_test.go
- Create: backend/internal/server/writing_event_adapter.go
- Modify: backend/internal/server/server.go
- Modify: backend/internal/websocket/protocol.go

**Step 1: 写 API 契约测试**

覆盖创建/确认 Contract、创建文档、获取版本、编译计划、创建 Run、查询 Run、SSE 事件、暂停、恢复、中止、质量和审计报告。缺少 Contract 或合法 Plan 时返回稳定错误。

**Step 2: 添加资源路径**

    POST /api/v2/documents
    GET  /api/v2/documents/:documentId
    GET  /api/v2/documents/:documentId/versions
    POST /api/v2/documents/:documentId/contracts
    POST /api/v2/contracts/:contractId/confirm
    POST /api/v2/documents/:documentId/plans
    POST /api/v2/runs
    GET  /api/v2/runs/:runId
    GET  /api/v2/runs/:runId/events
    POST /api/v2/runs/:runId/pause
    POST /api/v2/runs/:runId/resume
    POST /api/v2/runs/:runId/cancel
    GET  /api/v2/documents/:documentId/quality
    GET  /api/v2/documents/:documentId/audit-report

不把新字段继续堆进旧 workflow.start 或旧 process stream。

**Step 3: 实现事件投影**

事件包含 protocol、run_id、sequence、timestamp、status 和 typed payload。正文 delta 只表示 provisional；前端不得以最后一个 delta 推断 committed 或 Verified。

**Step 4: 实现权限和版本检查**

检查 user/workspace owner、Contract version、base document version、approval scope 和 capability permission。API 没有 BLOCKER waiver 入口。

**Step 5: 运行并提交**

    go test ./internal/server ./internal/websocket -count=1
    git add backend/internal/server backend/internal/websocket/protocol.go
    git commit -m "feat: expose governed writing APIs"

## Task 10: 接入前端状态和四区域写作工作台

**Files:**

- Create: frontend/src/lib/writing-runtime-types.ts
- Create: frontend/src/stores/writing-runtime-store.ts
- Create: frontend/src/stores/workspace-layout-store.ts
- Create: frontend/src/components/document/document-surface.tsx
- Create: frontend/src/components/document/document-block.tsx
- Create: frontend/src/components/document/revision-diff.tsx
- Create: frontend/src/components/runtime/run-summary-strip.tsx
- Create: frontend/src/components/runtime/run-detail-tabs.tsx
- Create: frontend/src/components/quality/quality-status.tsx
- Create: frontend/src/components/quality/quality-finding.tsx
- Create: frontend/src/components/assistant-ui/conversation-dock.tsx
- Create: frontend/tests/writing-runtime-store.test.ts
- Create: frontend/tests/workspace-layout-store.test.ts
- Create: frontend/tests/governed-workspace-ui.test.ts
- Create: frontend/tests/run-event-projection.test.ts
- Create: frontend/tests/conversation-panel-state.test.ts
- Create: frontend/tests/quality-state-ui.test.ts
- Modify: frontend/src/pages/writing-workspace.tsx
- Modify: frontend/src/components/sidebar/sidebar.tsx
- Modify: frontend/src/components/sidebar/detail-panel.tsx
- Modify: frontend/src/components/assistant-ui/thread.tsx
- Modify: frontend/src/components/composer/writing-composer.tsx
- Modify: frontend/src/components/composer/mode-picker.tsx
- Modify: frontend/src/components/tools/compact-step-timeline.tsx
- Modify: frontend/src/lib/types.ts
- Modify: frontend/src/index.css

**Step 1: 写状态和布局测试**

验证三项控件枚举、用户值优先、Provisional/Committed 映射、三级质量状态和布局偏好独立持久化。运行事件不得自动改变左栏、详情标签或对话状态。

布局状态：

    globalSidebar: expanded | collapsed
    detailPanel: expanded | collapsed | drawer
    detailTab: outline | materials | run | quality | versions
    conversationPanel: expanded | compact | minimized

**Step 2: 实现 stores 和类型**

writing-runtime-store 只消费 /api/v2 资源和事件；workspace-layout-store 按用户/设备、workspace、document 分别保存面板状态。旧 AgentStartPayload 只作为兼容适配器类型。

**Step 3: 保留左侧全局面板**

Sidebar 继续承担新建、模块、历史、用户和主题。用户主动控制展开/收回；运行、审批和质量事件不能调用 toggle。

**Step 4: 实现中央主舞台**

中央依次展示标题控件、最多两行 RunSummaryStrip、突出 DocumentSurface 和 ConversationDock。运行步骤使用写作语言，不展示 DAG/Agent 细节。

**Step 5: 实现右侧详情和对话三态**

DetailPanel 使用大纲、材料、运行、质量、版本标签。对话任务前默认展开；运行后保持用户选择；收起后显示具体状态并位于正文区域右下，不遮挡正文和详情。

**Step 6: 运行并提交**

    cd frontend
    npm run lint
    npm run build
    npm test
    git add frontend/src frontend/tests
    git commit -m "feat: add document-first governed workspace"

## Task 11: 将材料、来源和 Executor 结果纳入 Artifact

**Files:**

- Create: backend/internal/writingruntime/material_adapter.go
- Create: backend/internal/writingruntime/executor_adapters_test.go
- Modify: backend/internal/server/handlers_upload.go
- Modify: backend/internal/editorial/artifact_recorder.go
- Modify: backend/internal/editorial/dag_executor.go
- Modify: backend/internal/agent/harness.go
- Modify: backend/internal/engine/steps/steps.go
- Modify: frontend/src/components/topic/materials-tab.tsx

**Step 1: 写材料引用测试**

上传结果必须形成 Material/SourcePack Artifact，不能只存在 uploadedFileContent 或 Prompt 拼接字符串中。

**Step 2: 接入检索结果**

search、knowledge、read_source 结果映射为 SourcePack、ResearchNote 或 ClaimMap，并写入 provenance、content hash 和来源快照。

**Step 3: 接入三类 Executor**

输出 Outline、SectionDraft、ReviewReport 等 typed Artifact；executor 不得直接设置最终 Article 或 quality state。

**Step 4: 实现材料冲突**

用户材料优先级最高；来源冲突生成 Finding，并按 Contract 的 conflict_handling 进入询问或候选状态。

**Step 5: 运行并提交**

    go test ./internal/writingruntime ./internal/agent ./internal/engine ./internal/editorial -count=1
    git add backend/internal/server backend/internal/editorial backend/internal/agent backend/internal/engine frontend/src/components/topic/materials-tab.tsx
    git commit -m "feat: route writing materials through artifacts"

## Task 12: 打通三条纵向场景和发布门禁

**Files:**

- Create: specs/lcp/v1/fixtures/scenarios/*.json
- Create: backend/internal/writingruntime/integration_test.go
- Create: frontend/tests/governed-e2e-fixtures.test.ts
- Create: docs/releases/2026-08-27-governed-runtime-readiness.md

**Step 1: 测试长文创作**

Contract → Outline → Section Drafts → Full Draft → Style/Contract Validation → Candidate/Accepted/Verified。运行中允许用户调整左栏、对话和详情。

**Step 2: 测试多材料综合**

并行材料分析 → Conflict Detection → Synthesis → Audience Adaptation → Evidence/Consistency Validation；包含来源冲突和材料读取失败。

**Step 3: 测试忠实改写**

原文快照 → Meaning Snapshot → RevisionSet → Semantic Preservation → Style Validation。新增事实、核心观点变化和锁定块破坏必须为 BLOCKER。

**Step 4: 测试故障和降级**

覆盖 Worker 失联、SSE 断开、重复事件、重复提交、验证器超时、等价 fallback、非等价降级、Snapshot 失败、并发编辑和取消。

**Step 5: 双版本回归**

在两个目标仓库分别运行：

    make verify-backend
    make verify-frontend

另在临时 PostgreSQL 执行 migration up/down/up；用 cmp 检查共同文件。确认现有 Article Output Contract、DAG、Harness、Pipeline 和 WABench 测试不回归。

**Step 6: 分别提交 readiness**

    git add docs/releases/2026-08-27-governed-runtime-readiness.md
    git commit -m "docs: record governed runtime readiness"

不在本计划中自动 push；发布、灰度和旧入口下线需要另行确认。

## 生产不变量

以下不变量必须有单元测试、集成测试和至少一条线上指标：

    没有 WritingContract → 不得运行
    没有合法 ExecutablePlan → 不得调度
    Agent 不得绕过 Artifact/Revision 写正式文档
    Markdown 未解析和验证 → 不得提交
    存在 BLOCKER → 不得 Accepted、不得 Verified
    必需 Validator 未运行 → 不得 Verified
    非等价降级 → 必须降低 achieved_assurance
    实际保障低于合约要求 → 不得按原合约 Verified
    Snapshot 未持久化 → 不得正式提交或 Verified
    验证版本不等于提交版本 → 验证结果无效
    用户明确策略被系统静默替换 → 禁止
    无界循环、重试和并发 → 禁止
    普通质量摘要与审计报告状态不一致 → 禁止
    运行事件不得自动改动左栏、详情标签和对话状态

## 交付顺序

每个 Task 完成后独立提交并运行对应测试。新内核在第一条新请求上同时具备 Contract、Plan、Artifact、QualityState 和 Snapshot 引用；未具备这些条件时停留在 Planning/Candidate，不伪装为旧流程完成。
