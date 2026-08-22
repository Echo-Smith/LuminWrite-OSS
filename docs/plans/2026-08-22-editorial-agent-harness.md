# 编辑部 Agent 自主工具调用改造计划

> 日期: 2026-08-22
> 关联文档: `docs/dynamic-subagent-feasibility.md` (Phase 6 前置), `docs/12-editorial-system.md`
> 状态: **待评审**

---

## 一、背景与问题

### 1.1 断裂点

当前编辑部的架构链路是：

```
Planner（动态生成角色+DAG）
  → DAGExecutor（动态调度节点）
    → AgentExecutor.Execute()（固定 Pipeline Step）  ← 断裂点
```

Planner 为 Agent 生成了定制化的 Persona（如"你是宏观经济研究员，专注于 CPI 和利率分析"），但三个 Executor **完全不读这个 Persona**，而是跑固定的 Pipeline Step：

| Executor | 执行步骤 | 读取 Persona？ | 可用工具 |
|---|---|---|---|
| `ResearchAgentExecutor` | QueryPlanStep → SearchStep → RelevanceStep → CompressStep | ❌ | 无工具调用，只有固定的 SearchStep |
| `WritingAgentExecutor` | OutlineStep → WriteStep | ❌ | 无工具调用，WriteStep 内部有 Agent Loop 但不感知 Persona |
| `ReviewAgentExecutor` | PostReviewStep | ❌ | 无工具调用 |

### 1.2 知识库无法接入

因为 Executor 跑的是固定 Step，不经过 `ChatWithTools` 循环，所以：
- `search_knowledge` 工具无法被 LLM 自主调用
- `search_web` 工具也无法被 LLM 自主调用
- 当时的设计决策（`server.go:544`）—— "KB is now a standalone tool (search_knowledge), not mixed into SearchClient" —— 在编辑部模式下无法生效

### 1.3 对照：Harness 模式已经解决这些问题

Harness 模式通过 `ChatWithTools` 循环让 LLM 自主调用 `search_web` + `search_knowledge` + `read_source` + `write_article` 等工具，KB 自然接入，Persona 自然生效。但 Harness 是面向单用户的通用写作助手，不具备编辑部的角色分工、Artifact 传递、预算控制等能力。

### 1.4 目标

把编辑部的三个 Agent Executor 从"跑 Pipeline Step"改为"跑角色化的 ChatWithTools 循环"，使：
1. **Persona 生效** — Planner 生成的角色 Persona 注入 system prompt，指导 LLM 行为
2. **工具自主调用** — LLM 按需调用 `search_web`、`search_knowledge`、`read_source` 等工具
3. **KB 自然接入** — `search_knowledge` 作为独立工具，LLM 按需调用，和网络搜索分开
4. **Artifact 产出不变** — 工具循环结束后，仍然产出 `research_brief`、`draft`、`review_report` 等 Artifact

---

## 二、设计方案

### 2.1 核心抽象：RoleAgentRunner

在 `internal/editorial/` 新增 `role_agent_runner.go`，定义一个**角色化的 Agent Runner**，复用 `agent.ToolExecutorConfig` + `agent.BuildToolExecutor` + `tools.LLMClient.ChatWithTools` 的工具调用循环，但：

- **不用 `WritingSession`** — 编辑部 Agent 是无状态的，通过 Artifact 传递上下文，不需要跨轮会话
- **不用 `ClassifyIntent`** — 意图由角色固定（researcher=研究、writer=写作、reviewer=审校）
- **不用 `Harness.buildSystemPrompt`** — 改用角色 Persona + 上游 Artifact 构建 system prompt
- **不面向前端流式** — 通过 `noopEmitter` 或 `editorialWSEmitter` 转发事件

```go
// RoleAgentRunner 角色化 Agent 执行器
// 复用 Harness 的 ChatWithTools 循环，但锁定角色、工具集和 Persona
type RoleAgentRunner struct {
    llm        *tools.LLMClient
    search     *tools.SearchClient
    kbSearcher tools.KnowledgeSearcher
    profile    *profile.StyleProfile
    emitter    engine.EventEmitter
}

// RoleRunConfig 单次角色化执行配置
type RoleRunConfig struct {
    // 角色配置
    AgentConfig *AgentConfig       // 来自 Planner 或 BuiltinRoles

    // 输入
    Task            *Task
    AgentContext    *AgentContext  // 含上游 Artifact + OrgKnowledge
    ExecutionContext *engine.ExecutionContext

    // 工具集（由角色决定，而非意图判定）
    ToolDefs []tools.ToolDef

    // MaxCalls guard
    MaxCalls map[string]int

    // 完成信号：LLM 输出什么标志表示角色任务完成
    // researcher: "submit_research_brief"（信号工具）
    // writer: "write_article"（信号工具，触发流式输出）
    // reviewer: "submit_review_report"（信号工具）
    CompletionTool string
}

// Run 执行角色化 Agent 循环
// 返回 LLM 的最终文本输出 + Token 消耗
func (r *RoleAgentRunner) Run(ctx context.Context, cfg RoleRunConfig) (output string, tokens int, err error)
```

### 2.2 角色化工具集

每个角色的可用工具集，由 `BuiltinRoles` 的 `AllowedTools` 字段映射到具体的 `tools.ToolDef`：

| 角色 | 工具集 | 信号工具（完成标志） | 产出 Artifact |
|---|---|---|---|
| **researcher** | `search_web` + `search_knowledge` + `read_source` | `submit_research_brief` | `research_brief` + `source_pack` + `fact_claims` |
| **writer** | `search_knowledge`（只读参考）+ `read_source` + `generate_outline` | `write_article` | `outline` + `draft` / `revised_draft` |
| **reviewer** | `search_knowledge`（交叉验证）+ `fact_check` + `read_source` | `submit_review_report` | `review_report` |

**工具集隔离原则**：
- researcher 不能调用 `write_article` — 它只管研究
- writer 不能调用 `fact_check` — 它只管写作
- reviewer 不能调用 `write_article` — 它只管审校

新增两个信号工具：

```go
// submit_research_brief — 研究完成信号
// LLM 调用后，RoleAgentRunner 从 LLM 输出中提取研究简报内容
{
    "name": "submit_research_brief",
    "description": "提交研究简报。当你已完成搜索和素材收集，调用此工具提交结构化研究简报。",
    "parameters": {
        "type": "object",
        "properties": {
            "summary": {"type": "string", "description": "研究简报摘要"},
            "sources": {"type": "array", "description": "信源列表"},
            "claims": {"type": "array", "description": "事实声明列表"}
        },
        "required": ["summary"]
    }
}

// submit_review_report — 审校完成信号
// LLM 调用后，RoleAgentRunner 从参数中提取审查报告
{
    "name": "submit_review_report",
    "description": "提交审查报告。当你已完成审校，调用此工具提交结构化审查结果。",
    "parameters": {
        "type": "object",
        "properties": {
            "passed": {"type": "boolean", "description": "是否通过审校"},
            "issues": {"type": "array", "description": "问题列表"},
            "severity": {"type": "string", "description": "严重程度: low/medium/high"}
        },
        "required": ["passed"]
    }
}
```

writer 复用现有的 `write_article` 信号工具，行为与 Harness 模式一致。

### 2.3 System Prompt 构建

每个角色的 system prompt 由四部分组成：

```
[1. Persona]          — AgentConfig.Persona（来自 Planner 或 BuiltinRoles）
[2. 任务上下文]       — 选题描述 + 上游 Artifact 摘要
[3. 组织知识]         — OrgKnowledge（信源可信度、栏目偏好、活跃知识）
[4. 工具使用指引]     — 可用工具列表 + 完成信号说明
```

**不使用 WorldState diff** — 编辑部 Agent 是单轮执行（不是多轮对话），不需要增量推送。WorldState diff 是 Harness 多轮对话场景的优化，这里直接全量构建即可。

### 2.4 Executor 改造

三个 Executor 的 `Execute()` 方法从"跑 Pipeline Step"改为"跑 RoleAgentRunner"：

#### ResearchAgentExecutor 改造

```
旧：QueryPlanStep → SearchStep → RelevanceStep → CompressStep → 构建 Artifact
新：
  1. 构建 RoleRunConfig（Persona + 工具集 + 选题卡 + 组织知识）
  2. 调用 RoleAgentRunner.Run()
  3. LLM 自主调用 search_web / search_knowledge / read_source，最后调用 submit_research_brief
  4. 从信号工具参数中提取 research_brief / source_pack / fact_claims
  5. 创建 Artifact（与现有逻辑一致）
  6. 记录信源使用情况（与现有逻辑一致）
```

#### WritingAgentExecutor 改造

```
旧：OutlineStep → WriteStep
新：
  1. 构建 RoleRunConfig（Persona + 工具集 + 研究简报 + 组织知识 + 栏目偏好）
  2. 调用 RoleAgentRunner.Run()
  3. LLM 自主调用 generate_outline（可选）/ search_knowledge（参考范文）/ read_source
  4. LLM 调用 write_article 触发流式输出
  5. 从 ExecutionContext 中提取文章内容（与现有逻辑一致）
  6. 创建 Artifact（与现有逻辑一致）
```

#### ReviewAgentExecutor 改造

```
旧：PostReviewStep
新：
  1. 构建 RoleRunConfig（Persona + 工具集 + 草稿 + 信源包 + 事实声明 + 组织知识）
  2. 调用 RoleAgentRunner.Run()
  3. LLM 自主调用 search_knowledge（交叉验证）/ fact_check / read_source
  4. LLM 调用 submit_review_report
  5. 从信号工具参数中提取审查报告
  6. 创建 Artifact + 沉淀知识 + 更新信源可信度（与现有逻辑一致）
```

### 2.5 KB 接入路径

改造后，`server.go` 的编辑部初始化路径只需在创建 Executor 时传入 `kbSearcher`：

```go
// server.go — editorial 初始化处
kbAdapter := services.NewKbSearchAdapter(s.kbMgr)

// 三个 Executor 都传入 kbAdapter
researchExec := editorial.NewResearchAgentExecutor(defaultLLM, searchClient, embeddingClient, edStore, kbAdapter)
writingExec := editorial.NewWritingAgentExecutor(defaultLLM, defaultProfile, searchClient, edStore, kbAdapter)
reviewExec := editorial.NewReviewAgentExecutor(defaultLLM, defaultProfile, searchClient, edStore, kbAdapter)
```

Executor 把 `kbAdapter` 传给 `RoleAgentRunner`，`RoleAgentRunner` 把它放进 `ToolExecutorConfig.KBSearcher`，`executeSearchKnowledge` 就能正常工作。

**KB 和网络搜索的分离原则保持不变**：
- `search_web` 和 `search_knowledge` 是两个独立的 tool
- LLM 自主决定何时用哪个
- KB 结果返回 chunk 级精确内容，不经过 CompressStep 压缩
- 网络搜索结果在 LLM 的上下文中和 KB 结果并列展示，由 LLM 自行判断可信度

---

## 三、实施计划

### Phase 1: RoleAgentRunner 核心（3 天）

| 步骤 | 内容 | 产出文件 | 状态 |
|---|---|---|---|
| 1.1 | `RoleAgentRunner` 结构体 + `Run()` 方法 | `editorial/role_agent_runner.go` | ⏳ |
| 1.2 | 角色化 system prompt 构建（Persona + 任务上下文 + 组织知识 + 工具指引） | `editorial/role_agent_runner.go` | ⏳ |
| 1.3 | 信号工具 `submit_research_brief` + `submit_review_report` 定义 + 执行器 | `editorial/role_agent_runner.go` | ⏳ |
| 1.4 | 角色化工具集定义函数 `ToolsForRole(role string, hasSearch, hasKB bool) []ToolDef` | `editorial/role_agent_runner.go` | ⏳ |
| 1.5 | MaxCalls guard（研究 Agent search_web ≤ 5, search_knowledge ≤ 3 等） | `editorial/role_agent_runner.go` | ⏳ |
| 1.6 | 单元测试：Runner 基本流程 + 工具调用 + 信号工具触发 | `editorial/role_agent_runner_test.go` | ⏳ |

### Phase 2: Executor 改造（3 天）

| 步骤 | 内容 | 改动文件 | 状态 |
|---|---|---|---|
| 2.1 | `ResearchAgentExecutor` 改为使用 RoleAgentRunner | `editorial/agent_executors.go` | ⏳ |
| 2.2 | `WritingAgentExecutor` 改为使用 RoleAgentRunner | `editorial/agent_executors.go` | ⏳ |
| 2.3 | `ReviewAgentExecutor` 改为使用 RoleAgentRunner | `editorial/agent_executors.go` | ⏳ |
| 2.4 | 三个 Executor 的构造函数增加 `kbSearcher` 参数 | `editorial/agent_executors.go` | ⏳ |
| 2.5 | 保留 Artifact 产出逻辑（CreateArtifact + autoApprove + RecordSourceUsage 等） | 不变 | ⏳ |
| 2.6 | 单元测试：三个 Executor 端到端流程 | `editorial/agent_executors_test.go` | ⏳ |

### Phase 3: Server 注入 + DAG 联调（2 天）

| 步骤 | 内容 | 改动文件 | 状态 |
|---|---|---|---|
| 3.1 | `server.go` 编辑部初始化处传入 `kbSearcher` | `server/server.go` | ⏳ |
| 3.2 | DAG 模式下 `executeNode` 把 `AgentConfig.Persona` 传入 `AgentContext` | `editorial/dag_executor.go` | ⏳ |
| 3.3 | DAGExecutor 的 `RegisterExecutor` 适配新的 Executor 签名 | `editorial/dag_executor.go` | ⏳ |
| 3.4 | ExperimentRunner 适配新签名（对照实验三组模式） | `editorial/experiment_runner.go` | ⏳ |
| 3.5 | 集成测试：DAG 模式下 Planner → DAG → Agent 全链路 | 手动验证 | ⏳ |

### Phase 4: 兼容性 + 回归（1 天）

| 步骤 | 内容 | 状态 |
|---|---|---|
| 4.1 | 线性模式（Orchestrator）保持兼容 — 旧的 `executeNode` 路径不受影响 | ⏳ |
| 4.2 | Harness 模式不受影响 — 不改动 `agent/harness.go` | ⏳ |
| 4.3 | Pipeline 模式不受影响 — 不改动 `engine/steps/` | ⏳ |
| 4.4 | `noopEmitter` 保持兼容 | ⏳ |
| 4.5 | Go 编译 + vet + test 全通过 | ⏳ |

---

## 四、改动文件清单

### 新增文件

| 文件 | 说明 |
|---|---|
| `backend/internal/editorial/role_agent_runner.go` | RoleAgentRunner 核心 + 信号工具 + 角色化工具集 |
| `backend/internal/editorial/role_agent_runner_test.go` | 单元测试 |

### 修改文件

| 文件 | 改动范围 | 说明 |
|---|---|---|
| `backend/internal/editorial/agent_executors.go` | 大改 | 三个 Executor 从跑 Step 改为跑 RoleAgentRunner，构造函数增加 `kbSearcher` 参数 |
| `backend/internal/server/server.go` | 小改 | 编辑部初始化处传入 `kbSearcher`（~10 行） |
| `backend/internal/editorial/dag_executor.go` | 小改 | `executeNode` 把 `AgentConfig.Persona` 传入 AgentContext（~5 行） |
| `backend/internal/editorial/experiment_runner.go` | 小改 | 适配新签名（~10 行） |

### 不改动文件

| 文件 | 理由 |
|---|---|
| `backend/internal/agent/harness.go` | Harness 模式独立，不受影响 |
| `backend/internal/agent/tools.go` | 工具定义和执行器复用，不改动 |
| `backend/internal/engine/steps/` | Pipeline Step 不改动，Editorial 不再调用它们（但保留以备回退） |
| `backend/internal/editorial/planner.go` | Planner 不改动 |
| `backend/internal/editorial/planner_prompt.go` | Planner Prompt 不改动（"search" 描述已正确） |
| `backend/internal/editorial/roles.go` | BuiltinRoles 不改动 |
| `backend/internal/editorial/orchestrator.go` | 线性 Orchestrator 不改动（兼容保留） |
| `backend/internal/services/kb_search_adapter.go` | KbSearchAdapter 不改动 |

---

## 五、关键设计决策

### 5.1 为什么不直接复用 Harness？

| 维度 | Harness | RoleAgentRunner |
|---|---|---|
| **会话模型** | 多轮持续会话，跨轮保留文章/素材/记忆 | 单轮执行，通过 Artifact 传递上下文 |
| **意图判定** | 规则判定（ClassifyIntent） | 角色固定（researcher/writer/reviewer） |
| **System Prompt** | WorldState diff（增量推送，节省 Token） | 全量构建（单轮执行，不需要 diff） |
| **完成标志** | 无显式标志，LLM 自行结束 | 信号工具（submit_research_brief / write_article / submit_review_report） |
| **前端交互** | 流式输出到前端（onDelta/onReasoning） | 通过 editorialWSEmitter 转发事件 |
| **SessionStore** | 需要（跨轮持久化对话历史） | 不需要 |
| **WorldState** | 需要（Token 优化） | 不需要（单轮全量即可） |

复用 Harness 会带入大量不需要的复杂度（SessionStore、WorldState、意图判定、多轮对话），而且改动 Harness 会影响现有的用户写作流程。RoleAgentRunner 是一个更轻量的抽象，只取 ChatWithTools 循环这一核心能力。

### 5.2 为什么用信号工具而不是直接解析 LLM 输出？

1. **结构化产出** — `submit_research_brief` 的参数是 JSON，直接拿到 `summary`/`sources`/`claims` 结构，不需要从自由文本中解析
2. **明确完成时机** — LLM 调用信号工具 = 角色任务完成，RoleAgentRunner 可以立即终止循环，不需要等待 LLM 自行停止
3. **与 Harness 一致** — `write_article` 已经是信号工具模式，编辑部 writer 直接复用

### 5.3 为什么保留旧 Step 代码？

- **回退保障** — 如果 RoleAgentRunner 出问题，可以快速回退到 Pipeline Step
- **ExperimentRunner 对照** — 对照实验需要跑 Pipeline 模式作为基准
- **零删除风险** — 不删除旧代码 = 不破坏现有功能

### 5.4 MaxCalls Guard 设计

每个角色的工具调用次数限制：

| 角色 | 工具 | MaxCalls | 理由 |
|---|---|---|---|
| researcher | `search_web` | 5 | 防止无限搜索 |
| researcher | `search_knowledge` | 3 | KB 精度高，不需要太多 |
| researcher | `read_source` | 8 | 搜索结果的详细阅读 |
| writer | `search_knowledge` | 3 | 参考范文，不是主要工作 |
| writer | `read_source` | 3 | 读取研究简报细节 |
| reviewer | `search_knowledge` | 3 | 交叉验证事实 |
| reviewer | `fact_check` | 5 | 核查事实声明 |
| reviewer | `read_source` | 3 | 读取信源详情 |

---

## 六、风险与缓解

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| LLM 不调用信号工具，导致循环不终止 | 中 | 高 | MaxIterations 限制（默认 12）+ 超时（5 分钟）+ 超时后从 LLM 最终输出中提取内容 |
| LLM 输出质量不稳定（不如固定 Step） | 中 | 中 | BuiltinRoles Persona 经调优 + 对照实验验证 + 保留旧 Step 作为回退 |
| Token 成本增加（LLM 自主决策比固定 Step 多用 Token） | 中 | 中 | MaxCalls 限制 + WorldState diff 可后续引入 + 对比实验监控 |
| `submit_research_brief` 参数结构不完整 | 中 | 低 | 参数做 optional 处理 + fallback 从 LLM 自由文本中提取 |
| 研究_agent 搜索结果不再经过 RelevanceStep 去重 | 低 | 中 | LLM 自主判断相关性（它能看到搜索结果摘要）；后续可在 RoleAgentRunner 中加可选的去重逻辑 |

---

## 七、验收标准

### 7.1 功能验收

- [ ] researcher Agent 能自主调用 `search_web` 和 `search_knowledge`，KB 结果标记为 `source: "local_kb"`
- [ ] researcher Agent 调用 `submit_research_brief` 后，正确产出 `research_brief` + `source_pack` + `fact_claims` Artifact
- [ ] writer Agent 能自主调用 `search_knowledge` 查参考范文，然后调用 `write_article` 流式输出
- [ ] writer Agent 产出 `draft` / `revised_draft` Artifact，内容格式与现有一致
- [ ] reviewer Agent 能自主调用 `fact_check` 和 `search_knowledge`，然后调用 `submit_review_report`
- [ ] reviewer Agent 产出 `review_report` Artifact，内容格式与现有一致
- [ ] Planner 生成的 Persona 被正确注入 system prompt，影响 LLM 行为
- [ ] DAG 模式下，Planner → DAG → Agent 全链路跑通

### 7.2 兼容性验收

- [ ] Harness 模式（用户写作工作台）不受影响
- [ ] Pipeline 模式不受影响
- [ ] 线性编辑部模式（Orchestrator）不受影响
- [ ] 现有 Artifact 格式和状态机不变
- [ ] 现有 WebSocket 事件不变
- [ ] Go 编译 + vet + test 全通过
- [ ] 前端 TSC + Vite 构建 + lint 全通过

### 7.3 效果验收

- [ ] 知识库内容出现在研究简报中（KB 检索命中时）
- [ ] 研究简报质量 ≥ 现有 Pipeline Step 模式（对照实验验证）
- [ ] Token 成本增幅 ≤ 30%（对照实验验证）

---

## 八、里程碑

| 里程碑 | 内容 | 预计完成 |
|---|---|---|
| M1 | RoleAgentRunner 核心 + 单元测试 | 第 3 天 |
| M2 | 三个 Executor 改造完成 + 单元测试 | 第 6 天 |
| M3 | Server 注入 + DAG 联调 | 第 8 天 |
| M4 | 兼容性回归 + 验收 | 第 9 天 |

---

## 九、后续展望

### 9.1 WorldState diff 引入

当前 RoleAgentRunner 是单轮全量构建 system prompt。如果后续编辑部 Agent 需要多轮交互（如人类编辑与 Agent 对话修改），可以引入 WorldState diff 优化 Token。

### 9.2 动态工具集

当前工具集由角色固定。后续可以让 Planner 在生成 AgentConfig 时指定更细粒度的工具集（如某个 researcher 只用 KB 不用网络搜索），RoleAgentRunner 从 `AgentConfig.AllowedTools` 动态构建工具集。

### 9.3 Agent 间通信

当前 Agent 间通过 Artifact 传递上下文。后续可以引入 Codex 的 `InterAgentCommunication` 机制，让 Agent 在执行过程中直接通信（如 researcher 发现新线索通知 writer）。

### 9.4 Phase 6 联调

本计划完成后，`dynamic-subagent-feasibility.md` 的 Phase 6 联调可以正式推进——因为 Agent 执行器终于和 Planner/DAGExecutor 的动态化能力对齐了。
