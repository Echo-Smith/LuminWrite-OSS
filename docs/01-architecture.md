# 架构蓝图

## 1. 整体架构

```
┌─────────────────────────────────────────────────────────────┐
│                        Frontend (React 19)                    │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌────────────────┐ │
│  │ 写作工作台 │ │ 选题中心  │ │ 风格选择  │ │ Admin Dashboard │ │
│  └─────┬────┘ └─────┬────┘ └─────┬────┘ └───────┬────────┘ │
│        │            │            │              │           │
│        └────────────┴────────────┘              │           │
│                     │                           │           │
│              WebSocket + REST                   │           │
└─────────────────────┼───────────────────────────┼───────────┘
                      │                           │
┌─────────────────────┼───────────────────────────┼───────────┐
│              Go Backend (chi router)             │           │
│                     │                           │           │
│  ┌──────────────────┴───────────────────────────┴────────┐  │
│  │                   Agent Engine                         │  │
│  │  ┌────────┐  ┌─────────┐  ┌────────┐  ┌────────────┐ │  │
│  │  │ Intent │→ │ Search  │→ │ Write  │→ │PostReview  │ │  │
│  │  │  Step  │  │  Step   │  │  Step  │  │   Step     │ │  │
│  │  └────────┘  └─────────┘  └────────┘  └────────────┘ │  │
│  └───────────────────────────────────────────────────────┘  │
│                     │                                       │
│  ┌──────────┬──────────┬──────────┬──────────┬──────────┐  │
│  │ DeepSeek │  知乎     │ IMA 知识库│ Tavily   │ 通义Embed │  │
│  │  Client  │  Client  │  Client  │  Client  │  Client   │  │
│  └──────────┴──────────┴──────────┴──────────┴──────────┘  │
│                     │                                       │
│  ┌──────────────────┴───────────────────────────────────┐  │
│  │              灰度路由 + Style Profile                  │  │
│  │  ① Profile 标记高优先 → ② UID Hash 默认分流            │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────┼───────────────────────────────────────┐
│              PostgreSQL 17 + pgvector                        │
│  ┌─────────┬──────────┬──────────┬──────────┬────────────┐ │
│  │ styles  │ topics   │ feedback │ eval_set │ reputation  │ │
│  │profiles │ center   │ segments │          │             │ │
│  └─────────┴──────────┴──────────┴──────────┴────────────┘ │
│  ┌──────────────────────────────────────────────────────┐  │
│  │              pgvector (RAG + 语义去重)                 │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

## 2. Agent Engine 设计

### 2.1 核心概念

Agent Engine 是一个状态机式编排器，管理写作流程的每一步：

```go
// Step 接口 — 每个 Step 是写作流程中的一个可插拔阶段
type Step interface {
    Name() string
    Execute(ctx context.Context, state *ExecutionContext) error
    CanPause() bool
    OnPause(state *ExecutionContext)
    OnResume(state *ExecutionContext)
}

// ExecutionContext — 贯穿整个写作流程的上下文
type ExecutionContext struct {
    TraceID       string
    UserID        string
    SessionID     string
    StyleProfile  *StyleProfile
    UserInput     string
    WritingTask   *WritingTask
    SearchPlan    []SearchPlanEntry
    SearchResults []SearchResult
    Article       string
    ReviewResult  *ReviewResult
    Status        ExecutionStatus  // running | paused | completed | failed
    CurrentStep   string
    StepHistory   []StepRecord
    StartedAt     time.Time
    PausedAt      *time.Time
    Metadata      map[string]any
}

// AgentEngine — 编排器
type AgentEngine struct {
    steps       []Step
    stateStore  StateStore
    eventBus    EventBus  // WebSocket 事件推送
    pauseCh     chan string
    resumeCh    chan string
}
```

### 2.2 内置 Steps

| Step | 功能 | 可暂停 | 超时 | 关键 | 来源映射 (V1) |
|---|---|---|---|---|---|
| `ShortTermMemoryStep` | 短期记忆加载 + 工作记忆继承 + 随机态初始化 | ✗ | 15s | ✗ | V2 新增 |
| `MemoryGateStep` | 长期记忆检索 + 实体图网络检索 | ✗ | 30s | ✗ | V2 新增 |
| `IntentStep` | 意图分类 + 语音归一化 + 三级路由（规则→Embedding→LLM） | ✗ | 30s | ✓ | `intentClassifierService.js` |
| `QueryPlanStep` | 检索 Query 规划 + 信源策略 | ✗ | 30s | ✗ | `queryPlannerService.js` |
| `SearchStep` | 并发多源检索（知乎/IMA/Tavily/腾讯新闻） | ✓ | 45s | ✗ | `webSearchService.js` + `knowledgeService.js` |
| `RelevanceStep` | 素材相关性过滤 + 语义去重 + 随机态采样 | ✗ | 30s | ✗ | `sourceRelevanceService.js` + `dedupeService.js` |
| `CompressStep` | 搜索素材压缩为研究简报 | ✗ | 60s | ✗ | V2 新增 |
| `OutlineStep` | 结构规划（引导模式） | ✓ | 60s | ✓ | V2 新增 |
| `WriteStep` | 按风格 Profile 生成文章 | ✓ | 180s | ✓ | `builders.js` + `aiClient.js` |
| `PostReviewStep` | 写后自检 + 质量评分 + 敏感检查 | ✗ | 60s | ✗ | `postWriteReviewService.js` |
| `AutoFixStep` | 自动修正（低严重度问题，最多 2 次） | ✗ | 90s | ✗ | `postWriteReviewService.js` (auto-fix) |
| `ChatStep` | 对话模式 LLM 流式回复 | ✓ | 60s | ✓ | V2 新增 |
| `WorkingMemoryStep` | 增量摘要 + 步骤状态压缩 | ✗ | 30s | ✗ | V2 新增 |
| `MemoryExtractStep` | 长期记忆提取（实体/关系抽取） | ✗ | 60s | ✗ | V2 新增 |
| `ShortTermStoreStep` | 消息存储 + embedding 生成 + 工作摘要持久化 | ✗ | 15s | ✗ | V2 新增 |

> **关键步骤（Critical=true）**：失败时终止整个管道。  
> **非关键步骤（Critical=false）**：失败时触发优雅降级（跳过并继续），前端以琥珀色标识。  
> 详见 [§5. Agent 退出机制与优雅降级](#5-agent-退出机制与优雅降级) 和 [记忆系统架构](./11-memory-system.md)。

### 2.3 执行流程

```
用户输入
  │
  ▼
ShortTermMemoryStep ──→ 加载对话历史 + 继承工作记忆 + 初始化随机态
  │
  ▼
MemoryGateStep ──→ 长期记忆检索 + 实体图网络检索 (2-hop)
  │
  ▼
IntentStep ──→ 判定为 writing / polish / chat / shorten / expand
  │
  ├─ chat → ChatStep (注入历史+实体+工作记忆 → LLM 流式回复)
  │
  ▼ (writing)
QueryPlanStep ──→ 生成 searchPlan + 信源策略
  │
  ▼
SearchStep ──→ 并发 goroutine:
  │              ├── 知乎站内搜索
  │              ├── 知乎全网搜索
  │              ├── IMA 知识库检索
  │              └── Tavily / 腾讯新闻 (可选)
  │
  ▼
RelevanceStep ──→ 素材打分 (strong/medium/weak/conflict)
  │                  + pgvector 语义去重
  │                  + 随机态采样 (弱相关结果随机丢弃)
  │
  ▼
CompressStep ──→ 搜索素材压缩为研究简报 (减少 ~60% prompt token)
  │
  ▼
OutlineStep (引导模式) ──→ 生成提纲 → WebSocket 推送 → 等待用户确认/修改
  │                                          │ (confirm timeout 保护)
  │                                    用户确认/修改
  │                                          │
  ▼──────────────────────────────────────────┘
WriteStep ──→ 加载 StyleProfile → 构建 Prompt → 调用 DeepSeek (流式)
  │              ├── 注入对话历史 + 实体网络 + 工作摘要
  │              ├── 随机态温度调整
  │              ├── 流式输出通过 WebSocket 实时推送
  │              └── 用户可暂停（保存当前上下文）
  │
  ▼
PostReviewStep ──→ 质量评分 (factuality/relevance/structure/style/risk)
  │                   + 敏感检查 (title_sensitivity / content_safety)
  │                   + 篇幅校验
  │
  ├─ 无严重问题 → 跳过 AutoFix
  │
  ▼ (有可修正问题)
AutoFixStep ──→ 基于评分结果自动修正 (最多 MaxFixAttempts=2 次)
  │
  ▼
WorkingMemoryStep ──→ 增量摘要 + LLM 压缩
  │
  ▼
MemoryExtractStep ──→ 长期记忆提取 (LLM 实体/关系抽取 → 存入实体网络)
  │
  ▼
ShortTermStoreStep ──→ 消息存储 + 异步 embedding + 工作摘要持久化
  │
  ▼
输出成稿 + Trace 记录 + 反馈入口
```

### 2.4 WebSocket 控制协议

```
客户端 → 服务端:
  { "type": "agent.start",   "payload": { "message": "...", "style": "yinyue", "mode": "guided" } }
  { "type": "agent.pause",   "payload": { "traceId": "..." } }
  { "type": "agent.resume",  "payload": { "traceId": "..." } }
  { "type": "agent.cancel",  "payload": { "traceId": "..." } }
  { "type": "agent.confirm", "payload": { "traceId": "...", "step": "outline", "data": { ... } } }

服务端 → 客户端:
  { "type": "agent.step.start",   "payload": { "traceId": "...", "step": "search" } }
  { "type": "agent.step.complete","payload": { "traceId": "...", "step": "search", "result": {...} } }
  { "type": "agent.stream",       "payload": { "traceId": "...", "delta": "文章片段..." } }
  { "type": "agent.paused",       "payload": { "traceId": "...", "step": "write", "state": "..." } }
  { "type": "agent.await_input",  "payload": { "traceId": "...", "step": "outline", "data": {...} } }
  { "type": "agent.completed",    "payload": { "traceId": "...", "article": "...", "review": {...} } }
  { "type": "agent.error",        "payload": { "traceId": "...", "code": "...", "message": "..." } }
```

### 2.5 Tools（外部服务客户端）

```go
type Tool interface {
    Name() string
    Execute(ctx context.Context, input any) (any, error)
}
```

| Tool | 用途 | 对应 V1 |
|---|---|---|
| `DeepSeekClient` | LLM 调用（flash/pro + thinking 模式 + tool calls + JSON mode） | `aiClient.js` |
| `ZhihuClient` | 知乎站内/全网搜索 | `zhihuClient.js` |
| `IMAClient` | IMA 知识库检索 | `imaClient.js` |
| `TavilyClient` | 通用联网搜索 | `webSearchService.js` (Tavily 部分) |
| `TencentNewsClient` | 腾讯新闻搜索 | `tencentNewsService.js` |
| `WeiboClient` | 微博热搜/搜索 | `weiboOpenClawService.js` |
| `DashscopeEmbedding` | 通义 text-embedding-v3 | V2 新增 |
| `JiaozhenClient` | 较真事实核查 | `jiaozhenService.js` |
| `SensitiveCheck` | 敏感词检测 | `sensitiveCheckService.js` |

### 2.x DeepSeek V4 分级思考策略 (P0)

基于 DeepSeek V4 统一模型（flash / pro）的思考模式能力，Agent Engine 按步骤选择不同的思考策略：

| 步骤 | 思考模式 | reasoning_effort | 说明 |
|---|---|---|---|
| IntentStep | `disabled` | — | 快速意图分类，无需推理 |
| OutlineStep | `enabled` | `high` | 提纲需要结构化推理 |
| WriteStep (writing) | `enabled` | `high` | 深度写作推理 |
| WriteStep (polish/shorten/expand) | `disabled` | — | 机械文本操作，快速响应 |
| PostReviewStep | `enabled` | `high` | 多维度质量评审需要推理 |
| AutoFixStep | `disabled` | — | 机械修正，无需推理 |
| ChatStep | `disabled` | — | 对话快速响应 |

### 2.x 思维链可视化 (P1)

- `reasoning_content` 通过 WebSocket `agent.reasoning` 事件实时推送到前端
- 前端 `agent-store.ts` 将推理内容存入 `ReasoningPart`，可折叠展示
- 写作模式下用户可看到模型的思考过程，增强透明度

### 2.x strict JSON 模式 (P1)

- `IntentStep` 和 `PostReviewStep` 使用 `response_format: { type: "json_object" }`
- 确保模型输出严格 JSON，减少解析失败率
- 注意：JSON mode 与 tools 不兼容，不可同时使用

### 2.x Agent Loop + Tool Calls (P2)

- `WriteStep` 在写作模式下支持自适应 Agent Loop
- 当搜索结果 ≥ 3 条时，模型可通过 tool calls 自主请求更多上下文
- 定义了两个工具：`search_web`（搜索更多）和 `get_topic_context`（获取已有结果详情）
- Agent Loop 最多 5 轮迭代，超出后直接流式输出最终结果
- `ChatWithTools` 方法处理完整的 tool call → execute → re-request 循环

### 2.x 1M 上下文 + 全文素材 (P2)

- V4 模型支持 1M input tokens + 384K output tokens
- 搜索结果数量从 9 提升到 20，充分利用大上下文窗口
- `max_tokens` 默认值从 8192 提升到 16384（flash）/ 32768（pro）
- 用户素材全文注入，无需截断

## 3. 数据流

### 3.1 写作请求流

```
用户输入 ──→ WebSocket ──→ Agent Engine
                              │
                    ┌─────────┼──────────┐
                    ▼         ▼          ▼
              灰度路由    Profile加载   意图分类
                    │         │          │
                    └─────────┼──────────┘
                              ▼
                     Step 编排执行
                              │
                    ┌─────────┼──────────┐
                    ▼         ▼          ▼
                 检索(并发)  相关性过滤   写作生成
                    │                       │
                    ▼                       ▼
               pgvector存储           流式推送(WS)
                                              │
                                              ▼
                                        写后自检 + 反馈入口
```

### 3.2 IMA 同步流（定时任务 17:00）

```
Cron 17:00 ──→ IMA Client ──→ 拉取增量 ──→ pgvector upsert
                                     │
                                     ▼
                              更新 styles/knowledge 缓存
```

### 3.3 评测触发流

```
Profile 变更发布 ──→ 触发评测任务
                         │
                    ┌────┴────┐
                    ▼         ▼
              本地评分     导出评测集
              (规则+LLM)   (第三方平台)
                    │         │
                    └────┬────┘
                         ▼
                    评测报告
                    (Admin 可视化)
```

## 4. 并发设计

### 4.1 多源检索并发

```go
// SearchStep 中使用 errgroup 并发检索
g, ctx := errgroup.WithContext(ctx)
var zhihuResults, imaResults, tavilyResults []SearchResult

g.Go(func() error { zhihuResults = zhihuClient.Search(ctx, queries); return nil })
g.Go(func() error { imaResults = imaClient.Search(ctx, queries); return nil })
g.Go(func() error { tavilyResults = tavilyClient.Search(ctx, queries); return nil })

if err := g.Wait(); err != nil {
    // 部分失败不影响整体，降级处理
}
```

### 4.2 流式输出

- `WriteStep` 调用 DeepSeek 流式 API
- 每个 chunk 通过 WebSocket 实时推送给客户端
- 用户暂停时，保存当前上下文到 `ExecutionContext`，可恢复

### 4.3 定时任务

```go
// 使用 robfig/cron v3
c := cron.New()

// IMA 知识库同步 — 每天 17:00
c.AddFunc("0 17 * * *", func() {
    imaSyncService.Sync(ctx)
})

// 自定义 cron（Admin 后台配置）
for _, job := range cronJobs {
    c.AddFunc(job.Schedule, job.Handler)
}

c.Start()
```

## 5. Agent 退出机制与优雅降级

Agent 引擎实现了多层退出防护，确保在任何异常情况下都不会永久挂起或造成成本失控。

### 5.1 退出检查层次

每个 Step 执行前，引擎按优先级依次检查 6 层退出条件：

```
┌─────────────────────────────────────────────────────┐
│  Layer 1: 用户取消 (cancelCh)                        │
│  → 发送 agent.cancelled，终止流程                     │
├─────────────────────────────────────────────────────┤
│  Layer 2: 全局超时 (context.WithTimeout)              │
│  → 发送 error{code:"timeout"}，标记 failed            │
├─────────────────────────────────────────────────────┤
│  Layer 3: Token 预算 (CheckBudget)                    │
│  → 发送 error{code:"budget_exceeded"}，标记 failed    │
├─────────────────────────────────────────────────────┤
│  Layer 4: WebSocket 断连 (disconnectCh)               │
│  → 发送 paused{reason:"disconnect"}，标记 paused      │
├─────────────────────────────────────────────────────┤
│  Layer 5: 断路器 (ConsecutiveLLMFails ≥ MaxLLMFails)   │
│  → 发送 error{code:"circuit_breaker"}，标记 failed    │
├─────────────────────────────────────────────────────┤
│  Layer 6: 单步超时 (Timeouter → context.WithTimeout)   │
│  → 由步骤的 Critical() 决定降级或终止                  │
└─────────────────────────────────────────────────────┘
```

### 5.2 配置项

| 配置 | 默认值 | 说明 |
|------|--------|------|
| `AGENT_TIMEOUT` | `5m` | 全局超时（从 agent.start 到 agent.completed） |
| `AGENT_MAX_TOKENS` | `300000` | Token 预算上限（30 万），单对话上下文不限 |
| `AGENT_MAX_FIX_ATTEMPTS` | `2` | 自动修正最大尝试次数 |
| `AGENT_MAX_LLM_FAILS` | `3` | 断路器阈值：连续 LLM 失败次数 |
| `AGENT_CONFIRM_TIMEOUT` | `5m` | 用户确认提纲的超时时间 |
| `AGENT_MAX_CONCURRENT` | `10` | 全局并发写作上限 |
| `AGENT_MAX_CONCURRENT_PER_USER` | `1` | 每用户并发写作上限 |

### 5.3 Token 预算

```go
// ExecutionContext.CheckBudget() — 在每个 Step 前检查
func (ctx *ExecutionContext) CheckBudget() bool {
    return ctx.MaxTokens > 0 && ctx.TotalTokens >= ctx.MaxTokens
}
```

- **预算默认 30 万 Token**，覆盖整个写作流程（意图→检索→写作→自检→修正）
- **单个对话窗口的上下文不设上限**——长对话不会截断历史
- 预算耗尽时发送 `error{code:"budget_exceeded", message:"Token 预算已用尽（已用 N / 上限 M）"}`

### 5.4 断路器

```go
// RecordLLMFailure — 每次关键步骤 LLM 调用失败时递增
func (ctx *ExecutionContext) RecordLLMFailure() bool {
    ctx.ConsecutiveLLMFails++
    return ctx.MaxLLMFails > 0 && ctx.ConsecutiveLLMFails >= ctx.MaxLLMFails
}

// RecordLLMSuccess — 步骤成功时重置计数器
func (ctx *ExecutionContext) RecordLLMSuccess() {
    ctx.ConsecutiveLLMFails = 0
}
```

- 连续 3 次 LLM 失败（可配置）后触发断路器，立即终止流程
- **成功一次即重置**——间歇性错误不会累积
- `isLLMError()` 通过错误消息关键词识别 LLM 错误（`llm`、`api request failed`、`rate limit`、`deepseek`、`chat completions`）

### 5.5 优雅降级

步骤可实现 `CriticalStep` 接口声明自身是否关键：

```go
type CriticalStep interface {
    Critical() bool
}
```

| 步骤 | Critical | 降级行为 |
|------|----------|----------|
| IntentStep | ✅ true | 失败 → 终止流程 |
| QueryPlanStep | ✅ true | 失败 → 终止流程 |
| SearchStep | ❌ false | 失败 → 跳过，用已有素材继续 |
| RelevanceStep | ❌ false | 失败 → 跳过，不过滤 |
| OutlineStep | ✅ true | 失败 → 终止流程 |
| WriteStep | ✅ true | 失败 → 终止流程 |
| PostReviewStep | ❌ false | 失败 → 跳过，不评分 |
| AutoFixStep | ❌ false | 失败 → 跳过，不修正 |

降级条件：**非关键步骤** + **LLM 错误或超时** → 跳过该步骤并继续

降级时发送 `step.complete{result:{degraded:true, error:"..."}}`，前端以琥珀色标识。

### 5.6 单步超时

步骤可实现 `Timeouter` 接口声明自身超时：

```go
type Timeouter interface {
    Timeout() time.Duration
}
```

引擎在执行前用 `context.WithTimeout` 包装步骤上下文。超时后：
- **关键步骤**：终止整个流程
- **非关键步骤**：触发降级，跳过并继续

### 5.7 WebSocket 断连检测

```go
// SignalDisconnect — WS 关闭时调用，幂等
func (ctx *ExecutionContext) SignalDisconnect() {
    select {
    case <-ctx.disconnectCh:
        // already closed
    default:
        close(ctx.disconnectCh)
    }
}
```

- 断连后 Agent **暂停**（非终止），状态标记为 `paused`
- 发送 `paused{reason:"disconnect"}`，前端提示"📡 连接已断开，重连后可继续写作"
- `WaitForConfirmWithTimeout` 也会监听断连信号，避免在用户离开时无限等待

### 5.8 并发限制

```go
// 全局并发：最多 10 个同时运行的 Agent
// 每用户并发：最多 1 个同时运行的 Agent
```

超出限制时返回 `error{code:"concurrent_limit"}`，前端提示"⏳ 已有写作任务进行中"。

### 5.9 退出错误码一览

| 错误码 | 触发条件 | 前端提示 |
|--------|----------|----------|
| `timeout` | 全局上下文超时 | ⏱️ 写作超时，请简化选题后重试 |
| `budget_exceeded` | Token 预算耗尽 | 💰 Token 预算已用尽，请稍后重试 |
| `circuit_breaker` | LLM 连续失败 ≥ 阈值 | 🔌 AI 服务暂时不可用，请稍后重试 |
| `concurrent_limit` | 超过并发限制 | ⏳ 已有写作任务进行中，请等待完成或取消后再试 |
| `server_busy` | 全局并发已满 | 🔧 服务器繁忙，请稍后重试 |
| `step_failed` | 关键步骤执行失败 | ❌ 步骤执行失败：{具体信息} |

### 5.10 V1 降级策略（保留）

| 场景 | 降级策略 |
|---|---|
| 知乎搜索超时 | 跳过知乎，使用 Tavily + IMA 结果 |
| IMA 知识库不可达 | 跳过 KB 检索，标注"无知识库参考" |
| DeepSeek API 限流 | 断路器接管（连续 3 次后终止） |
| Embedding 服务不可达 | 降级为纯关键词检索 |
| PostgreSQL 连接失败 | 内存缓存兜底 + 告警 |
| WebSocket 断连 | 暂停 Agent + 前端提示重连 |

## 6. 安全与合规

### 6.1 内容安全

- **标题敏感检查**：`PostReviewStep` 中增加 `title_sensitivity` 检查，禁止用伤亡数字等做标题
- **正文安全检查**：基于 V1 `sensitiveCheckService.js` 的词库 + 规则
- **严格程度可调**：Admin 后台可配置敏感检查级别（宽松/标准/严格），避免误拦

### 6.2 API 安全

- JWT 认证 + WebSocket Token 验证
- 速率限制（chi middleware）
- CORS 配置
- 请求体大小限制

## 7. 可观测性

| 维度 | 方案 |
|---|---|
| 结构化日志 | Go `slog` + JSON 输出 |
| Trace | 每次写作生成 `TraceID`，记录每步耗时和结果 |
| Metrics | Prometheus（请求量、Token 用量、Step 耗时分布） |
| 错误追踪 | Sentry（可选） |
