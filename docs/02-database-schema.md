# 数据库 Schema 设计

## 概述

- 数据库：PostgreSQL 17
- 向量扩展：pgvector
- 迁移工具：golang-migrate
- 字符集：UTF-8

## 扩展安装

```sql
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "vector";
```

## 表设计

### 1. style_profiles — 风格 Profile

```sql
CREATE TABLE style_profiles (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    slug            VARCHAR(64) UNIQUE NOT NULL,        -- 如 'yinyue', 'shenlun', 'xiaohongshu'
    name            VARCHAR(128) NOT NULL,               -- 显示名称
    description     TEXT,
    version         INTEGER NOT NULL DEFAULT 1,          -- 版本号
    status          VARCHAR(16) NOT NULL DEFAULT 'draft',-- draft | published | archived

    -- Profile 核心内容 (JSONB)
    config          JSONB NOT NULL,                      -- 风格配置（见 Style Profile 文档）

    -- 灰度标记 (高优先级)
    rollout_type    VARCHAR(16) NOT NULL DEFAULT 'full', -- full | whitelist | percentage
    whitelist_uids  TEXT[] DEFAULT '{}',                 -- rollout_type=whitelist 时的 UID 列表
    rollout_percent INTEGER DEFAULT 100,                 -- rollout_type=percentage 时的百分比

    -- 发布信息
    published_at    TIMESTAMPTZ,
    published_by    VARCHAR(64),

    -- 时间戳
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- 版本唯一约束：同一 slug 同一 version 只能有一个
    CONSTRAINT uk_style_slug_version UNIQUE (slug, version)
);

-- 查询当前生效的 Profile
CREATE INDEX idx_style_profiles_slug_status ON style_profiles (slug, status) WHERE status = 'published';
```

### 2. profile_versions — Profile 版本历史

```sql
CREATE TABLE profile_versions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    profile_slug    VARCHAR(64) NOT NULL,
    version         INTEGER NOT NULL,
    config          JSONB NOT NULL,
    changelog       TEXT,
    status          VARCHAR(16) NOT NULL DEFAULT 'draft',
    published_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      VARCHAR(64),

    CONSTRAINT fk_profile_versions_slug FOREIGN KEY (profile_slug)
        REFERENCES style_profiles (slug) ON DELETE CASCADE,
    CONSTRAINT uk_profile_versions UNIQUE (profile_slug, version)
);
```

### 3. users — 用户表

```sql
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    uid             VARCHAR(64) UNIQUE NOT NULL,         -- 外部 UID
    name            VARCHAR(128),
    email           VARCHAR(256),
    reputation      DECIMAL(5,2) NOT NULL DEFAULT 1.00,  -- 信誉权重 (0.00-100.00)
    adoption_count  INTEGER NOT NULL DEFAULT 0,           -- 累计录用次数
    feedback_count  INTEGER NOT NULL DEFAULT 0,           -- 累计反馈次数
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_uid ON users (uid);
```

### 4. topics — 选题中心

```sql
CREATE TABLE topics (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title           VARCHAR(256) NOT NULL,
    description     TEXT,
    source          VARCHAR(64) NOT NULL DEFAULT 'system', -- system | user | hotlist
    source_uid      VARCHAR(64),                           -- 来源用户（source=user 时）
    platform        VARCHAR(32),                           -- zhihu | weibo | baidu | custom
    hot_rank        INTEGER,                               -- 热搜排名
    raw_data        JSONB,                                 -- 原始数据
    fetched_at      TIMESTAMPTZ,
    status          VARCHAR(16) NOT NULL DEFAULT 'active', -- active | archived
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_topics_source_status ON topics (source, status);
CREATE INDEX idx_topics_fetched_at ON topics (fetched_at DESC);
```

### 5. knowledge_base — 知识库 (pgvector)

```sql
CREATE TABLE knowledge_base (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    source          VARCHAR(32) NOT NULL,                  -- ima | user_upload | web_crawl
    source_id       VARCHAR(256),                          -- IMA 文档 ID / 用户上传 ID
    title           VARCHAR(512) NOT NULL,
    content         TEXT NOT NULL,
    content_hash    VARCHAR(64) NOT NULL,                  -- 用于去重
    embedding       vector(1024),                          -- 通义 text-embedding-v3 输出维度
    metadata        JSONB DEFAULT '{}',                    -- 额外元数据
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_kb_content_hash UNIQUE (content_hash)
);

-- 向量相似度检索索引 (IVFFlat)
CREATE INDEX idx_kb_embedding ON knowledge_base
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- 全文检索
CREATE INDEX idx_kb_content_fts ON knowledge_base
    USING gin (to_tsvector('simple', content));
```

### 6. agent_traces — Agent 执行记录

```sql
CREATE TABLE agent_traces (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    trace_id        VARCHAR(64) UNIQUE NOT NULL,
    user_id         UUID REFERENCES users (id),
    session_id      VARCHAR(64),

    -- 输入
    user_input      TEXT NOT NULL,
    style_slug      VARCHAR(64),
    mode            VARCHAR(16) NOT NULL DEFAULT 'auto',   -- auto | writing | guided | polish

    -- 执行状态
    status          VARCHAR(16) NOT NULL DEFAULT 'running',-- running | paused | completed | failed | cancelled
    current_step    VARCHAR(64),
    step_history    JSONB DEFAULT '[]',                     -- [{step, status, startedAt, completedAt, durationMs, result}]

    -- 输出
    article         TEXT,
    review_result   JSONB,

    -- 元数据
    token_usage     JSONB DEFAULT '{}',                    -- {prompt_tokens, completion_tokens, total_tokens}
    duration_ms     INTEGER,
    error           TEXT,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);

CREATE INDEX idx_traces_user_id ON agent_traces (user_id);
CREATE INDEX idx_traces_status ON agent_traces (status);
CREATE INDEX idx_traces_created_at ON agent_traces (created_at DESC);
```

### 7. feedback_segments — 分段反馈

```sql
CREATE TABLE feedback_segments (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    trace_id        VARCHAR(64) NOT NULL REFERENCES agent_traces (trace_id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users (id),

    -- 反馈定位
    segment_type    VARCHAR(16) NOT NULL,                  -- title | paragraph | sentence | overall
    segment_index   INTEGER,                               -- 段落/句子序号
    segment_text    TEXT,                                  -- 反馈对应的原文片段

    -- 反馈内容
    rating          INTEGER NOT NULL,                      -- 1-5 星
    feedback_type   VARCHAR(32),                           -- good | bad | suggestion
    comment         TEXT,                                  -- 用户文字反馈

    -- 信誉权重快照（反馈时的用户信誉）
    user_reputation DECIMAL(5,2) NOT NULL DEFAULT 1.00,

    -- 录用标记 (来自 workbuddy)
    is_adopted      BOOLEAN NOT NULL DEFAULT FALSE,        -- 是否被知识库录用
    adopted_at      TIMESTAMPTZ,
    adopted_source  VARCHAR(64),                           -- workbuddy

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_feedback_trace_id ON feedback_segments (trace_id);
CREATE INDEX idx_feedback_user_id ON feedback_segments (user_id);
CREATE INDEX idx_feedback_style ON feedback_segments (feedback_type);
CREATE INDEX idx_feedback_adopted ON feedback_segments (is_adopted) WHERE is_adopted = TRUE;
```

### 8. feedback_aggregation — 反馈聚合 (风格迭代用)

```sql
CREATE TABLE feedback_aggregation (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    style_slug      VARCHAR(64) NOT NULL,
    profile_version INTEGER NOT NULL,

    -- 聚合统计
    total_feedback  INTEGER NOT NULL DEFAULT 0,
    total_adopted   INTEGER NOT NULL DEFAULT 0,
    avg_rating      DECIMAL(3,2) NOT NULL DEFAULT 0.00,
    weighted_score  DECIMAL(5,2) NOT NULL DEFAULT 0.00,   -- 信誉加权后的得分

    -- 分维度统计
    dimension_scores JSONB DEFAULT '{}',                   -- {title: 4.2, structure: 3.8, ...}

    -- 是否达到迭代阈值
    ready_for_iteration BOOLEAN NOT NULL DEFAULT FALSE,
    iteration_threshold INTEGER NOT NULL DEFAULT 30,       -- 累计反馈达到此数量可迭代

    period_start   TIMESTAMPTZ NOT NULL,
    period_end     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uk_feedback_agg UNIQUE (style_slug, profile_version, period_start)
);
```

### 9. evaluation_sets — 评测集

```sql
CREATE TABLE evaluation_sets (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name            VARCHAR(128) NOT NULL,
    style_slug      VARCHAR(64) NOT NULL,
    description     TEXT,

    -- 评测集状态
    status          VARCHAR(16) NOT NULL DEFAULT 'draft', -- draft | ready | archived
    sample_count    INTEGER NOT NULL DEFAULT 0,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_eval_sets_style ON evaluation_sets (style_slug);
```

### 10. evaluation_samples — 评测样本

```sql
CREATE TABLE evaluation_samples (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    set_id          UUID NOT NULL REFERENCES evaluation_sets (id) ON DELETE CASCADE,

    -- 样本内容
    topic           VARCHAR(256) NOT NULL,                 -- 评测题目
    input_prompt    TEXT NOT NULL,                         -- 完整输入
    style_slug      VARCHAR(64) NOT NULL,

    -- 人工标注
    expected_structure  JSONB,                             -- 期望结构
    expected_keywords   TEXT[],                            -- 期望关键词
    expected_length     INTEGER,                           -- 期望字数
    red_flags           TEXT[],                            -- 禁止出现的表述
    annotator           VARCHAR(64),                       -- 标注人
    annotation_notes    TEXT,

    -- 评分标准
    scoring_criteria    JSONB NOT NULL,                    -- {factuality: weight, structure: weight, ...}

    status              VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending | annotated | reviewed
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_eval_samples_set_id ON evaluation_samples (set_id);
CREATE INDEX idx_eval_samples_style ON evaluation_samples (style_slug);
```

### 11. evaluation_runs — 评测执行记录

```sql
CREATE TABLE evaluation_runs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    set_id          UUID NOT NULL REFERENCES evaluation_sets (id),
    profile_slug    VARCHAR(64) NOT NULL,
    profile_version INTEGER NOT NULL,

    -- 触发原因
    trigger_type    VARCHAR(32) NOT NULL,                  -- profile_change | manual | scheduled
    trigger_detail  TEXT,

    -- 评测结果
    status          VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending | running | completed | failed
    total_samples   INTEGER NOT NULL DEFAULT 0,
    completed_count INTEGER NOT NULL DEFAULT 0,
    results         JSONB DEFAULT '[]',                     -- [{sample_id, scores, issues}]

    -- 聚合分数
    overall_score   DECIMAL(5,2),
    dimension_scores JSONB DEFAULT '{}',

    -- 导出信息 (第三方平台)
    export_url      VARCHAR(512),                          -- 导出到第三方平台的链接
    external_run_id VARCHAR(128),

    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_eval_runs_profile ON evaluation_runs (profile_slug, profile_version);
```

### 12. api_keys — API 密钥管理 (Admin)

```sql
CREATE TABLE api_keys (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    provider        VARCHAR(32) NOT NULL,                  -- deepseek | zhihu | ima | tavily | dashscope
    key_name        VARCHAR(128) NOT NULL,
    encrypted_key   TEXT NOT NULL,                         -- AES-256 加密存储
    base_url        VARCHAR(512),
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 13. token_usage — Token 用量统计

```sql
CREATE TABLE token_usage (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    trace_id        VARCHAR(64),
    user_id         UUID REFERENCES users (id),
    provider        VARCHAR(32) NOT NULL,                  -- deepseek | dashscope
    model           VARCHAR(64) NOT NULL,
    prompt_tokens   INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens    INTEGER NOT NULL DEFAULT 0,
    cost_usd        DECIMAL(10,4) DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_token_usage_user_id ON token_usage (user_id);
CREATE INDEX idx_token_usage_created_at ON token_usage (created_at DESC);
CREATE INDEX idx_token_usage_provider_model ON token_usage (provider, model);
```

### 14. cron_jobs — 自定义定时任务 (Admin)

```sql
CREATE TABLE cron_jobs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name            VARCHAR(128) NOT NULL,
    description     TEXT,
    schedule        VARCHAR(128) NOT NULL,                 -- cron 表达式
    job_type        VARCHAR(32) NOT NULL,                  -- ima_sync | evaluation | hotlist | custom
    job_config      JSONB DEFAULT '{}',                    -- 任务参数
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at     TIMESTAMPTZ,
    last_run_status VARCHAR(16),                           -- success | failed | running
    last_run_error  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 15. model_configs — 模型配置 (Admin)

```sql
CREATE TABLE model_configs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    provider        VARCHAR(32) NOT NULL,                  -- deepseek | dashscope
    model_id        VARCHAR(64) NOT NULL,                  -- deepseek-v4-flash, deepseek-v4-pro
    display_name    VARCHAR(128) NOT NULL,
    is_thinking     BOOLEAN NOT NULL DEFAULT FALSE,
    temperature     DECIMAL(3,2) NOT NULL DEFAULT 0.70,
    max_tokens      INTEGER NOT NULL DEFAULT 16000,
    timeout_ms      INTEGER NOT NULL DEFAULT 120000,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    is_default      BOOLEAN NOT NULL DEFAULT FALSE,
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 16. sensitive_words — 敏感词库 (Admin 可编辑)

```sql
CREATE TABLE sensitive_words (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    word            VARCHAR(128) NOT NULL,
    category        VARCHAR(32) NOT NULL,                  -- political | violence | privacy | clickbait
    severity        VARCHAR(16) NOT NULL DEFAULT 'medium', -- low | medium | high
    action          VARCHAR(16) NOT NULL DEFAULT 'warn',   -- warn | block | replace
    replacement     VARCHAR(128),                          -- action=replace 时的替换词
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sensitive_words_category ON sensitive_words (category, is_active);
CREATE INDEX idx_sensitive_words_severity ON sensitive_words (severity, is_active);
```

### 17. conversation_messages — 对话消息（短期记忆）

存储每个会话中的用户/助手消息，支持语义检索和动态窗口。属于短期记忆系统，超过 30 天的记录自动清理。

```sql
CREATE TABLE IF NOT EXISTS conversation_messages (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    conversation_id VARCHAR(64) NOT NULL,           -- 前端会话 ID，同一会话内的消息组成一段对话
    user_id         UUID REFERENCES users(id) ON DELETE CASCADE,
    trace_id        VARCHAR(64),                     -- 关联的 agent_traces.trace_id

    role            VARCHAR(16) NOT NULL,            -- user | assistant | system
    content         TEXT NOT NULL,
    content_type    VARCHAR(16) NOT NULL DEFAULT 'text', -- text | article | review | outline
    intent          VARCHAR(32) NOT NULL DEFAULT '',  -- writing | chat | polish | ...

    -- 语义检索
    embedding       vector(1024),

    -- Token 预算管理
    token_count     INTEGER NOT NULL DEFAULT 0,

    -- 元数据
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_conv_messages_conversation
    ON conversation_messages (conversation_id, created_at);

CREATE INDEX IF NOT EXISTS idx_conv_messages_user
    ON conversation_messages (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_conv_messages_embedding
    ON conversation_messages
    USING hnsw (embedding vector_cosine_ops);

CREATE INDEX IF NOT EXISTS idx_conv_messages_created_at
    ON conversation_messages (created_at);
```

### 18. memory_entities — 记忆实体（长期记忆网络）

实体记忆网络将用户偏好、话题、风格等建模为图结构的节点。

```sql
CREATE TABLE IF NOT EXISTS memory_entities (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- 实体分类
    entity_type     VARCHAR(32) NOT NULL,            -- topic | style | preference | concept | person | tone | structure
    name            VARCHAR(256) NOT NULL,            -- 实体名称（如"短文风格"、"科技话题"）
    description     TEXT NOT NULL DEFAULT '',         -- 实体描述

    -- 语义检索
    embedding       vector(1024),

    -- 置信度与生命周期
    confidence      DECIMAL(3,2) NOT NULL DEFAULT 0.50,
    occurrence_count INTEGER NOT NULL DEFAULT 1,
    source_trace_id VARCHAR(64),

    -- 状态
    status          VARCHAR(16) NOT NULL DEFAULT 'active', -- active | superseded | archived
    superseded_by   UUID REFERENCES memory_entities(id),

    -- 时间
    first_seen      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- 约束：同一用户同一类型+名称只有一条活跃记录
    CONSTRAINT uk_user_entity UNIQUE (user_id, entity_type, name, status)
);

CREATE INDEX IF NOT EXISTS idx_entities_user_active
    ON memory_entities (user_id, status)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_entities_user_type
    ON memory_entities (user_id, entity_type, status);

CREATE INDEX IF NOT EXISTS idx_entities_embedding
    ON memory_entities
    USING hnsw (embedding vector_cosine_ops);
```

### 19. memory_relations — 记忆关系（长期记忆网络边）

实体间的关系建模为图结构的边，支持偏好、共现、对比等关系类型。

```sql
CREATE TABLE IF NOT EXISTS memory_relations (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    source_entity_id UUID NOT NULL REFERENCES memory_entities(id) ON DELETE CASCADE,
    target_entity_id UUID NOT NULL REFERENCES memory_entities(id) ON DELETE CASCADE,

    -- 关系类型
    relation_type   VARCHAR(32) NOT NULL,            -- prefers | dislikes | related_to | evolved_from | co_occurs_with | contrasts_with
    weight          DECIMAL(3,2) NOT NULL DEFAULT 0.50, -- 关系强度 0.0-1.0
    evidence_count  INTEGER NOT NULL DEFAULT 1,       -- 支持该关系的证据数量
    source_trace_id VARCHAR(64),

    -- 时间
    first_seen      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- 约束：同一用户同一关系类型+source+target 只有一条记录
    CONSTRAINT uk_user_relation UNIQUE (user_id, source_entity_id, target_entity_id, relation_type)
);

CREATE INDEX IF NOT EXISTS idx_relations_user
    ON memory_relations (user_id);

CREATE INDEX IF NOT EXISTS idx_relations_source
    ON memory_relations (source_entity_id);

CREATE INDEX IF NOT EXISTS idx_relations_target
    ON memory_relations (target_entity_id);
```

### 20. working_summaries — 工作记忆摘要

持久化每次执行的增量摘要，跨请求继承工作记忆，使新请求能参考上一轮的执行上下文。

```sql
CREATE TABLE IF NOT EXISTS working_summaries (
    conversation_id  VARCHAR(64) NOT NULL,
    trace_id         VARCHAR(64),
    summary          JSONB NOT NULL,
    last_updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conversation_id)
);

CREATE INDEX IF NOT EXISTS idx_working_summaries_conversation
    ON working_summaries (conversation_id);
```

### 28. editorial_tasks — 编辑部任务

编辑部工作流的核心实体，代表一个选题从立项到发布的完整生命周期。

```sql
CREATE TABLE editorial_tasks (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title           VARCHAR(256) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    owner_id        UUID REFERENCES users(id),
    assignee_type   VARCHAR(32) NOT NULL DEFAULT 'human', -- human | research_agent | writing_agent | review_agent
    deadline        TIMESTAMPTZ,
    status          VARCHAR(32) NOT NULL DEFAULT 'draft', -- draft | pending_approval | research | writing | review | pending_publish | published | archived
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

### 29. editorial_artifacts — 编辑部交付物

Agent 之间传递的结构化交付物，每个交付物有版本控制和审批状态。

```sql
CREATE TABLE editorial_artifacts (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    task_id     UUID NOT NULL REFERENCES editorial_tasks(id) ON DELETE CASCADE,
    type        VARCHAR(32) NOT NULL, -- topic_card | research_brief | source_pack | fact_claims | outline | draft | review_report | revised_draft
    version     INTEGER NOT NULL DEFAULT 1,
    content     JSONB NOT NULL DEFAULT '{}',
    status      VARCHAR(16) NOT NULL DEFAULT 'draft', -- draft | submitted | approved | rejected | superseded
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

### 30. editorial_decisions — 编辑部决策

记录每篇稿件的决策链：谁、在什么角色下、做了什么决策、依据是什么。

```sql
CREATE TABLE editorial_decisions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    task_id         UUID NOT NULL REFERENCES editorial_tasks(id) ON DELETE CASCADE,
    type            VARCHAR(32) NOT NULL, -- approve_topic | select_angle | trust_source | accept_review | allow_rewrite | publish | escalate
    decided_by      UUID REFERENCES users(id),
    decided_by_type VARCHAR(32) NOT NULL, -- human | research_agent | writing_agent | review_agent | system
    status          VARCHAR(16) NOT NULL DEFAULT 'pending', -- pending | approved | rejected | escalated
    rationale       TEXT NOT NULL DEFAULT '',
    evidence        TEXT NOT NULL DEFAULT '',
    artifact_id     UUID REFERENCES editorial_artifacts(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at      TIMESTAMPTZ
);
```

## 初始数据

```sql
-- 默认模型配置
INSERT INTO model_configs (provider, model_id, display_name, is_thinking, is_default)
VALUES
    ('deepseek', 'deepseek-v4-flash', 'Flash', FALSE, TRUE),
    ('deepseek', 'deepseek-v4-pro', 'Pro (思考模式)', TRUE, FALSE);

-- 默认 IMA 同步定时任务
INSERT INTO cron_jobs (name, description, schedule, job_type, job_config, is_active)
VALUES
    ('IMA 知识库同步', '每日 17:00 拉取 IMA 最新知识库内容', '0 17 * * *', 'ima_sync', '{}', TRUE);
```
