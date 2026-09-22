-- 018: Supplement evaluation samples to reach 25+20+20 = 65 total
-- Current: yinyue=8, shenlun=6, xiaohongshu=6 = 20
-- Adding: yinyue=17, shenlun=14, xiaohongshu=14 = 45 more

-- ═══════════════════════════════════════════════════════════
-- Yinyue: 17 more samples (8 → 25)
-- ═══════════════════════════════════════════════════════════
DO $$
DECLARE
    v_set_id UUID;
BEGIN
    SELECT id INTO v_set_id FROM evaluation_sets WHERE style_slug = 'yinyue' AND name = '印月三谈·深度时评评测集' LIMIT 1;
    IF v_set_id IS NULL THEN RETURN; END IF;

    -- Sample 9: 城中村改造
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '城中村改造的温度与尺度', '多地推进城中村改造，部分城市采取"拆改建"结合模式，但过程中也出现强制拆迁、补偿不合理等问题。请就此撰写深度时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['城中村', '改造', '补偿', '民生', '城市更新'], 1200, ARRAY['一味支持拆迁', '忽视居民权益', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 10: 职业教育改革
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '职业教育的破局之路', '职业教育法修订后，职教地位提升，但社会认可度仍然不高，"分流焦虑"持续存在。请就此撰写深度时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['职业教育', '分流', '认可度', '技能人才', '改革'], 1200, ARRAY['简单否定普职分流', '缺乏建设性建议', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 11: 医保改革
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '医保改革的民生底色', '多地实施医保改革，个人账户资金减少引发争议，虽然门诊统筹待遇提升，但部分群众理解不足。请就此撰写时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['医保', '个人账户', '门诊统筹', '民生', '改革'], 1200, ARRAY['片面否定改革', '煽动医患对立', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 12: 未成年人网络保护
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '未成年人网络保护的合力', '《未成年人网络保护条例》实施，对网络游戏沉迷、网络欺凌等问题作出规定，但执行中面临平台责任界定、家长配合等挑战。请就此撰写时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['未成年人', '网络保护', '游戏沉迷', '平台责任', '家庭'], 1300, ARRAY['将责任全推给平台', '忽视家庭教育', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 13: 适老化改造
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '城市适老化改造的细节之美', '多地推进老旧小区加装电梯、公共设施适老化改造，但进度不一、资金筹措困难。请就此撰写深度时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['适老化', '加装电梯', '老旧小区', '民生', '细节'], 1200, ARRAY['一味否定政策', '缺乏可行性建议', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 14: 新就业形态
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '新就业形态的制度突围', '网约车、外卖、直播等新就业形态规模超2亿人，劳动关系认定、社保缴纳等制度保障滞后。请就此撰写深度时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['新就业形态', '劳动关系', '社保', '制度', '权益'], 1300, ARRAY['平台与劳动者简单对立', '忽视灵活就业正面价值', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 15: 托育服务
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '托育困境的破解之道', '三孩政策出台后，0-3岁托育服务供给严重不足，"没人带娃"成为家庭生育决策的主要障碍。请就此撰写时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['托育', '生育', '普惠', '公共服务', '家庭'], 1200, ARRAY['简单催生', '忽视养育成本', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 16: 乡村振兴文化
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '乡村文化振兴的根与魂', '多地通过非遗工坊、乡村文创等方式推动文化振兴，但也出现同质化、表面化问题。请就此撰写深度时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['乡村文化', '非遗', '文创', '振兴', '同质化'], 1200, ARRAY['过度行政化', '忽视文化本体', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 17: 垃圾分类
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '垃圾分类的长效之策', '垃圾分类推行数年，部分城市效果显著，但也有地区出现"前分后混"、形式主义等问题。请就此撰写时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['垃圾分类', '长效机制', '形式主义', '习惯', '管理'], 1100, ARRAY['简单否定政策', '一味批评', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 18: 心理健康
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '关注青少年心理健康之困', '青少年心理健康问题日益突出，多地学校配备心理教师但效果有限，"看病难"延伸至"看心理难"。请就此撰写深度时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['青少年', '心理健康', '学校', '家庭', '社会支持'], 1300, ARRAY['将问题归咎学校', '忽视家庭责任', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 19: AI伦理
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, 'AI时代的伦理边界与人文关怀', '生成式AI快速发展，在带来效率提升的同时也引发版权争议、就业焦虑、信息真实性等伦理问题。请就此撰写深度时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['AI', '伦理', '版权', '就业', '人文'], 1300, ARRAY['技术决定论', '一味否定AI', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 20: 老旧小区改造
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '老旧小区改造的民生账本', '全国推进老旧小区改造，加装电梯、完善配套设施，但低层住户利益、资金分摊等问题引发矛盾。请就此撰写时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['老旧小区', '改造', '电梯', '民生', '协商'], 1200, ARRAY['偏袒一方', '缺乏协商思维', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 21: 平台经济反垄断
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '平台经济反垄断的平衡之道', '互联网平台反垄断执法加强，"二选一"等问题得到整治，但如何平衡创新与监管引发讨论。请就此撰写深度时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['平台经济', '反垄断', '创新', '监管', '平衡'], 1200, ARRAY['一味反对监管', '否定平台价值', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 22: 农村养老
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '农村养老的突围之策', '农村老龄化程度高于城市，但养老服务供给严重不足，互助养老、日间照料等模式面临可持续性挑战。请就此撰写时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['农村养老', '互助养老', '老龄化', '公共服务', '可持续'], 1200, ARRAY['城市视角', '忽视农村实际', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 23: 数字鸿沟
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '跨越数字鸿沟的制度温度', '数字化公共服务推进中，老年群体、偏远地区居民面临"不会用""不能用"困境。请就此撰写深度时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['数字鸿沟', '适老化', '公共服务', '老年人', '包容'], 1200, ARRAY['技术至上论', '忽视弱势群体', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 24: 社区治理
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '社区治理的微观之治', '多地探索"微网格"治理模式，将管理下沉到楼栋、单元，但也存在形式主义、增加基层负担等问题。请就此撰写时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['社区治理', '微网格', '基层', '形式主义', '精细化'], 1200, ARRAY['简单否定创新', '忽视实际效果', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 25: 消费者权益
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '消费者权益保护的新命题', '直播带货、预付卡消费等新模式下，消费者维权面临举证难、追责难等问题。请就此撰写深度时评。', 'yinyue', '{"type": "three_part", "argument_pattern": "首在-重在-贵在"}'::jsonb, ARRAY['消费者权益', '直播带货', '预付卡', '维权', '制度'], 1200, ARRAY['一味否定新业态', '缺乏制度建议', '无核心比喻'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;
END $$;

-- ═══════════════════════════════════════════════════════════
-- Shenlun: 14 more samples (6 → 20)
-- ═══════════════════════════════════════════════════════════
DO $$
DECLARE
    v_set_id UUID;
BEGIN
    SELECT id INTO v_set_id FROM evaluation_sets WHERE style_slug = 'shenlun' AND name = '申论·公考写作评测集' LIMIT 1;
    IF v_set_id IS NULL THEN RETURN; END IF;

    -- Sample 7: 基层治理
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '夯实基层治理根基', '给定材料：基层是国家治理的"神经末梢"。一些地方通过"街乡吹哨、部门报到"机制破解基层执法难题，但权责不对等、资源不下沉等问题仍然存在。\n\n请结合材料，围绕"夯实基层治理根基"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['基层治理', '权责对等', '资源下沉', '执法', '体制机制'], 1000, ARRAY['空泛议论无对策', '对策不具操作性', '缺乏政策依据'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 8: 共同富裕
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '扎实推进共同富裕', '给定材料：共同富裕是社会主义的本质要求。当前收入差距、城乡差距仍然突出，需要通过初次分配、再分配、三次分配协调配套来实现。\n\n请结合材料，围绕"扎实推进共同富裕"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['共同富裕', '收入差距', '分配制度', '乡村振兴', '社会保障'], 1000, ARRAY['平均主义', '只分配不发展', '对策缺乏系统性'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 9: 科技自立自强
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '科技自立自强 筑牢发展根基', '给定材料：关键核心技术"卡脖子"问题突出，芯片、高端制造等领域面临外部封锁。需要加强基础研究、推进产学研深度融合。\n\n请结合材料，围绕"科技自立自强"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['科技自立', '核心技术', '基础研究', '产学研', '创新'], 1000, ARRAY['闭关锁国论', '忽视国际合作', '对策空泛'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 10: 人口老龄化
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '积极应对人口老龄化', '给定材料：我国进入深度老龄化社会，劳动力减少、养老金压力增大，但银发经济也带来新机遇。多地探索延迟退休、居家养老等举措。\n\n请结合材料，围绕"积极应对人口老龄化"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['老龄化', '延迟退休', '银发经济', '养老服务', '社会保障'], 1000, ARRAY['悲观论调', '将老人视为负担', '缺乏系统性对策'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 11: 食品安全
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '筑牢食品安全防线', '给定材料：食品安全事关人民群众身体健康。预制菜标准争议、外卖卫生问题等引发关注，监管需跟上新业态发展。\n\n请结合材料，围绕"筑牢食品安全防线"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['食品安全', '预制菜', '监管', '标准', '新业态'], 1000, ARRAY['一味否定新业态', '监管建议空泛', '缺乏法治思维'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 12: 区域协调发展
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '推动区域协调发展 构建新格局', '给定材料：京津冀、长三角、粤港澳大湾区等区域协同发展成效显著，但部分区域差距仍大，产业同构、行政壁垒等问题突出。\n\n请结合材料，围绕"推动区域协调发展"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['区域协调', '协同发展', '产业分工', '行政壁垒', '一体化'], 1000, ARRAY['简单否定发达地区', '对策不具操作性', '缺乏系统性'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 13: 人才评价
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '破除人才评价"四唯"之弊', '给定材料：人才评价中"唯论文、唯职称、唯学历、唯奖项"问题突出，阻碍了创新人才发展。多地推进分类评价改革，但落地效果参差。\n\n请结合材料，围绕"破除人才评价之弊"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['人才评价', '四唯', '分类评价', '创新', '改革'], 1000, ARRAY['简单否定学术评价', '对策空泛', '缺乏操作性'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 14: 数字经济
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '做强数字经济 赋能高质量发展', '给定材料：数字经济成为经济增长新引擎，数据要素市场化配置改革推进中，但数据安全、平台垄断、数字鸿沟等问题不容忽视。\n\n请结合材料，围绕"做强数字经济"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['数字经济', '数据要素', '数据安全', '平台经济', '高质量发展'], 1000, ARRAY['技术决定论', '忽视风险治理', '对策缺乏针对性'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 15: 就业优先
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '实施就业优先战略 稳住民生基本盘', '给定材料：高校毕业生就业难、农民工就业不稳等问题突出。多地推出减负稳岗、技能培训、创业扶持等举措。\n\n请结合材料，围绕"实施就业优先战略"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['就业优先', '高校毕业生', '农民工', '技能培训', '稳岗'], 1000, ARRAY['只关注一类群体', '对策不具操作性', '缺乏政策依据'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 16: 社会信用
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '推进社会信用体系建设 构建诚信社会', '给定材料：社会信用体系建设取得进展，但也出现信用泛化、惩戒过度等问题，需要法治化、规范化。\n\n请结合材料，围绕"推进社会信用体系建设"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['社会信用', '诚信', '法治化', '信用泛化', '惩戒'], 1000, ARRAY['简单否定信用体系', '缺乏法治思维', '对策空泛'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 17: 乡村振兴产业
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '产业振兴筑牢乡村振兴之基', '给定材料：产业振兴是乡村振兴的重中之重。多地发展特色农产品、乡村旅游等，但存在同质化、产业链短等问题。\n\n请结合材料，围绕"产业振兴筑牢乡村振兴之基"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['产业振兴', '特色农业', '乡村旅游', '产业链', '品牌'], 1000, ARRAY['过度行政化', '忽视市场机制', '对策不具操作性'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 18: 公共服务均等化
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '推进基本公共服务均等化 共享发展成果', '给定材料：城乡公共服务差距仍然明显，教育、医疗、养老等资源向城市集中。推进均等化需要制度保障和财政支持。\n\n请结合材料，围绕"推进基本公共服务均等化"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['公共服务', '均等化', '城乡差距', '教育', '医疗'], 1000, ARRAY['简单否定城市发展', '对策空泛', '缺乏财政思维'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 19: 知识产权保护
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '加强知识产权保护 激励创新活力', '给定材料：知识产权保护是创新驱动发展的重要保障。专利侵权、商标抢注等问题仍然存在，惩罚性赔偿制度不断完善。\n\n请结合材料，围绕"加强知识产权保护"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['知识产权', '专利', '惩罚性赔偿', '创新', '法治'], 1000, ARRAY['保护过度论', '忽视公共利益', '对策缺乏法治思维'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 20: 应急管理
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '提升应急管理能力 筑牢安全防线', '给定材料：极端天气、公共卫生事件等突发事件频发，应急管理需从"事后救援"向"事前预防"转变，基层应急能力建设是短板。\n\n请结合材料，围绕"提升应急管理能力"撰写申论文章。', 'shenlun', '{"type": "three_part", "argument_pattern": "提出-分析-解决"}'::jsonb, ARRAY['应急管理', '预防', '基层', '预警', '救援'], 1000, ARRAY['事后追责思维', '忽视预防', '对策不具操作性'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;
END $$;

-- ═══════════════════════════════════════════════════════════
-- Xiaohongshu: 14 more samples (6 → 20)
-- ═══════════════════════════════════════════════════════════
DO $$
DECLARE
    v_set_id UUID;
BEGIN
    SELECT id INTO v_set_id FROM evaluation_sets WHERE style_slug = 'xiaohongshu' AND name = '小红书·种草内容评测集' LIMIT 1;
    IF v_set_id IS NULL THEN RETURN; END IF;

    -- Sample 7: 护肤心得
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '敏感肌换季护肤心得', '素材：敏感肌换季护肤经验。核心：精简护肤+修护屏障。推荐：温和洁面、神经酰胺面霜、物理防晒。避免：猛药叠加、频繁去角质。换季前2周开始调整护肤流程。\n\n请根据素材撰写一篇小红书护肤分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['敏感肌', '换季', '护肤', '屏障修护', '神经酰胺'], 400, ARRAY['硬广', '无使用感受', '无emoji', '缺乏注意事项'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 8: 穿搭分享
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '微胖女生夏日穿搭指南', '素材：微胖女生夏日穿搭技巧。高腰A字裙遮胯宽，V领上衣拉长颈线，垂感面料显瘦。色彩以深色系为主，局部亮色点缀。避免紧身款和横条纹。\n\n请根据素材撰写一篇小红书穿搭分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['微胖', '穿搭', '夏日', '显瘦', '高腰'], 450, ARRAY['身材焦虑暗示', '无搭配技巧', '无emoji', '只晒图不分享'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 9: 猫咪养护
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '新手养猫避坑指南', '素材：新手养猫必备知识和避坑经验。必备：猫砂盆、猫粮、水碗、猫抓板。避坑：别买花里百合有毒、别频繁换粮、别用水晶猫砂。预算月均200-500元。\n\n请根据素材撰写一篇小红书养宠分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['养猫', '新手', '避坑', '猫粮', '猫砂'], 400, ARRAY['不提有毒植物', '无预算信息', '无emoji', '纯罗列无经验'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 10: 职场心得
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '职场新人沟通技巧分享', '素材：职场新人沟通经验。核心：主动汇报进度、学会提问、邮件留痕。技巧：会议前准备要点、反馈先肯定再建议、deadline提前确认。避坑：不越级汇报、不在群里争论。\n\n请根据素材撰写一篇小红书职场分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['职场', '新人', '沟通', '汇报', '技巧'], 450, ARRAY['贩卖焦虑', '无实操技巧', '无emoji', '说教感强'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 11: 烹饪教程
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '15分钟搞定的懒人晚餐', '素材：15分钟懒人晚餐食谱。意面版：煮面8分钟+炒酱5分钟。饭团版：隔夜饭+金枪鱼+蛋黄酱。沙拉版：鸡胸肉+混合蔬菜+油醋汁。贴心提示：周末备菜可以大幅缩短工作日做饭时间。\n\n请根据素材撰写一篇小红书美食分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['懒人', '晚餐', '快手', '15分钟', '备菜'], 400, ARRAY['步骤不清', '无时间提示', '无emoji', '缺乏营养搭配'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 12: 数码好物
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '提升幸福感的桌面好物分享', '素材：桌面改造好物推荐。1. 显示器支架（释放桌面空间）。2. 洞洞板（收纳耳机线材）。3. 桌面台灯（护眼+氛围）。4. 机械键盘（打字手感提升）。5. 桌面小绿植（解压+美观）。预算500以内。\n\n请根据素材撰写一篇小红书好物分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['桌面', '好物', '改造', '收纳', '幸福感'], 450, ARRAY['硬广感', '无使用感受', '无emoji', '无预算'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 13: 旅行攻略
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '成都两日游超详细攻略', '素材：成都两日游攻略。Day1: 春熙路→太古里→宽窄巷子→人民公园喝茶。Day2: 大熊猫基地（早上去）→武侯祠→锦里。美食：串串香、甜水面、蛋烘糕。交通：地铁覆盖主要景点。\n\n请根据素材撰写一篇小红书旅行攻略笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['成都', '攻略', '两日游', '熊猫', '美食'], 500, ARRAY['信息堆砌', '无个人体验', '无emoji', '无实用Tips'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 14: 学习方法
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '高效学习法分享之费曼技巧', '素材：费曼学习法实践经验。核心：用简单语言解释复杂概念。步骤：1选择概念→2教给别人→3发现盲区→4简化重述。适合考前复习和新知识内化。亲测一周搞定一门课的重难点。\n\n请根据素材撰写一篇小红书学习分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['费曼', '学习法', '高效', '复习', '内化'], 400, ARRAY['过度承诺', '步骤不清', '无emoji', '无个人感受'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 15: 居家好物
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '租房党必看的平价居家好物', '素材：租房平价好物推荐。1. 免打孔置物架（不破坏墙面）。2. 可移动边几（灵活布局）。3. 遮光窗帘（提升睡眠质量）。4. 香薰蜡烛（氛围感拉满）。5. 折叠脏衣篓（省空间）。总预算300以内。\n\n请根据素材撰写一篇小红书居家分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['租房', '平价', '好物', '免打孔', '氛围'], 450, ARRAY['贵价推荐', '无使用感受', '无emoji', '无预算'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 16: 亲子遛娃
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '周末遛娃好去处之免费公园', '素材：城市免费遛娃公园推荐。1. 植物园（认知自然）。2. 湿地公园（观鸟+骑行）。3. 社区体育公园（免费游乐设施）。Tips：带防晒+水壶+换洗衣物，上午10点前或下午4点后最佳。\n\n请根据素材撰写一篇小红书亲子分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['遛娃', '免费', '公园', '周末', '亲子'], 400, ARRAY['收费景点推荐', '无实用Tips', '无emoji', '无安全提示'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 17: 减脂餐
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '一周减脂餐分享不重样', '素材：一周减脂餐安排。周一：鸡胸肉沙拉。周二：全麦三明治。周三：番茄龙利鱼。周四：牛肉杂粮饭。周五：虾仁豆腐汤。原则：高蛋白+粗粮+蔬菜，控制油盐。备餐技巧：周末批量煮粗粮和鸡胸。\n\n请根据素材撰写一篇小红书减脂分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['减脂', '餐单', '高蛋白', '粗粮', '备餐'], 500, ARRAY['极端节食', '无营养搭配', '无emoji', '步骤不清'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 18: 手机摄影
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '手机拍出氛围感的5个技巧', '素材：手机摄影氛围感技巧。1. 黄金时刻拍摄（日出后/日落前1小时）。2. 利用前景遮挡制造层次。3. 大面积留白+小主体。4. 调低曝光增加质感。5. 后期降低饱和提升胶片感。\n\n请根据素材撰写一篇小红书摄影分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['手机摄影', '氛围感', '黄金时刻', '构图', '后期'], 400, ARRAY['过度修图', '技巧不清', '无emoji', '无实拍示例'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 19: 职场穿搭
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '职场通勤穿搭之胶囊衣橱', '素材：职场胶囊衣橱搭建经验。核心单品：白衬衫×2、黑色西装裤、灰色西裙、针织开衫、黑色平底鞋。搭配公式：衬衫+西裤、针织+西裙、开衫+连衣裙。色彩以黑白灰为主，方便混搭。\n\n请根据素材撰写一篇小红书穿搭分享笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['职场', '通勤', '胶囊衣橱', '搭配', '基础款'], 450, ARRAY['奢侈品推荐', '无搭配公式', '无emoji', '只晒图不分享'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;

    -- Sample 20: 追剧推荐
    INSERT INTO evaluation_samples (id, set_id, topic, input_prompt, style_slug, expected_structure, expected_keywords, expected_length, red_flags, scoring_criteria, status, created_at)
    VALUES (uuid_generate_v4(), v_set_id, '治愈系韩剧推荐熬夜也要看', '素材：5部治愈系韩剧推荐。1.《请回答1988》-邻里温情天花板。2.《机智的医生生活》-成年人友谊。3.《海岸村恰恰恰》-慢节奏治愈。4.《我的大叔》-生活至暗中的光。5.《山茶花开时》-小城温情。适合周末窝沙发追。\n\n请根据素材撰写一篇小红书追剧推荐笔记。', 'xiaohongshu', '{"type": "free_form"}'::jsonb, ARRAY['韩剧', '治愈', '追剧', '推荐', '周末'], 400, ARRAY['剧透太多', '只列名无介绍', '无emoji', '无个人感受'], '{"factuality": 0.3, "structure": 0.2, "style": 0.2, "relevance": 0.2, "risk": 0.1}'::jsonb, 'pending', NOW()) ON CONFLICT DO NOTHING;
END $$;

-- Update sample counts
UPDATE evaluation_sets SET sample_count = 25, updated_at = NOW() WHERE style_slug = 'yinyue' AND name = '印月三谈·深度时评评测集';
UPDATE evaluation_sets SET sample_count = 20, updated_at = NOW() WHERE style_slug = 'shenlun' AND name = '申论·公考写作评测集';
UPDATE evaluation_sets SET sample_count = 20, updated_at = NOW() WHERE style_slug = 'xiaohongshu' AND name = '小红书·种草内容评测集';
