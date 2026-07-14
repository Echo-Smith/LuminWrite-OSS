# 评测系统

## 1. 设计目标

- 按**写作风格**划分评测集
- 初始每种风格 **20-30 条**样本，后续持续扩充
- 支持**人工标注**样本
- 每次 **Profile 变更发布时自动触发**评测
- 支持导出到**第三方平台**跑多模型对比
- 评测结果在 Admin Dashboard 可视化展示

## 2. 评测集结构

### 2.1 评测集（evaluation_sets）

每个写作风格对应一个评测集：

| 风格 | 初始样本量 | 状态 |
|---|---|---|
| 预设评论风格 (yinyue) | 25 条 | 待标注 |
| 申论 (shenlun) | 20 条 | 待标注 |
| 小红书 (xiaohongshu) | 20 条 | 待标注 |

### 2.2 评测样本（evaluation_samples）

每条样本包含：

```jsonc
{
  "topic": "外卖骑手闯红灯的治理困境",
  "input_prompt": "请基于热搜写一篇关于外卖骑手闯红灯的评论，1200字以内",
  "style_slug": "yinyue",

  // 人工标注的期望结果
  "expected_structure": {
    "type": "three_part",
    "opening_type": "现象点题",
    "argument_count": { "min": 2, "max": 4 },
    "argument_pattern": "首在-重在-贵在"
  },
  "expected_keywords": ["外卖骑手", "交通安全", "平台算法", "城市治理"],
  "expected_length": { "min": 1000, "max": 1500 },
  "red_flags": [
    "使用伤亡数字做标题",
    "虚构场景或人物对白",
    "未包含核心比喻",
    "分论点未递进"
  ],

  // 评分标准及权重
  "scoring_criteria": {
    "factuality":   { "weight": 0.20, "description": "事实准确性，无编造" },
    "structure":    { "weight": 0.20, "description": "结构符合三段式闭环" },
    "style":        { "weight": 0.20, "description": "风格符合 Profile 规范" },
    "rhetoric":     { "weight": 0.15, "description": "修辞（比喻/排比/设问）" },
    "length":       { "weight": 0.10, "description": "篇幅在规定范围内" },
    "title_quality":{ "weight": 0.10, "description": "标题质量，无敏感问题" },
    "safety":       { "weight": 0.05, "description": "内容安全，无敏感问题" }
  },

  "status": "annotated",
  "annotator": "admin"
}
```

## 3. 评测执行

### 3.1 触发条件

| 触发方式 | 说明 |
|---|---|
| Profile 变更发布 | 自动触发对应风格的评测集 |
| 手动触发 | Admin 后台点击"运行评测" |
| 定时触发 | Admin 可配置 cron 定时评测 |

### 3.2 评测流程

```
Profile 发布 (v3 → v4)
       │
       ▼
创建 evaluation_run (status=pending)
       │
       ▼
加载该风格的所有评测样本
       │
       ▼
┌──────┬──────┬──────┬──────┐
│ 样本1 │ 样本2 │ ...  │ 样本N │  (并发执行)
└──┬───┴──┬───┴──────┴──┬───┘
   │      │              │
   ▼      ▼              ▼
 Agent Engine 使用 v4 Profile 生成文章
   │      │              │
   ▼      ▼              ▼
 评分模块 (规则 + LLM-as-Judge)
   │      │              │
   ▼      ▼              ▼
 记录每个样本的得分和问题
       │
       ▼
聚合计算 overall_score + dimension_scores
       │
       ▼
evaluation_run status=completed
       │
       ▼
Admin Dashboard 展示评测报告
```

### 3.3 评分方式

#### 规则评分（自动）

| 维度 | 规则 |
|---|---|
| `length` | 字数在 `[min, max]` 范围内 → 1.0，超出 → 0.0 |
| `title_quality` | 标题长度 10-25 字 → +0.5；不含 forbidden_patterns → +0.5 |
| `safety` | 不命中敏感词 → 1.0；命中 → 0.0 |
| `structure` | 包含"## "标题标记 → +0.3；包含 `---MODIFICATIONS---` → +0.3；段落数 3-6 → +0.4 |

#### LLM-as-Judge 评分（自动）

| 维度 | 评判方式 |
|---|---|
| `factuality` | LLM 判断文章是否编造事实、时态是否正确 |
| `style` | LLM 判断文章是否符合 Profile 中的风格规范 |
| `rhetoric` | LLM 判断是否包含核心比喻、排比、设问 |

LLM-as-Judge 使用 DeepSeek Pro（thinking 模式），Prompt 中注入 Profile 的评分标准。

### 3.4 第三方平台导出

评测集可导出为标准 JSON 格式，上传到第三方评测平台：

```jsonc
{
  "eval_set_name": "预设评论风格 v3",
  "style_slug": "yinyue",
  "samples": [
    {
      "id": "sample_001",
      "input": "请基于热搜写一篇关于外卖骑手闯红灯的评论...",
      "expected": {
        "keywords": [...],
        "length_range": [1000, 1500],
        "structure": "three_part"
      },
      "scoring_criteria": {...}
    }
  ]
}
```

## 4. 评测报告

### 4.1 单次评测报告

```
评测报告: 预设评论风格 v4
触发: Profile 变更 (v3 → v4)
时间: 2026-07-07 17:30

┌──────────────────────────────────────┐
│  总分: 87.5 / 100                     │
│  (v3 基准: 85.2, 变化: +2.3)          │
├──────────────────────────────────────┤
│  分维度得分:                           │
│  事实准确性    ████████░░  0.89       │
│  结构合规      █████████░  0.92       │
│  风格符合      ████████░░  0.85       │
│  修辞运用      ███████░░░  0.78       │
│  篇幅控制      █████████░  0.95       │
│  标题质量      ████████░░  0.82       │
│  内容安全      ██████████  1.00       │
├──────────────────────────────────────┤
│  问题样本:                             │
│  • 样本 #3: 修辞得分低（缺少设问句）    │
│  • 样本 #7: 标题包含数字（"3人死亡"）   │
│  • 样本 #15: 分论点未递进              │
├──────────────────────────────────────┤
│  [导出报告]  [查看详情]  [对比 v3]     │
└──────────────────────────────────────┘
```

### 4.2 版本对比

Admin 可选择两个版本对比评测结果，查看各维度得分变化趋势。

## 5. 评测集扩充

- Admin 可随时添加新样本
- 从用户反馈中提取高质量样本（用户反馈"写得好"的 Trace 可转化为评测样本）
- 持续扩充，目标每种风格 50+ 条

## 6. 平台能力说明

当前平台暂时无法进行多模型的接入和对比跑，因此：
- 评测集生成后可**导出**到第三方平台
- 本地评测仅支持当前接入的模型（DeepSeek V4 flash/pro）
- 未来支持多模型接入后，可在 Admin Dashboard 直接进行 A/B 对比
