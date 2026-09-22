-- 011: Seed evaluation sets and samples for built-in styles (yinyue, shenlun, xiaohongshu)

-- ─── Helper: insert evaluation set only if none exist for this style ───
-- We use ON CONFLICT-free approach with WHERE NOT EXISTS to avoid duplicate inserts.

-- ═══════════════════════════════════════════════════════════
-- Set 1: 印月三谈 — 深度时评评测集
-- ═══════════════════════════════════════════════════════════
INSERT INTO evaluation_sets (id, name, style_slug, description, status, sample_count, created_at, updated_at)
SELECT
    uuid_generate_v4(),
    '印月三谈·深度时评评测集',
    'yinyue',
    '覆盖民生温度、城市治理、社会现象等典型时评选题，验证三段式结构、首在-重在-贵在递进模式、核心比喻贯穿、排比+设问修辞等风格要素。',
    'published',
    8,
    NOW(),
    NOW()
WHERE NOT EXISTS (
    SELECT 1 FROM evaluation_sets WHERE style_slug = 'yinyue' AND name = '印月三谈·深度时评评测集'
);

-- Samples for yinyue set
DO $$
DECLARE
    v_set_id UUID;
BEGIN
    SELECT id INTO v_set_id FROM evaluation_sets WHERE style_slug = 'yinyue' AND name = '印月三谈·深度时评评测集' LIMIT 1;
    IF v_set_id IS NULL THEN RETURN; END IF;

    -- Sample 1: 外卖骑手困境
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '外卖骑手的红灯困境',
        '近日，多地外卖骑手闯红灯引发交通事故的消息引发社会关注。据报道，部分平台对超时订单的处罚机制迫使骑手在安全和效率之间做出冒险选择。请就这一现象撰写一篇深度时评。',
        'yinyue',
        '{"type": "three_part", "opening": "现象点题", "body": "分层论述", "conclusion": "总结升华", "argument_pattern": "首在-重在-贵在"}'::jsonb,
        ARRAY['平台', '骑手', '算法', '安全', '制度'],
        1200,
        ARRAY['煽动性标题', '伤亡数字做标题', '情绪化攻击平台', '无建设性建议'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 2: 城市温度
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '城市温度，从一条背篓专线说起',
        '重庆地铁4号线专门为背着背篓进城卖菜的农民开设"背篓专线"，早班车提前发车、设置专属车厢。此举引发网友热议，被称为"城市温度的体现"。请就此撰写一篇时评。',
        'yinyue',
        '{"type": "three_part", "opening": "现象点题", "body": "分层论述", "conclusion": "总结升华", "argument_pattern": "首在-重在-贵在"}'::jsonb,
        ARRAY['背篓专线', '城市温度', '民生', '公共服务', '包容'],
        1200,
        ARRAY['过度赞美缺乏思考', '忽略实际问题', '无核心比喻'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 3: 老龄化与数字鸿沟
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '银发群体与数字鸿沟',
        '随着社会全面数字化，老年人面临挂号难、打车难、支付难等问题。多地开始探索"适老化"改造方案，但推进进度参差不齐。请就此现象撰写深度时评。',
        'yinyue',
        '{"type": "three_part", "opening": "现象点题", "body": "分层论述", "conclusion": "总结升华", "argument_pattern": "首在-重在-贵在"}'::jsonb,
        ARRAY['老年人', '数字鸿沟', '适老化', '公共服务', '技术普惠'],
        1300,
        ARRAY['将老年人描述为负担', '技术决定论', '缺乏共情'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 4: 教育内卷
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '教育减负的根本之策',
        '"双减"政策实施以来，校外培训大幅减少，但家长焦虑并未完全消退，部分培训转入地下。如何从根源上破解教育内卷问题，引发广泛讨论。请就此撰写一篇时评。',
        'yinyue',
        '{"type": "three_part", "opening": "现象点题", "body": "分层论述", "conclusion": "总结升华", "argument_pattern": "首在-重在-贵在"}'::jsonb,
        ARRAY['双减', '教育内卷', '评价体系', '资源均衡', '根本之策'],
        1200,
        ARRAY['简单否定政策', '片面归因', '无建设性对策'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 5: 乡村振兴
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '乡村振兴中的人才回流',
        '近年来，越来越多年轻人选择返乡创业，带动了乡村产业振兴。但也面临基础设施不足、融资困难等挑战。请就此现象撰写一篇深度时评。',
        'yinyue',
        '{"type": "three_part", "opening": "现象点题", "body": "分层论述", "conclusion": "总结升华", "argument_pattern": "首在-重在-贵在"}'::jsonb,
        ARRAY['乡村振兴', '人才回流', '创业', '基础设施', '政策支持'],
        1200,
        ARRAY['过度浪漫化乡村生活', '忽视现实困难', '缺乏具体建议'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 6: 共享经济治理
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '共享单车乱象的治理之道',
        '共享单车在解决"最后一公里"出行问题的同时，也带来了乱停乱放、车辆损坏、企业退出后押金难退等问题。多地出台管理细则，但执行效果不一。请就此撰写时评。',
        'yinyue',
        '{"type": "three_part", "opening": "现象点题", "body": "分层论述", "conclusion": "总结升华", "argument_pattern": "首在-重在-贵在"}'::jsonb,
        ARRAY['共享单车', '治理', '城市管理', '企业责任', '精细化'],
        1100,
        ARRAY['一味否定共享经济', '只批评不建设', '无核心比喻'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 7: 灵活就业权益
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '灵活就业者的权益保障',
        '网约车司机、外卖骑手、直播主播等灵活就业群体规模已超2亿人，但其社保缴纳、劳动关系认定、工伤赔偿等权益保障存在明显空白。请就此撰写深度时评。',
        'yinyue',
        '{"type": "three_part", "opening": "现象点题", "body": "分层论述", "conclusion": "总结升华", "argument_pattern": "首在-重在-贵在"}'::jsonb,
        ARRAY['灵活就业', '权益保障', '社保', '劳动关系', '制度创新'],
        1300,
        ARRAY['将平台与劳动者对立', '忽视灵活就业的正面价值', '情绪化表述'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 8: 社区养老
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '社区食堂的冷暖之思',
        '多地社区食堂在开业初期门庭若市，但随后出现亏损运营、菜品单一等问题，部分已悄然关门。社区食堂如何实现可持续运营引发关注。请就此撰写时评。',
        'yinyue',
        '{"type": "three_part", "opening": "现象点题", "body": "分层论述", "conclusion": "总结升华", "argument_pattern": "首在-重在-贵在"}'::jsonb,
        ARRAY['社区食堂', '养老', '可持续', '公益', '市场化'],
        1100,
        ARRAY['简单否定社区食堂模式', '忽视公益属性', '缺乏运营建议'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;
END $$;

-- ═══════════════════════════════════════════════════════════
-- Set 2: 申论 — 公考申论评测集
-- ═══════════════════════════════════════════════════════════
INSERT INTO evaluation_sets (id, name, style_slug, description, status, sample_count, created_at, updated_at)
SELECT
    uuid_generate_v4(),
    '申论·公考写作评测集',
    'shenlun',
    '覆盖基层治理、公共服务、文化建设等申论高频主题，验证提出-分析-解决三段结构、政策引用准确性、对策可操作性等申论核心要素。',
    'published',
    6,
    NOW(),
    NOW()
WHERE NOT EXISTS (
    SELECT 1 FROM evaluation_sets WHERE style_slug = 'shenlun' AND name = '申论·公考写作评测集'
);

DO $$
DECLARE
    v_set_id UUID;
BEGIN
    SELECT id INTO v_set_id FROM evaluation_sets WHERE style_slug = 'shenlun' AND name = '申论·公考写作评测集' LIMIT 1;
    IF v_set_id IS NULL THEN RETURN; END IF;

    -- Sample 1: 基层减负
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '破解基层形式主义难题',
        '给定材料：近年来，基层工作中文山会海、过度留痕、督查检查频繁等形式主义问题突出，加重了基层干部负担。中央多次强调要为基层减负，但部分地区减负效果不明显，甚至出现"以形式主义反形式主义"的现象。\n\n请结合给定材料，围绕"破解基层形式主义难题"撰写一篇申论文章。',
        'shenlun',
        '{"type": "three_part", "opening": "提出问题", "body": "分析问题", "conclusion": "解决问题", "argument_pattern": "提出-分析-解决"}'::jsonb,
        ARRAY['形式主义', '基层减负', '制度建设', '考核机制', '务实'],
        1000,
        ARRAY['空喊口号无对策', '对策不可操作', '缺乏政策依据'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 2: 数字政府建设
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '推进数字政府建设 提升治理效能',
        '给定材料：数字政府建设是推进国家治理体系和治理能力现代化的重要举措。当前各地积极推进政务服务"一网通办"、"跨省通办"，但数据壁垒、重复建设、信息安全等问题仍然存在，数字鸿沟现象不容忽视。\n\n请结合给定材料，就"推进数字政府建设"撰写一篇申论文章。',
        'shenlun',
        '{"type": "three_part", "opening": "提出问题", "body": "分析问题", "conclusion": "解决问题", "argument_pattern": "提出-分析-解决"}'::jsonb,
        ARRAY['数字政府', '一网通办', '数据壁垒', '信息安全', '治理效能'],
        1000,
        ARRAY['技术至上论', '忽视群众需求', '对策空泛'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 3: 乡村振兴与人才
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '以人才振兴赋能乡村全面振兴',
        '给定材料：乡村振兴关键在人。当前农村面临人才外流、老龄化严重等问题。各地通过"雁归工程"、乡村合伙人等政策吸引人才返乡，但在配套服务、发展空间等方面仍有不足。\n\n请结合材料，围绕"以人才振兴赋能乡村全面振兴"撰写申论文章。',
        'shenlun',
        '{"type": "three_part", "opening": "提出问题", "body": "分析问题", "conclusion": "解决问题", "argument_pattern": "提出-分析-解决"}'::jsonb,
        ARRAY['乡村振兴', '人才振兴', '返乡创业', '政策配套', '产才融合'],
        1000,
        ARRAY['过度行政化思维', '忽视市场机制', '缺乏系统性对策'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 4: 文化自信
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '坚定文化自信 守正创新发展',
        '给定材料：近年来，国潮兴起、非遗出圈、传统文化类综艺节目走红，展现了文化自信的深厚底蕴。但也存在跟风模仿、过度商业化、缺乏深度等问题。如何在守正中创新，在创新中守正，成为重要课题。\n\n请结合材料，围绕"坚定文化自信"撰写申论文章。',
        'shenlun',
        '{"type": "three_part", "opening": "提出问题", "body": "分析问题", "conclusion": "解决问题", "argument_pattern": "提出-分析-解决"}'::jsonb,
        ARRAY['文化自信', '守正创新', '传统文化', '国潮', '创造性转化'],
        1000,
        ARRAY['简单否定商业化', '文化虚无主义', '对策缺乏针对性'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 5: 营商环境优化
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '优化营商环境 激发市场活力',
        '给定材料：营商环境是企业生存发展的土壤。近年来我国营商环境持续改善，但在市场准入、知识产权保护、政策稳定性等方面仍有改进空间。部分企业反映"新官不理旧账"、隐性壁垒等问题依然存在。\n\n请结合材料，围绕"优化营商环境"撰写申论文章。',
        'shenlun',
        '{"type": "three_part", "opening": "提出问题", "body": "分析问题", "conclusion": "解决问题", "argument_pattern": "提出-分析-解决"}'::jsonb,
        ARRAY['营商环境', '市场准入', '法治保障', '公平竞争', '放管服'],
        1000,
        ARRAY['一味否定政府作用', '市场万能论', '对策缺乏法治思维'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 6: 生态文明
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '践行生态文明理念 推动绿色发展',
        '给定材料：绿水青山就是金山银山。多地通过生态修复、产业转型实现了经济发展与生态保护的双赢，但也有地区存在"先污染后治理"的惯性思维，生态补偿机制尚不完善。\n\n请结合材料，围绕"践行生态文明理念"撰写申论文章。',
        'shenlun',
        '{"type": "three_part", "opening": "提出问题", "body": "分析问题", "conclusion": "解决问题", "argument_pattern": "提出-分析-解决"}'::jsonb,
        ARRAY['生态文明', '绿色发展', '生态补偿', '产业转型', '可持续发展'],
        1000,
        ARRAY['极端环保主义', '忽视发展权', '缺乏系统性思维'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;
END $$;

-- ═══════════════════════════════════════════════════════════
-- Set 3: 小红书 — 种草文评测集
-- ═══════════════════════════════════════════════════════════
INSERT INTO evaluation_sets (id, name, style_slug, description, status, sample_count, created_at, updated_at)
SELECT
    uuid_generate_v4(),
    '小红书·种草内容评测集',
    'xiaohongshu',
    '覆盖美食探店、旅行推荐、好物分享等小红书高频内容类型，验证口语化表达、emoji使用、互动引导、短句节奏等风格要素。',
    'published',
    6,
    NOW(),
    NOW()
WHERE NOT EXISTS (
    SELECT 1 FROM evaluation_sets WHERE style_slug = 'xiaohongshu' AND name = '小红书·种草内容评测集'
);

DO $$
DECLARE
    v_set_id UUID;
BEGIN
    SELECT id INTO v_set_id FROM evaluation_sets WHERE style_slug = 'xiaohongshu' AND name = '小红书·种草内容评测集' LIMIT 1;
    IF v_set_id IS NULL THEN RETURN; END IF;

    -- Sample 1: 美食探店
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '杭州宝藏咖啡店探店',
        '素材：位于杭州中山北路的一家社区咖啡店，主营手冲咖啡。店主每天精选3款单品豆，价格区间35-55元。店面不大但氛围温馨，有户外座位。周末经常排队。附近还有几家不错的面包店可以搭配。\n\n请根据素材撰写一篇小红书探店笔记。',
        'xiaohongshu',
        '{"type": "free_form", "opening": "吸引眼球的开头", "body": "核心内容", "conclusion": "互动引导"}'::jsonb,
        ARRAY['咖啡', '探店', '杭州', '手冲', '宝藏'],
        500,
        ARRAY['过度广告感', '缺乏真实体验', '无emoji', '长段落无分段'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 2: 旅行推荐
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '大理三日游攻略分享',
        '素材：大理三日游行程。Day1: 大理古城+崇圣寺三塔，Day2: 环洱海骑行（租电动车约80元/天），途径喜洲古镇、双廊。Day3: 苍山索道+寂照庵。推荐住宿：洱海边民宿。最佳季节3-4月。美食推荐：喜洲粑粑、乳扇、酸辣鱼。\n\n请根据素材撰写一篇小红书旅行攻略笔记。',
        'xiaohongshu',
        '{"type": "free_form", "opening": "吸引眼球的开头", "body": "核心内容", "conclusion": "互动引导"}'::jsonb,
        ARRAY['大理', '洱海', '攻略', '旅行', '民宿'],
        600,
        ARRAY['信息堆砌无重点', '无个人体验感', '缺乏实用Tips', '无emoji'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 3: 好物分享
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '夏日防晒好物推荐',
        '素材：分享几款夏日防晒好物。1. 某品牌物理防晒霜SPF50+，适合敏感肌，价格约150元。2. 防晒喷雾，方便补涂，适合身体。3. 防晒口罩+防晒帽组合，UPF50+。4. 冰袖，轻薄透气。注意事项：每2小时补涂一次，阴天也要防晒。\n\n请根据素材撰写一篇小红书好物分享笔记。',
        'xiaohongshu',
        '{"type": "free_form", "opening": "吸引眼球的开头", "body": "核心内容", "conclusion": "互动引导"}'::jsonb,
        ARRAY['防晒', '好物推荐', '夏日', '敏感肌', 'SPF'],
        400,
        ARRAY['硬广感觉太强', '无使用感受', '缺乏价格信息', '无互动引导'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 4: 家居收纳
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '小户型收纳改造分享',
        '素材：30平小户型收纳改造经验。核心思路：垂直利用+隐藏收纳。推荐好物：洞洞板（玄关）、床底收纳箱、可折叠衣架、磁吸调料盒。改造前后对比明显，杂物消失，空间感大增。总花费约500元。\n\n请根据素材撰写一篇小红书家居分享笔记。',
        'xiaohongshu',
        '{"type": "free_form", "opening": "吸引眼球的开头", "body": "核心内容", "conclusion": "互动引导"}'::jsonb,
        ARRAY['收纳', '小户型', '改造', '洞洞板', '整理'],
        500,
        ARRAY['只罗列产品无方法', '缺乏前后对比', '无emoji', '无预算信息'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 5: 健身打卡
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '居家健身30天打卡心得',
        '素材：居家健身30天打卡心得。方案：每周4练，2天有氧（跳绳/刘畊宏），2天力量（哑铃+深蹲+俯卧撑）。变化：体重减3kg，核心力量明显提升。心得：坚持比强度重要，拍照记录很有用，找一个搭子互相监督效果翻倍。装备：瑜伽垫+可调哑铃。\n\n请根据素材撰写一篇小红书健身打卡笔记。',
        'xiaohongshu',
        '{"type": "free_form", "opening": "吸引眼球的开头", "body": "核心内容", "conclusion": "互动引导"}'::jsonb,
        ARRAY['健身', '打卡', '居家', '减脂', '坚持'],
        450,
        ARRAY['过度承诺效果', '忽视循序渐进', '无emoji', '缺乏真实感受'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;

    -- Sample 6: 读书分享
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (
        uuid_generate_v4(), v_set_id,
        '治愈系书单分享',
        '素材：推荐5本治愈系书籍。1.《被讨厌的勇气》-阿德勒心理学通俗版。2.《也许你该找个人聊聊》-心理咨询师的真实故事。3.《蛤蟆先生去看心理医生》-童话外壳下的心理科普。4.《焦虑的人》-巴克曼的温情小说。5.《当下的力量》-正念入门。适合睡前阅读，缓解焦虑。\n\n请根据素材撰写一篇小红书读书分享笔记。',
        'xiaohongshu',
        '{"type": "free_form", "opening": "吸引眼球的开头", "body": "核心内容", "conclusion": "互动引导"}'::jsonb,
        ARRAY['书单', '治愈', '心理学', '阅读', '推荐'],
        400,
        ARRAY['只列书名无介绍', '剧透过多', '无emoji', '无个人感受'],
        '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb,
        'pending', NOW()
    ) ON CONFLICT DO NOTHING;
END $$;
