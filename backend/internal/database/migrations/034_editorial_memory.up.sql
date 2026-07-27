-- 编辑部组织记忆系统：信源可信度 + 栏目偏好 + 审稿标准沉淀

-- 信源可信度表 — 记录每个信源的历史使用情况和可信度评分
CREATE TABLE IF NOT EXISTS editorial_source_credibility (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    source_domain   VARCHAR(256) NOT NULL,       -- 信源域名（如 "finance.sina.com.cn"）
    source_name     VARCHAR(128) NOT NULL DEFAULT '', -- 信源显示名
    category        VARCHAR(64) NOT NULL DEFAULT 'general', -- 分类: news | gov | academic | social | blog
    credibility_score  FLOAT NOT NULL DEFAULT 0.5,    -- 可信度评分 0.0-1.0
    total_uses      INTEGER NOT NULL DEFAULT 0,       -- 总使用次数
    verified_count  INTEGER NOT NULL DEFAULT 0,       -- 被验证为真的次数
    refuted_count   INTEGER NOT NULL DEFAULT 0,       -- 被证伪的次数
    last_task_id    UUID,                             -- 最后一次使用的任务
    notes           TEXT NOT NULL DEFAULT '',          -- 编辑备注
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_source_domain UNIQUE (source_domain)
);

-- 栏目偏好表 — 每个栏目（标签）的写作偏好和审稿标准
CREATE TABLE IF NOT EXISTS editorial_column_preferences (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    column_tag      VARCHAR(64) NOT NULL,              -- 栏目标签（如 "科技" "财经" "时评"）
    style_slug      VARCHAR(64) NOT NULL DEFAULT 'yinyue', -- 关联的风格 Profile
    preferred_length_min INTEGER NOT NULL DEFAULT 500,  -- 偏好字数下限
    preferred_length_max INTEGER NOT NULL DEFAULT 2000, -- 偏好字数上限
    tone            VARCHAR(32) NOT NULL DEFAULT '',    -- 偏好语气
    forbidden_words TEXT[] NOT NULL DEFAULT '{}',       -- 禁用词列表
    review_criteria TEXT NOT NULL DEFAULT '',           -- 审稿标准（自由文本）
    acceptance_rate FLOAT NOT NULL DEFAULT 0.0,         -- 历史通过率
    total_tasks     INTEGER NOT NULL DEFAULT 0,         -- 总任务数
    published_count INTEGER NOT NULL DEFAULT 0,         -- 已发布数
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_column_tag UNIQUE (column_tag)
);

-- 编辑部知识沉淀表 — 审稿经验、退稿原因、常见问题
CREATE TABLE IF NOT EXISTS editorial_knowledge (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    category        VARCHAR(64) NOT NULL,              -- rejection_reason | review_tip | style_guideline | fact_check_note
    column_tag      VARCHAR(64) NOT NULL DEFAULT '',   -- 关联栏目（空表示通用）
    title           VARCHAR(256) NOT NULL,
    content         TEXT NOT NULL,
    source_task_id  UUID,                              -- 来源任务
    source_artifact_id UUID,                           -- 来源交付物
    confidence      FLOAT NOT NULL DEFAULT 0.5,        -- 置信度
    occurrence_count INTEGER NOT NULL DEFAULT 1,        -- 出现次数（同类问题反复出现则递增）
    status          VARCHAR(16) NOT NULL DEFAULT 'active', -- active | archived
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Agent 信誉表 — 记录每个 Agent 角色的执行历史和信誉评分
CREATE TABLE IF NOT EXISTS editorial_agent_reputation (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    agent_role      VARCHAR(32) NOT NULL,              -- research_agent | writing_agent | review_agent
    total_executions INTEGER NOT NULL DEFAULT 0,        -- 总执行次数
    success_count   INTEGER NOT NULL DEFAULT 0,         -- 成功次数
    failure_count   INTEGER NOT NULL DEFAULT 0,         -- 失败次数
    avg_token_cost  INTEGER NOT NULL DEFAULT 0,         -- 平均 Token 消耗
    avg_quality_score FLOAT NOT NULL DEFAULT 0.0,       -- 平均质量评分（0.0-1.0）
    avg_duration_ms INTEGER NOT NULL DEFAULT 0,         -- 平均执行时长
    last_task_id    UUID,                              -- 最后执行的任务
    last_execution_at TIMESTAMPTZ,                     -- 最后执行时间
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_agent_role UNIQUE (agent_role)
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_source_credibility_category ON editorial_source_credibility(category);
CREATE INDEX IF NOT EXISTS idx_source_credibility_score ON editorial_source_credibility(credibility_score DESC);
CREATE INDEX IF NOT EXISTS idx_column_pref_tag ON editorial_column_preferences(column_tag);
CREATE INDEX IF NOT EXISTS idx_editorial_knowledge_cat ON editorial_knowledge(category, column_tag);
CREATE INDEX IF NOT EXISTS idx_editorial_knowledge_status ON editorial_knowledge(status);
CREATE INDEX IF NOT EXISTS idx_agent_reputation_role ON editorial_agent_reputation(agent_role);
