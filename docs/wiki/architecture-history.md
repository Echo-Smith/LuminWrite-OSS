# 架构演进历史

> 本文档记录 LuminBuddy V2 已弃用的架构方案，供历史追溯参考。当前生产环境使用 Harness 架构（见 `docs/architecture-c-design.md`）。

---

## 1. UnifiedAgent（ReAct 单 Agent 动态编排）— 已弃用

### 生命周期

| 阶段 | 时间 | 状态 |
|------|------|------|
| 创建 | 2026-Q1 | 替代固定 Pipeline，引入 LLM 驱动编排 |
| 弃用 | 2026-08 | Harness 单层持续会话方案替代 |
| 源码清理 | 2026-08 | `unified_agent.go` 已删除，`agent` 包仅保留 Harness 文件 |

### 设计概述

UnifiedAgent 用一个 LLM 驱动的 ReAct（Reasoning + Acting）循环替代固定步骤流水线。LLM 作为"编排大脑"，根据当前执行状态自主选择下一步要执行的工具。

```
用户 → UnifiedAgent(ReAct 外壳) → determineNextStep(硬编码不变量)
    → Step1(intent, 内部调 LLM)
    → Step2(query_plan, 内部调 LLM)
    → ...
    → StepN(write, 内部 agent loop)
  13 次 LLM 调用，首字延迟 30-60s
```

### 核心机制

**ReAct 循环**：LLM 在每轮迭代中接收当前状态摘要，选择一个工具调用，工具执行后结果回传给 LLM，最多 12 轮迭代。

**规则强制不变量**（`determineNextStep`）：
1. `intent` 必须最先执行（意图分类）
2. `post_review` 必须在文章产出后执行（质量评审）
3. `auto_fix` 后强制重新评审

**工具注册表**：所有 Pipeline 步骤（IntentStep、QueryPlanStep、SearchStep 等）被包装为 `AgentTool`，注册到 `ToolRegistry`，每个工具有依赖声明、可重复性、终态标记。

### 弃用原因

| 问题 | Harness 如何解决 |
|------|-----------------|
| 13 次 LLM 调用，首字延迟 30-60s | 1 次持续会话，首字延迟 3-5s |
| 每轮重发 system prompt + 累积 messages，Token 膨胀 | 单次 system prompt，会话内自然继承 |
| LLM 可能跳过关键步骤（如审校），需硬编码不变量兜底 | 工具集按意图分组，LLM 在持续会话中自主调用 |
| 13 次独立 LLM 调用串联，延迟叠加 | ChatWithTools 单次会话内 tool call 循环 |

### 循环依赖问题（开发期间）

开发 UnifiedAgent 时遇到 Go 循环依赖问题：

```
engine 包需要 tools.LLMClient → engine → tools
tools 包需要 engine.SearchResult → tools → engine
形成 engine ⇄ tools 循环
```

解决方案：将 `UnifiedAgent` 从 `engine` 包移到新的 `agent` 包，打破循环：

```
之前： engine → tools → engine  (cycle)
之后： agent → engine + tools    (无环)
      engine → tools             (无环)
```

**经验教训**：Go 不允许循环依赖。新增跨包引用时，先用 `grep` 检查目标包是否已引用当前包。当 A 包需要 B 包的类型、B 包需要 A 包的类型时，引入第三个中间包。

---

## 2. 架构演进对比

### 三代架构

```
架构 A（Pipeline，已弃用）:
  用户 → 固定 Step 数组 → IntentStep → QueryPlanStep → ... → WriteStep → PostReviewStep
  9 步固定流程，每步独立调 LLM

架构 B（UnifiedAgent，已弃用）:
  用户 → ReAct 循环 → LLM 选择工具 → 执行 → 回传 → 循环
  13 次 LLM 调用，LLM 自主编排但延迟高

架构 C（Harness，当前使用）:
  用户 → Harness(规则路由) → LLM 持续会话(ChatWithTools)
  1 次 LLM 会话，工具自主调用，首字延迟 3-5s
```

### 对照实验框架（历史）

曾设计三组对照实验验证架构选型：

1. 固定 Pipeline（AgentEngine）
2. 单 Agent 动态编排（UnifiedAgent）
3. 编辑部 Multi-Agent（三 Agent 协作）

指标包括：选题到可发布稿件时间、人类实际操作时长、初审通过率、平均返工轮次、事实问题漏检率、单篇 Token 成本等。

实验框架代码位于 `backend/internal/editorial/experiment_runner.go`，使用盲评 LLM 评分（accuracy/structure/style/insight/readability/safety 六维度）。

---

## 3. 编辑部多 Agent 编排（保留但未接入用户写作路径）

> 此架构代码完整且注册了路由，但当前用户写作走 Harness，不走 Orchestrator。详见 `docs/12-editorial-system.md`。

编辑部多 Agent 编排设计用于编辑部内部协作场景（编辑选题 → Agent 研究 → Agent 写作 → Agent 审校 → 主编审批），与 Harness 是互补关系而非替代：

| 维度 | Harness（用户写作） | Orchestrator（编辑部协作） |
|------|---------------------|--------------------------|
| 场景 | 普通用户写一篇文章 | 编辑部正式供稿流程 |
| LLM 调用 | 1 次持续会话 | 3+ 次独立调用 |
| 人工门控 | 无 | 2-3 个确认点 |
| 可审计性 | 低（黑盒） | 高（每步独立 Artifact） |
| 速度 | 快（3-5s 首字） | 慢（60-120s+） |
| 知识沉淀 | 用户记忆系统 | 编辑部知识 + 信源可信度 + Agent 信誉 |

---

## 4. 版本变更记录（历史）

| 版本 | 日期 | 变更内容 | 影响范围 |
|------|------|---------|---------|
| v2.0.0 | 2025-Q1 | 初始版本：基础写作管线 + WebSocket | 全部 |
| v2.1.0 | 2025-Q2 | 多源搜索 + 知识库集成 | Search, KB |
| v2.2.0 | 2025-Q3 | 四层记忆系统 + 实体网络 | Memory |
| v2.3.0 | 2025-Q4 | 编辑部多 Agent 编排 | Editorial |
| v2.4.0 | 2026-Q1 | MCP 双向集成 + UnifiedAgent（已弃用） | Agent, MCP |
| v2.5.0 | 2026-Q1 | GraphRAG + 灰度发布 + WebAuthn | KB, Profile, Auth |
| v2.6.0 | 2026-08 | Harness 替代 UnifiedAgent + 红队评估 + Prompt Injection 防御 | Security, Eval, Ops |

---

*最后更新：2026-08-20*
*维护者：Writing Agent V2 Team*
