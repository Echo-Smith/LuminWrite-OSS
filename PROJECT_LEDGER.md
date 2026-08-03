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
| 统一 Agent (ReAct) | ✅ 完成 | 2026-Q1 | UnifiedAgent LLM 驱动编排 |
| **安全加固 + 生产化** | 🔄 进行中 | 2026-08 | 红队评估、Prompt Injection 防御、运维手册 |
| 自演进闭环 | 📋 规划中 | — | feedback → candidate → eval gate → rollout |
| 多实例水平扩展 | 📋 规划中 | — | Docker Swarm / K8s |

---

## 3. 架构概览

```
┌──────────────┐     WebSocket/SSE      ┌──────────────────────────────────┐
│  Frontend    │◄──────────────────────►│         Backend (Go)             │
│  React+Vite  │     JWT Auth           │                                  │
│  Nginx       │                        │  ┌────────────────────────────┐  │
└──────────────┘                        │  │   Agent Engine / Unified    │  │
                                        │  │   ReAct Loop               │  │
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

### D-001 | 2025-Q1 | Agent 执行模式：双模式（Pipeline + Unified）

- **背景**：需要兼顾确定性流程和 LLM 自主编排
- **选项**：(a) 仅固定 Pipeline (b) 仅 LLM ReAct (c) 双模式可切换
- **选择**：(c) 双模式
- **理由**：Pipeline 保证关键步骤不遗漏；Unified 提供灵活编排；通过 `AGENT_MODE` 环境变量切换
- **重新评估**：如果 Unified 模式在 95%+ 场景下优于 Pipeline，考虑废弃 Pipeline

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

---

## 5. 版本变更日志

| 版本 | 日期 | 变更内容 | 影响范围 |
|---|---|---|---|
| v2.0.0 | 2025-Q1 | 初始版本：基础写作管线 + WebSocket | 全部 |
| v2.1.0 | 2025-Q2 | 多源搜索 + 知识库集成 | Search, KB |
| v2.2.0 | 2025-Q3 | 四层记忆系统 + 实体网络 | Memory |
| v2.3.0 | 2025-Q4 | 编辑部多 Agent 编排 | Editorial |
| v2.4.0 | 2026-Q1 | MCP 双向集成 + UnifiedAgent | Agent, MCP |
| v2.5.0 | 2026-Q1 | GraphRAG + 灰度发布 + WebAuthn | KB, Profile, Auth |
| v2.6.0 | 2026-08 | 红队评估 + Prompt Injection 防御 + 运维手册 | Security, Eval, Ops |

---

## 6. 操作日志

> 记录重大操作（部署、迁移、配置变更、事故响应）

| 日期 | 操作 | 执行者 | 原因 | 结果 |
|---|---|---|---|---|
| 2026-08-03 | 创建项目台账 | AgentOps 审计 | 健康检查 P0 缺口 | ✅ 完成 |
| 2026-08-03 | 增加红队评估集 | AgentOps 审计 | 健康检查 P1 缺口 | ✅ 完成 |
| 2026-08-03 | 增加 Prompt Injection 防御 | AgentOps 审计 | 健康检查 P1 缺口 | ✅ 完成 |

---

## 7. 证据索引

| 证据 ID | 类型 | 描述 | 路径 |
|---|---|---|---|
| E-001 | 架构审计 | AgentOps 健康检查报告 | `agentops-health-check-2026-08-03.md` |
| E-002 | 代码 | UnifiedAgent ReAct 循环 | `backend/internal/agent/unified_agent.go` |
| E-003 | 代码 | 编辑部编排器 | `backend/internal/editorial/orchestrator.go` |
| E-004 | 代码 | 记忆服务 | `backend/internal/memory/service.go` |
| E-005 | 代码 | MCP 注册表 | `backend/internal/mcp/registry.go` |
| E-006 | 代码 | 工具注册表 | `backend/internal/engine/tool_registry.go` |
| E-007 | 配置 | Docker Compose 部署 | `docker-compose.yml` |
| E-008 | 代码 | 评估服务 | `backend/internal/services/evaluation.go` |
| E-009 | 代码 | 红队评估集 | `backend/internal/services/redteam_eval.go` |
| E-010 | 代码 | Prompt Injection 防御 | `backend/internal/engine/guardrails.go` |
| E-011 | 文档 | 运维手册 | `docs/runbook.md` |

---

## 8. 门控日志

| 日期 | 门控类型 | 检查项 | 结果 | 备注 |
|---|---|---|---|---|
| 2026-08-03 | 证据门 | 项目台账是否存在 | ✅ 通过 | 本文件创建 |
| 2026-08-03 | 风险门 | Prompt Injection 防御 | ✅ 通过 | guardrails.go 实现完成 |
| 2026-08-03 | 评估门 | 红队评估集 | ✅ 通过 | 20+ 对抗用例创建 |
| — | 发布门 | 回归评估通过 | ⏳ 待执行 | 需要在 profile publish 时触发 |
| — | 安全门 | 红队测试通过率 > 90% | ⏳ 待执行 | 需要运行红队评估 |

---

## 9. 活跃风险

| 风险 ID | 描述 | 概率 | 影响 | 缓解措施 | 状态 |
|---|---|---|---|---|---|
| R-001 | LLM API 额度耗尽 | 中 | 高 | 断路器 + 额度监控 + 降级模式 | 已缓解 |
| R-002 | 搜索结果注入恶意 prompt | 中 | 高 | guardrails.go sanitization + system prompt 防御 | 已缓解 |
| R-003 | 记忆无限增长 | 低 | 中 | 需要实现 forgetting 策略 | 待处理 |
| R-004 | 编辑部 Agent 死锁 | 低 | 中 | Lease 超时 + 重试上限 + 人类升级 | 已缓解 |
| R-005 | DB 迁移失败 | 低 | 高 | migrator.go 回滚 + 启动前检查 | 已缓解 |

---

## 10. 下一个决策点

| 日期 | 决策 | 负责人 | 状态 |
|---|---|---|---|
| 待定 | 是否实现自演进闭环 | — | 📋 规划中 |
| 待定 | 是否迁移到 K8s | — | 📋 规划中 |
| 待定 | 是否增加 RBAC 细粒度权限 | — | 📋 规划中 |
| 待定 | 记忆 forgetting 策略选型 | — | 📋 规划中 |

---

*最后更新：2026-08-03*
*维护者：Writing Agent V2 Team*
