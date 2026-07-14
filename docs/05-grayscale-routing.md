# 灰度路由系统

## 1. 设计目标

- 支持新风格版本/新功能的安全灰度发布
- **两级路由**：Profile 标记（高优先级）→ UID Hash（低优先级默认）
- **不支持多维度交叉**：灰度策略简单清晰，避免组合爆炸

## 2. 路由流程

```
请求进入 (UID + Style Slug)
       │
       ▼
┌──────────────────────────────────┐
│  Level 1: Profile 标记 (高优先级)  │
│                                    │
│  查询该 slug 的灰度配置:            │
│  rollout_type = ?                  │
│    ├─ full → 所有用户使用新版本      │
│    ├─ whitelist → 检查 UID 是否在   │
│    │              白名单中           │
│    └─ percentage → hash(UID) % 100  │
│                   < rollout_percent │
│                   ? 新版本 : 旧版本  │
└──────────────┬─────────────────────┘
               │
       ┌───────┴───────┐
       │               │
       ▼ 命中灰度       ▼ 未命中
  使用新版本       ┌──────────────────────────┐
                  │  Level 2: UID Hash 分流    │
                  │  (低优先级默认规则)          │
                  │                            │
                  │  hash(UID) % 100           │
                  │  → 决定使用哪个已发布版本     │
                  │                            │
                  │  默认: 最新 published 版本   │
                  └────────────────────────────┘
```

## 3. 数据结构

### 3.1 灰度配置（存储在 style_profiles 表）

```jsonc
{
  "rollout_type": "percentage",    // full | whitelist | percentage
  "whitelist_uids": ["uid_001", "uid_002"],
  "rollout_percent": 10,            // 10% 用户使用新版本
  "target_version": 4,              // 灰度目标版本
  "fallback_version": 3             // 未命中时使用的旧版本
}
```

### 3.2 路由决策表

| rollout_type | 命中条件 | 结果 |
|---|---|---|
| `full` | 所有用户 | `target_version` |
| `whitelist` | `UID ∈ whitelist_uids` | `target_version` |
| `whitelist` | `UID ∉ whitelist_uids` | `fallback_version` |
| `percentage` | `hash(UID) % 100 < rollout_percent` | `target_version` |
| `percentage` | `hash(UID) % 100 >= rollout_percent` | `fallback_version` |
| 无灰度配置 | — | 最新 `published` 版本 |

## 4. Hash 函数

使用 FNV-1a hash，稳定且快速：

```go
func uidHash(uid string) int {
    h := fnv.New32a()
    h.Write([]byte(uid))
    return int(h.Sum32() % 100)
}
```

特点：
- 同一 UID 永远映射到同一桶
- UID 分布均匀
- 无需维护映射表

## 5. Admin 灰度配置界面

```
Admin Dashboard → 风格管理 → 预设评论风格 → 灰度配置

┌─────────────────────────────────────────┐
│  灰度发布配置                             │
│                                          │
│  当前发布版本: v3                         │
│  灰度目标版本: v4 (draft)                 │
│                                          │
│  灰度方式:                                │
│  ○ 全量发布 (所有用户立即切换到 v4)        │
│  ○ 白名单灰度 (指定 UID 列表)             │
│  ● 百分比灰度 (按 UID Hash 分流)          │
│                                          │
│  灰度比例: 10%                            │
│  ┌────────────────────────────┐ 10%     │
│  └────────────────────────────┘          │
│                                          │
│  [预览命中情况]  [保存]  [发布]           │
└─────────────────────────────────────────┘
```

### 5.1 预览命中情况

Admin 可以输入 UID 查看该用户会命中哪个版本：

```
输入 UID: uid_abc123
→ hash("uid_abc123") % 100 = 42
→ 42 >= 10 → 命中 fallback_version (v3)
```

## 6. 灰度切换流程

```
v3 (published) ──→ Admin 创建 v4 (draft)
                        │
                   Admin 编辑 v4 并配置灰度
                        │
                   Admin "发布" v4
                        │
              ┌─────────┼──────────────┐
              ▼          ▼              ▼
          10% 用户    90% 用户      Admin 监控
          使用 v4    使用 v3       评测 + 反馈
              │          │
              │    Admin 观察数据
              │          │
              ▼          ▼
         Admin "全量切换" v4
              │
              ▼
     v4 全量, v3 archived
```

## 7. 灰度监控

Admin Dashboard 灰度监控面板：

| 指标 | 说明 |
|---|---|
| 灰度流量比 | 实际命中灰度的请求占比 |
| v4 评测得分 | 灰度版本的评测分数 vs 旧版本 |
| v4 反馈评分 | 灰度用户的反馈均分 vs 旧版本 |
| v4 错误率 | 灰度版本的错误率 |
| v4 平均耗时 | 灰度版本的平均写作耗时 |

当灰度版本的评测得分或反馈评分显著低于旧版本时，系统自动告警，Admin 可一键回滚。
