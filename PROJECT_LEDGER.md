# PROJECT_LEDGER — Writing Agent V2 (笔润智谈)

> **单一事实来源**：项目目标、阶段、决策记录、版本变更和操作日志。
> 每次重大变更必须在此文件追加记录，不允许仅存在于对话或 Slack 中。

---

## 1. 项目目标

### 1.1 使命

为内容创作者提供 AI 驱动的写作助手，支持多源素材检索、多风格 Profile、质量评审、记忆系统和编辑部多 Agent 协作，通过 WebSocket 实时通信交付流式写作体验。

### 1.2 核心指标

| 指标 | 当前值 | 目标值 | 衡量方式 |
|---|---|---|---|
| 文章生成平均耗时 | ~45s | < 30s | `agent_execution_duration_seconds` P50 |
| 质量评审通过率 | ~75% | > 85% | `post_review` step `passed=true` / total |
| 用户录用率 | 待统计 | > 40% | `workbuddy_adoptions` / `agent_traces completed` |
| LLM 调用错误率 | < 5% | < 2% | `llm_errors_total` / `llm_calls_total` |
| WS 连接稳定性 | > 99% | > 99.9% | `websocket_errors_total` / `websocket_connections_active` |

### 1.3 Non-Goals

- 不做通用对话机器人（聚焦写作场景）
- 不执行用户代码（无代码沙箱需求）
- 不做多租户 SaaS 平台（当前单部署模式）
- 不做自训练模型（使用第三方 LLM API）

---

## 2. 当前阶段

| 阶段 | 状态 | 起始日期 | 说明 |
|---|---|---|---|
| MVP — 基础写作管线 | ✅ 完成 | 2025-Q1 | Intent → Search → Write → Review → AutoFix |
| 多源搜索 + 知识库 | ✅ 完成 | 2025-Q2 | 8+ 搜索源、BM25+Dense+RRF 混合搜索、GraphRAG |
| 记忆系统 | ✅ 完成 | 2025-Q3 | 四层记忆 + 文件层 + 实体网络 |
| 编辑部多 Agent | ✅ 完成 | 2025-Q4 | 研究→写作→审校三 Agent 编排 |
| MCP 集成 | ✅ 完成 | 2026-Q1 | MCP 客户端 + 服务端 |
| **安全加固 + 生产化** | 🔄 进行中 | 2026-08 | 红队评估、Prompt Injection 防御、运维手册 |
| 自演进闭环 | 📋 规划中 | — | feedback → candidate → eval gate → rollout |
| 多实例水平扩展 | ✅ 完成 | 2026-08 | Redis session adapter + Docker Swarm + Nginx sticky session |
| **治理型写作运行时（Task11/Task12）** | 🔄 local shadow 就绪 | 2026-08-27 | WritingContract → ExecutablePlan → Artifact / Document → Quality Gates 已实现；仅允许本地 shadow 验证，未授权放量或生产 |

---

## 3. 架构概览

```
┌──────────────┐     WebSocket/SSE      ┌──────────────────────────────────┐
│  Frontend    │◄──────────────────────►│         Backend (Go)             │
│  React+Vite  │     JWT Auth           │                                  │
│  Nginx       │                        │  ┌────────────────────────────┐  │
│  │   Harness (LLM 持续会话)     │  │
                                        │  └──────────┬─────────────────┘  │
                                        │             │                    │
                                        │  ┌──────────▼─────────────────┐  │
                                        │  │  Steps (Intent→Search→    │  │
                                        │  │  Compress→Write→Review→   │  │
                                        │  │  AutoFix→Memory)          │  │
                                        │  └──────────┬─────────────────┘  │
                                        │             │                    │
                                        │  ┌──────────▼─────────────────┐  │
                                        │  │  Tool Registry             │  │
                                        │  │  (Step+Built-in+MCP)       │  │
                                        │  └────────────────────────────┘  │
                                        │                                  │
                                        │  ┌────────────┐ ┌────────────┐  │
                                        │  │ Memory Svc │ │ Editorial  │  │
                                        │  │ (4-layer)  │ │ Orchestrator│  │
                                        │  └─────┬──────┘ └─────┬──────┘  │
                                        └────────┼──────────────┼─────────┘
                                                 │              │
                                        ┌────────▼──────────────▼─────────┐
                                        │     PostgreSQL + pgvector        │
                                        │     + paradedb (BM25)            │
                                        └──────────────────────────────────┘
                                                 │
                                        ┌────────▼────────┐
                                        │   Docreader     │
                                        │   (gRPC sidecar)│
                                        └─────────────────┘
```

### 关键技术栈

| 层 | 技术 |
|---|---|
| Backend | Go 1.22+, chi router, coder/websocket |
| Frontend | React 18, Vite, TypeScript, Tailwind |
| Database | PostgreSQL 17 + pgvector + paradedb |
| LLM | DeepSeek API (默认), 支持 OpenAI 兼容接口 |
| Embedding | DashScope text-embedding-v3 (1024维) |
| 部署 | Docker Compose (4 服务) |
| 监控 | Prometheus 指标 + slog 结构化日志 |

---

## 4. 决策记录

> 格式：`D-<编号>` | 日期 | 决策 | 背景 | 选项 | 选择 | 理由 | 重新评估触发条件

### D-001 | 2025-Q1 | Agent 执行模式：双模式（Pipeline + Harness）

- **背景**：需要兼顾确定性流程和 LLM 自主编排
- **选项**：(a) 仅固定 Pipeline (b) 仅 Harness (c) 双模式可切换
- **选择**：(c) 双模式
- **理由**：Pipeline 保证关键步骤不遗漏；Harness 提供灵活编排；通过 `AGENT_MODE` 环境变量切换
- **重新评估**：已评估，Harness 已成为默认模式（详见 `docs/wiki/architecture-history.md`）

### D-002 | 2025-Q2 | 知识库方案：本地 PG + paradedb 替代外部 WeKnora

- **背景**：原依赖外部 WeKnora 服务，增加部署复杂度
- **选项**：(a) 继续使用 WeKnora (b) 迁移到本地 PG + paradedb
- **选择**：(b) 本地 PG
- **理由**：减少服务数量、统一数据层、BM25+Dense+GraphRAG 三路混合搜索质量更高
- **重新评估**：如果数据量超过 100 万文档，考虑独立搜索引擎

### D-003 | 2025-Q3 | 记忆系统：四层架构 + 文件层双向同步

- **背景**：用户偏好需要跨会话记忆，但纯 DB 方案不可审计
- **选项**：(a) 仅 DB (b) 仅文件 (c) DB + 文件双向同步
- **选择**：(c) DB + 文件双向同步
- **理由**：文件层提供人类可读/可编辑的记忆视图；DB 支持高效检索；`FileMemorySyncer` 保证一致性
- **重新评估**：如果同步冲突频繁，考虑 CRDT 或 last-write-wins 策略

### D-004 | 2025-Q4 | 编辑部编排：事件/决策双层模型

- **背景**：多 Agent 协作需要区分"客观完成事件"和"需要选择的决策"
- **选项**：(a) 统一用 Decision (b) 统一用 Event (c) 事件/决策双层
- **选择**：(c) 事件/决策双层
- **理由**：Agent 完成工作是客观事件（不涉及选择），用 Event 驱动状态转换；需要人类/系统选择时创建 Decision
- **重新评估**：如果双层模型导致状态追踪复杂度过高，考虑简化

### D-005 | 2026-Q1 | MCP 双向集成

- **背景**：既要消费外部 MCP 工具，又要将内置能力暴露给外部客户端
- **选项**：(a) 仅 MCP 客户端 (b) 仅 MCP 服务端 (c) 双向
- **选择**：(c) 双向
- **理由**：客户端模式扩展 Agent 能力；服务端模式允许其他工具调用搜索/知识库/记忆
- **重新评估**：如果 MCP 协议发生重大变更，需要同步更新

### D-006 | 2026-08-03 | 增加红队评估集和 Prompt Injection 防御

- **背景**：AgentOps 健康检查发现搜索结果直接注入 LLM prompt 存在 injection 风险
- **选项**：(a) 仅 system prompt 防御 (b) 仅输入 sanitization (c) 两者都做
- **选择**：(c) 两者都做
- **理由**：system prompt 防御指令降低 LLM 被劫持概率；输入 sanitization 在源头过滤已知注入模式
- **重新评估**：如果出现新型 injection 攻击，需要扩展 sanitization 规则和红队用例

### D-007 | 2026-08-27 | 以治理型写作运行时统一新写作内核

- **背景**：Harness、Pipeline 与 Editorial 已覆盖多个写作入口，但流式 Markdown 和执行完成事件不足以表达可审计的正式内容提交、质量状态与恢复边界。
- **选择**：以版本化 WritingContract、ExecutablePlan、Artifact、Document AST、Snapshot 和 Candidate / Accepted / Verified 质量状态为新能力的共同内核；旧入口仅经适配器兼容。
- **理由**：让用户意图、自动策略、内容版本、质量证据和执行恢复均可追溯，并防止执行器直接绕过提交与质量门。
- **重新评估**：如实际写作场景证明约束导致不可接受的延迟或质量损失，应调整策略、能力和审批规则，而非建立第二套权威状态路径。

### D-008 | 2026-08-29 | 受治理适配器采用 shadow-first 发布策略

- **背景**：新内核需与旧 Harness / Pipeline / Editorial 三类真实执行器并行验证，不能把候选结果直接写入正式内容。
- **选择**：默认仅 local shadow；shadow 使用独立内容 namespace、独立生命周期与 evidence，baseline 不等待候选，也不受其错误、取消或计费影响。
- **理由**：先在不影响 canonical Artifact、用户积分或基线路径的前提下收集对比证据。
- **重新评估**：allowlist 前必须具备持久化 evidence/sink、晋升运维门禁、真实 LLM 纵向验收及双版本全量回归；percentage 和生产另行授权。

### D-009 | 2026-09-01 | 晋升权限由持久化证据与 exact-scope 审批共同授予

- **背景**：Task12 local shadow 的 evidence 与 shadow body 仅在进程内，缺少可审计且不可绕过的 allowlist 晋升判断。
- **选择**：evidence 继续写入 append-only RunLedger；shadow body 写入独立 PostgreSQL 表；allowlist 必须同时满足健康证据、有效 policy 和绑定 exact hash/version/activation key 的限时追加审批。percentage / enabled 由该 gate 拒绝。
- **理由**：把晋升从操作纪律变成 fail-closed 代码与数据库约束，同时保留审批、激活和部署为彼此独立的授权动作。
- **重新评估**：只有在目标环境积累稳定的 allowlist evidence 并完成单独授权后，才讨论 percentage；本决策不自动授权生产。

---

## 5. 版本变更日志

| 版本 | 日期 | 变更内容 | 影响范围 |
|---|---|---|---|
| v2.0.0 | 2025-Q1 | 初始版本：基础写作管线 + WebSocket | 全部 |
| v2.1.0 | 2025-Q2 | 多源搜索 + 知识库集成 | Search, KB |
| v2.2.0 | 2025-Q3 | 四层记忆系统 + 实体网络 | Memory |
| v2.3.0 | 2025-Q4 | 编辑部多 Agent 编排 | Editorial |
| v2.4.0 | 2026-Q1 | MCP 双向集成（含已弃用的 UnifiedAgent） | Agent, MCP |
| v2.5.0 | 2026-Q1 | GraphRAG + 灰度发布 + WebAuthn | KB, Profile, Auth |
| v2.6.0 | 2026-08 | 红队评估 + Prompt Injection 防御 + 运维手册 | Security, Eval, Ops |
| v2.7.0-dev | 2026-08-27—29 | 治理型写作内核、材料 Artifact、三类旧执行器适配与 shadow rollout 加固 | Writing runtime, LCP, Store, Quality, Rollout |
| v2.7.0-dev | 2026-09-01 | Task13 持久化 shadow/evidence、晋升门禁、真实模型三场景验收 | Writing runtime, Store, Migration, Ops |

---

## 6. 操作日志

> 记录重大操作（部署、迁移、配置变更、事故响应）

| 日期 | 操作 | 执行者 | 原因 | 结果 |
|---|---|---|---|---|
| 2026-08-03 | 创建项目台账 | AgentOps 审计 | 健康检查 P0 缺口 | ✅ 完成 |
| 2026-08-03 | 增加红队评估集 | AgentOps 审计 | 健康检查 P1 缺口 | ✅ 完成 |
| 2026-08-03 | 增加 Prompt Injection 防御 | AgentOps 审计 | 健康检查 P1 缺口 | ✅ 完成 |
| 2026-08-27—29 | 完成治理运行时及 Task11/Task12 local-shadow 加固 | 稳定化分支 | 将正式内容、质量与影子验证收敛到可审计边界 | ✅ 代码与本地验证完成；未部署、未放量 |
| 2026-09-01 | 完成 Task13 工程化与双仓验收 | 稳定化分支 | 补齐持久化、晋升门禁和真实模型纵向证据 | ✅ local shadow 前置完成；未审批、未激活、未部署 |
| 2026-09-02 | 打通 allowlist 证据积累闭环并交付采集作业 | 稳定化分支 | 修复 allowlist miss 与证据 hash 的语义缝，证据可累积在治理 policy hash 下 | ✅ 双仓同步；仅采证据，未审批、未激活、未部署 |
| 2026-09-02 | 交付 percentage 阶梯工程与 ProjectMemory/Context Compiler 设计 | 稳定化分支 | 补齐 allowlist→percentage 阶梯的门禁、证据与审批工程；定稿 V2.9 记忆/上下文设计 | ✅ 双仓字节一致、容器验证全绿；未审批、未激活、未部署 |
| 2026-09-02 | 交付 production（enabled）晋升门禁并收口 V2.8 | 稳定化分支 | percentage→enabled 最后一级门禁、阶梯与审批链路；V2.8 八项全部完成 | ✅ 双仓字节一致、容器验证全绿；无生产审批、未激活、未部署 |

---

## 7. 证据索引

| 证据 ID | 类型 | 描述 | 路径 |
|---|---|---|---|
| E-001 | 架构审计 | AgentOps 健康检查报告 | `agentops-health-check-2026-08-03.md` |
| E-002 | 文档 | 已弃用架构历史 | `docs/wiki/architecture-history.md` |
| E-003 | 代码 | 编辑部编排器 | `backend/internal/editorial/orchestrator.go` |
| E-004 | 代码 | 记忆服务 | `backend/internal/memory/service.go` |
| E-005 | 代码 | MCP 注册表 | `backend/internal/mcp/registry.go` |
| E-006 | 代码 | 工具注册表 | `backend/internal/engine/tool_registry.go` |
| E-007 | 配置 | Docker Compose 部署 | `docker-compose.yml` |
| E-008 | 代码 | 评估服务 | `backend/internal/services/evaluation.go` |
| E-009 | 代码 | 红队评估集 | `backend/internal/services/redteam_eval.go` |
| E-010 | 代码 | Prompt Injection 防御 | `backend/internal/engine/guardrails.go` |
| E-011 | 文档 | 运维手册 | `docs/runbook.md` |
| E-012 | 文档 | 治理型写作运行时设计 | `docs/19-governed-writing-runtime.md` |
| E-013 | 代码 | 治理运行时、执行器适配与 shadow 隔离 | `backend/internal/writingruntime/` |
| E-014 | 代码 | 运行 / Artifact / Snapshot 事务存储 | `backend/internal/writingstore/` |
| E-015 | 文档 | Task12 就绪评估与放量前置 | `docs/releases/2026-08-29-governed-runtime-readiness.md` |
| E-016 | 代码 | Task13 持久化 shadow、evidence health 与晋升门禁 | `backend/internal/writingstore/rollout.go`、`backend/internal/writingruntime/persistent_rollout.go`、`promotion_gate.go` |
| E-017 | 验收 | Task13 真实模型、数据库与双仓回归报告 | `docs/releases/2026-09-01-task13-governance-productionization.md` |
| E-018 | 代码+验收 | allowlist 证据积累闭环与采集作业 | `backend/internal/writingruntime/rollout.go`、`rollout_executor.go`、`evidence_accumulation_test.go`、`docs/releases/2026-09-02-allowlist-evidence-accumulation.md` |
| E-019 | 代码+验收 | 治理 policy 表固化（`evidence_policy.go` + `cmd/evidence-policy-dump`）与本地证据库 3 场景证据 + exact-scope 审批记录（3×`allowed=true`，approval ID 见 release 文档；激活未授权） | `backend/internal/writingruntime/evidence_policy.go`、`backend/cmd/evidence-policy-dump/`、`backend/internal/writingruntime/vertical_test.go`、`docs/runbook.md` §9.5.1 |
| E-020 | 代码+验收 | percentage 阶梯工程：miss 证据语义修复、受众稳定桶、`PercentagePromotionGate`（含 allowlist 阶梯机械检查）、审批存储放开 target_mode（迁移 097）、CLI 支持；双仓字节一致 | `backend/internal/writingruntime/rollout.go`、`promotion_gate.go`、`backend/internal/writingstore/rollout.go`、`backend/internal/database/migrations/097_percentage_promotion.*.sql`、`backend/cmd/governance-gate/`、`docs/releases/2026-09-02-percentage-promotion-engineering.md`、`docs/runbook.md` §9.6 |
| E-021 | 设计 | ProjectMemory 与 Context Compiler 设计（四层分离、有效期区间 facts、受控词表、确定性编译、驻留层、Manifest 契约、M1–M5 顺序） | `docs/18-project-memory-context-compiler.md` |
| E-022 | 代码+验收 | production（enabled）晋升门禁：`ProductionPromotionGate`（percentage 阶段证据 + 阶段审批在期 + exact-scope 生产审批）、provider fail-closed（`production_gate_not_configured`）、审批存储放开 enabled（迁移 098，enabled 审批 health 携带 percentage hash）、CLI enabled 路由与阶梯校验 | `backend/internal/writingruntime/promotion_gate.go`、`backend/internal/writingstore/rollout.go`、`backend/internal/database/migrations/098_production_promotion.*.sql`、`backend/cmd/governance-gate/`、`docs/releases/2026-09-02-production-promotion-policy.md`、`docs/runbook.md` §9.8 |

---

## 8. 门控日志

| 日期 | 门控类型 | 检查项 | 结果 | 备注 |
|---|---|---|---|---|
| 2026-08-03 | 证据门 | 项目台账是否存在 | ✅ 通过 | 本文件创建 |
| 2026-08-03 | 风险门 | Prompt Injection 防御 | ✅ 通过 | guardrails.go 实现完成 |
| 2026-08-03 | 评估门 | 红队评估集 | ✅ 通过 | 20+ 对抗用例创建 |
| — | 发布门 | 回归评估通过 | ⏳ 待执行 | 需要在 profile publish 时触发 |
| — | 安全门 | 红队测试通过率 > 90% | ⏳ 待执行 | 需要运行红队评估 |
| 2026-08-29 | local shadow 门 | 双版本运行时、迁移、前后端门禁和真实纵向治理链路 | ✅ 通过 | 详见 `docs/releases/2026-08-29-governed-runtime-readiness.md`；不构成 allowlist 或生产授权 |
| 2026-09-01 | allowlist 前置工程门 | durable evidence/sink、晋升清单、真实 LLM 纵向验收、双仓全量回归 | ✅ 通过 | 只代表可评估；尚无真实审批、subject 激活或生产授权 |
| 2026-09-02 | allowlist 证据积累门 | 证据可在治理 policy hash 下跨次运行累积；采集作业与只读评估入口 | ✅ 通过 | 工程闭环打通；真实审批与激活仍待授权 |
| 2026-09-02 | allowlist 证据+审批门 | 本地证据库 3 场景 ×3 条 `shadow_compared`、0 失败；`assess allowed=true` ×3；append-only exact-scope 审批 ×3（24h TTL，2026-09-02T17:40Z 到期） | ✅ 通过 | 审批 ID 与 hash 记录于 `docs/releases/2026-09-02-allowlist-evidence-accumulation.md`；仅覆盖证据+审批，**激活仍是独立未授权变更** |
| — | allowlist 激活门 | 对指定 subject 激活 allowlist policy 的独立受控变更（需显式授权 + 双仓回归 + 复核审批在有效期内） | ⏳ 待授权 | 本任务未执行；percentage/production 仍被门禁拒绝 |
| 2026-09-02 | percentage 工程门 | miss 证据在 percentage policy hash 下累积、受众桶跨放量稳定、gate fail-closed、阶梯机械检查、迁移 097 双仓验证（fresh DB 自动应用） | ✅ 通过 | 纯工程交付：无 percentage 审批、未调 basis points、未部署、未切流；percentage 激活仍需 §9.6 走查 + 独立授权 |
| 2026-09-02 | 设计门 | ProjectMemory/Context Compiler 设计定稿（参考 NarraCat/LucidWrite/DeepSeek Harness 调研） | ✅ 通过 | 仅设计文档（`docs/18`）；实现待 V2.9 排期 |
| 2026-09-02 | production 工程门 | percentage 阶段证据与审批在期校验、enabled fail-closed、迁移 098 双仓验证（fresh DB 自动应用、append-only 保持） | ✅ 通过 | V2.8 清单收口：纯工程交付，无 enabled 审批、未部署、未切流；生产激活仍需 §9.8 走查 + 独立授权 |

---

## 9. 活跃风险

| 风险 ID | 描述 | 概率 | 影响 | 缓解措施 | 状态 |
|---|---|---|---|---|---|
| R-001 | LLM API 额度耗尽 | 中 | 高 | 断路器 + 额度监控 + 降级模式 | 已缓解 |
| R-002 | 搜索结果注入恶意 prompt | 中 | 高 | guardrails.go sanitization + system prompt 防御 | 已缓解 |
| R-003 | 记忆无限增长 | 低 | 中 | 需要实现 forgetting 策略 | 待处理 |
| R-004 | 编辑部 Agent 死锁 | 低 | 中 | Lease 超时 + 重试上限 + 人类升级 | 已缓解 |
| R-005 | DB 迁移失败 | 低 | 高 | migrator.go 回滚 + 启动前检查 | 已缓解 |
| R-006 | 将 shadow 结论误作生产就绪 | 中 | 高 | 明确 rollout 阶梯、独立 shadow namespace、fail-closed gate 与单独授权 | 已缓解，持续关注 |
| R-007 | shadow evidence 仅在内存，无法支撑晋升 | 高 | 高 | RunLedger evidence 与独立 shadow sink 已持久化 | 已缓解 |

---

## 10. 下一个决策点

| 日期 | 决策 | 负责人 | 状态 |
|---|---|---|---|
| 待定 | 是否实现自演进闭环 | — | 📋 规划中 |
| 待定 | 是否迁移到 K8s | — | 📋 规划中 |
| 待定 | 是否增加 RBAC 细粒度权限 | — | 📋 规划中 |
| 待定 | 记忆 forgetting 策略选型 | — | 📋 规划中 |
| 待授权 | 是否对指定 subject 激活 allowlist policy | — | ⛔ 未授权 |
| 待定 | 是否进入 percentage 评估 | — | ⛔ 需先积累 allowlist 证据 |

---

*最后更新：2026-09-02*
*维护者：Writing Agent V2 Team*
