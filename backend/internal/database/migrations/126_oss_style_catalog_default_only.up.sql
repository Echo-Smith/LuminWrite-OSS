-- Migration: OSS style catalog — first-party editorial styles out (docs/04 §3)
--
-- 「引擎开源、内容自有」：编辑风格指南（印月三谈/申论/小红书）属于第一方
-- 内容资产，有意不进入开源仓库；OSS 全新安装仅内置引擎级通用骨架 default
-- （三段式结构 + 事实约束，无栏目定位、无范文语料），保证开箱可用。
--
-- 背景：早期版本的 Go loader（getBuiltinProfiles）曾内置三个第一方编辑
-- 风格并在空库时种子入库；本迁移把它们从既有库中移除，使库内目录与 OSS
-- 内容政策一致（商业版另行保留，见商业版 migration 118）。
--
-- 范围说明：
--   * profile_versions 经外键 ON DELETE CASCADE 随主表一并清除；
--   * user_style_profiles / user_style_profile_versions 是用户自有数据
--     （用户可能导入/复制过同名风格），不在本迁移范围；
--   * agent_traces / writing_runs / evaluation_* 中的历史 style_slug 保持
--     原值（不可变历史）；解析器对未知 slug 按既定语义回退 default/中性
--     prompt，不会失败。

BEGIN;

-- 1. 移除三个第一方编辑风格（版本快照经 CASCADE 一并删除）
DELETE FROM style_profiles
WHERE slug IN ('yinyue', 'shenlun', 'xiaohongshu');

-- 2. 确保引擎级 default 骨架存在（与 Go getBuiltinProfiles 的 defaultJSON
--    逐字同源；ON CONFLICT DO NOTHING 保证对已存在行的幂等）
INSERT INTO style_profiles (
    id, slug, name, description, version, status, config,
    rollout_type, whitelist_uids, rollout_percent,
    published_at, published_by, created_at, updated_at
)
VALUES (
    uuid_generate_v4(),
    'default',
    '通用写作风格',
    '适用于大多数场景的通用写作风格，结构清晰、逻辑连贯',
    1,
    'published',
    '{
  "name": "通用写作风格",
  "slug": "default",
  "tags": ["通用", "默认"],
  "version": 1,
  "rhetoric": {
    "required_metaphor": false,
    "metaphor_description": "",
    "required_parallelism": false,
    "required_rhetorical_question": false
  },
  "structure": {
    "body": "展开论述，深入分析",
    "type": "three_part",
    "opening": "引入主题，概述背景",
    "conclusion": "总结要点，升华主题",
    "argument_count": {"max": 4, "min": 2},
    "argument_pattern": "引入→展开→总结",
    "argument_variations": null,
    "argument_instruction": ""
  },
  "fact_guard": {
    "forbidden_results": [],
    "future_tense_required": ["将", "即将", "预计", "计划", "拟"],
    "user_material_priority": true
  },
  "word_range": {"max": 1500, "min": 800, "hard_limit": false},
  "description": "适用于大多数场景的通用写作风格，结构清晰、逻辑连贯",
  "output_format": {
    "separator": "---MODIFICATIONS---",
    "note_label": "",
    "title_prefix": "## ",
    "use_markdown": true,
    "include_modification_notes": false
  },
  "system_prompt": "你是 Lumi，一位专业的写作助手。你的写作风格：\n\n1. **结构清晰**：采用「引入→展开→总结」的三段式结构，逻辑连贯\n2. **论述充分**：每个观点都有充分的论据支撑，避免空洞陈述\n3. **语言准确**：用词精准，表达流畅，避免模糊和歧义\n4. **贴合素材**：严格基于用户提供的素材进行创作，不编造事实\n5. **适度修辞**：根据内容需要灵活运用修辞手法，不刻意堆砌\n6. **格式规范**：输出标准 Markdown 格式，标题、段落层次分明\n\n请根据用户的要求和提供的素材，创作一篇结构完整、内容充实的文章。",
  "length_profiles": {
    "writing": {"max": 1500, "min": 800, "hard_limit": false},
    "polish_long": {"max": 1200, "min": 600, "hard_limit": false},
    "polish_short": {"max": 600, "min": 100, "hard_limit": false}
  },
  "title_guidelines": {
    "style": "简洁明确，概括主题",
    "length": {"max": 25, "min": 8},
    "examples": ["从现象到本质：关于XX的思考", "探索XX背后的逻辑"],
    "forbidden_patterns": []
  },
  "writing_standard": "篇幅 800-1500 字，结构完整，论述充分，语言流畅",
  "value_orientation": {
    "type": "balanced",
    "keywords": ["思考", "探索", "理解", "洞察"],
    "emotional_gradient": "客观→深入→启发"
  }
}'::jsonb,
    'full',
    '{}',
    100,
    NOW(),
    'system',
    NOW(),
    NOW()
)
ON CONFLICT (slug) DO NOTHING;

INSERT INTO profile_versions (id, profile_slug, version, config, changelog, status, published_at, created_at, created_by)
SELECT uuid_generate_v4(), 'default', 1, config, 'Initial seed', 'published', NOW(), NOW(), 'system'
FROM style_profiles
WHERE slug = 'default'
ON CONFLICT (profile_slug, version) DO NOTHING;

COMMIT;
