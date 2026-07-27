# 记忆系统架构

## 概述

V2 引擎实现了三层记忆架构，模拟人类写作时的认知过程：

```
┌─────────────────────────────────────────────────────────────┐
│                     用户请求 (User Input)                     │
└───────────────────────────┬─────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│  短期记忆 (Short-Term Memory)                                 │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ 同一会话内的对话历史                                        ││
│  │ 语义裁切 → 动态窗口 → 注入 LLM 上下文                      ││
│  │ 存储: conversation_messages 表 (pgvector)                 ││
│  └─────────────────────────────────────────────────────────┘│
├─────────────────────────────────────────────────────────────┤
│  工作记忆 (Working Memory)                                    │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ 当前执行周期的增量摘要 + 随机态                              ││
│  │ 步骤摘要 → LLM 压缩 → 跨请求继承                           ││
│  │ 存储: working_summaries 表 (JSONB)                        ││
│  └─────────────────────────────────────────────────────────┘│
├─────────────────────────────────────────────────────────────┤
│  长期记忆 (Long-Term Memory)                                  │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ 实体记忆网络 (Entity Memory Network)                      ││
│  │ 语义检索 → 2-hop 图遍历 → 格式化为 prompt                   ││
│  │ 存储: memory_entities + memory_relations 表 (pgvector)    ││
│  └─────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
```

| 记忆层 | 生命周期 | 注入时机 | 核心目标 |
|--------|----------|----------|----------|
| 短期记忆 | 单会话（~30 天） | IntentStep 之前 | 保持对话连贯性 |
| 工作记忆 | 单次执行 + 跨请求继承 | 每个关键步骤后 | 上下文压缩 + 输出多样性 |
| 长期记忆 | 永久（用户级） | MemoryGateStep | 用户画像 + 偏好学习 |

## 1. 短期记忆（Short-Term Memory）

### 1.1 设计目标

管理同一会话内的对话历史，使其参与 LLM 推理，确保多轮对话的连贯性。

### 1.2 核心算法

#### 语义裁切（Semantic Chunking）

将对话历史按语义相似度分组，识别"话题切换边界"，形成语义连贯的对话片段：

```
消息序列: A → B → C → D → E
          ├─────┤    ├─────┤
          Chunk 1    Chunk 2
          (话题A)    (话题B)

切分条件: 相邻消息的 embedding 余弦相似度 < 0.45
```

```go
type SemanticChunker struct {
    config DynamicWindowConfig
}

func (c *SemanticChunker) Chunk(messages []ConversationMessage) []SemanticChunk
```

#### 动态窗口（Dynamic Window）

在 token 预算内选择最优的消息组合：

1. **最近窗口（Recency Window）**：始终保留最近 N 条消息（默认 4 条）
2. **语义相关性窗口**：从旧到新，按语义相似度排序，贪心填充剩余预算
3. **时间顺序输出**：保持消息的原始时间顺序

```
Token 预算: 4096
┌──────────────────────────────────────────────┐
│ 最近窗口 (4 条)          │ 语义窗口 (贪心填充)   │
│ [最新] E D C B          │ [相关] A ← chunk_0   │
│                         │       A ← chunk_2   │
│ ~2048 tokens            │ ~2048 tokens        │
└──────────────────────────────────────────────┘
```

### 1.3 配置

```go
type DynamicWindowConfig struct {
    TokenBudget         int     // 默认 4096
    RecencyWindow       int     // 默认 4
    RelevanceThreshold  float64 // 默认 0.35
    ChunkSplitThreshold float64 // 默认 0.45
}
```

### 1.4 执行流程

```
ShortTermMemoryStep (管道最早期)
  │
  ├── 加载会话历史 (最近 50 条)
  ├── 加载上一轮工作记忆摘要 (跨请求继承)
  ├── 生成当前查询的 embedding
  ├── 语义裁切 → 动态窗口选择
  ├── 注入 ExecutionContext.ConversationHistory
  └── 初始化 StochasticState (随机态)
      ↓
... Agent 执行 (Intent → Search → Write → Review) ...
      ↓
ShortTermStoreStep (管道结束后)
  │
  ├── 存储用户消息 + 异步 embedding
  ├── 存储助手响应 + 异步 embedding
  └── 持久化工作记忆摘要
```

### 1.5 数据表

- `conversation_messages` — 对话消息，含 embedding 向量，支持语义检索
- 超过 30 天的消息自动清理

## 2. 工作记忆（Working Memory）

### 2.1 设计目标

在单次任务执行期间维护一个紧凑的执行状态摘要，并在跨请求时继承上一轮的上下文。

### 2.2 增量摘要（Incremental Summarization）

每个关键步骤完成后，将该步骤的输出摘要追加到工作记忆：

```
执行步骤:
  IntentStep  → "意图分类: writing (置信度 0.95)"
  QueryPlanStep → "检索规划: 话题「AI」, 3 个查询"
  SearchStep  → "搜索完成: 15 条结果"
  OutlineStep → "大纲生成: 标题「...」, 5 个要点"
  WriteStep   → "文章生成: 3200 字"
  PostReviewStep → "审查完成: 通过=true, 2 个问题"

摘要列表:
  [intent] 意图分类: writing (置信度 0.95)
  [query_plan] 检索规划: 话题「AI」, 3 个查询
  [search] 搜索完成: 15 条结果
  ...

超过 5 步 → 触发 LLM 压缩:
  "用户请求写作关于AI的文章，已检索15条素材，生成5要点大纲..."
```

```go
type IncrementalSummarizer struct {
    config SummarizerConfig
    llm    LLMSummarizer
}

type SummarizerConfig struct {
    MaxStepSummaries    int // 默认 8
    MaxCompressedLength int // 默认 800
    CompressThreshold   int // 默认 5
}
```

### 2.3 随机态（Stochastic State）

在搜索结果选择、大纲生成等环节引入受控随机性：

```go
type StochasticState struct {
    Seed               int64   // 确定性种子
    TemperatureShift   float64 // ±0.1 温度偏移
    SearchSamplingRate float64 // 0.7-1.0 搜索采样率
}
```

| 机制 | 应用场景 | 效果 |
|------|----------|------|
| 温度偏移 | WriteStep、ChatStep | 同一输入产生不同输出 |
| 搜索采样 | RelevanceStep | 弱相关结果随机丢弃 |
| 探索决策 | 搜索策略选择 | 以一定概率尝试新路径 |

种子由用户输入确定性生成（`GenerateSeedFromInput`），保证可复现性。

### 2.4 跨请求继承

```
请求 1:
  ShortTermMemoryStep → 加载历史 (空) → 执行 → WorkingMemoryStep → 摘要
  ShortTermStoreStep → 持久化 working_summaries

请求 2 (同一会话):
  ShortTermMemoryStep → 加载历史 + 加载上一轮 WorkingSummary
  → 继承压缩摘要和步骤摘要
  → WorkingMemoryStep 增量处理新步骤
```

### 2.5 数据表

- `working_summaries` — 以 `conversation_id` 为主键，存储 JSONB 格式的摘要

## 3. 长期记忆 — 实体记忆网络（Entity Memory Network）

### 3.1 设计目标

将用户偏好、话题、风格等建模为图结构，提供比扁平 key-value 更丰富的上下文关联。

### 3.2 数据模型

```
┌──────────┐         ┌──────────┐
│ Entity A │ ──prefers→ │ Entity B │
│ (topic:  │         │ (style:  │
│  AI)     │         │  科技评论)│
└──────────┘         └──────────┘
      │                    │
   related_to          co_occurs_with
      │                    │
      ▼                    ▼
┌──────────┐         ┌──────────┐
│ Entity C │         │ Entity D │
│ (concept:│         │ (tone:   │
│  数据驱动)│         │  严肃)    │
└──────────┘         └──────────┘
```

#### 实体类型

| 类型 | 说明 | 示例 |
|------|------|------|
| `topic` | 话题 | 人工智能、乡村振兴 |
| `style` | 写作风格 | 科技评论、时评 |
| `preference` | 偏好 | 短文、幽默语气 |
| `concept` | 概念 | 结构化论证、数据驱动 |
| `tone` | 语气 | 严肃、轻松 |
| `structure` | 结构 | 三段论、递进式 |

#### 关系类型

| 类型 | 说明 |
|------|------|
| `prefers` | 用户偏好 A 胜过 B |
| `dislikes` | 用户不喜欢 |
| `related_to` | A 与 B 相关 |
| `evolved_from` | A 由 B 演化而来 |
| `co_occurs_with` | A 与 B 共现 |
| `contrasts_with` | A 与 B 对比 |

### 3.3 图检索算法

```
查询: "帮我写一篇关于AI的科技评论"
  │
  ▼
1. 语义检索 (Hop 0)
   └─ embedding 相似度 → 找到种子实体 (如 "AI", "科技评论")
  │
  ▼
2. 图遍历 (Hop 1) — 批量获取邻居
   └─ "AI" → related_to → "数据驱动"
   └─ "科技评论" → co_occurs_with → "严肃语气"
  │
  ▼
3. 图遍历 (Hop 2) — 批量获取邻居的邻居
   └─ "数据驱动" → prefers → "短文"
  │
  ▼
4. 格式化为 prompt 文本
   ┌──────────────────────────┐
   │ --- 用户画像网络 ---       │
   │ [关注话题] AI、数据驱动    │
   │ [写作风格] 科技评论        │
   │ [语气偏好] 严肃            │
   │ [偏好关系] AI→偏好 科技评论 │
   └──────────────────────────┘
```

**性能优化**：使用 `GetNeighborsBatch` 批量查询，最多 3 次 DB 查询完成 2-hop 遍历。

### 3.4 实体提取

实体和关系通过 LLM 从文章和用户查询中自动提取：

```go
type EntityExtractor interface {
    ExtractFromArticle(ctx context.Context, article, styleSlug string) ([]ExtractedEntity, error)
    ExtractFromQuery(ctx context.Context, query string) ([]ExtractedEntity, error)
}
```

提取流程：
1. 文章生成后（MemoryExtractStep），LLM 分析文章内容
2. 提取实体（话题、风格、偏好等）和关系
3. 为每个实体生成 embedding 向量
4. 存入 `memory_entities` 和 `memory_relations` 表
5. 已存在的实体：增加 `occurrence_count`，更新 `last_seen`

### 3.5 实体生命周期

```
首次出现 → confidence=0.50, occurrence=1
  │
  ├── 再次出现 → confidence↑, occurrence++
  │
  ├── 演化 → 新版本创建，旧版本 status=superseded
  │
  └── 长期未出现 → status=archived
```

### 3.6 数据表

- `memory_entities` — 实体节点，含 embedding 向量和置信度
- `memory_relations` — 实体关系边，含权重和证据计数

## 4. 注入时机与 Step 集成

### 4.1 管道流程中的记忆注入点

```
ShortTermMemoryStep ← 短期记忆加载 + 工作记忆继承 + 随机态初始化
  │
  ▼
MemoryGateStep ← 长期记忆检索 + 实体图检索
  │
  ▼
IntentStep
  │
  ▼
... (QueryPlan → Search → Relevance ← 随机态采样)
  │
  ▼
OutlineStep ← 等待用户确认 (confirm timeout 保护)
  │
  ▼
WriteStep ← 注入对话历史 + 实体网络 + 工作摘要 + 随机态温度
  │
  ▼
PostReviewStep
  │
  ▼
AutoFixStep ← fix 次数限制 (MaxFixAttempts)
  │
  ▼
WorkingMemoryStep ← 增量摘要
  │
  ▼
MemoryExtractStep ← 长期记忆提取 (实体提取)
  │
  ▼
ShortTermStoreStep ← 消息存储 + 工作摘要持久化
```

### 4.2 ChatStep 中的记忆注入

Chat 模式同样注入三层记忆：

```go
// 1. 实体记忆网络
if execCtx.EntityContext != nil {
    promptBuilder.WriteString(entityCtx.FormattedContext)
}

// 2. 对话历史
if execCtx.ConversationHistory != nil {
    for _, msg := range history {
        messages = append(messages, LLMMessage{Role: msg.Role, Content: msg.Content})
    }
}

// 3. 工作记忆摘要
if execCtx.WorkingSummary != nil {
    promptBuilder.WriteString(FormatWorkingSummaryForPrompt(ws))
}

// 4. 随机态温度调整
if execCtx.StochasticState != nil {
    chatOpts = append(chatOpts, WithTemperature(ss.AdjustedTemperature(0.6)))
}
```

## 5. Token 预算管理

记忆注入受 Token 预算约束，防止上下文溢出：

| 记忆层 | 预算 | 截断策略 |
|--------|------|----------|
| 短期记忆 | 4096 tokens | 动态窗口选择 |
| 工作记忆 | 800 字符 | LLM 压缩 |
| 长期记忆 | 10 个实体 | 图遍历 Top-N |
| 助手历史文章 | 500 字符 | SafeTruncate |

```go
// EstimateTokens — 基于 rune 计数的 token 估算
// 中文: 1 字 ≈ 1.5 tokens, 英文: 1 词 ≈ 1.3 tokens
// 混合: rune 数 × 0.8
func EstimateTokens(text string) int {
    runeCount := utf8.RuneCountInString(text)
    return int(float64(runeCount) * 0.8)
}

// SafeTruncate — 安全截断，不切断 UTF-8 多字节字符
func SafeTruncate(s string, maxBytes int) string
```

## 6. 灰度控制

记忆功能按用户灰度开启：

```go
// MemoryGateStep.ShouldSkip
if !s.svc.IsEnabledForUser(execCtx.UserID) {
    return nil // 灰度未开启的用户跳过记忆
}
```

- 匿名/游客用户：跳过所有记忆功能
- 灰度用户：按配置开启短期/工作/长期记忆
- 全量用户：所有记忆功能默认开启

## 7. 文件结构

```
backend/
├── pkg/memory/                    # 记忆系统核心（无外部依赖）
│   ├── shortterm.go               # 短期记忆：语义裁切 + 动态窗口
│   ├── entity.go                  # 长期记忆：实体网络 + 图检索
│   └── working.go                 # 工作记忆：增量摘要 + 随机态
│
├── internal/memory/               # 记忆服务实现（依赖 DB + LLM）
│   ├── service.go                 # 记忆服务总入口
│   ├── pg_store.go                # PostgreSQL 记忆存储
│   ├── shortterm_entity_store.go  # 短期记忆 + 实体网络 PG 实现
│   └── entity_extractor.go        # LLM 实体提取器
│
├── internal/engine/steps/
│   ├── shortterm_working_steps.go # 短期/工作记忆 Steps
│   ├── memory_steps.go            # 长期记忆 Gate/Extract Steps
│   └── ...
│
└── internal/database/migrations/
    ├── 028_conversation_messages.{up,down}.sql
    ├── 029_memory_entities_relations.{up,down}.sql
    └── 030_working_summaries.{up,down}.sql
```
