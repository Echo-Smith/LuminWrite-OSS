# PROJECT_LEDGER — Writing Agent V2 (笔润智谈)

> **2026-09-07 测试更新**：SenseNova 接口下长文、多素材、忠实改写三个独立场景已有通过记录；完整 HTTP 链路仍未通过；曾返回 RPM/分配额度错误，但 9/7 09:57 同凭据最小请求已恢复成功，不能断言账户额度耗尽；人工盲评待做。不同场景测试参数及本次兼容修复见 [真实模型测试记录](docs/releases/2026-09-07-sensenova-live-tests.md)。下文 9/6 额度不足记录为历史状态。

> **单一事实来源**：项目目标、阶段、决策记录、版本变更和操作日志。
> 每次重大变更必须在此文件追加记录，不允许仅存在于对话或 Slack 中。

---

## 1. 项目目标

### 1.1 使命

为内容创作者提供 AI 驱动的写作助手，支持多源素材检索、多风格 Profile、质量评审、记忆系统和编辑部多 Agent 协作，通过 WebSocket 实时通信交付流式写作体验。

### 1.2 核心指标

> 下表为历史记录，未经本次 P0 构建重新采样，不作为当前版本验收结果。

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

### P0 当前状态（2026-09-07）

以下区分代码能力、验证证据、服务接入与线上启用；历史里程碑不代表当前构建通过生产验收。

| 项目 | 实现 | 测试 | 服务接入 | 线上启用 |
|---|---|---|---|---|
| A0 状态文档 | 本次完成 README/台账分列 | 文档交叉核对 | 不适用 | 不适用 |
| A1 三种模板与真实检索 | 完成 | HTTP 模板与无来源暂停回归通过 | 已接入治理运行路径 | 未确认部署；默认 off |
| A2 个人风格、可信属主与知识库 | 完成 | 属主隔离等回归通过 | 已接入数据库与素材路径 | 未确认部署；默认 off |
| A3 运行时监控 | 指标与仪表盘完成 | 指标回归通过 | exporter 已接入 | 未确认生产采集/仪表盘导入 |
| A4 策略持久化与 allowlist | 完成 | 策略与门控回归通过 | 策略 CLI、加载与门控已接入 | 未激活；percentage/enabled 服务路径不在本轮范围 |
| A5 跨进程执行与恢复 | 完成 | 锁竞争、失锁及 race 回归通过 | worker 扫描与恢复已接入 | 未确认生产运行 |
| A6 综合验收 | 自动化与证据工具就绪 | 9/6 双仓后端、前端回归通过；当前构建真实模型/人工盲评未完成 | 本地机器验证完成 | 新版本 1Panel 升级与冒烟未确认 |

证据见 [P0 发布记录](docs/releases/2026-09-06-p0-service-readiness.md)。9/6 真实模型尝试因 API 额度不足失败；9/1 候选侧材料仅为历史样本，不构成当前构建成对盲评。`WRITING_RUNTIME_MODE=off` 为默认配置。

### 历史阶段记录

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
| **治理型写作运行时（Task11/Task12）** | 🔄 P0 服务实现已接入，验收待收口 | 2026-08-27 | WritingContract → ExecutablePlan → Artifact / Document → Quality Gates 已实现；最新状态以本节 P0 分列表为准；未授权放量或生产 |

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

### D-010 | 2026-09-12 | AR-012 候选评估：运行后评估端点 + Proprietary sidecar 出仓部署

- **背景**：T10 要求接入 AutoResearch AR-012 独立综述生成服务作为对比候选。上游包声明 Proprietary 且无 LICENSE 文件；sidecar 为同步 650 秒级调用，无取消/队列，幂等签名不含内容 hash。
- **选择**：(1) License 纪律——上游 fork 只存在于工作区（`review-sidecar/`），不进任何发布仓库/镜像，本仓库仅含自有 Go 适配层；(2) 形态——运行后评估端点 + `writing_ar_review_jobs` 持久作业（迁移 111），不改模板/编译器/枚举，research_review 既有路径零回归；(3) 幂等——owner+contract+pack+outline+generator_version+mode 派生键（design.md §8），同输入重放原作业、超时只对账不重发；(4) 隔离——候选仅存内容寻址 blob + 作业行引用，永不写 run artifact/文档版本（A18 结构化保证）；(5) 参数化——REVIEW_SPEC.json 让批准提纲即体裁，阈值按语料等比放低，跨学科可验收。
- **理由**：仓库既有文档（requirements L56 / design §8 / 评估文档）已锁定 license 与 shadow-first 边界；运行后端点以约 60% 的改动面满足 T10 的对照与隔离验收，且后续升级为模板内并行节点时 client/转换器/幂等派生可整体复用。
- **重新评估**：12 案例人工盲评通过且成本可接受后，才立"候选成为用户可选生成器/模板内并行节点"任务；typed claims/locator corpus 升级与 compare_only 接入随之再议；跨机部署需先补受控 artifact 上传 API。

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
| 2026-09-02 | V2.9 启动：落地 ProjectMemory M1 | 稳定化分支 | 项目记忆基础：canon facts 区间行、候选道、HITL 门（对照代码矫正设计稿 §18.9） | ✅ 双仓字节一致、容器验证全绿；无运行时变更 |
| 2026-09-02 | V2.9 M2：交付 claims 佐证道与实体候选池 | 稳定化分支 | 非虚构定位矫正（§18.10）后的证据累积 → user 晋升闭环 | ✅ 双仓字节一致、容器验证全绿；无运行时变更 |
| 2026-09-02 | V2.9 M2.5：交付 curated 项目状态四类对象 | 稳定化分支 | 术语/决策/未决问题/领域中立贯穿线账本（迁移 101），全部沿用候选道 + user-only 门 + 内容不可变 | ✅ 双仓字节一致、容器验证全绿；无运行时变更 |
| 2026-09-02 | V2.9 M3：交付 Context Compiler MVP | 稳定化分支 | 确定性上下文编译：九块装配、分块预算、驻留层 fail-closed、显式缺省、envelope 落库（迁移 102） | ✅ 双仓字节一致、容器验证全绿；编译器未接入执行路径（M4） |
| 2026-09-02 | V2.9 M4a：交付 Manifest 上下文契约 + 编译器影子接线 | 稳定化分支 | 编译器成为 Node 执行数据入口（影子）；required 缺块只记录不拒绝 | ✅ 双仓字节一致、容器验证全绿；fail-closed 待 M4b，存量 run 零影响 |
| 2026-09-02 | V2.9 M4b：激活 required context fail-closed（outline + research） | 稳定化分支 | per-manifest EnforceRequiredContext 开关 + CONTEXT_REQUIRED_MISSING 拒绝语义；draft/quality/finalize 保持影子 | ✅ 双仓字节一致、容器验证全绿；激活面仅两个 capability |

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
| E-023 | 代码+验收 | ProjectMemory M1：`writing_projects`/`project_memory_candidates`/`project_facts`（迁移 099）、受控词表 v1、候选整批暂存、user-only commit/supersede、单值谓词自动区间失效、幂等 replay | `backend/internal/database/migrations/099_project_memory.*.sql`、`backend/internal/projectmemory/`、`backend/internal/writingstore/projectmemory.go`、`docs/releases/2026-09-02-project-memory-m1.md`、`docs/18-project-memory-context-compiler.md` §18.9 |
| E-024 | 代码+验收 | ProjectMemory M2：claims 佐证道（evidence hash 幂等、supported 仅 advisory、user-only 晋升复用单值失效机制）、实体候选池（非虚构封闭类型集、出生证明不可变、归档释放身份）、`insertFactWithSupersede` 双路复用、nil-slice JSON 归一修复 | `backend/internal/database/migrations/100_project_memory_m2.*.sql`、`backend/internal/projectmemory/claims.go`、`backend/internal/writingstore/projectmemory.go`、`docs/releases/2026-09-02-project-memory-m2.md` |
| E-025 | 代码+验收 | ProjectMemory M2.5：`project_terminology`（stage 期唯一 + 归档释放）、`project_decisions`（supersedes 链事务内关闭）、`project_open_questions`（open/answered/dropped）、`project_threads`（领域中立贯穿线 + resident 驻留标记）；`MaxAliases` 统一上限；测试暴露并修复三处实现缝隙（answer 状态漏翻转、threads 触发器列清单误含 resolved_fact_id、supersedes 占位符） | `backend/internal/database/migrations/101_project_memory_curated.*.sql`、`backend/internal/projectmemory/curated.go`、`backend/internal/writingstore/projectmemory_curated.go`、`docs/releases/2026-09-02-project-memory-m25.md` |
| E-026 | 代码+验收 | Context Compiler MVP：`internal/contextcompiler`（纯函数、`CompilerVersion` 钉死块集/预算、行粒度裁剪、missing/trimmed/diagnostics 三通道、hash 仅覆盖 Blocks）、迁移 102 专表（`(run,node,attempt,hash)` 唯一幂等）、store 落库三方法；测试驱动修正 payload 形态（Blocks 数组 → 完整 envelope 对象满足 jsonb object 约束） | `backend/internal/contextcompiler/`、`backend/internal/database/migrations/102_context_envelopes.*.sql`、`backend/internal/writingstore/context_envelope.go`、`docs/releases/2026-09-02-context-compiler-m3.md`、`docs/18-project-memory-context-compiler.md` §18.11 |
| E-027 | 代码+验收 | CapabilityManifest 上下文契约（required/optional/forbidden + budget + 校验）、5 个内置 capability 契约草案（research 禁止 style_directives）、编译器 Wanted 白名单语义、orchestrator 影子接线（ContextSource/EnvelopeSink 接口 + 编译→落库→注入 + telemetry）、`DocumentProjectID`；测试驱动修正：fixture manifest 契约缺失暴露、白名单改变 M3 缺省测试预期 | `backend/internal/writingplan/capability.go`、`backend/internal/contextcompiler/compiler.go`、`backend/internal/writingruntime/context.go`、`orchestrator.go`、`executor.go`、`executor_adapters.go`、`backend/internal/writingstore/projectmemory.go`、`docs/releases/2026-09-02-capability-context-m4a.md`、`docs/18-project-memory-context-compiler.md` §18.12 |
| E-028 | 代码+验收 | M4b：`ContextContract.EnforceRequiredContext`（默认 false，per-manifest 激活）、错误码 `CONTEXT_REQUIRED_MISSING`（RetryNever + attempt completion 落账）、基础设施降级与上下文缺口双通道分离、遥测 `MetricContextEnvelope` 五状态；激活 outline + research（required 有数据源），draft/quality/finalize 影子待 document_state 数据源 | `backend/internal/writingplan/capability.go`、`backend/internal/writingruntime/context.go`、`errors.go`、`orchestrator.go`、`telemetry.go`、`docs/releases/2026-09-02-required-context-m4b.md`、`docs/18-project-memory-context-compiler.md` §18.13 |
| E-029 | 代码+验收 | M5 Context Runtime：分段感知确定性分词（`tokenCount`，CJK ~1/rune + 拉丁 ~rune/4，CompilerVersion 1→2，`ErrResidentOverflow` 哨兵，envelope `TotalTokens` 元数据）、压力 0.70 预警 / 0.85 受控预压缩（`ContextRuntime` per-capability 冷却 5min + 进行中守卫 + 0.8× 压缩预算下限，遥测 `MetricContextPressure` 七状态）、溢出优先级保留遍历（share + 未认领预算回收，驻留闲置入池；`ContextContract.RetentionPriority` 校验与接线，draft 声明 document_state 优先）、按失败类别命名恢复路径（`RecoveryPathFor` 决策表，遥测 Reason 携带路径名）、document_state 数据源（`renderDocumentState` + 无版本真实空态）与 draft/quality/finalize enforce 激活；设计矫正：分词为估算器非 provider BPE、驻留闲置回收（M3 锁死语义矫正）、M3 尾裁测试重写为保留遍历不变式 | `backend/internal/contextcompiler/compiler.go`、`compiler_test.go`、`backend/internal/writingruntime/context.go`、`context_runtime.go`、`context_runtime_test.go`、`context_test.go`、`documentstate.go`、`orchestrator.go`、`telemetry.go`、`backend/internal/writingplan/capability.go`、`docs/releases/2026-09-03-context-runtime-m5.md`、`docs/18-project-memory-context-compiler.md` §18.14 |
| E-030 | 代码+验收 | M6 Memory Forgetting（V2.9 清单第 12 项收口）：版本化 `ForgetPolicy` v1 纯决策表（候选 30d/claim 60d/决策冷却 90d，零值=永不遗忘，`Hash()` 绑定台账；canon 无 horizon 字段——类型系统保证"Canon 不自动衰减"）、迁移 103 append-only `project_memory_forgetting_log`（真实 from→to、规则、policy hash、actor）、`Preview/ApplyForgettingPolicy`（单事务、条件 UPDATE 复核、幂等、policy|user 门禁、sweep 表集合结构性不含 facts）、`cmd/memory-forget` CLI（policy-dump/preview/apply/log）；设计矫正：遗忘=转换+台账非删除、canon 不变量结构化、sweep 与交互式 user-only 归档分离 | `backend/internal/projectmemory/forgetting.go`、`forgetting_test.go`、`backend/internal/database/migrations/103_memory_forgetting_log.*.sql`、`backend/internal/writingstore/projectmemory_forgetting.go`、`projectmemory_forgetting_test.go`、`backend/cmd/memory-forget/`、`docs/releases/2026-09-03-memory-forgetting-m6.md`、`docs/18-project-memory-context-compiler.md` §18.15、`docs/runbook.md` §9.9 |
| E-031 | 代码+验收 | 独立小修（消除 M4b 遗留已知问题）：`MigrateDB` 用会话级 `pg_advisory_lock` 串行化并发 migrator（锁在读取已应用快照前获取、跑在同一条 `db.Conn` 专用连接上→连接池为 1 也不死锁、helper 改 `migrationDB` 接口零测试改动）；并发回归测试 `TestMigrateDB_ConcurrentMigratorsSerialize`（新库 4 goroutine + 池恰 4）+ 负向验证（无锁 5/5 必现 `CREATE EXTENSION` 竞态）；双仓默认并行 `-p` 带真实 DB 全绿（OSS 19/commercial 20），不再依赖 `-p 1` 规避 | `backend/internal/database/migrator.go`、`migrator_test.go`、`docs/releases/2026-09-03-migration-advisory-lock.md` |
| E-032 | 设计+代码 | **进入 V3.0**：`docs/20` 实施计划（W0–W7 → M0–M7，Everything as Capability 但 kernel 不插件化，M0 服务路径接线设计含 `WRITING_RUNTIME_MODE` off/shadow/allowlist 默认 off）；M0a 地基：`writingruntime.RuntimeMode` + `NormalizeRuntimeMode`（未知/空→off，部署手误绝不误挂载）+ config `WritingRuntime.Mode`（env `WRITING_RUNTIME_MODE`）；盘点确认治理运行时此前"建好未挂载"（`newPersistentWritingAPI` controller=nil、无 `Orchestrator.Execute` 调用点） | `docs/20-v3-capability-runtime.md`、`backend/internal/writingruntime/runtime_mode.go`、`runtime_mode_test.go`、`backend/internal/config/config.go` |
| E-033 | 代码 | V3.0 M0b-1 composition 脊柱：`internal/server/governed_runtime.go` 的 `newGovernedWritingRuntime`（按 capability 组装 baseline + shadow-isolated candidate + `NewShadowRolloutExecutor` + Orchestrator 含 StoreContextSource/Envelopes/ContextRuntime）、`governedRunController` 适配 `writingRunController`（Resume→error，编译期断言）；mode=off→nil、allowlist→显式未接线报错、shadow 缺依赖→拒绝；工厂 capability-agnostic（runner 作入参）。发现并记录 M0b-2 缺口：`writingstore.Store` 未实现 `TransitionStore`（缺 `RecordTransition`）、search 能力是多步分支需 composite step | `backend/internal/server/governed_runtime.go`、`governed_runtime_test.go`、`docs/20-v3-capability-runtime.md` §20.2 |
| E-034 | 代码 | V3.0 M0b-2a 破 TransitionStore 缺口：`writingstore.Store.AppendRunEvent`（Store 级，复用 tx 版维护 last_event_sequence 投影 + 幂等）；`writingruntime.WritingStoreTransitionRecorder` 实现 `TransitionStore`（TransitionRecord→run.transitioned/rejected 事件映射，编译期断言；因 writingruntime→writingstore 单向依赖，适配器必须放 runtime 侧）；DB 门禁测试 `TestStoreAppendRunEventProjection`（投影推进/幂等重放/节点身份拒绝）真实 PG 通过 | `backend/internal/writingstore/runs.go`、`runs_event_test.go`、`backend/internal/writingruntime/transition_store.go` |
| E-035 | 代码 | V3.0 M0b-2b 地基：`engine.SequentialGroup` composite step——把多步子链（research 的 QueryPlan→Search→Relevance→Compress）绑成单个 governed capability 节点，镜像 ParallelGroup 的 Skipper 语义（全子步可跳则整组跳、按序执行、首个错误短路、CanPause=false）；单测钉顺序/错误短路/逐子步 skip/全 skip。per-capability runner 工厂（线程真实 LLM/search/embedding/KB + Usage 提取）与挂载/触发/真实模型 shadow 验证留 M0b-2b 续 + 2c/2d | `backend/internal/engine/sequential.go`、`sequential_test.go` |
| E-036 | 代码+验收 | V3.0 M0b-2d 真实模型 live 验证：把 V2.9 context 三件套接进纵向 harness——`fakeRuntimeStore.SaveContextEnvelope`、`runVerticalScenarioWithBackend` 的 orchestrator 加 `Context`(fixedContextSource)/`Envelopes`/`ContextRuntime` 并断言每节点落一条带 hash+版本的 `context_envelope`；离线纵向测试全绿，**live 用 sensenova OpenAI-compatible 端点（deepseek-v4-flash）跑 long_form 场景 PASS（44s）**，证明治理 orchestrator + V2.9 上下文编译 + shadow rollout + durable evidence 在真实模型下全链路执行（key 仅经 env 注入、未落任何文件）。剩 M0b-2b 续（生产 per-capability runner 工厂）+ 2c（server HTTP 挂载 + 执行触发） | `backend/internal/writingruntime/orchestrator_test.go`、`vertical_test.go` |
| E-037 | 代码+验收 | V3.0 M0b-2c 内容存储（方案 A，内容寻址）：迁移 104 `writing_artifact_contents(content_hash PK, media_type, body BYTEA)` + 不可变触发器（照抄 096 shadow-contents 范式但 canonical/无 run 作用域/无过期）；`Store.PutArtifactContent/GetArtifactContent`（ON CONFLICT DO NOTHING 幂等、hash 格式校验）；`writingruntime.WritingStoreContentGateway` 实现 canonical `ContentGateway`（Load by hash / Stage 内容寻址）。DB 门禁测试真实 PG 通过（round-trip/幂等/不可变 UPDATE 拒绝/非法 hash/缺失 ErrNotFound）。至此 2c 依赖齐备（sink/evidence/transition/checkpoints/initial/canonical 全有生产实现），仅剩 `governedEngineRunners` 工厂 + server 挂载/触发 | `backend/internal/database/migrations/104_artifact_contents.*.sql`、`backend/internal/writingstore/artifact_content.go`、`artifact_content_test.go`、`backend/internal/writingruntime/artifact_content_gateway.go` |
| E-038 | 设计 | V3.0 M0b-2c 范围发现（起服务验证前排查执行路径）：默认模板 plan 引用的 `core.document.finalize`（kernel 提交步，无 engine step）与 `core.validation.evidence`/`core.validation.fact`（不在已注册 5 能力、无 executor）当前无可运行 executor——"挂载+起服务走真实路由"会在此失败，属能力未实现而非接线可补。据此收敛 M0 边界：M0 交付 composition 脊柱 + 内容层 + 对有 executor 能力（research/outline/draft/quality）的链路 live 验证（2d 已 PASS）；"全量服务真实 plan"（补 validator/finalize executor + per-request style/kb）移交 M1 Capability Registry 统一。回写 docs/20 §20.2.1 | `docs/20-v3-capability-runtime.md` §20.2.1 |
| E-039 | 代码+验收 | **V3.0 M1.0 交付提交协议**（governed 路径此前从未驱动 store 的提交原语，只读核查证实：`PutQualityReport`/`CommitDocumentVersion` 非测试零调用者、orchestrator snapshot 只存进度不含质量绑定）：定稿协议 docs/21 §21.8——draft 节点提交 candidate 版本 → quality 节点后 `CommitCheckpoint(bundle{Snapshot(CandidateVersionID+QualityReportID), QualityReport(accepted), DocumentPromotion})` 单事务落库+推进质量态（store 原语齐备：`PutQualityReport` 校验 report↔snapshot 绑定、promotion UPDATE 版本 quality_state 回填引用）；**简化发现**：promotion 推进已存在版本行 → M1.1 finalize 不再提交新版本，只产 revision_set 记账。DB 门禁测试 `TestDeliveryCommitProtocol` 真实 PG 通过（A→B 全链：snapshot 绑定/quality_report 行/版本 promotion 回填/幂等重放/blocker fail-closed 无残留/绑定错拒绝） | `backend/internal/writingstore/delivery_protocol_test.go`、`docs/21-m1-1-finalize-executor.md` §21.8 |
| E-040 | 代码+验收 | **V3.0 M1.0 接线**：`DeliveryProtocol` 组件（`CommitDraftCandidate` lcp 解析 AST 提交 candidate 版本；`BuildQualityDelivery` 解析质量报告统计；`WritingStoreContentGateway` canonical 内容网关）+ Orchestrator 主循环 hook（draft 后提交、quality 后 pendingDelivery→saveCheckpoint 带 delivery 单事务）+ `RuntimeStore.SaveInitialArtifacts`（initial 捕获落真实 artifact 行绑 synthetic "initial" attempt，recovery 过滤 InitialCaptureNodeID）+ **真实模型 live 端到端**：`TestDeliveryProtocolThroughOrchestrator` 真实 PG 上 draft→quality 全链（candidate 版本提交+promotion 回填+report 谱系）+ 既有测试断言适配（Task12×2/orchestrator×3/vertical 计数/Recover initial 过滤/fakeRuntimeStore SaveInitialArtifacts+SaveContextEnvelope） | `backend/internal/writingruntime/delivery_protocol.go`、`orchestrator.go`、`orchestrator_test.go`、`recovery.go`、`checkpoint.go`、`writingstore/initial_artifacts.go`、`runtime.go`、`documents.go`、`docs/20` §20.5、`docs/21` |
| E-041 | 代码+验收 | **V3.0 M1.1 finalize executor**：`FinalizeRunner`（`core.document.finalize`，ActorSystem `writingruntime.finalize`）按 docs/21 §21.8 简化版收尾记账——`LoadRuntimeRun` → `CurrentDocumentVersionID` 读已 promoted 版本 → 产 revision_set JSON（`document_id/base_version_id/version_id/quality_state` + quality_report content hash 引用）经 canonical ContentGateway 落内容寻址 `writing_artifact_contents`（不调 `CommitDocumentVersion`：提交与质量推进由 M1.0 A/B 完成，质量门自然满足）；DB 验收：`TestDeliveryProtocolThroughOrchestrator` 扩为 draft→quality→finalize 三节点全链（真实 PG：revision_set artifact hash → 内容寻址体 → `version_id==candidate ∧ document_id ∧ quality_state=accepted_draft`），deliveryTestPlan 支持尾节点类动态追加；双仓 3 文件字节一致；双仓 `-p 1` 全树带 DB 零失败（默认并行下该测试 FAIL 属已知共享 DB TRUNCATE 互删，单包/单测 PASS） | `backend/internal/writingruntime/finalize_runner.go`、`delivery_protocol_test.go`、`docs/20` §20.5、`docs/21` §21.9 |
| E-042 | 代码+验收 | **V3.0 M1.2 validator executor**：`ValidatorRunner`（`core.validation.evidence`/`core.validation.fact` 双 kind，实现 LegacyNodeRunner）——报告生产者非门（findings 只进报告不 fail 节点）、degrade-don't-fail（无 LLM/调用失败/坏响应/缺输入一律产 `mode=degraded` + review_skipped issue，沿 PostReview graceful 先例）、LLM 路径 token 记账、draft/信源截断防上下文爆；目录补齐 3 manifest（evidence/fact validator + `core.retrieval.strict_search` 与 collect 共享 executor，strict 差异留 M1.3）——**sourced/strict 模板自此可编译为 T1**（此前模板引用的 validation.evidence/fact/research.strict 三类无声明，只能 T4）；验收：sourced/strict 编译钉死测试 + 6 runner 单测（httptest fake LLM 含正常路径/degraded 路径）+ `TestSourcedDeliveryWithValidatorThroughOrchestrator` 真实 PG sourced 形状全链（draft→evidence→quality→finalize，evidence_report 落 canonical 且降级模式诚实入账，交付谱系一致）；双仓 5 文件字节一致，双仓 `-p 1` 全树带 DB 零失败 | `backend/internal/writingruntime/validator_runner.go`、`validator_runner_test.go`、`delivery_protocol_test.go`、`backend/internal/writingplan/capability.go`、`templates_test.go`、`docs/22`、`docs/20` §20.5 |
| E-043 | 代码+验收 | **V3.0 M1.3 per-request style 进 runner**：迁移 105 `writing_runs.style_slug`（run 是请求单元，style 随 run 不随 composition）+ `RunRecord/RuntimeRun` 接线 + `ExecutionRequest.StyleSlug/UserID`（orchestrator 从 run 戳入）+ `EngineStepRunner.StepFactory` 升级 `func(StepEnv){Request,Profile}(engine.Step,error)` 每 attempt 构造 step + `StyleResolver`/`LoaderStyleResolver`（nil/错误/miss → nil profile 即默认语义，永不 fail 节点；未知 slug 走 Loader fallback 与 legacy 对齐；`my_` 用户风格留 M1.4 组合 resolver）；KB 绑定随 profile（`KbID` 可达 factory）；验收：`TestStyledRunResolvesProfileThroughOrchestrator` 真实 PG（slug 经 105 列 round-trip → orchestrator 戳入 → factory 观察到正确 profile 实例）+ 5 单测 + factory 错误传播钉死；双仓 15 文件字节一致，双仓 `-p 1 -count=1` 全树带 DB 零失败 | `backend/internal/database/migrations/105_run_style_slug.*.sql`、`backend/internal/writingstore/runs.go`、`runtime.go`、`backend/internal/writingruntime/style_resolver.go`、`style_resolver_test.go`、`executor.go`、`executor_adapters.go`、`orchestrator.go`、`delivery_protocol_test.go`、`docs/23`、`docs/20` §20.5 |
| E-044 | 代码+验收 | **独立小修（M1.4 前置）：测试隔离——每测试进程一个私有数据库**：`internal/database/dbtest.Open`（读 TEST_DATABASE_URL → 建 `test_<16hex>` 库（template0，迁移自带 CREATE EXTENSION 自包含）→ 全量迁移 → 返回 cleanup（关池 + `DROP DATABASE WITH (FORCE)`））；改造 5 个 DB 打开点（writingstore/editorial TestMain 全局 + writingruntime delivery/live×2 内联），TRUNCATE 全部保留（现只触碰本进程数据）；**验收：双仓默认并行 `-count=1` 全树带 DB 零失败**（M1.1 复现过的 TRUNCATE 互删/死锁/固定 id 冲突三类失败根除），结束后 `pg_database` 零 `test_%` 残留（创建→销毁闭环）；`-p 1` 不再是 DB 回归的必要条件；`WABENCH_TEST_DATABASE_URL` 用户（opt-in）保持原样 | `backend/internal/database/dbtest/dbtest.go`、`backend/internal/writingstore/store_test.go`、`backend/internal/editorial/integration_test.go`、`backend/internal/writingruntime/delivery_protocol_test.go`、`live_vertical_acceptance_test.go`、`evidence_accumulation_test.go` |
| E-045 | 代码+验收 | **V3.0 M1.4 挂载+触发+端到端（M1 脊柱收官）**：`server/governed_runners.go`（per-capability runner 工厂：draft=NewWriteStepWithKB（StepEnv profile+KbID）、quality=PostReview 全家桶、outline=governedOutlineStep 包装、research=SequentialGroup 四步链（M0b-2b 兑现）、validators=ValidatorRunner、finalize=FinalizeRunner）+ `mountGovernedRuntime`（WRITING_RUNTIME_MODE 门控，默认 off 零行为）+ **触发双点**（CreateRun 无审批计划即触发 / ApproveRun 审批后触发，每 run 每进程幂等）+ `writingplan.CapabilityRegistry.Activate` seam（default 目录 declared-only → 治理组合唯一有权激活，CompilePlan/dispatch 共用同一可执行 registry）+ `WritingStoreTransitionRecorder` 重写为委托 RecordRunTransition（恢复 writing_runs.status 投影——e2e 发现事件-only 让 run 永停 planned）+ StoreContextSource 的 document_state 移出 project 分支（文档属性非 project 属性，e2e 发现）；**验收**：`TestGovernedRunEndToEndThroughHTTP` 真实 Server+chi 路由+一次性 PG 双模 PASS——离线：执行链全通至首个模型步治理性 pause（unsafe_retry 纪律）；live（deepseek-v4-flash，凭据仅 env 注入）：outline→draft→quality→finalize 四节点全链 completed、revision_set=1、文档 accepted_draft（约 107s）；双仓 10 文件字节一致，双仓默认并行 `-count=1` 全树带 DB 零失败 | `backend/internal/server/governed_runners.go`、`governed_runners_test.go`、`governed_runtime.go`、`writing_api.go`、`handlers_writing_run.go`、`server.go`、`backend/internal/writingruntime/context.go`、`transition_store.go`、`backend/internal/writingplan/capability.go`、`docs/24`、`docs/20` §20.5 |
| E-046 | 代码+验收 | **V3.0 M1.5 切片 1：统一能力视图 + 单权威守卫**：新包 `internal/capability`（`UnifiedManifest`（id/class/source/version/description/IO/permissions/available）+ `Catalog` 接口 + `Resolver`（ByID/ByClass/Sources）+ `CheckSingleAuthority` 常驻守卫）——只读收敛层，注册权威 100% 留各 owning registry；**前缀命名空间结构化防冲突**（governed 独占裸 id，tool./editorial./validator. 强制前缀，跨面 claim 结构上不可表达）；四 surface adapter（governed/tool（含 mcp__ 前缀工具）/editorial（工具+动态 agent）/validator）+ `Server.newCapabilityResolver()` 组装（缺失面=空视图，部署形态如实可见）；legacy 面权限用标记（legacy.tool/legacy.editorial）不发明等价物；支撑性只读扩展 `ToolRegistry.Descriptors()`/`EditorialToolRegistry.All()`；验收：capability 包 3 单测 + `TestUnifiedCapabilityResolverCoversSurfaces`（真实 booted Server 四面齐+守卫过）+ `TestUnifiedCapabilityGuardKeepsNamespacesDisjoint`（漂移条目落 editorial 命名空间、governed id 唯一归属）；双仓 7 文件字节一致，双仓默认并行 `-count=1` 全树带 DB 零失败 | `backend/internal/capability/capability.go`、`capability_test.go`、`backend/internal/server/capability_catalogs.go`、`capability_catalogs_test.go`、`backend/internal/engine/tool_registry.go`、`backend/internal/editorial/editorial_tool_registry.go`、`server.go`、`docs/25`、`docs/20` §20.5 |
| E-047 | 代码+验收 | **V3.0 M1.5 切片 2–4：消费方迁移 + Role Policy 管道 + 权限语义统一（M1.5 收官）**：**切片 2**——admin 端点 `GET /api/v2/admin/capabilities`（Resolver 第一个生产消费方；每次读取运行 CheckSingleAuthority，守卫违例以 500 CAPABILITY_AUTHORITY_CONFLICT 暴露）+ `Resolver.BySource`；**切片 3**——`RolePolicy` 接口 + `PrefixRolePolicy`（前缀授权/缺省拒绝/`*` 全授）+ `Resolver.VisibleToRole`（nil=AllowAll 契约）+ server 侧 `editorialRolePolicy()`（researcher/writer/reviewer/admin 按 §20.7 角色隔离设计）+ 端点 `?role=` 角色视图（**职责边界：resolver 侧管道，调度强制点随 V3.0 M5 落地，今天不改变实际执行**）；**切片 4**——`GovernedPermissions(marker)` 文档即代码映射（legacy.tool/legacy.editorial → model.invoke,materials.read,external.research,validation.run；治理名与未知标记透传），tool/editorial adapter 的 Permissions 字段改为展开后治理权限名（视图不再暴露裸 legacy 标记；旧面网关仍自身权威）；验收：capability 包 5 单测 + `TestAdminCapabilitiesEndpoint`（真实路由 admin JWT：四面清单 + researcher 视图含 search 不含 draft + legacy 条目带治理权限名，顺带补 UnifiedManifest JSON tag）；双仓 10 文件字节一致，双仓默认并行 `-count=1` 全树带 DB 零失败 | `backend/internal/capability/capability.go`、`capability_test.go`、`backend/internal/server/handlers_capability.go`、`handlers_capability_test.go`、`role_policy.go`、`capability_catalogs.go`、`server.go`、`docs/25` §25.4、`docs/20` §20.5 |
| E-048 | 代码+验收 | **V3.0 M2 Thin Orchestrator**：主循环六动词显式化（新 `dispatch.go`：`authorizeNode`（Resolve+Authorize：manifest/权限/预算/executor）、`prepareDispatch`（Dispatch setup：inputs/request/M1.3 style/subject/context 编译注入）、`dispatchAttempt`（计时执行+控制观察）、`observeResult`（result binding/预算/shadow 泄漏不变量）——逐字搬移，行为字节不变）+ **delivery 路由表下沉**（`DeliveryProtocol.Drive` + `deliveryDrivers()` class→driver 表：orchestrator 不再硬编码 draft/quality 分支，新交付类=协议内注册一行、loop 保持能力无关；store 提交仍是 kernel 行为，表只搬 switch 不开能力写面）+ **kernel 不变量反射守卫**（`TestCapabilityContractHasNoWriteSurface`：ExecutionRequest/ExecutionResult/LegacyNodeInput/OutputArtifactDraft 禁止 func/chan/unsafe/`*writingstore.Store`——能力拿值不拿代码与句柄，一切提交在 orchestrator，结构上不可绕过）+ 未注册 class Drive=no-op、已知类缺输入 fail-closed 钉死；验收：orchestrator.go 796→749（六动词实现拆至 dispatch.go 118 行）+ writingruntime 包全测试 PASS + 双仓 4 文件字节一致、双仓默认并行 `-count=1` 全树带 DB 零失败 | `backend/internal/writingruntime/dispatch.go`、`orchestrator_invariants_test.go`、`orchestrator.go`、`delivery_protocol.go`、`docs/26`、`docs/20` §20.5 |
| E-049 | 代码+验收 | **V3.0 M3 Runtime Lifecycle + Hook Bus**：9 个命名 lifecycle 时刻（run.dispatched / capability.before / artifact.submitted / checkpoint.before / capability.after / run.completed|paused|failed|cancelled——以 orchestrator 实际拥有的时刻为根，RunCreated 留 API 层）+ `LifecycleSnapshot`（值类型：id/class/state/完成集/成本/artifact **类型**列表，无正文无句柄）+ `hookBus`（注册序同步 fan-out、单 hook panic 遏制记日志、nil bus/hook 安全）+ **kernel 守卫**（快照反射禁 func/chan/store——hook 只观察，永不改 run/拒迁移/动 artifact/实现可绕过内核面）+ **telemetry 作第一个 hook**（`lifecycleTelemetryHook` → `MetricLifecycle` 有界流，M2 遥测面与 M3 bus 一条管线）+ `terminalLifecycleEvent` 集中分类 + orchestrator 六处终态/四处过程 emit（saveCheckpoint 入口=checkpoint.before，质量交付同事务 rides 它）；验收：bus fan-out/panic 遏制/守卫/分类 4 单测 + `TestOrchestratorEmitsLifecycleSequence`（真实夹具逐位匹配：dispatched→before→submitted→checkpoint.before→after→completed，遥测同 bus 可见）；双仓 5 文件字节一致，双仓默认并行 `-count=1` 全树带 DB 零失败 | `backend/internal/writingruntime/hooks.go`、`hooks_test.go`、`dispatch.go`、`orchestrator.go`、`telemetry.go`、`docs/27`、`docs/20` §20.5 |
| E-050 | 代码+验收（部分） | **P0 Service Readiness（A1–A5）**：strict_search 模板激活+真实材料溯源研究步、`my_` 个人风格按持久 owner 解析（RuntimeRun.OwnerUserID）、governed runtime 四组指标+Grafana 看板、allowlist 服务接线（append-only 策略迁移 106 + runtime-policy 操作 CLI + per-dispatch 策略重载，percentage/enabled fail-closed）、PG advisory lock 运行独占+5s 持久化队列扫描恢复；验收：OSS/商业版全量 22+22 包零失败 + 四包 race 通过 + 前端双仓 build/test/lint 通过；**边界**：真实模型垂直验收因 API 余额为零未在当前构建执行，人工盲评未执行（仅导出 2026-09-01 历史候选侧材料） | `backend/internal/server/governed_{policy,research,styles,worker}.go`、`governed_p0_test.go`、`cmd/runtime-policy`、迁移 106、`monitoring/grafana/governed-runtime.json`、`docs/releases/2026-09-06-p0-service-readiness.md` |

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
| 2026-09-02 | V2.9 M1 设计矫正门 | ProjectMemory M1 交付：四项设计矫正回写（project 一级对象、ActorType 硬门禁、复用 089 append-only、词表 Go 侧版本化）+ 单值/多值谓词二分；迁移 099 双仓 fresh DB 验证 | ✅ 通过 | M1 完成；M2 claims 晋升/实体/术语、M3 编译器待排期 |
| 2026-09-02 | V2.9 M2 设计矫正门 | 非虚构定位落地：实体封闭类型集（无角色卡）、through_line 改造为领域中立未决线索账本（顺延 M2.5）；claim 佐证永不自动晋升（HITL 保持 store 层硬门禁）；迁移 100 双仓 fresh DB 验证 | ✅ 通过 | M2 完成；M2.5 terminology/decisions/through_line、M3 编译器待排期 |
| 2026-09-02 | V2.9 M2.5 设计矫正门 | 非虚构贯穿线落地为领域中立线索账本（resident 驻留语义为 M3 编译器预留）；四类对象 canon 路径与 M1/M2 同等 HITL 纪律；迁移 101 双仓 fresh DB 验证 | ✅ 通过 | M2.5 完成；ProjectMemory 数据面齐备，下一步 M3 Context Compiler MVP |
| 2026-09-02 | V2.9 M3 设计矫正门 | 编译器纯函数化（不连 store、Input 预装载）、行数记账近似（真实分词归 M5）、落库从 run 事件矫正为专表、hash 语义钉死为模型可见 Blocks；迁移 102 双仓 fresh DB 验证 | ✅ 通过 | M3 完成；M4 Manifest 契约接入执行路径待排期 |
| 2026-09-02 | V2.9 M4a 设计矫正门 | ContextContract 嵌入 manifest 而非独立文件；白名单语义成为契约前提；影子模式 fail-closed 后置（M4b）；无 project 的 run 全 missing 优雅降级；双仓 11 文件字节一致 | ✅ 通过 | M4a 完成；M4b required fail-closed 激活、M5 Context Runtime 待排期 |
| 2026-09-02 | V2.9 M4b 激活门 | per-manifest 激活评审：outline/research 的 required（contract_digest）有稳定数据源才打开；基础设施降级永不拒绝节点；拒绝点补写 attempt 台账（测试驱动）；全树 19 包 -p 1 全绿 | ✅ 通过 | M4b 完成；M5 Context Runtime（真实分词/预算监控/预压缩）待排期 |
| 2026-09-03 | V2.9 M5 设计矫正门 | M5 Context Runtime 交付：分词为确定性分段估算器（非 provider BPE，纯函数契约优先）、驻留闲置回收进保留池（驻留保护 = 从不裁剪 + fail-closed 不变，M3 锁死语义矫正）、压缩/恢复为运行时组件不进 kernel、document_state 无版本渲染真实空态使 required 首次 run 可满足；激活 draft/quality/finalize enforce（per-manifest 评审同 M4b 流程）；双仓 10 文件字节一致，全树 -p 1 全绿 | ✅ 通过 | M5 完成；V2.9 上下文线收口；`context_pressure` 看板、provider-exact 分词、source_evidence/style_directives 数据源留作后续 |
| 2026-09-03 | V2.9 M6 设计矫正门 | Memory Forgetting 交付：遗忘=状态转换+append-only 台账非删除（零 schema 侵入，转换落在既有 CHECK 值内）、canon 不变量结构化（策略无 fact horizon、sweep 表集合无 project_facts，测试钉死）、sweep（policy|user 门禁）与交互式 user-only 归档分离、策略版本化 hash 绑定台账；真实一次性 PG 验证迁移 103 fresh DB 应用 + sweep 全语义（preview/幂等/槽位释放/台账逐行），双仓 7 文件字节一致，全树 -p 1（无 DB 口径）全绿 | ✅ 通过 | M6 完成；**V2.9 全部 12 项收口**；附带修复 editorial DB 夹具（uid→id + editorial_tasks→agent_traces schema 漂移，双仓同步，全树带真实 DB 现全绿）；UserMemory 四层衰减、sweep 调度器、M4b 并行迁移 advisory lock 留作后续 |
| 2026-09-03 | 迁移并发串行化门（独立小修） | `MigrateDB` 会话级 `pg_advisory_lock` 串行化并发 migrator，锁与迁移同钉一条专用连接（池为 1 不死锁）、helper 改 `migrationDB` 接口零测试改动；并发回归测试 + 负向验证（无锁 5/5 必现竞态）；双仓默认并行 `-p` 带真实 DB 全绿（OSS 19/commercial 20） | ✅ 通过 | M4b 遗留的并行迁移竞态已知问题消除，不再依赖 `-p 1` 规避 |
| 2026-09-06 | P0 服务就绪门（A1–A5） | strict_search/个人风格/遥测/allowlist 接线/任务所有权五项实现；OSS+商业版全量 22+22 包零失败、四包 race 通过、前端双仓通过；运行时默认 off，allowlist 需持久化 active 策略 + 新鲜精确范围审批 | ✅ 通过（机器验证） | **真实模型垂直验收被阻**（TASK13 LLM 账户余额为零）；人工盲评未执行；percentage/enabled 服务接线与 A0 台账/README 分列留作后续 |
| 2026-09-04 | V3.0 设计门 + M0a | `docs/20` 定 V3.0 里程碑序列（M0 服务路径接线→M1 Registry 统一→M2 Thin Orchestrator→M3 Hook→M4 Quality Router→M5 Role Policy→M6 Provider→M7 Reliability），kernel 不插件化；M0a 落地 `RuntimeMode`（off/shadow/allowlist，未知→off）+ config 门控，默认 off 零行为变化；盘点确认治理运行时此前建好未挂载 | ✅ 通过 | 进入 V3.0；M0b（composition 工厂 + engine adapter 线程 + 执行触发 + controller 挂载，默认 shadow）为下一增量 |
| 2026-09-04 | V3.0 M0b-1 门 | composition 脊柱 `newGovernedWritingRuntime`（baseline+shadow candidate+RolloutExecutor+Orchestrator 含 context 三件套）、`governedRunController` 适配（Resume→error，编译期断言）、mode 门控（off→nil/allowlist→未接线报错/shadow 缺依赖拒绝）；双仓 2 文件字节一致、gofmt 干净、全树回归绿 | ✅ 通过 | M0b-1 脊柱落地（默认 off 未挂载，零行为变化）；M0b-2 待做：真实 step 映射、`writingstore.Store` 补 `RecordTransition`、执行触发、挂载、shadow 纵向测试 |
| 2026-09-04 | V3.0 M1.1 设计矫正门 | finalize executor 交付：按 docs/21 §21.8 简化版（finalize 只产 revision_set 记账，不提交版本——提交/晋升由 M1.0 协议 A/B 完成）、ActorSystem 纪律、revision_set 落内容寻址存储；真实 PG 三节点全链验收（candidate 版本+promotion+report 谱系+revision_set 一致）；双仓 3 文件字节一致、`-p 1` 全树带 DB 零失败 | ✅ 通过 | M1.1 完成；下一步 M1.2 validator executor（evidence/fact）→ M1.3 per-request style/kb → M1.4 挂载+触发+起服务端到端 `tpl_fast_v1` |
| 2026-09-04 | V3.0 M1.2 设计矫正门 | validator executor 交付：报告生产者非门（findings 不 fail 节点，门在 quality）、degrade-don't-fail（降级诚实入账 mode=degraded）、目录补齐 validation.evidence/fact + research.strict 三 manifest——sourced/strict 模板自此可编译 T1（原只能 T4 诊断）；strict 与 collect 共享 search executor（manifest 为能力身份，strict 差异归 M1.3）；验收：编译钉死 + 6 单测（fake LLM）+ 真实 PG sourced 全链；双仓 5 文件字节一致、`-p 1` 全树带 DB 零失败 | ✅ 通过 | M1.2 完成；下一步 M1.3 per-request style/kb → M1.4 挂载+触发+起服务端到端（M1.4 开工前先修测试隔离：共享 DB TRUNCATE 互删） |
| 2026-09-04 | V3.0 M1.3 设计矫正门 | per-request style 进 runner 交付：style 挂 run（迁移 105 style_slug）不挂 composition、StepFactory 升级 StepEnv 每 attempt 构造、StyleResolver 可插拔降级（风格缺口≠执行契约缺口，永不 fail 节点）、未知 slug 走 Loader fallback（legacy 对齐）、my\_ 留 M1.4 组合 resolver；KB 绑定随 profile 可达 factory；DB 验收 slug round-trip 全链；双仓 15 文件字节一致、`-p 1 -count=1` 全树带 DB 零失败 | ✅ 通过 | M1.3 完成，M1 脊柱剩 M1.4；开工前先修测试隔离（共享 DB TRUNCATE 互删，本次回归再次复现）→ 挂载+触发+起服务端到端 `tpl_fast_v1`（真实模型 + 一次性 PG） |
| 2026-09-04 | 测试隔离门（M1.4 前置独立小修） | `dbtest.Open` 每测试进程一个私有数据库（template0 + 自迁移 + FORCE 销毁），5 个 DB 打开点改造；**双仓默认并行 `-count=1` 全树带 DB 零失败**（TRUNCATE 互删/死锁/固定 id 冲突根除），测试库零残留 | ✅ 通过 | 已知问题"共享 DB 全树并行测试数据残留"消除；后续 DB 回归不再需要 `-p 1`；M1.4 起服务端到端可放心并行验证 |
| 2026-09-04 | V3.0 M1.4 门（M1 脊柱收官） | 治理运行时挂上生产写路径：mode 门控挂载（默认 off 零行为）、per-capability runner 工厂（真实 LLM/KB/敏感词/jiaozhen + SequentialGroup 研究链）、CreateRun/ApproveRun 双点幂等触发、`Activate` seam 统一编译与 dispatch registry、TransitionRecorder 恢复状态投影、document_state 解析修序；**端到端双模验收**：离线=治理性 pause（执行链全通）、live=真实模型四节点 completed（revision_set=1、accepted_draft，约 107s）；双仓 10 文件字节一致、默认并行 `-count=1` 全树带 DB 零失败 | ✅ 通过 | **M1 脊柱 M1.1–M1.4 全部交付，治理运行时经真实模型端到端验证**；1panel 灰度只需 WRITING_RUNTIME_MODE=shadow + 模型 env；本门不构成 allowlist/percentage/production 授权；下一步 M1.5 Registry 统一（L） |
| 2026-09-04 | V3.0 M1.5 切片1 门 | 统一能力视图落地：`internal/capability`（UnifiedManifest+Resolver+CheckSingleAuthority）+ 四 surface adapter（governed/tool/editorial/validator，MCP 随 tool 面）+ 真实 Server 组装验收；前缀命名空间使跨面 claim 结构上不可表达，守卫常驻防漂移；注册权威 100% 留 owning registry（视图非二写）；双仓 7 文件字节一致、默认并行 `-count=1` 全树带 DB 零失败 | ✅ 通过 | M1.5 切片 1 完成（L 级分片交付）；切片 2–4（消费方迁移/role policy/权限语义统一）待排期；`docs/25` 记录总设计与切片路线 |
| 2026-09-04 | V3.0 M1.5 切片2–4 门（M1.5 收官） | admin `/capabilities` 端点（守卫常驻读取）+ RolePolicy/PrefixRolePolicy/VisibleToRole 管道与 editorial 角色策略（调度强制点留 M5，今天零执行行为变化）+ GovernedPermissions 映射（视图全治理权限名语义）；真机端点验收（四面清单 + 角色视图含 search 不含 draft + legacy 条目治理权限名）；双仓 10 文件字节一致、默认并行 `-count=1` 全树带 DB 零失败 | ✅ 通过 | **M1.5 全部切片交付，V3.0 M1 里程碑整体收官**（治理运行时挂载+端到端验证+能力面统一）；下一步 V3.0 M2 Thin Orchestrator；role 强制点随 M5 |
| 2026-09-04 | V3.0 M2 门（Thin Orchestrator） | 主循环六动词显式化（dispatch.go 逐字搬移，行为字节不变）+ delivery 路由表下沉（Drive/deliveryDrivers，orchestrator 摘除 draft/quality 硬编码分支，loop 能力无关）+ kernel 不变量反射守卫（capability 合同面禁 func/chan/store 句柄——能力拿值不拿代码，提交不可绕过）+ Drive 未知类 no-op/缺输入 fail-closed 钉死；orchestrator.go 796→749；双仓 4 文件字节一致、默认并行 `-count=1` 全树带 DB 零失败 | ✅ 通过 | **M2 交付：orchestrator 只剩六动词骨架，业务路由归 capability 侧协议表**；M3（Hook Bus）的 Observe 插件化前置已就位；下一步 M3 或按需排期 M4–M7 |
| 2026-09-04 | V3.0 M3 门（Lifecycle + Hook Bus） | 9 个命名 lifecycle 时刻 + LifecycleSnapshot 值快照 + hookBus（同步 fan-out/panic 遏制/nil 安全）+ kernel 守卫（快照反射禁执行面——hook 只观察，不可实现可绕过内核面）+ telemetry 作第一个 hook（MetricLifecycle 同管线）+ terminalLifecycleEvent 集中分类 + orchestrator 十处 emit；真实夹具序列测试逐位匹配（dispatched→before→submitted→checkpoint.before→after→completed）；双仓 5 文件字节一致、默认并行 `-count=1` 全树带 DB 零失败 | ✅ 通过 | **M3 交付：Observe 动词插件化，telemetry/audit/context/memory/rollout 的 hook 化通道就绪**；RunCreated 留 API 层；下一步 M4 Quality Router 或按需排期 M5–M7 |

---

## 9. 活跃风险

| 风险 ID | 描述 | 概率 | 影响 | 缓解措施 | 状态 |
|---|---|---|---|---|---|
| R-001 | LLM API 额度耗尽 | 中 | 高 | 断路器 + 额度监控 + 降级模式 | 已缓解 |
| R-002 | 搜索结果注入恶意 prompt | 中 | 高 | guardrails.go sanitization + system prompt 防御 | 已缓解 |
| R-003 | 记忆无限增长 | 低 | 中 | M6 forgetting v1：ProjectMemory 候选/claim/冷却决策按版本化策略转换 + append-only 台账（canon 永不自动衰减）；UserMemory 四层衰减待独立设计 | 已缓解（ProjectMemory v1），UserMemory 持续关注 |
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
| 2026-09-03 | 记忆 forgetting 策略选型 | — | ✅ ProjectMemory 侧已落地（M6：ForgetPolicy v1 + sweep + append-only 台账，canon 永不自动衰减）；UserMemory 四层衰减待独立设计 |
| 待授权 | 是否对指定 subject 激活 allowlist policy | — | ⛔ 未授权 |
| 待定 | 是否进入 percentage 评估 | — | ⛔ 需先积累 allowlist 证据 |
| 2026-09-04 | V3.0 M0b 服务路径接线（composition + engine adapter 线程 + 执行触发 + controller 挂载，默认 shadow） | — | 🚧 M0b-1 脊柱已落（工厂+适配器+门控，默认 off 未挂载）；M0b-2 待做（真实 step 映射 + TransitionStore 缺口 + 触发 + 挂载 + shadow 纵向测试） |

---

*最后更新：2026-09-04*
*维护者：Writing Agent V2 Team*
