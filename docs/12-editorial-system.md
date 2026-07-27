# 编辑部系统设计

## 概述

将 LuminBuddy 从"单用户写作助手"升级为"人机协作编辑部工作系统"。不是给写作工具增加几个 Agent，而是围绕**选题和稿件**为协作对象，由人类编辑与多个 Agent 共同推进。

## 核心价值

- 提升编辑部稿件吞吐量
- 沉淀选题、信源和审稿标准
- 降低沟通、返工和事实风险
- 让人类编辑只介入真正需要判断的节点
- 保留每篇稿件的责任链和证据链

## 1. 三个结构化对象

Agent 之间不传递无限聊天记录，而是围绕三个结构化对象协作：

### 1.1 Task — 任务

```go
type Task struct {
    ID             string
    Title          string         // 选题标题
    Description    string         // 选题目标
    OwnerID        string         // 当前负责人（人类编辑或 Agent 角色）
    AssigneeType   string         // human | research_agent | writing_agent | review_agent
    Deadline       *time.Time
    Status         TaskStatus     // draft → pending_approval → research → writing → review → pending_publish → published → archived
    AcceptCriteria string         // 验收标准
    AllowedTools   []string       // 可用工具列表
    TokenBudget    int            // Token 预算
    TokenUsed      int            // 已用 Token
    Priority       int            // 优先级 0-5
    Tags           []string       // 标签（栏目、热点等）
    StyleSlug      string         // 风格 Profile
    ConversationID string         // 关联会话 ID
    CreatedBy      string         // 创建者
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

### 1.2 Artifact — 交付物

Agent 之间传递可验收的结构化交付物，而非聊天记录：

```go
type Artifact struct {
    ID          string
    TaskID      string
    Type        ArtifactType   // topic_card | research_brief | source_pack | fact_claims | outline | draft | review_report | revised_draft
    Version     int            // 版本号（同一类型可有多个版本）
    Content     string         // JSON 或 Markdown 内容
    Status      ArtifactStatus // draft → submitted → approved → rejected → superseded
    ProducedBy  string         // 产出者（agent 角色 ID）
    ReviewedBy  string         // 审批者
    ReviewNote  string         // 审批备注
    ParentID    *string        // 前一版本 ID（版本链）
    TokenCost   int            // 产出此交付物的 Token 消耗
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

#### Artifact 类型

| 类型 | 产出者 | 消费者 | 说明 |
|------|--------|--------|------|
| `topic_card` | 人类编辑 | 研究 Agent | 选题卡：选题目标、角度、验收标准 |
| `research_brief` | 研究 Agent | 写作 Agent | 研究简报：压缩后的素材摘要 |
| `source_pack` | 研究 Agent | 审校 Agent | 信源包：原始信源 + 分级 + 可信度 |
| `fact_claims` | 研究 Agent | 审校 Agent | 事实声明表：每条事实声明 + 证据绑定 |
| `outline` | 写作 Agent | 人类编辑 | 提纲：标题 + 要点结构 |
| `draft` | 写作 Agent | 审校 Agent | 初稿 |
| `review_report` | 审校 Agent | 写作 Agent / 人类编辑 | 审查报告：问题 + 证据 + 修改建议 |
| `revised_draft` | 写作 Agent | 审校 Agent / 人类编辑 | 修改稿（版本化） |

### 1.3 Decision — 决策

```go
type Decision struct {
    ID           string
    TaskID       string
    Type         DecisionType  // approve_topic | select_angle | trust_source | accept_review | allow_rewrite | publish | escalate
    DecidedBy    string         // 决策者（人类编辑 ID 或 Agent 角色）
    DecidedByType string        // human | research_agent | writing_agent | review_agent | system
    Status       DecisionStatus // pending → approved → rejected → escalated
    Rationale    string         // 决策理由
    Evidence     string         // 依据（Artifact ID 或具体证据）
    ArtifactID   *string        // 关联的 Artifact
    CreatedAt    time.Time
    DecidedAt    *time.Time
}
```

## 2. 三 Agent 架构

### 2.1 角色定义

```
┌──────────────────────────────────────────────────────┐
│                  人类编辑                              │
│  立项权 | 高风险事实裁决权 | 风格决定权 | 最终发布权     │
└────────────┬────────────────────┬────────────────────┘
             │                    │
     ┌───────▼───────┐   ┌───────▼───────┐
     │  研究 Agent    │   │  审校 Agent    │
     │                │   │                │
     │ • 多源检索     │   │ • 事实核查     │
     │ • 信源分级     │   │ • 风格审查     │
     │ • 事实声明     │   │ • 风险检查     │
     │ • 矛盾标记     │   │ • 驳回权       │
     │                │   │                │
     │ 输出:          │   │ 输出:          │
     │ 研究简报       │   │ 审查报告       │
     │ 信源包         │   │ (问题-证据-建议)│
     │ 事实声明表     │   │                │
     └───────┬───────┘   └───────▲───────┘
             │                    │
             ▼                    │
     ┌───────────────────────────┐│
     │      写作 Agent            ││
     │                           ││
     │ • 基于已批准研究包写作     ││
     │ • 不能引入未验证事实       ││
     │ • 按风格 Profile 生成      ││
     │ • 接受审校意见修改         ││
     │                           ││
     │ 输出: 提纲 → 初稿 → 修改稿 ││
     └───────────┬───────────────┘│
                 │                │
                 └────────────────┘
                 (初稿/修改稿 → 审校)
```

### 2.2 上下文隔离

每个 Agent 拥有独立的局部上下文，只通过 Artifact 通信：

```go
// AgentContext — 每个 Agent 的局部上下文
type AgentContext struct {
    AgentRole      AgentRole      // research | writing | review
    TaskID         string
    TraceID        string
    InputArtifacts []ArtifactRef  // 只读，来自上游
    OutputArtifact *Artifact      // 自己产出的
    LocalMemory    interface{}    // Agent 自己的短期/工作记忆
    TokenUsage     int            // 自己的消耗
    Timeout        time.Duration
    CircuitBreaker int
}

// TaskContext — 编辑部级共享状态（只含共享信息，不含 Agent 私有状态）
type TaskContext struct {
    Task         *Task
    Artifacts    []Artifact       // 版本化交付物链
    Decisions    []Decision       // 决策记录
    TokenBudget  int              // 聚合 Token 预算
    TokenUsed    int              // 聚合已用 Token
    SharedMemory interface{}      // 编辑部规范、栏目偏好
}
```

### 2.3 现有 V2 能力复用映射

| V2 现有能力 | 编辑部角色 | 说明 |
|-------------|-----------|------|
| QueryPlanStep + SearchStep + RelevanceStep + CompressStep | 研究 Agent | 多源检索 + 信源分级 + 素材压缩 |
| OutlineStep + WriteStep + StyleProfile | 写作 Agent | 提纲 + 初稿生成 |
| PostReviewStep + 事实核查(jiaozhen) + AutoFixStep | 审校 Agent | 质量评审 + 事实核查 + 修改建议 |
| Guided Outline (await_input) | 人类决策节点 | 提纲确认 |
| Trace + Token + StepRecord | 协作审计 | 责任链 + 成本统计 |
| Memory 系统 | 编辑部规范 | 栏目偏好、作者历史 |
| Topic Center | 选题池 | 编辑部选题管理 |
| Feedback + 录用结果 | Agent 信誉 | 效果反馈 |
| UnifiedAgent | 主编调度 | ReAct 循环编排基础 |
| Exit Mechanism (6 层退出) | Agent 退出保护 | 断路器 + 预算 + 超时 |

## 3. 任务状态机

```
                    ┌─────────┐
                    │  draft  │  人类编辑创建选题
                    └────┬────┘
                         │ 人类审批
                         ▼
                ┌─────────────────┐
                │ pending_approval │
                └────────┬────────┘
                         │ 批准立项
                         ▼
                   ┌──────────┐
                   │ research │  研究 Agent 工作
                   └────┬─────┘
                        │ 研究包就绪
                        ▼
                   ┌──────────┐
                   │ writing  │  写作 Agent 工作
                   └────┬─────┘
                        │ 初稿就绪
                        ▼
                   ┌─────────┐
                   │ review  │  审校 Agent 工作
                   └────┬────┘
                        │
               ┌────────┼────────┐
               │        │        │
               ▼        ▼        ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │  通过    │ │  驳回    │ │  升级    │
        │          │ │ (回写作) │ │ (回人类) │
        └────┬─────┘ └────┬─────┘ └────┬─────┘
             │             │             │
             ▼             │             ▼
      ┌──────────────┐     │      ┌──────────┐
      │pending_publish│     │      │ pending   │
      └──────┬───────┘     │      │ approval  │
             │             │      └──────────┘
             ▼             │
      ┌──────────┐         │
      │ published│         │
      └──────────┘         │
                           │
                           ▼
                      回到 writing
```

## 4. API 设计

### 4.1 REST API

```
# 任务管理
POST   /api/v2/editorial/tasks              创建任务（选题）
GET    /api/v2/editorial/tasks              列表（支持状态过滤）
GET    /api/v2/editorial/tasks/{id}         任务详情
PATCH  /api/v2/editorial/tasks/{id}         更新任务
DELETE /api/v2/editorial/tasks/{id}         删除任务
POST   /api/v2/editorial/tasks/{id}/advance 推进任务到下一阶段

# Artifact 管理
GET    /api/v2/editorial/tasks/{id}/artifacts       任务的所有交付物
GET    /api/v2/editorial/artifacts/{id}             交付物详情
GET    /api/v2/editorial/artifacts/{id}/versions    版本历史
POST   /api/v2/editorial/tasks/{id}/artifacts       提交新交付物
PATCH  /api/v2/editorial/artifacts/{id}/review      审批交付物

# Decision 管理
GET    /api/v2/editorial/tasks/{id}/decisions       任务的决策记录
POST   /api/v2/editorial/tasks/{id}/decisions       提交决策

# 统计
GET    /api/v2/editorial/stats                      编辑部统计（吞吐量、通过率等）
```

### 4.2 WebSocket 事件

```
# 任务级事件
task.created        任务创建
task.status_changed 任务状态变更
task.assigned       任务分配

# Artifact 事件
artifact.produced   Agent 产出交付物
artifact.submitted  交付物提交审批
artifact.approved   交付物通过审批
artifact.rejected   交付物被驳回

# Decision 事件
decision.required   需要人类决策
decision.made       决策已做出

# Agent 活动事件（复用现有 step 事件）
agent.started       Agent 开始工作
agent.completed     Agent 完成工作
agent.failed        Agent 执行失败
```

## 5. 数据库 Schema

### tasks 表

```sql
CREATE TABLE editorial_tasks (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title           VARCHAR(256) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    owner_id        UUID REFERENCES users(id),
    assignee_type   VARCHAR(32) NOT NULL DEFAULT 'human',
    deadline        TIMESTAMPTZ,
    status          VARCHAR(32) NOT NULL DEFAULT 'draft',
    accept_criteria TEXT NOT NULL DEFAULT '',
    allowed_tools   TEXT[] DEFAULT '{}',
    token_budget    INTEGER NOT NULL DEFAULT 300000,
    token_used      INTEGER NOT NULL DEFAULT 0,
    priority        SMALLINT NOT NULL DEFAULT 3,
    tags            TEXT[] DEFAULT '{}',
    style_slug      VARCHAR(64) NOT NULL DEFAULT 'yinyue',
    conversation_id VARCHAR(64),
    created_by      UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### artifacts 表

```sql
CREATE TABLE editorial_artifacts (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    task_id     UUID NOT NULL REFERENCES editorial_tasks(id) ON DELETE CASCADE,
    type        VARCHAR(32) NOT NULL,
    version     INTEGER NOT NULL DEFAULT 1,
    content     JSONB NOT NULL DEFAULT '{}',
    status      VARCHAR(16) NOT NULL DEFAULT 'draft',
    produced_by VARCHAR(32) NOT NULL,
    reviewed_by UUID REFERENCES users(id),
    review_note TEXT NOT NULL DEFAULT '',
    parent_id   UUID REFERENCES editorial_artifacts(id),
    token_cost  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_artifact_version UNIQUE (task_id, type, version)
);
```

### decisions 表

```sql
CREATE TABLE editorial_decisions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    task_id         UUID NOT NULL REFERENCES editorial_tasks(id) ON DELETE CASCADE,
    type            VARCHAR(32) NOT NULL,
    decided_by      UUID REFERENCES users(id),
    decided_by_type VARCHAR(32) NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'pending',
    rationale       TEXT NOT NULL DEFAULT '',
    evidence        TEXT NOT NULL DEFAULT '',
    artifact_id     UUID REFERENCES editorial_artifacts(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at      TIMESTAMPTZ
);
```

## 6. 文件结构

```
backend/
├── pkg/editorial/                  # 编辑部领域模型（无外部依赖）
│   ├── task.go                     # Task 领域模型 + 状态机
│   ├── artifact.go                 # Artifact 领域模型 + 版本链
│   ├── decision.go                 # Decision 领域模型
│   └── agent_roles.go              # Agent 角色定义 + 权限
│
├── internal/editorial/             # 编辑部服务实现
│   ├── store.go                    # PostgreSQL 存储实现
│   ├── service.go                  # 任务编排服务
│   ├── orchestrator.go             # 三 Agent 编排器
│   └── handlers.go                 # HTTP/WS 处理器
│
└── internal/database/migrations/
    ├── 031_editorial_tasks.{up,down}.sql
    ├── 032_editorial_artifacts.{up,down}.sql
    └── 033_editorial_decisions.{up,down}.sql

frontend/
├── src/
│   ├── pages/
│   │   └── editorial/              # 编辑部界面
│   │       ├── task-board.tsx      # 任务看板
│   │       ├── task-detail.tsx     # 任务详情
│   │       └── artifact-viewer.tsx # 交付物查看器
│   ├── stores/
│   │   └── editorial-store.ts      # 编辑部状态管理
│   └── components/
│       └── editorial/              # 编辑部组件
│           ├── task-card.tsx       # 任务卡片
│           ├── artifact-timeline.tsx
│           └── decision-log.tsx
```

## 7. 验证指标

三组对照实验：

1. **现有固定 Pipeline**（AgentEngine）
2. **单 Agent 动态编排**（UnifiedAgent）
3. **编辑部 Multi-Agent**（三 Agent 协作）

关键指标：

| 指标 | 采集方式 |
|------|----------|
| 选题到可发布稿件时间 | `task.created_at` → `task.status=published` |
| 人类实际操作时长 | 人类 Decision 的时间总和 |
| 初审通过率 | `artifact.status=approved / total` |
| 平均返工轮次 | 同一 type 的 artifact 版本数 - 1 |
| 事实问题漏检率 | 发布后被发现的事实问题 / 总发布数 |
| 单篇 Token 成本 | `task.token_used` |
| 每位编辑每日稿件数 | 按用户聚合 task 计数 |
| Agent 意见接受/驳回比例 | `decision.status` 统计 |

---

## 8. 组织记忆（已实现）

编辑部系统集成了组织记忆层，在 Agent 执行过程中"静默"注入约束和偏好，无需修改底层 Step 逻辑。

### 8.1 信源可信度（Source Credibility）

系统自动追踪每个信源的历史表现，形成可信度评分：

```go
type SourceCredibility struct {
    ID          string
    Domain      string    // 信源域名
    TotalUses   int       // 总引用次数
    VerifiedRate float64  // 事实核查通过率
    AvgScore    float64   // 编辑评分均值
    Tier        string    // A | B | C | D
    UpdatedAt   time.Time
}
```

- 研究 Agent 引用信源时，自动读取可信度评分，优先选择高 Tier 信源
- 审校 Agent 核查后，自动更新信源可信度统计
- 前端"洞察"视图展示信源排行

### 8.2 栏目偏好（Column Preference）

按栏目维度注入写作约束，由历史录用稿件自动提炼：

```go
type ColumnPreference struct {
    ColumnTag     string   // 栏目标识
    Tone          string   // 语气风格
    BannedWords   []string // 禁用词
    WordCountMin  int      // 最小字数
    WordCountMax  int      // 最大字数
    UpdateFreq    string   // 更新频率
}
```

- 写作 Agent 执行前，自动注入栏目偏好到 `UserInput`
- 审校 Agent 检查是否符合栏目约束
- 偏好数据存储在 `editorial_column_preferences` 表

### 8.3 知识沉淀（Editorial Knowledge）

审校发现转化为可复用的编辑部知识：

```go
type EditorialKnowledge struct {
    ID          string
    Category    string  // style | fact | legal | format
    Title       string
    Content     string
    SourceTask  string  // 来源任务 ID
    Severity    string  // low | medium | high
    CreatedAt   time.Time
}
```

- 审校 Agent 发现的共性问题自动沉淀为知识条目
- 研究/写作 Agent 执行时检索相关知识条目注入上下文
- 前端"洞察"视图展示知识库

### 8.4 Agent 信誉（Agent Reputation）

跟踪每个 Agent 角色的执行历史和效果：

```go
type AgentReputation struct {
    ID              string
    AgentRole       string  // research | writing | review
    TotalExecutions int
    AvgTokenCost    int
    AvgDurationMs   int64
    PassRate        float64 // 产出通过率
    AvgQualityScore float64
    LastExecutedAt  time.Time
}
```

- 编排器根据 Agent 信誉选择执行策略
- 信誉低的 Agent 产出会触发额外人工审核
- 前端"洞察"视图展示 Agent 排行

## 9. 预算降级与动态路由（已实现）

### 9.1 Token 预算管理

每个 Task 携带 Token 预算，Agent 执行过程中实时追踪消耗：

```
TokenBudget = 300,000 (默认)
├── 研究 Agent: ~30% (90k)
├── 写作 Agent: ~50% (150k)
└── 审校 Agent: ~20% (60k)
```

- 预算使用超过 80% 时触发预警（前端看板标红）
- 预算耗尽时，编排器降级为快速模式（跳过可选步骤）
- 预算信息通过 `task.token_used / task.token_budget` 实时暴露

### 9.2 动态路由

编排器根据 Agent 产出内容动态决定下一步：

```
研究 Agent 产出
    │
    ├─ 包含 source_pack + fact_claims → 直接进入写作
    ├─ 仅 research_brief（纯文本） → 结构化后进入写作
    └─ 信源不足 → 回退研究（补充检索）

写作 Agent 产出
    │
    ├─ content 字段 + outline → 进入审校
    └─ 缺失关键字段 → 补充生成

审校 Agent 产出
    │
    ├─ review_report.passed = true → 进入待发布
    ├─ review_report.passed = false → 回退写作（附审查报告）
    └─ review_report.severity = high → 升级人工审核
```

关键实现位于 `orchestrator.go` 的 `routeAfterResearch` / `routeAfterWriting` / `routeAfterReview` 方法中。

## 10. 对照实验框架（已实现）

### 10.1 架构

```
ExperimentRunner
├── runPipelineMode()   — 固定 Pipeline (QueryPlan→Search→Write→Review)
├── runUnifiedMode()    — UnifiedAgent (ReAct 单 Agent 动态编排)
└── runEditorialMode()  — Editorial Multi-Agent (三 Agent 协作)
```

三组模式并行执行同一选题，采集多维指标：

| 指标 | 字段 |
|------|------|
| Token 成本 | `token_cost` |
| 执行耗时 | `duration_ms` |
| 文章字数 | `word_count` |
| 信源数量 | `source_count` |
| 审校通过 | `review_passed` |
| 问题数量 | `issue_count` |
| 质量评分 | `quality_score` (0.0-1.0) |
| 文章标题 | `article_title` |
| 文章摘要 | `article_excerpt` |

### 10.2 API

```
POST   /api/v2/editorial/experiments              创建实验
GET    /api/v2/editorial/experiments              列出实验
GET    /api/v2/editorial/experiments/{id}         实验详情
POST   /api/v2/editorial/experiments/{id}/run     运行实验（三组并行）
```

### 10.3 前端

编辑部界面"实验"视图支持：
- 创建实验（输入选题标题和描述）
- 一键运行实验（三组并行执行）
- 实时查看运行状态（运行中自动 10s 轮询刷新）
- 展开查看三组对比指标（Token、耗时、字数、信源、审校、质量）
- 汇总标签（最省 Token / 最快 / 质量最高）

## 11. 实际文件结构

```
backend/internal/editorial/
├── types.go                # 领域模型（Task, Artifact, Decision, Experiment 等）
├── store.go                # PostgreSQL 存储（Task/Artifact/Decision CRUD）
├── experiment_store.go     # 实验存储（Experiment CRUD）
├── memory_store.go         # 组织记忆存储（信源/栏目/知识/Agent 信誉）
├── service.go              # 编辑部服务（任务编排 + 实验管理 API）
├── orchestrator.go         # 三 Agent 编排器（动态路由 + 预算管理）
├── agent_executors.go      # Agent 执行器（记忆注入 + 产出结构化）
├── experiment_runner.go    # 实验运行器（三组并行 + 指标采集）
└── handlers.go             # HTTP 处理器

frontend/src/
├── pages/editorial/
│   └── editorial-board.tsx  # 编辑部工作台（决策台 + 看板 + 洞察 + 实验）
├── stores/
│   └── editorial-store.ts   # 编辑部状态管理（含实验状态）
```

## 12. 数据库迁移

| 迁移文件 | 说明 |
|----------|------|
| `031_editorial_tasks` | 编辑部任务表 |
| `032_editorial_artifacts` | 交付物表（版本化） |
| `033_editorial_decisions` | 决策记录表 |
| `034_editorial_experiments` | 对照实验表 |
| `035_editorial_source_credibility` | 信源可信度表 |
| `036_editorial_column_preferences` | 栏目偏好表 |
| `037_editorial_knowledge` | 编辑部知识沉淀表 |
| `038_editorial_agent_reputation` | Agent 信誉表 |
