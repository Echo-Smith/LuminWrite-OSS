-- 018: Down — remove supplemented evaluation samples (samples 7+ from each set)
-- This migration only removes the supplemental samples added by 018_up.
-- Original samples (from migration 011) are preserved.

-- Remove yinyue samples 9-25 (by matching topics that didn't exist in migration 011)
DELETE FROM evaluation_samples WHERE style_slug = 'yinyue' AND topic IN (
    '城中村改造的温度与尺度',
    '职业教育的破局之路',
    '医保改革的民生底色',
    '未成年人网络保护的合力',
    '城市适老化改造的细节之美',
    '新就业形态的制度突围',
    '托育困境的破解之道',
    '乡村文化振兴的根与魂',
    '垃圾分类的长效之策',
    '关注青少年心理健康之困',
    'AI时代的伦理边界与人文关怀',
    '老旧小区改造的民生账本',
    '平台经济反垄断的平衡之道',
    '农村养老的突围之策',
    '跨越数字鸿沟的制度温度',
    '社区治理的微观之治',
    '消费者权益保护的新命题'
);

-- Remove shenlun samples 7-20
DELETE FROM evaluation_samples WHERE style_slug = 'shenlun' AND topic IN (
    '夯实基层治理根基',
    '扎实推进共同富裕',
    '科技自立自强 筑牢发展根基',
    '积极应对人口老龄化',
    '筑牢食品安全防线',
    '推动区域协调发展 构建新格局',
    '破除人才评价"四唯"之弊',
    '做强数字经济 赋能高质量发展',
    '实施就业优先战略 稳住民生基本盘',
    '推进社会信用体系建设 构建诚信社会',
    '产业振兴筑牢乡村振兴之基',
    '推进基本公共服务均等化 共享发展成果',
    '加强知识产权保护 激励创新活力',
    '提升应急管理能力 筑牢安全防线'
);

-- Remove xiaohongshu samples 7-20
DELETE FROM evaluation_samples WHERE style_slug = 'xiaohongshu' AND topic IN (
    '敏感肌换季护肤心得',
    '微胖女生夏日穿搭指南',
    '新手养猫避坑指南',
    '职场新人沟通技巧分享',
    '15分钟搞定的懒人晚餐',
    '提升幸福感的桌面好物分享',
    '成都两日游超详细攻略',
    '高效学习法分享之费曼技巧',
    '租房党必看的平价居家好物',
    '周末遛娃好去处之免费公园',
    '一周减脂餐分享不重样',
    '手机拍出氛围感的5个技巧',
    '职场通勤穿搭之胶囊衣橱',
    '治愈系韩剧推荐熬夜也要看'
);

-- Restore original sample counts
UPDATE evaluation_sets SET sample_count = 8, updated_at = NOW() WHERE style_slug = 'yinyue' AND name = '印月三谈·深度时评评测集';
UPDATE evaluation_sets SET sample_count = 6, updated_at = NOW() WHERE style_slug = 'shenlun' AND name = '申论·公考写作评测集';
UPDATE evaluation_sets SET sample_count = 6, updated_at = NOW() WHERE style_slug = 'xiaohongshu' AND name = '小红书·种草内容评测集';
