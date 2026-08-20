# 架构 C 技术方案：Harness-LLM 单层持续会话

## 一、核心设计

### 设计原则（继承 dsh/Pi 理念 [[memory:178679655388010121476]]）

1. **Harness 编排，LLM 执行** — Harness 管：意图路由、工具注册、状态管理、断路器、超时、断线重连。LLM 在持续会话中自主决定调用什么工具、何时写作、何时修正。
2. **工具混合粒度** — `write_article` 整篇流式写 + `revise_section` 分段定向改。
3. **对话自动判断** — 简单问题纯流式，需要查资料的带工具。
4. **会话状态持久化** — 同一对话框内多轮交互，文章/素材/记忆跨轮保留。

### 与当前架构的对比

```
已弃用（架构 B — UnifiedAgent）:
  用户 → ReAct 循环 → LLM 选择工具 → 执行 → 回传
  13 次 LLM 调用，首字延迟 30-60s
  详见 docs/wiki/architecture-history.md

架构 C:
  用户 → Harness(规则路由)
    → writing:  LLM 持续会话（ChatWithTools + 写作工具集）
    → chat:     LLM 纯流式 / 带工具流式
    → polish:   LLM 持续会话（revise_section）
  1 次 LLM 持续会话，首字延迟 3-5s
```

## 二、整体架构

```
┌─ Server (HTTP/WebSocket 入口) ──────────────────────────────┐
│                                                                │
│  agent.start → 创建/恢复 Session → 启动 Harness               │
│                                                                │
│  ┌─ Harness ───────────────────────────────────────────────┐ │
│  │                                                            │ │
│  │  1. 加载 Session 状态（对话历史 + 当前文章 + 已有素材）    │ │
│  │  2. 意图判定（规则 → LLM fallback）                        │ │
│  │  3. 按意图选择工具集                                       │ │
│  │  4. 构建 LLM 会话（system + history + user + tools）      │ │
│  │  5. 启动 LLM 持续会话                                      │ │
│  │  6. 收尾：保存文章/素材到 Session，存储消息，提取记忆     │ │
│  │                                                            │ │
│  │  ┌─ LLM 持续会话 (ChatWithTools) ─────────────────────┐  │ │
│  │  │  Harness → LLM: system + user + tools              │  │ │
│  │  │  LLM → tool_call(search_web) → Harness 执行 → 返回 │  │ │
│  │  │  LLM → tool_call(read_source) → Harness 执行 → 返回│  │ │
│  │  │  LLM → tool_call(write_article) → 流式输出给前端   │  │ │
│  │  │  LLM → tool_call(review_article) → Harness 评审    │  │ │
│  │  │  LLM → tool_call(revise_section) → 流式输出修正    │  │ │
│  │  │  LLM → 最终文本回复（无 tool_call）→ 结束          │  │ │
│  │  └────────────────────────────────────────────────────┘  │ │
│  └────────────────────────────────────────────────────────────┘ │
└────────────────────────────────────────────────────────────────┘
```

## 三、关键数据结构

### 3.1 WritingSession — 会话级状态

```go
// internal/engine/session.go

type WritingSession struct {
    ConversationID string
    UserID         string
    StyleSlug      string
    
    // 跨轮保留的产出物
    CurrentArticle   string              // 当前文章（支持多轮修改）
    ArticleTitle     string
    SearchResults    []SearchResult      // 已有素材（可复用，不重复搜索）
    ReviewResult     *ReviewResult       // 最近一次评审结果
    UserMaterials    []string            // 用户素材
    
    // 对话历史（从 DB 加载）
    Messages         []ConversationMessage
}
```

### 3.2 工具集定义

```go
// internal/agent/tools.go

// 写作工具集 — LLM 在持续会话中可调用的工具
var WritingToolDefs = []tools.ToolDef{
    {
        Type: "function",
        Function: tools.ToolDefFunction{
            Name:        "search_web",
            Description: "搜索网络获取信息。用于补充写作素材或回答问题。",
            Parameters:  { query: string (required) },
        },
    },
    {
        Type: "function",
        Function: tools.ToolDefFunction{
            Name:        "read_source",
            Description: "读取已有搜索结果的完整内容。传入结果序号(1-based)。",
            Parameters:  { index: int (required), url: string (optional) },
        },
    },
    {
        Type: "function",
        Function: tools.ToolDefFunction{
            Name:        "write_article",
            Description: "生成并流式输出完整文章。调用后文章内容会实时流式展示给用户。",
            Parameters:  { topic: string, style_hint: string (optional) },
        },
    },
    {
        Type: "function",
        Function: tools.ToolDefFunction{
            Name:        "review_article",
            Description: "对当前文章进行质量评审，返回评分和问题列表。",
            Parameters:  { article: string (required) },
        },
    },
    {
        Type: "function",
        Function: tools.ToolDefFunction{
            Name:        "revise_section",
            Description: "定向修改文章的某一部分。流式输出修改后的完整文章。",
            Parameters:  {
                section_hint: string (required, "要修改的部分，如'标题''第三段''结尾'"),
                instruction: string (required, "修改指令"),
            },
        },
    },
}
```

### 3.3 意图路由

```go
// internal/agent/harness.go

type Intent string

const (
    IntentWriting   Intent = "writing"
    IntentChat      Intent = "chat"
    IntentPolish    Intent = "polish"
    IntentShorten   Intent = "shorten"
    IntentExpand    Intent = "expand"
    IntentExtract   Intent = "extract_points"
)

// Harness 规则路由：毫秒级，不调 LLM
func ClassifyIntent(input string, session *WritingSession) Intent {
    // 如果有当前文章且用户要改 → polish/shorten/expand
    if session.CurrentArticle != "" {
        if matchesAny(input, "改", "修改", "换成", "调整", "润色") → IntentPolish
        if matchesAny(input, "缩短", "精简", "缩写") → IntentShorten
        if matchesAny(input, "扩写", "展开", "补充") → IntentExpand
    }
    
    // 写作意图
    if matchesAny(input, "写一篇", "写稿", "撰写", "写评论", "基于") → IntentWriting
    
    // 对话意图（默认）
    return IntentChat
}

// 工具集按意图分组
func ToolsForIntent(intent Intent, hasSearch bool) []ToolDef {
    switch intent {
    case IntentWriting:
        return WritingToolDefs  // 全部 5 个工具
    case IntentPolish, IntentShorten, IntentExpand:
        return []ToolDef{revise_section, search_web}  // 只改不写新的
    case IntentChat:
        if hasSearch {
            return []ToolDef{search_web, read_source}  // 可查资料
        }
        return nil  // 纯流式
    }
}
```

## 四、核心流程

### 4.1 Harness 主流程

```go
// internal/agent/harness.go

func (h *Harness) Run(ctx context.Context, execCtx *ExecutionContext, session *WritingSession) error {
    // 1. 加载会话状态
    session.LoadFromDB()
    
    // 2. 意图判定
    intent := ClassifyIntent(execCtx.UserInput, session)
    
    // 3. 构建消息
    messages := h.buildMessages(execCtx, session, intent)
    
    // 4. 选择工具集
    toolDefs := ToolsForIntent(intent, h.search != nil)
    
    // 5. 构建工具执行器
    executor := h.buildExecutor(session, execCtx)
    
    // 6. 启动 LLM 持续会话
    //    - ChatWithTools 最多 8 轮迭代（当前是 3，需要提高）
    //    - 每轮可以调用工具或输出文本
    //    - 文本通过 onDelta 流式转发给前端
    //    - tool_call 通过 executor 执行
    
    var fullText string
    var tokens int
    var err error
    
    if len(toolDefs) > 0 {
        fullText, tokens, err = h.llm.ChatWithTools(
            ctx, messages,
            h.streamDelta(execCtx),     // onDelta
            h.streamReasoning(execCtx), // onReasoning
            h.streamReset(execCtx),    // onReset
            toolDefs, executor,
            h.buildLLMOptions(intent, session)...,
        )
    } else {
        // 纯流式对话
        fullText, tokens, err = h.llm.ChatStreamWithReasoning(
            ctx, messages,
            h.streamDelta(execCtx),
            h.streamReasoning(execCtx),
            h.buildLLMOptions(intent, session)...,
        )
    }
    
    // 7. 收尾
    session.CurrentArticle = fullText  // 如果是写作，fullText 就是文章
    h.saveSession(session, execCtx)
    
    return nil
}
```

### 4.2 消息构建

```go
func (h *Harness) buildMessages(execCtx *ExecutionContext, session *WritingSession, intent Intent) []LLMMessage {
    var messages []LLMMessage
    
    // System message
    systemPrompt := h.buildSystemPrompt(session, intent)
    messages = append(messages, LLMMessage{Role: "system", Content: systemPrompt})
    
    // 对话历史（从 session 加载，最多保留最近 N 轮）
    for _, msg := range session.RecentMessages(6) {
        messages = append(messages, LLMMessage{
            Role:    string(msg.Role),
            Content: msg.Content,
        })
    }
    
    // 当前用户输入
    messages = append(messages, LLMMessage{
        Role:    "user",
        Content: execCtx.NormalizedInput,
    })
    
    return messages
}

func (h *Harness) buildSystemPrompt(session *WritingSession, intent Intent) string {
    prompt := "你是笔润智谈，一个专业的中文写作助手。"
    
    // 风格 Profile
    if h.profile != nil {
        prompt += h.profile.SystemPrompt
    }
    
    // 当前文章（如果有，让 LLM 知道已有内容）
    if session.CurrentArticle != "" {
        prompt += fmt.Sprintf("\n\n当前文章：\n%s", session.CurrentArticle)
    }
    
    // 已有素材（如果有，让 LLM 知道不用重复搜索）
    if len(session.SearchResults) > 0 {
        prompt += fmt.Sprintf("\n\n已有素材：%d 条搜索结果可用。", len(session.SearchResults))
    }
    
    // 用户素材
    if len(session.UserMaterials) > 0 {
        prompt += "\n\n用户上传素材：\n" + strings.Join(session.UserMaterials, "\n")
    }
    
    // 记忆偏好
    if session.MemoryContext != nil {
        prompt += FormatMemoryForPrompt(session.MemoryContext)
    }
    
    // 当前日期
    prompt += fmt.Sprintf("\n当前日期：%s。", time.Now().Format("2006年1月2日"))
    
    // 意图相关指令
    switch intent {
    case IntentWriting:
        prompt += "\n请先搜索素材，素材足够后调用 write_article 生成文章。"
        prompt += "\n文章写完后调用 review_article 评审，如有问题调用 revise_section 修正。"
    case IntentPolish:
        prompt += "\n用户要修改已有文章。请调用 revise_section 定向修改，不要重写全文。"
    case IntentChat:
        prompt += "\n用户在和你对话。如果需要查资料可以调用 search_web。"
    }
    
    prompt += engine.PromptInjectionDefenseDirective
    return prompt
}
```

### 4.3 工具执行器

```go
func (h *Harness) buildExecutor(session *WritingSession, execCtx *ExecutionContext) ToolExecutor {
    return func(name string, arguments string) (string, error) {
        switch name {
        case "search_web":
            // 复用 SearchClient.Search
            var args struct{ Query string }
            json.Unmarshal([]byte(arguments), &args)
            results := h.search.Search(ctx, args.Query, 5)
            // 保存到 session 供后续复用
            session.SearchResults = append(session.SearchResults, results...)
            return formatSearchResults(results), nil
            
        case "read_source":
            var args struct{ Index int; URL string }
            json.Unmarshal([]byte(arguments), &args)
            // 从 session.SearchResults 取全文，或抓取 URL
            content := h.fetchFullContent(args, session)
            return content, nil
            
        case "write_article":
            // 这是一个"信号工具" — LLM 调用后，Harness 知道
            // 下一段流式输出就是文章内容
            // 返回 "开始写作"，LLM 接下来会流式输出文章
            h.emitter.StepStart("write", 0)
            return "好的，请开始写作，流式输出文章内容。", nil
            
        case "review_article":
            var args struct{ Article string }
            json.Unmarshal([]byte(arguments), &args)
            review := h.reviewArticle(args.Article, execCtx)
            session.ReviewResult = review
            h.emitter.StepComplete("post_review", review, 0)
            return formatReviewResult(review), nil
            
        case "revise_section":
            var args struct {
                SectionHint string
                Instruction string
            }
            json.Unmarshal([]byte(arguments), &args)
            // 返回修改指令，LLM 接下来会流式输出修改后的完整文章
            return fmt.Sprintf("请修改「%s」：%s。流式输出修改后的完整文章。",
                args.SectionHint, args.Instruction), nil
            
        default:
            return "", fmt.Errorf("unknown tool: %s", name)
        }
    }
}
```

## 五、ChatWithTools 改造

### 5.1 提高迭代上限

```go
// deepseek.go ChatWithTools
const maxIterations = 8  // 从 3 提高到 8
```

### 5.2 write_article 和 revise_section 作为"信号工具"

`write_article` 和 `revise_section` 本身不产出内容 — 它们是"信号"，
告诉 Harness"接下来 LLM 的流式输出就是文章"。

当 LLM 调用 `write_article` 后：
1. Harness 返回"请开始写作，流式输出文章内容"
2. LLM 在下一轮收到 tool_result，开始流式输出文章
3. `onDelta` 回调将文章内容实时转发给前端
4. LLM 可能继续调用 `review_article`，或直接结束（无 tool_call）

### 5.3 流式输出 + 工具调用混合

```
LLM 第1轮: [thinking] → tool_call(search_web) → 返回结果
LLM 第2轮: [thinking] → tool_call(read_source) → 返回结果
LLM 第3轮: [thinking] → tool_call(write_article) → 返回"请开始写作"
LLM 第4轮: 流式输出文章 ← onDelta 实时转发给前端
           → 无 tool_call → 最终文本 → 结束

或：

LLM 第4轮: 流式输出文章 ← onDelta
           → tool_call(review_article) → 返回评审结果
LLM 第5轮: [thinking] → tool_call(revise_section) → 返回"请修改"
LLM 第6轮: 流式输出修正后的文章 ← onDelta (前端先 stream.reset)
           → 无 tool_call → 结束
```

## 六、前端适配

### 6.1 WebSocket 事件协议（兼容现有）

不需要新增事件类型。架构 C 复用现有的事件：

| 事件 | 用途 | 变化 |
|------|------|------|
| `agent.step.start` | 工具调用开始 | step 名改为工具名（search_web, write_article 等） |
| `agent.step.complete` | 工具调用完成 | result 包含工具返回值 |
| `agent.stream` | 流式文章内容 | 无变化 |
| `agent.stream.reset` | 重置流 | revise_section 前触发 |
| `agent.reasoning` | 思考过程 | 无变化 |
| `agent.completed` | 完成 | article + review 无变化 |
| `agent.error` | 错误 | 无变化 |

### 6.2 前端 StepName 扩展

```typescript
// types.ts
export type AgentStepName =
  | "search_web"      // 新
  | "read_source"     // 新
  | "write_article"   // 替代 write
  | "review_article"  // 替代 post_review
  | "revise_section"  // 替代 auto_fix
  | "chat"            // 对话模式
  // 保留旧值用于历史会话回放
  | "intent" | "query_plan" | "search" | "relevance"
  | "outline" | "write" | "post_review" | "auto_fix"
  | "memory_gate" | "memory_extract" | "parallel_pre_write"
```

### 6.3 前端 constants.ts 扩展

```typescript
export const STEP_LABELS: Record<AgentStepName, string> = {
  // 新
  search_web: "联网搜索",
  read_source: "读取全文",
  write_article: "文章生成",
  review_article: "质量评审",
  revise_section: "定向修正",
  chat: "对话回复",
  // 旧（兼容）
  intent: "意图识别",
  query_plan: "检索规划",
  // ...
};
```

## 七、文件改动清单

### 新增文件

| 文件 | 用途 |
|------|------|
| `backend/internal/agent/harness.go` | Harness 主编排器 |
| `backend/internal/agent/tools.go` | 写作工具定义和执行器 |
| `backend/internal/agent/session.go` | WritingSession 会话状态 |
| `backend/internal/agent/intent.go` | 规则意图判定 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `backend/internal/tools/deepseek.go` | ChatWithTools maxIterations 3→8 |
| `backend/internal/engine/step.go` | EventEmitter 接口不变（复用） |
| `backend/internal/server/server.go` | agent.start 路由到 Harness |
| `backend/internal/server/emitter.go` | WSEmitter 不变（复用） |
| `frontend/src/lib/types.ts` | AgentStepName 扩展 |
| `frontend/src/lib/constants.ts` | STEP_LABELS / STEP_ICONS 扩展 |
| `frontend/src/stores/agent-store.ts` | 工具名映射兼容 |

### 保留不动的文件

| 文件 | 原因 |
|------|------|
| `backend/internal/engine/context.go` | ExecutionContext 复用 |
| `backend/internal/engine/tool_registry.go` | 保留，但不用于 Harness |
| `backend/internal/engine/steps/*.go` | 保留，pipeline 模式仍可用 |
| `backend/internal/tools/deepseek_responses.go` | Responses API 格式已修复 |
| `backend/internal/server/logging_emitter.go` | 事件日志复用 |
| `backend/internal/database/session_events.go` | 事件存储复用 |

## 八、实施步骤

### Phase 1: 后端核心 (harness + tools + session)
1. 创建 `internal/agent/session.go` — WritingSession 结构
2. 创建 `internal/agent/intent.go` — 规则意图判定
3. 创建 `internal/agent/tools.go` — 工具定义和执行器
4. 创建 `internal/agent/harness.go` — Harness 主流程
5. 修改 `deepseek.go` — maxIterations 提高到 8

### Phase 2: 后端接入 (server 路由)
6. 修改 `server.go` — agent.start 路由到 Harness
7. 兼容现有 ExecutionContext / EventEmitter / WSEmitter
8. 接入 SessionStore（复用短期记忆存储）

### Phase 3: 前端适配
9. 修改 `types.ts` — AgentStepName 扩展
10. 修改 `constants.ts` — STEP_LABELS / STEP_ICONS
11. 修改 `agent-store.ts` — 工具名映射兼容

### Phase 4: 编译验证
12. Docker 编译验证
13. 端到端测试

## 九、风险和缓解

| 风险 | 缓解 |
|------|------|
| ChatWithTools 8 轮可能不够 | 最后一轮 fallback 无工具流式输出（已有机制） |
| write_article 作为"信号工具" LLM 不理解 | prompt 里明确说明用法 |
| 对话模式带工具可能 LLM 不调工具 | 不调工具直接流式回复也是正确行为 |
| revise_section 流式输出覆盖前文 | 前端先 stream.reset 再 stream.delta |
| 历史会话回放 | 保留旧 StepName 兼容 |
