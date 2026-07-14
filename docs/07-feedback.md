# 反馈与信誉系统

## 1. 设计目标

- **分段反馈**：用户可对标题、段落、句子分别评分
- **信誉加权**：不同用户的反馈权重不同，基于"录用信号"计算
- **风格迭代触发**：累计反馈达到阈值后，提示 Admin 可进行风格迭代
- **录用标记**：通过 workbuddy 的作者名称匹配，标记文章是否被录用

## 2. 反馈入口

### 2.1 写作完成后展示反馈面板

```
┌─────────────────────────────────────────┐
│  文章已生成                               │
│                                          │
│  ## 外卖骑手的红灯困境                    │
│  ⭐⭐⭐⭐☆  [点击评分]                    │
│                                          │
│  在杭州的大街小巷，外卖骑手风驰电掣...     │
│  ⭐⭐⭐⭐⭐  [点击评分]                    │
│                                          │
│  首在认知：平台算法对时间的极致压缩...     │
│  ⭐⭐⭐☆☆  [点击评分]                     │
│                                          │
│  ┌─────────────────────────────────┐    │
│  │ 整体评价:                        │    │
│  │ ○ 好  ○ 一般  ○ 差              │    │
│  │ 补充说明: [_________________]    │    │
│  └─────────────────────────────────┘    │
│                                          │
│  [提交反馈]  [稍后评价]                   │
└─────────────────────────────────────────┘
```

### 2.2 反馈数据结构

```jsonc
{
  "trace_id": "trace_xxx",
  "segments": [
    {
      "segment_type": "title",         // title | paragraph | sentence | overall
      "segment_index": 0,
      "segment_text": "外卖骑手的红灯困境",
      "rating": 4,                      // 1-5
      "feedback_type": "good",          // good | bad | suggestion
      "comment": "标题切中要害但不够吸引人"
    },
    {
      "segment_type": "paragraph",
      "segment_index": 2,
      "segment_text": "首在认知：平台算法...",
      "rating": 3,
      "feedback_type": "suggestion",
      "comment": "这部分论述不够深入，缺少具体案例"
    }
  ]
}
```

## 3. 信誉权重系统

### 3.1 信誉分计算

```
用户信誉 = base_weight × (1 + adoption_bonus)

base_weight = 1.00 (所有用户初始值)

adoption_bonus:
  adoption_count = 0  → bonus = 0     (信誉 = 1.00)
  adoption_count = 1  → bonus = 0.5   (信誉 = 1.50)
  adoption_count = 3  → bonus = 1.0   (信誉 = 2.00)
  adoption_count = 5  → bonus = 1.5   (信誉 = 2.50)
  adoption_count = 10 → bonus = 2.0   (信誉 = 3.00)
  adoption_count > 10 → bonus = 3.0   (信誉 = 4.00, 封顶)

公式: bonus = min(3.0, adoption_count × 0.3)
```

### 3.2 录用信号来源

**workbuddy 录用标记**：

- 用户在 workbuddy 系统中有一个基于作者名称的录用标记
- 当用户通过 Writing Agent 生成文章 → 提交到知识库 → 被 IMA 录用 → workbuddy 标记录用
- workbuddy 通过回调 API 通知 Writing Agent：

```
POST /api/v2/feedback/adopt
{
  "trace_id": "trace_xxx",
  "author_name": "张三",
  "adopted_source": "workbuddy",
  "article_url": "https://..."
}
```

- 收到回调后：
  1. 更新 `feedback_segments.is_adopted = TRUE`
  2. 更新 `users.adoption_count += 1`
  3. 重新计算 `users.reputation`

### 3.3 信誉加权反馈聚合

```sql
-- 计算某风格某版本的加权反馈得分
SELECT
    style_slug,
    profile_version,
    COUNT(*) AS total_feedback,
    SUM(CASE WHEN is_adopted THEN 1 ELSE 0 END) AS total_adopted,
    AVG(rating) AS avg_rating,
    -- 信誉加权得分
    SUM(rating * user_reputation) / SUM(user_reputation) AS weighted_score
FROM feedback_segments
WHERE style_slug = 'yinyue' AND profile_version = 3
GROUP BY style_slug, profile_version;
```

## 4. 风格迭代触发

### 4.1 迭代阈值

```
当某风格某版本累计反馈达到 30 条 → ready_for_iteration = TRUE
```

Admin Dashboard 会显示"可迭代"标记：

```
预设评论风格 v3
├── 累计反馈: 32 条
├── 加权评分: 4.2 / 5.0
├── 录用率: 15.6%
├── 🔔 已达到迭代阈值，建议进行风格优化
└── [查看反馈详情]  [创建 v4]
```

### 4.2 反馈分析辅助

达到迭代阈值后，系统自动生成反馈分析报告：

| 分析维度 | 内容 |
|---|---|
| 低分段落分布 | 哪些段落/部分得分最低 |
| 高频问题 | 用户反馈中最常见的问题 |
| 录用 vs 未录用对比 | 被录用文章的特征 vs 未录用的 |
| 风格偏移 | 用户反馈的"风格不像"集中在哪些方面 |

Admin 基于报告决定是否创建新版本，以及新版本应优化哪些方面。

## 5. 反馈数据流

```
用户写作完成
    │
    ▼
展示分段反馈面板
    │
    ▼
用户提交反馈 (POST /api/v2/feedback)
    │
    ├─ 保存到 feedback_segments (含 user_reputation 快照)
    │
    ▼
异步: 更新 feedback_aggregation
    │
    ├─ total_feedback += 1
    ├─ 重新计算 avg_rating, weighted_score
    ├─ total_adopted (如有录用标记)
    └─ ready_for_iteration = (total_feedback >= threshold)
    │
    ▼
ready_for_iteration = TRUE?
    │
    └─ Admin Dashboard 显示"可迭代"标记
        │
        └─ Admin 查看反馈分析报告 → 决定是否创建新版本

并行: workbuddy 录用回调
    │
    ▼
POST /api/v2/feedback/adopt
    │
    ├─ feedback_segments.is_adopted = TRUE
    ├─ users.adoption_count += 1
    └─ users.reputation = base_weight × (1 + min(3.0, adoption_count × 0.3))
```
