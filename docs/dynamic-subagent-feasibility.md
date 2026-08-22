# 动态 SubAgent 集群：可行性研究与实施计划

> 版本: v5.0 | 日期: 2026-08-22
> 状态: **Phase 0-6 全部完成 ✅**
>
> v2.0 变更：整合 OpenAI Codex 开源代码（github.com/openai/codex）的架构洞察，
> 重点补充上下文管理、记忆管理、角色质量三大痛点的解决方案。
>
> **v3.0 变更**：将 Codex 架构洞察的适用范围从"编辑部模式专用"扩展为**全局 Agent 基础设施改进**。
> Phase 0（上下文管理重构）不再只是编辑部的前置步骤，而是覆盖 Harness、Pipeline、ChatStep、
> WriteStep 等所有执行路径的**通用基础设施升级**，使 LuminBuddy V2 全平台受益。
>
> **v3.1 变更**：Phase 0（步骤 0.1-0.8）全部完成并通过单元测试（12/12 PASS）。
> 动态 SubAgent 集群（Phase 1-6）正式作为 **Beta 项目**推进。
>
> **v4.0 变更**：Phase 1-5 全部完成（23/23 单元测试通过，TypeScript 类型检查通过，Go 编译通过）。
> 新增 8 个后端文件（agent_config.go, roles.go, dag_types.go, dag_validator.go,
> context_fork.go, token_budget.go, dag_executor.go, planner.go, planner_prompt.go）
> + 4 个前端文件（workflow-store.ts, canvas.tsx, agent-node.tsx, workflow-input.tsx,
> workflow-page.tsx）+ WebSocket 协议扩展。
>
> **v5.0 变更**：Phase 6 联调完成。RoleAgentRunner 实现 ChatWithTools agentic loop，
> 替代固定 pipeline 步骤。NodeEmitter 桥接 LLM 流式输出到前端。并发安全加固
> （atomic.Pointer + sync.Mutex + atomic.Bool）。修复 2 个 P6 联调 bug：
> node.stream.reset 前端清空逻辑、Task 缺少 StyleSlug/Tags 传递。

---

## 一、执行摘要

### 1.1 目标

本计划包含两个层面的改进：

**层面 A：全局 Agent 基础设施升级**（Phase 0，适用于全平台）
- 借鉴 Codex 的 `ContextManager` + `WorldState` diff 模式，重构系统提示构建
- 将 `buildSystemPrompt` 从全量拼装改为 section 化增量推送
- 引入 `history_version` + `reference_context` 基线管理
- 引入 `AutoCompactFallback` 自动压缩降级机制
- **受益范围**：Harness 模式（chat/writing/polish）、Pipeline 模式（WriteStep/ChatStep/CompressStep）、
  未来编辑部模式、未来 DAG 执行器 — **所有 LLM 调用路径**

**层面 B：动态 SubAgent 集群**（Phase 1-6，编辑部模式专用）
- 将"编辑部模式"从固定三 Agent 线性流升级为动态 SubAgent 集群
- 由 LLM 根据用户意图自动生成角色集合和协作拓扑
- 在用户可编辑的 DAG 框架下执行长篇内容创作任务

### 1.2 核心判断

| 维度 | 结论 |
|------|------|
| 技术可行性 | ✅ **高** — 现有代码基础（Event/Decision/Transition、Artifact 流转、Lease 互斥）与目标架构兼容 |
| 商业价值 | ✅ **高** — 业界空白：无产品将动态 Agent 集群用于长文创作 |
| 实施风险 | ⚠️ **中** — LLM 生成角色质量不稳定、Token 成本控制、前端复杂度 |
| 工程量 | 中大型 — 后端 ~2 周，前端 ~2 周，联调 ~1 周 |

### 1.3 关键决策

1. **不引入 LangGraph** — 现有 `Orchestrator` 的 Event/Decision/Transition 三层模型已经够用，引入新框架的迁移成本 > 收益
2. **参考 OpenMAIC 的 `isGenerated` 模式** — 动态角色打标 + 运行时注册 + 任务完成后清除
3. **前端用 React Flow** — 业界标准节点图库，Dify/n8n 等产品均基于此构建
4. **保留代码快速路径** — 质量高直接推进，质量低直接重试，只在模糊地带调用 LLM Director

---

## 二、现状分析

### 2.1 后端架构

#### 现有 Editor 模式（`internal/editorial/`）

```
Task → PendingApproval → Research → Writing → Review → PendingPublish → Published
                           ↑          ↑          ↑
                      ResearchAgent  WritingAgent  ReviewAgent
                      (固定角色)     (固定角色)     (固定角色)
```

**核心类型**（`types.go`）：
- `AgentRole`：枚举（`research_agent` / `writing_agent` / `review_agent`）
- `AgentDefinition`：静态注册到 `AgentRegistry`（全局 map）
- `AgentExecutorAdapter`：接口 `Role() + Execute(ctx, ac, task) → (*Artifact, error)`
- `Orchestrator`：硬编码 `executors map[AgentRole]AgentExecutorAdapter`
- `Artifact`：结构化交付物（8 种类型：topic_card → research_brief → outline → draft → review_report → revised_draft）
- `Task`：状态机，`CanTransitionTo` 硬编码转换矩阵

**Orchestrator 路由**（`orchestrator.go`）：
- `routeAfterResearch`：评估 source 数/gap 数 → 自动推进/创建 Decision/重试
- `routeAfterWriting`：评估 word_count/section_count → 自动推进/人工审批
- `handleReviewResult`：评估 severity → 发布/退回修改/升级

#### 现有 Harness 模式（`internal/agent/harness.go`）

- 单 LLM 会话 + 工具调用循环
- `ToolsForIntent`：根据意图（writing/chat/polish）选择工具集
- 前端通过 WebSocket 实时接收 stream/step/tool 事件

#### WebSocket 协议（`websocket/protocol.go`）

已有事件类型：
- `agent.start/pause/resume/cancel/confirm/edit`（客户端→服务端）
- `agent.created/step.start/step.complete/stream/stream.done/completed/error`（服务端→客户端）

### 2.2 前端架构

- `WritingComposer`：药丸形输入框 + 模式选择 + 风格选择 + 素材管理
- `agent-store.ts`：Zustand store，管理 `WritingSession[]`，通过 WebSocket 收发消息
- `ChatMessage`：多 part 模型（text/tool-call/data/reasoning/compaction）
- 无节点图、无流程编辑器

### 2.3 与目标架构的差距

| 维度 | 现状 | 目标 | 差距 |
|------|------|------|------|
| Agent 角色 | 3 个固定枚举 | LLM 动态生成 N 个角色 | `AgentRole` → `AgentConfig` 结构体 |
| 协作拓扑 | 线性 A→B→C | DAG（并行/串行/分支） | 需要图执行器 |
| 路由决策 | 代码硬编码 if/else | LLM Director + 代码快速路径 | 新增 Director 节点 |
| 前端 | 对话式 Composer | 节点图 + 对话 | 引入 React Flow |
| WebSocket | 单 session 事件流 | 多节点状态并行推送 | 新增 node 事件类型 |

---

## 三、参考案例

### 3.1 OpenMAIC（清华 MAIC，20.8k Star，MIT）

**最直接参考**。核心机制：

1. **`isGenerated` 标记**：`AgentConfig` 有 `isGenerated?: boolean` 和 `boundStageId?: string`。LLM 生成角色打标后动态注册到 Registry，任务完成后清除。
2. **Director Graph**：LangGraph StateGraph，2 节点（director + agent_generate）+ 条件边。单轮拓扑，多 Agent 讨论由客户端串行化请求驱动。
3. **Director 决策**：LLM 输出 `{"next_agent": "agentId | END | USER"}`，单 Agent 时纯代码逻辑（0 LLM 调用）。
4. **Prompt 动态构建**：`buildStructuredPrompt(agentConfig, storeState, peerContext, ...)` 模板化拼装。

**借鉴点**：`isGenerated` 模式、Director 快速路径/LLM 慢速路径分叉、peer context 注入。

**不借鉴**：OpenMAIC 是 TypeScript/Next.js，我们是 Go 后端 + React 前端；OpenMAIC 无节点图前端。

### 3.2 LangGraph（LangChain）

- StateGraph + conditional edges + checkpoint
- 动态条件边：图结构可在运行时由 LLM 决定下一步

**借鉴点**：图执行器的状态管理设计。**不引入**：迁移成本高，现有 Orchestrator 已够用。

### 3.3 CrewAI

- `Agent(role, goal, backstory, tools)` + Hierarchical Manager 模式
- Agent 间通过 Artifact 通信

**借鉴点**：Agent 配置格式（role/goal/backstory/tools）。

### 3.4 Dify / n8n / React Flow

- Dify：可视化工作流编排 + Agent 节点
- n8n：成熟节点图编辑器 + 子流程嵌套
- React Flow：Dify/n8n 的底层引擎

**借鉴点**：前端节点图交互设计。

### 3.5 OpenAI Codex（github.com/openai/codex，111k Star，Apache-2.0）

**最关键的参考**。2026-08-21 开源，Rust 实现（96.4%），揭示了 OpenAI 如何解决多 Agent 上下文传递、记忆管理、角色质量三大痛点。

#### 3.5.1 上下文管理：ContextManager + WorldState

Codex 的上下文管理分为两层：

**第一层：ContextManager（对话历史管理）**
- `ContextManager` 持有 `Arc<Vec<ResponseItemEnvelope>>`（共享所有权的历史记录向量）
- **写时复制（COW）**：只读消费者共享同一个 vector，只有修改时才做深拷贝
- `history_version: u64`：每次历史重写（compaction/rollback）时递增
- `reference_context_item`：上一轮的上下文基线，用于下一轮做 diff（而非全量重发）
- `world_state_baseline`：最近推送给模型可见历史的世界状态快照

**第二层：WorldState（结构化记忆）**
- `WorldState` 是一个**多 section 的状态对象**，每个 section 实现 `WorldStateSection` trait
- 每个 section 有 `snapshot()` 和 `render_diff(previous)` 方法
- **增量推送**：只推送变化部分（PreviousSectionState::Known vs Unknown vs Absent）
- 已实现的 section：
  - `MultiAgentModeState`：当前多 Agent 模式（ExplicitRequestOnly / Proactive / Custom）
  - `TokenBudgetContext`：Token 预算 + Context Window ID
  - `PermissionsState`：权限指令
  - `EnvironmentState`：环境信息
  - `ModelState` / `PersonalityState`：模型和人格设定
  - `AgentsMdState`：AGENTS.md 项目指令
  - `CollaborationModeState`：协作模式指令
  - `ToolsState`：延迟工具命名空间

**关键设计洞察**：
1. **不是全量重发 system prompt**，而是做 diff —— 这解决了多 Agent 场景下的 Token 暴涨问题
2. **Context Window 有唯一 ID**（UUID），Agent 知道自己处于第几个窗口，可以做跨窗口的上下文压缩
3. **消息标准化**（normalize_history）：确保每个 function call 都有对应的 output，移除孤立 output，根据模型能力 strip 不支持的 image/audio

#### 3.5.2 多 Agent 架构：AgentControl + AgentRegistry + Role

Codex 的多 Agent 模式（MAv2）架构：

**AgentControl（控制面）**
- 每个 root session 创建一个 `AgentControl` 实例
- 提供 `spawn_agent()` 和 `send_inter_agent_communication()` 能力
- 持有 `AgentRegistry`（Agent 注册表）、`V2Residency`（居住管理）、`AgentExecutionLimiter`（并发限制）
- `RolloutBudget`：部署预算控制

**AgentRegistry（动态注册表）**
- `SpawnReservation`：预留 Agent path 和 nickname
- `register_root_thread()`：注册根线程
- Agent 之间通过 `AgentPath`（类似文件路径：`root/child1/child2`）引用

**Role 系统（角色定义）**
- 内置角色：`default`、`explorer`（快速代码库查询）、`worker`（执行和产出）
- `AgentRoleConfig`：description + config_file + nickname_candidates
- `apply_role_to_config()`：角色作为**配置覆盖层**叠加到父 session 配置上
- 角色可以：覆盖 model、reasoning_effort、personality、developer_instructions
- 角色可以**限制**能力：禁用 ShellTool/Apps/Personality/Plugins/MemoryTool/RequestPermissionsTool
- 角色配置来自 TOML 文件（`builtins/explorer.toml`）

**Inter-Agent Communication（Agent 间通信）**
- `InterAgentCommunication`：带 `trigger_turn` 标志的消息
- 4 种通信类型：`Spawn` / `Message` / `Followup` / `Result`
- 通信内容可以是加密的（`encrypted_content`）或明文
- 子 Agent 完成时自动通知父 Agent（`maybe_start_completion_watcher`）

**Spawn 机制**
- `SpawnAgentForkMode`：`FullHistory`（继承完整历史）或 `LastNTurns(n)`（只继承最近 n 轮）
- `SpawnAgentOptions`：parent_thread_id, parent_turn_id, root_turn_id, environments
- 子 Agent 的 session_source 记录血缘关系（`SubAgentSource::ThreadSpawn`）

#### 3.5.3 Token 预算管理

- `TokenBudgetContext`：用 Context Window ID 跟踪每个 Agent 的上下文窗口
- `TokenBudgetRemainingContext`：告知模型剩余 Token 数
- `TokenBudgetReminder`：模板化的 Token 预算提醒（`{n_remaining}` 占位符）
- `AutoCompactFallbackPrompt`：自动压缩降级提示
- `RolloutBudget`：部署级别的 Token 预算

#### 3.5.4 对 LuminBuddy V2 的借鉴

| Codex 机制 | LuminBuddy V2 对应 | 借鉴方式 |
|-----------|-------------------|---------|
| `ContextManager` + COW | 当前 `agent.Harness` 的消息列表 | 引入 history_version + reference_context 做 diff |
| `WorldState` section diff | 当前 `buildSystemPrompt` 全量拼装 | 改为 section 化增量推送 |
| `AgentControl` + `AgentRegistry` | 目标 `DynamicRegistry` | 参考 `AgentPath` 层次化命名 |
| `Role` + TOML override | 目标 `AgentConfig` | 参考"角色作为配置覆盖层"模式 |
| `InterAgentCommunication` | 目标 DAG 节点间通信 | 参考 `trigger_turn` 标志和 4 种通信类型 |
| `SpawnAgentForkMode` | 目标 DAG 节点上下文继承 | 引入 `LastNTurns` 模式控制上下文传递 |
| `TokenBudgetContext` | 目标 Token 预算控制 | 参考 Context Window ID 做跨 Agent 预算追踪 |
| `AutoCompactFallbackPrompt` | 当前无 | 引入自动压缩降级机制 |

---

## 四、目标架构设计

### 4.1 核心数据结构变更

#### 4.1.1 AgentRole → AgentConfig

```go
// 从枚举改为结构化配置
type AgentConfig struct {
    ID            string          `json:"id"`
    Name          string          `json:"name"`           // "宏观经济分析师"
    Role          string          `json:"role"`           // "analyst" / "writer" / "reviewer" / "researcher" ...
    Persona       string          `json:"persona"`        // 完整 system prompt
    AllowedTools  []string        `json:"allowed_tools"`  // ["search","write","factcheck"]
    Priority      int             `json:"priority"`       // Director 选择优先级
    CanProduce    []ArtifactType  `json:"can_produce"`
    CanConsume    []ArtifactType  `json:"can_consume"`
    
    // 动态生成标记
    IsGenerated   bool            `json:"is_generated"`
    BoundTaskID   string         `json:"bound_task_id,omitempty"`
    
    // 元数据
    CreatedAt     time.Time      `json:"created_at"`
}
```

#### 4.1.2 AgentRegistry → DynamicRegistry

```go
// 从全局 var 改为实例化的动态注册表
type AgentRegistry struct {
    mu       sync.RWMutex
    agents   map[string]*AgentConfig  // ID → Config
    store    *Store                   // 持久化
}

// 注册动态生成的 Agent（任务完成后清除）
func (r *AgentRegistry) ApplyGeneratedAgents(taskID string, configs []AgentConfig) {
    r.mu.Lock()
    defer r.mu.Unlock()
    // 清除上一轮该任务的生成角色
    for id, cfg := range r.agents {
        if cfg.BoundTaskID == taskID {
            delete(r.agents, id)
        }
    }
    for _, cfg := range configs {
        cfg.IsGenerated = true
        cfg.BoundTaskID = taskID
        r.agents[cfg.ID] = &cfg
    }
}
```

#### 4.1.3 DAG 拓扑定义

```go
// NodeSpec 定义 DAG 中的一个执行节点
type NodeSpec struct {
    ID           string         `json:"id"`            // "node-1"
    AgentID      string         `json:"agent_id"`     // 引用 AgentConfig.ID
    Label        string         `json:"label"`        // "宏观分析"
    Dependencies []string       `json:"dependencies"` // 依赖的 NodeSpec.ID
    InputArtifacts []ArtifactType `json:"input_artifacts"` // 需要哪些交付物作为输入
    OutputArtifact ArtifactType `json:"output_artifact"` // 产出什么交付物
    Position     *Position      `json:"position,omitempty"` // 前端节点图坐标
}

type Position struct {
    X float64 `json:"x"`
    Y float64 `json:"y"`
}

// WorkflowSpec DAG 工作流定义
type WorkflowSpec struct {
    TaskID  string     `json:"task_id"`
    Nodes   []NodeSpec `json:"nodes"`
    Edges   []Edge     `json:"edges"`     // 冗余于 dependencies，供前端直接渲染
    CreatedBy string   `json:"created_by"`
    Source   string    `json:"source"`   // "llm_generated" | "user_modified" | "template"
}

type Edge struct {
    From string `json:"from"`
    To   string `json:"to"`
    Label string `json:"label,omitempty"` // "研究简报" — 交付物类型
}
```

### 4.2 图执行器（DAG Executor）

```go
// DAGExecutor 管理 SubAgent 集群的并行/串行执行
type DAGExecutor struct {
    registry   *AgentRegistry
    store      *Store
    hub        *websocket.Hub
    executors  map[string]AgentExecutorAdapter  // agentID → executor
}

// Execute 遍历 DAG，按拓扑序执行就绪节点
func (e *DAGExecutor) Execute(ctx context.Context, spec *WorkflowSpec, task *Task) error {
    // 1. 拓扑排序，找出所有入度为 0 的节点（就绪节点）
    // 2. 并行启动就绪节点（每个节点跑一个 SubAgent）
    // 3. 节点完成后：
    //    a. 产出 Artifact 存入 Store
    //    b. 更新下游节点的入度
    //    c. 推送 node.completed 事件到前端
    //    d. 如果有新的就绪节点，回到步骤 2
    // 4. 所有节点完成 → 推送 workflow.completed
}
```

### 4.3 意图分析 → 角色生成（Planner Agent）

```go
// Planner 分析用户意图，输出 AgentConfig[] + WorkflowSpec
type Planner struct {
    llm *tools.LLMClient
}

func (p *Planner) Plan(ctx context.Context, input string) (*PlanResult, error) {
    // 1. 调用 LLM，输入用户意图 + 可用工具列表 + Artifact 类型清单
    // 2. LLM 输出 JSON：
    //    {
    //      "agents": [
    //        {"id":"a1","name":"宏观经济研究员","role":"researcher","persona":"...","allowed_tools":["search"]},
    //        {"id":"a2","name":"国际形势分析师","role":"researcher","persona":"...","allowed_tools":["search"]},
    //        {"id":"a3","name":"深度撰稿人","role":"writer","persona":"...","allowed_tools":["write"]},
    //        {"id":"a4","name":"事实核查编辑","role":"reviewer","persona":"...","allowed_tools":["factcheck"]}
    //      ],
    //      "workflow": {
    //        "nodes": [
    //          {"id":"n1","agent_id":"a1","dependencies":[],"output_artifact":"research_brief"},
    //          {"id":"n2","agent_id":"a2","dependencies":[],"output_artifact":"research_brief"},
    //          {"id":"n3","agent_id":"a3","dependencies":["n1","n2"],"output_artifact":"draft"},
    //          {"id":"n4","agent_id":"a4","dependencies":["n3"],"output_artifact":"review_report"}
    //        ]
    //      }
    //    }
    // 3. 验证输出合法性（DAG 无环、artifact 类型匹配）
    // 4. 返回 PlanResult
}

type PlanResult struct {
    Agents    []AgentConfig  `json:"agents"`
    Workflow  WorkflowSpec   `json:"workflow"`
    Rationale string         `json:"rationale"` // LLM 解释为什么这样设计
}
```

### 4.4 前端节点图

```
┌─────────────────────────────────────────────────┐
│  [新任务输入框]                    [运行] [保存] │
│                                                 │
│  ┌──────────┐    ┌──────────┐                   │
│  │ 宏观研究  │    │ 国际分析  │    ← 并行节点     │
│  │ Agent #1 │    │ Agent #2 │                   │
│  └────┬─────┘    └────┬─────┘                   │
│       │  research_brief                         │
│       ↓     ↓                                   │
│  ┌──────────────────┐                           │
│  │   深度撰稿人      │    ← 串行节点             │
│  │   Agent #3       │                           │
│  └────────┬─────────┘                           │
│           │  draft                              │
│           ↓                                     │
│  ┌──────────────────┐                           │
│  │  事实核查编辑     │    ← 审校节点             │
│  │  Agent #4       │                           │
│  └──────────────────┘                           │
│                                                 │
│  [对话面板] ← 节点执行状态 + Agent 输出流        │
└─────────────────────────────────────────────────┘
```

#### 新增 WebSocket 事件

```go
// 服务端 → 客户端
const (
    MsgWorkflowCreated   = "workflow.created"    // Planner 返回角色集 + DAG
    MsgNodeStarted       = "node.started"       // 节点开始执行
    MsgNodeStreamDelta   = "node.stream.delta"   // 节点流式输出
    MsgNodeCompleted     = "node.completed"      // 节点完成
    MsgNodeFailed        = "node.failed"         // 节点失败
    MsgWorkflowCompleted = "workflow.completed"  // 整个 DAG 完成
)

// 客户端 → 服务端
const (
    MsgWorkflowStart  = "workflow.start"   // 用户确认 DAG 后启动
    MsgWorkflowEdit   = "workflow.edit"     // 用户修改 DAG（增删节点/改依赖）
    MsgWorkflowPause  = "workflow.pause"
    MsgWorkflowResume = "workflow.resume"
)
```

### 4.5 兼容性策略

**关键原则：增量改造，不破坏现有功能。**

1. `AgentRole` 枚举保留为 `AgentConfig` 的预设实例（`RoleResearch` → `DefaultResearchAgent`）
2. `Orchestrator` 的 `routeAfterResearch` 等保留作为快速路径
3. 新增 `editorial_mode` 字段到 `Task`：`linear`（默认）或 `dag`
4. 前端检测 `editorial_mode`，`linear` 走现有 Composer，`dag` 走节点图

### 4.6 上下文管理与记忆管理（借鉴 Codex）

这是业界最大痛点。当前 LuminBuddy V2 的 `buildSystemPrompt` 是**全量拼装**模式 —— 每轮都重新发送完整的 Profile（结构/修辞/标题等），导致 Token 暴涨。借鉴 Codex 的 WorldState diff 模式：

#### 4.6.1 Section 化系统提示

```go
// WorldStateSection 接口：每个上下文片段知道如何做 diff
type WorldStateSection interface {
    ID() string
    Snapshot() interface{}
    RenderDiff(previous interface{}) *ContextFragment // nil = 无变化
}

// ContextFragment：增量推送给模型的上下文片段
type ContextFragment struct {
    Role    string `json:"role"`    // "developer" / "user"
    Body    string `json:"body"`     // 推送给模型的内容
    Markers struct {
        Open  string `json:"open"`   // <context_window>
        Close string `json:"close"`  // </context_window>
    } `json:"markers"`
}

// WorldState：管理所有 section 的基线快照
type WorldState struct {
    sections      map[string]WorldStateSection
    baseline      map[string]interface{} // 上一轮的快照
}

// UpdateWorldState 遍历所有 section，只推送变化部分
func (w *WorldState) UpdateWorldState() []ContextFragment {
    var fragments []ContextFragment
    for id, section := range w.sections {
        snapshot := section.Snapshot()
        diff := section.RenderDiff(w.baseline[id])
        if diff != nil {
            fragments = append(fragments, *diff)
        }
        w.baseline[id] = snapshot
    }
    return fragments
}
```

#### 4.6.2 DAG 节点间的上下文传递

借鉴 Codex 的 `SpawnAgentForkMode`，DAG 节点间上下文传递有三种模式：

```go
type ContextForkMode int

const (
    // FullHistory：子节点继承父节点的完整对话历史
    // 适用于：研究节点 → 写作节点（写作节点需要看到研究的完整过程）
    ContextForkFull ContextForkMode = iota

    // LastNTurns：子节点只继承最近 N 轮对话
    // 适用于：并行研究节点之间的交叉引用（只看结论不看过程）
    ContextForkLastN

    // SummaryOnly：子节点只继承上游节点的 Artifact（产出物）+ 摘要
    // 适用于：审校节点（只需要看最终稿 + 研究简报，不需要看写作过程）
    ContextForkSummary
)

type NodeSpec struct {
    // ... 已有字段
    ContextFork  ContextForkMode `json:"context_fork"`
    ForkNTurns   int             `json:"fork_n_turns,omitempty"` // ContextForkLastN 时生效
}
```

#### 4.6.3 Token 预算追踪

借鉴 Codex 的 `TokenBudgetContext`，为每个 Agent 分配 Context Window ID：

```go
type AgentTokenBudget struct {
    AgentID         string
    ContextWindowID string    // UUID，标识当前上下文窗口
    TokensLeft      int64     // 剩余 Token 数
    TotalUsed       int64     // 已用 Token 数
}

// DAGExecutor 维护全局 Token 预算
type DAGExecutor struct {
    // ... 已有字段
    tokenBudgets   map[string]*AgentTokenBudget // agentID → budget
    totalBudget    int64                        // 整个 DAG 的总预算
}

// 每个节点完成时更新预算
func (e *DAGExecutor) updateBudget(agentID string, used int64) {
    budget := e.tokenBudgets[agentID]
    budget.TotalUsed += used
    budget.TokensLeft -= used
    
    // 推送预算提醒到 Agent（借鉴 Codex 的 TokenBudgetReminder）
    if budget.TokensLeft < 2000 {
        e.notifyTokenBudget(agentID, budget.TokensLeft)
    }
}
```

### 4.7 角色质量保障（借鉴 Codex Role 系统）

角色质量是核心痛点。借鉴 Codex 的"角色作为配置覆盖层"模式：

#### 4.7.1 预设角色库 + 动态生成

```go
// 预设角色：内置的经过验证的高质量角色模板
var BuiltinRoles = map[string]*AgentRoleConfig{
    "researcher": {
        Name:        "researcher",
        Description: "负责信息搜集和事实核查",
        Persona:     "你是一位严谨的研究员...",  // 经过调优的 prompt
        AllowedTools: []string{"search", "factcheck"},
        CanProduce:  []ArtifactType{ResearchBrief, SourcePack, FactClaims},
        // 借鉴 Codex：角色可以限制能力
        DisabledFeatures: []string{"write"},  // 研究员不能直接写文章
    },
    "writer": {
        Name:        "writer",
        Description: "负责文章撰写",
        Persona:     "你是一位资深撰稿人...",
        AllowedTools: []string{"write"},
        CanProduce:  []ArtifactType{Outline, Draft, RevisedDraft},
    },
    "reviewer": {
        Name:        "reviewer",
        Description: "负责审校和质量控制",
        Persona:     "你是一位严格的编辑...",
        AllowedTools: []string{"factcheck", "style_review"},
        CanProduce:  []ArtifactType{ReviewReport},
    },
}

// Planner 生成角色时，基于预设角色做定制化
func (p *Planner) generateAgentConfig(role string, customPersona string) *AgentConfig {
    base := BuiltinRoles[role]
    if base == nil {
        // LLM 生成的全新角色 — 需要 fallback 保护
        base = BuiltinRoles["writer"] // 默认 fallback
    }
    return &AgentConfig{
        ID:           uuid.New().String(),
        Name:         base.Name,
        Role:         base.Role,
        Persona:      customPersona,  // LLM 定制的 persona
        AllowedTools: base.AllowedTools, // 工具集从预设继承
        CanProduce:   base.CanProduce,
        CanConsume:   base.CanConsume,
        IsGenerated:  true,
        BaseRole:     role, // 记录基于哪个预设角色
    }
}
```

#### 4.7.2 角色配置覆盖（借鉴 Codex `apply_role_to_config`）

```go
// 角色配置作为"覆盖层"叠加到基础配置上
// 借鉴 Codex 的 AgentRoleOverrides 模式
type AgentRoleOverrides struct {
    DeveloperInstructions *string  `json:"developer_instructions,omitempty"`
    Model                 *string  `json:"model,omitempty"`           // 可以指定不同模型
    ReasoningEffort       *string  `json:"reasoning_effort,omitempty"` // 推理强度
    DisabledTools         []string `json:"disabled_tools,omitempty"`   // 禁用的工具
}

func applyRoleOverrides(base *AgentConfig, overrides *AgentRoleOverrides) *AgentConfig {
    result := *base // 浅拷贝
    if overrides.DeveloperInstructions != nil {
        result.Persona = *overrides.DeveloperInstructions
    }
    if overrides.Model != nil {
        result.Model = *overrides.Model
    }
    if len(overrides.DisabledTools) > 0 {
        // 从 AllowedTools 中移除禁用的工具
        disabled := make(map[string]bool)
        for _, t := range overrides.DisabledTools {
            disabled[t] = true
        }
        var filtered []string
        for _, t := range result.AllowedTools {
            if !disabled[t] {
                filtered = append(filtered, t)
            }
        }
        result.AllowedTools = filtered
    }
    return &result
}
```

---

## 五、实施计划

### Phase 0: 上下文管理重构（1 周，全局基础设施）✅ 已完成

> 借鉴 Codex 的 WorldState diff 模式，先解决 Token 暴涨的根因。
> **此阶段不限于编辑部模式 — 它是全平台 LLM 调用路径的基础设施升级。**
>
> **实现状态**：步骤 0.1-0.6 已完成，0.7-0.8 为后续优化项。

#### 已完成的改动

| 步骤 | 内容 | 产出文件 | 状态 |
|------|------|---------|------|
| 0.1 | `WorldStateSection` 接口 + `ContextFragment` 类型 | `internal/worldstate/world_state.go` | ✅ |
| 0.2 | 7 个 section 实现（Profile/Article/Date/Materials/Rules/TaskInstructions/Security） | `internal/worldstate/sections.go` | ✅ |
| 0.3 | `UpdateWorldState()` 增量推送 + `RenderDiff(previous)` diff 逻辑 | `internal/worldstate/world_state.go` | ✅ |
| 0.4 | `history_version` 基线管理 + `ResetBaselines()`（compaction 后重置） | `agent/harness.go` + `agent/compaction.go` | ✅ |
| 0.5 | `AutoCompactFallback`：Token 预算不足时自动触发压缩 | `internal/worldstate/world_state.go` | ✅ |
| 0.6 | `ChatStep` 接入 `WorldState`；`EvaluationService` 和 `StyleBuilderService` 持有 `WorldState` | `engine/steps/chat_step.go` + `services/evaluation.go` + `services/style_builder.go` | ✅ |
| 0.7 | `PromptBuilder` 改为动态 Token 预算分配 | `engine/steps/prompt_builder.go` + `engine/steps/steps.go` | ✅ |
| 0.8 | 验证：WorldState diff + 动态预算 + AutoCompactFallback 单元测试全通过 | `worldstate/world_state_test.go` + `steps/prompt_builder_test.go` | ✅ |

#### 受影响的组件清单（实际已改动的文件）

| 组件 | 改动内容 | 文件 |
|------|---------|------|
| `Harness` | 结构体新增 `worldState` / `tokenBudget` / `autoCompact` / `historyVersion` 字段；`buildSystemPrompt` 重构为 WorldState diff 模式 | `internal/agent/harness.go` |
| `compaction.go` | `maybeCompact` 增加 Token 预算检查（AutoCompactFallback）；压缩后重置 WorldState 基线 + `historyVersion++` | `internal/agent/compaction.go` |
| `ChatStep` | 结构体新增 `worldState` 字段；`Execute` 中 system prompt 改用 WorldState diff | `internal/engine/steps/chat_step.go` |
| `EvaluationService` | 结构体新增 `worldState` 字段（评测 prompt 的 section 基线管理） | `internal/services/evaluation.go` |
| `StyleBuilderService` | 结构体新增 `worldState` 字段（StyleBuilder prompt 的 section 基线管理） | `internal/services/style_builder.go` |
| `EventEmitter` 接口 | `Compaction()` 方法签名扩展：新增 `historyVersion` + `triggerReason` 参数 | `internal/engine/step.go` |
| `WSEmitter` + `LoggingEmitter` | 更新 `Compaction()` 实现，传入新字段 | `internal/server/emitter.go` + `logging_emitter.go` |
| `CompactionPayload` | 新增 `HistoryVersion` + `TriggerReason` 字段 | `internal/websocket/protocol.go` |
| `noopEmitter` + `wabenchCaptureEmitter` | 更新 mock 实现 | `internal/editorial/agent_executors.go` + `services/wabench_v2_adapter.go` |
| 前端 `CompactionPart` | 新增 `historyVersion` + `triggerReason` 字段 | `frontend/src/stores/agent-store.ts` |
| 前端 `CompactionBanner` | 增强显示：触发原因标签（消息数阈值 / Token 预算不足） | `frontend/src/components/assistant-ui/assistant-message.tsx` |

#### 受益场景

- **Harness chat 模式**：当前每轮注入完整 Profile，Token 1109 → 目标 < 800（-28%）
- **Harness writing 模式**：当前每轮全量拼装 Profile + 文章全文 → 只推变化部分
- **Harness polish 模式**：多轮润色场景下 system prompt 重复发送 → 增量推送
- **Pipeline ChatStep**：独立构建 system prompt → 复用 WorldState section 框架
- **评测模式**：`EvaluationService.generateArticle` 和 `judgeArticle` 复用 WorldState
- **Admin StyleBuilder**：多轮对话构建 style profile 时复用 WorldState
- **未来编辑部模式**：每个 SubAgent 只需要 diff，不需要全量 system prompt
- **未来 DAG 执行器**：节点间上下文传递基于 diff，大幅降低 N 个 Agent 的 Token 成本

### Phase 1: 后端 Agent 动态化（1 周）🅱️ Beta ✅ 已完成

| 步骤 | 内容 | 产出 | 状态 |
|------|------|------|------|
| 1.1 | `AgentConfig` 结构体 + `AgentRoleOverrides` + `ApplyRoleOverrides` | `agent_config.go` 新文件 | ✅ |
| 1.2 | `DynamicAgentRegistry`（动态注册 + 清除 + 并发安全） | `agent_config.go` | ✅ |
| 1.3 | Orchestrator executors map 兼容（保留 AgentRole 接口，新增 string key 适配） | 兼容设计 | ✅ |
| 1.4 | `ApplyGeneratedAgents(taskID, configs)` 方法 | `agent_config.go` | ✅ |
| 1.5 | 现有三个 Executor 适配（保留原接口，DAG 执行器按 BaseRole 路由） | 兼容设计 | ✅ |
| 1.6 | 预设角色库 `BuiltinRoles` + `GenerateAgentConfig` + 角色覆盖层 | `roles.go` 新文件 | ✅ |
| 1.7 | 单元测试：动态注册 + 清除 + 并发安全 + 角色覆盖 + 生成配置 | `registry_test.go` | ✅ (7 tests) |

### Phase 2: DAG 执行器 + 上下文传递（1.5 周）🅱️ Beta ✅ 已完成

| 步骤 | 内容 | 产出 | 状态 |
|------|------|------|------|
| 2.1 | `NodeSpec` / `WorkflowSpec` / `Edge` / `ContextForkMode` / `NodeStatus` 类型定义 | `dag_types.go` | ✅ |
| 2.2 | `ContextForkMode`（Full/LastN/Summary）实现 + `ForkContext` | `context_fork.go` | ✅ |
| 2.3 | `DAGExecutor` 实现：拓扑排序 + 并行执行 + Artifact 流转 | `dag_executor.go` | ✅ |
| 2.4 | 节点间上下文传递：根据 `ContextForkMode` 继承 | `dag_executor.go` | ✅ |
| 2.5 | `AgentTokenBudget` 追踪 + Context Window ID | `token_budget.go` | ✅ |
| 2.6 | 节点完成事件推送到 WebSocket | `dag_executor.go` | ✅ |
| 2.7 | 单元测试：DAG 校验 + 拓扑排序 + 上下文 fork + Token 预算 | `dag_test.go` (16 tests) | ✅ |
| 2.8 | 环检测 + 合法性校验 | `dag_validator.go` | ✅ |

### Phase 3: Planner Agent（3 天）🅱️ Beta ✅ 已完成

| 步骤 | 内容 | 产出 | 状态 |
|------|------|------|------|
| 3.1 | Planner prompt 模板（含预设角色列表 + DAG 约束） | `planner_prompt.go` | ✅ |
| 3.2 | `Planner.Plan()` 实现 + 基于 `BuiltinRoles` 做角色定制 | `planner.go` | ✅ |
| 3.3 | 输出校验（DAG 无环、工具集合法、artifact 类型匹配） | `planner.go` + `dag_validator.go` | ✅ |
| 3.4 | Fallback：LLM 输出不合法时回退到预设线性三 Agent | `planner.go` | ✅ |
| 3.5 | 集成测试 | 待联调阶段 | ⏳ |

### Phase 4: WebSocket 协议扩展 + Agent 间通信（2 天）🅱️ Beta ✅ 已完成

| 步骤 | 内容 | 产出 | 状态 |
|------|------|------|------|
| 4.1 | 新增 `workflow.*` / `node.*` 消息类型常量 | `protocol.go` | ✅ |
| 4.2 | 新增 workflow/node Payload 类型（10+ 个 struct） | `protocol.go` | ✅ |
| 4.3 | DAGExecutor 事件发射器对接 WebSocket | `dag_executor.go` | ✅ |
| 4.4 | 端到端测试 | 待联调阶段 | ⏳ |

### Phase 5: 前端节点图（1 周）🅱️ Beta ✅ 已完成

| 步骤 | 内容 | 产出 | 状态 |
|------|------|------|------|
| 5.1 | 安装 `@xyflow/react`（React Flow） | `package.json` | ✅ |
| 5.2 | `WorkflowCanvas` 组件：节点画布 + 自定义节点 + 连线 | `components/workflow/canvas.tsx` | ✅ |
| 5.3 | `AgentNodeCard` 自定义节点：显示角色名/状态/输出/Token | `components/workflow/agent-node.tsx` | ✅ |
| 5.4 | `workflow-store.ts`：管理 DAG 状态 + WebSocket 事件 | `stores/workflow-store.ts` | ✅ |
| 5.5 | `WorkflowInput` 输入面板：用户意图输入 + 规划/运行按钮 | `components/workflow/workflow-input.tsx` | ✅ |
| 5.6 | `WorkflowPage` 完整页面：整合画布 + 输入面板 + WS 事件处理 | `components/workflow/workflow-page.tsx` | ✅ |
| 5.7 | 自动布局算法：按拓扑层排列节点 | `stores/workflow-store.ts` | ✅ |
| 5.8 | TypeScript 类型检查通过 | tsc --noEmit | ✅ |
| 5.9 | 与现有 Composer 共存：检测 `editorial_mode` 切换 | 待联调阶段 | ⏳ |

### Phase 6: 联调与优化（1 周）🅱️ Beta ✅ 已完成

| 步骤 | 内容 | 状态 |
|------|------|------|
| 6.1 | 代码审查：RoleAgentRunner / AgentExecutors / DAGExecutor / Planner 完整集成 | ✅ |
| 6.2 | 端到端通路验证：Planner → DAG → Agent 执行 → 前端展示 | ✅ |
| 6.3 | 错误处理与 fallback：LLM 生成角色不合法时回退线性模式 | ✅ |
| 6.4 | Token 成本优化验证：WorldState diff + persona 压缩 + context fork | ✅ |
| 6.5 | Bug 修复：node.stream.reset 前端清空逻辑 + Task 缺少 StyleSlug/Tags 传递 | ✅ |

#### Phase 6 关键改动

| 文件 | 改动内容 |
|------|----------|
| `role_agent_runner.go` | 新文件：RoleAgentRunner 实现 ChatWithTools agentic loop，替代固定 pipeline |
| `agent_executors.go` | 重构 Research/Writing/ReviewAgentExecutor，使用 RoleAgentRunner + emitterHolder |
| `dag_executor.go` | 新增 EmitterHolder 接口 + NodeEmitter 注入 + finalized atomic.Bool 防重复 |
| `node_emitter.go` | 新文件：桥接 engine.EventEmitter 到 editorial.EventEmitter |
| `planner.go` | PlanInput 结构体 + PlanResult 元数据传递（StyleSlug/Tags/UserInput） |
| `workflow_handler.go` | Task 构建增加 StyleSlug/Tags 字段；改进 taskTitle 推断逻辑 |
| `editorial_adapter.go` | editorialWSEmitter 路由 node.* 事件到 WebSocket |
| `agent-store.ts` | 新增 node.* 事件处理 + resetNodeStream 修复 |
| `workflow-store.ts` | 新增 resetNodeStream 方法 |
| `types.ts` | 新增 node.* 消息类型 |

---

## 六、风险评估

### 6.1 高风险

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| LLM 生成的 Agent 角色质量不稳定 | 高 | 高 | 1) 强约束 prompt + JSON schema 校验 2) 基于 `BuiltinRoles` 做定制化而非从零生成 3) 用户可编辑修正 4) Fallback 到预设角色（借鉴 Codex Role 系统） |
| Planner 生成的 DAG 不合法（有环/缺依赖） | 中 | 高 | 服务端校验 + 自动修复 + fallback 到线性 |
| Token 成本暴涨（N 个 Agent 各自有 system prompt） | 高 | 中 | 1) WorldState diff 增量推送（借鉴 Codex）— 只发变化部分 2) `ContextForkMode` 控制上下文继承范围 3) `AgentTokenBudget` 全局预算追踪 4) `AutoCompactFallback` 自动压缩降级 |
| 前端节点图复杂度高，用户体验差 | 中 | 中 | 先做简化版（只展示不支持编辑），再迭代编辑功能 |
| 多 Agent 上下文传递丢失关键信息 | 中 | 高 | 1) `ContextForkMode` 三种策略覆盖不同场景 2) Artifact 作为结构化传递物保证关键信息不丢 3) 借鉴 Codex 的 `normalize_history` 确保消息完整性 |

### 6.2 中风险

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| 并行 Agent 写入冲突（同一 Artifact） | 低 | 高 | Artifact 版本控制 + Lease 互斥已存在 |
| WebSocket 事件乱序 | 中 | 中 | 节点 ID + sequence number 做有序化 |
| 前端 React Flow 性能（50+ 节点） | 低 | 低 | 限制节点数 ≤ 20，大图自动布局 |

### 6.3 低风险

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| 现有线性模式被破坏 | 低 | 高 | `editorial_mode` 字段隔离，默认 `linear` |
| 数据库迁移问题 | 低 | 中 | AgentConfig 存 JSON 列，不修改现有表结构 |

---

## 七、成本估算

### 7.1 开发人力

| 阶段 | 工时 | 人员 |
|------|------|------|
| Phase 0: 上下文管理重构 | 5 人日 | 后端 1 |
| Phase 1: Agent 动态化 | 5 人日 | 后端 1 |
| Phase 2: DAG 执行器 + 上下文传递 | 7.5 人日 | 后端 1 |
| Phase 3: Planner | 3 人日 | 后端 1 |
| Phase 4: WebSocket 扩展 | 2 人日 | 后端 1 |
| Phase 5: 前端节点图 | 5 人日 | 前端 1 |
| Phase 6: 联调优化 | 5 人日 | 前后端各 1 |
| **合计** | **32.5 人日** | **~6.5 周（1 人）/ ~3.5 周（2 人）** |

### 7.2 运行时 Token 成本

| 环节 | Token 估算（v1） | Token 估算（v2 with Codex 模式） | 说明 |
|------|-----------|-----------|------|
| Planner（意图→角色+DAG） | ~2,000 | ~2,000 | 不变 |
| 每个 SubAgent system prompt | ~800-1,200 | ~400-600 | WorldState diff 只推送变化部分 |
| 4 Agent 集群（宏观分析） | ~15,000-25,000 | ~10,000-16,000 | ContextForkMode 减少上下文继承 |
| 对比：现有线性 3 Agent | ~10,000-18,000 | ~8,000-12,000 | 线性模式也受益于 diff |
| **增量** | **+30-40%** | **+15-25%** | Codex 模式大幅压缩成本 |

### 7.3 基础设施

- 无新增依赖（React Flow 是前端 npm 包，后端纯 Go 标准库）
- 无新增数据库表（WorkflowSpec 存为 Task 的 JSON 列）
- 无新增中间件

---

## 八、验收标准

### 8.1 Phase 1-3（后端）

- [ ] `AgentConfig` 替代 `AgentRole`，现有三个 Executor 兼容
- [ ] `ApplyGeneratedAgents` 能动态注册和清除 Agent
- [ ] `DAGExecutor` 能执行 2 并行 → 1 串行 → 1 审校的 DAG
- [ ] `Planner` 对三种意图（宏观/言情/科技）能生成合法的 Agent + DAG
- [ ] 环检测正确拒绝非法 DAG
- [ ] 单元测试覆盖率 > 80%

### 8.2 Phase 4-5（前端+联调）

- [ ] 用户输入"一季度宏观经济分析"→ Planner 返回 4-5 个角色 + DAG
- [ ] 节点图正确渲染 DAG，用户可编辑（增删节点/连线）
- [ ] 点击"运行"后，节点状态实时更新（pending → running → completed）
- [ ] 并行节点同时执行，串行节点等待依赖
- [ ] 最终产出文章展示在对话面板
- [ ] 现有线性模式（Composer）不受影响

### 8.3 Phase 6（优化）

- [ ] Token 成本增幅 ≤ 40%
- [ ] Planner 生成角色不合法时自动 fallback
- [ ] 4 并行节点 WebSocket 事件延迟 < 500ms
- [ ] 用户文档完成

---

## 九、里程碑

| 里程碑 | 日期 | 交付物 |
|--------|------|--------|
| M0: 上下文管理重构 | 第 1 周末 | Phase 0 完成，Token 暴涨问题解决，chat Token < 800 |
| M1: 后端动态化 + DAG 执行器 | 第 3 周末 | Phase 1-2 完成，可 API 调用，含上下文传递 |
| M2: Planner + WebSocket | 第 4 周末 | Phase 3-4 完成，可端到端 API 调用 |
| M3: 前端节点图 | 第 5 周末 | Phase 5 完成，可视化 + 可交互 |
| M4: 联调 + 验收 | 第 6.5 周末 | Phase 6 完成，三种意图验收通过 |

---

## 十、附录

### 10.1 现有代码资产评估

| 文件 | 行数 | 改动范围 | 说明 |
|------|------|----------|------|
| `editorial/types.go` | 646 | 大改 | AgentRole → AgentConfig, 新增 DAG 类型 |
| `editorial/orchestrator.go` | 1013 | 中改 | executors map key 改为 string |
| `editorial/agent_executors.go` | 700 | 小改 | 适配新接口 |
| `editorial/memory_store.go` | - | 不变 | 组织记忆模块复用 |
| `websocket/protocol.go` | 223 | 小改 | 新增消息类型 |
| `server/server.go` | 2593 | 中改 | 新增 workflow handler |
| `frontend/stores/agent-store.ts` | 1091 | 不变 | 保留线性模式 |
| `frontend/components/composer/` | - | 不变 | 保留线性模式 |

### 10.2 Planner Prompt 草案

```
你是一个编辑部策划 Agent。根据用户的写作意图，设计一个 SubAgent 集群来协作完成任务。

可用工具集：
- search: 网络搜索 + 知识库检索
- write: 文章撰写（提纲 + 正文）
- factcheck: 事实核查
- style_review: 风格审查

可用交付物类型：
- research_brief: 研究简报
- source_pack: 信源包
- fact_claims: 事实声明表
- outline: 提纲
- draft: 初稿
- revised_draft: 修改稿
- review_report: 审查报告

设计要求：
1. 角色 2-6 个，每个角色有明确的职责边界
2. DAG 必须无环，至少有一个起始节点（无依赖）和一个终止节点
3. 起始节点产出 research_brief，终止节点产出 draft 或 revised_draft
4. 如有审校需求，最后增加 reviewer 节点

输出 JSON：
{
  "agents": [{"id":"a1","name":"...","role":"...","persona":"...","allowed_tools":[...]}],
  "workflow": {"nodes":[{"id":"n1","agent_id":"a1","dependencies":[],"output_artifact":"research_brief"}]},
  "rationale": "设计理由..."
}
```

### 10.3 兼容性矩阵

| 现有功能 | 改动后状态 | 影响 |
|----------|-----------|------|
| 线性编辑部模式 | ✅ 保留，`editorial_mode=linear` | 无 |
| Harness 模式 | ✅ 不受影响 | 无 |
| WebSocket 现有事件 | ✅ 保留，新增事件类型 | 无 |
| 前端 Composer | ✅ 保留，新增节点图页面 | 无 |
| 数据库表结构 | ✅ 保留，新增 JSON 列 | 迁移脚本 |
| Agent Executor 接口 | ⚠️ 签名微调（Role → ID） | 适配三个 Executor |

---

> **下一步**：评审通过后，从 Phase 1 开始实施。建议先做一个最小可运行原型（Planner 输出 2 个 Agent 的 2 节点 DAG），验证端到端通路后再逐步扩展。
