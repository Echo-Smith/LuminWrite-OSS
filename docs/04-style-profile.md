# Style Profile 热插拔系统

## 1. 设计目标

- **用户端可选**：用户根据写作需求在界面端选择风格，系统不强制绑定
- **运行时热加载**：从 DB 读取已发布的 Profile，无需重启服务
- **Admin 可编辑**：Admin 后台可创建/编辑/发布 Profile
- **二次确认发布**：编辑后可"保存草稿"或"确认发布"
- **版本管理**：每次发布生成新版本，可回滚
- **用户自定义风格（预留）**：从用户上传的范文提取风格特征，初期不上架但预留埋点

## 2. Profile 数据结构

```jsonc
{
  "slug": "my-style",
  "name": "...",
  "//": "schema skeleton only - preset style content is not part of this repository"
}
```

## 3. 内置风格

### 3.1 预设评论风格（yinyue）

- 来源：V1 `writing-standard.md` + `system.js`
- 定位：深度政论时评
- 篇幅：1000-1500 字
- 结构：三段式闭环 + 首在-重在-贵在递进
- 核心修辞：比喻 + 排比 + 设问
- 状态：上架

### 3.2 应用文风格（shenlun）

- 定位：公务员申论写作
- 篇幅：800-1200 字
- 结构：提出问题 → 分析问题 → 解决问题
- 核心修辞：规范表达 + 政策引用
- 状态：上架

### 3.3 小红书风格（xiaohongshu）

- 定位：社交媒体种草/分享
- 篇幅：300-800 字
- 结构：吸引眼球的开头 → 核心内容 → 互动引导
- 核心修辞：emoji + 短句 + 口语化
- 状态：上架

### 3.4 用户自定义风格（custom）— 预留

- 用户上传范文 → 系统提取风格特征（句式、用词、结构、修辞偏好）
- 生成用户专属 Profile
- 初期不上架，预留埋点（`profile_extraction_triggered` 事件）

## 4. Profile 加载流程

```
用户选择风格 slug
       │
       ▼
查询 style_profiles WHERE slug=? AND status='published'
       │
       ├─ 命中 → 从 DB 加载 config (JSONB)
       │         │
       │         └─ 注入到 ExecutionContext.StyleProfile
       │
       └─ 未命中 → 降级到默认 Profile (yinyue latest published)
```

### 4.1 缓存策略

- L1：进程内 LRU 缓存（5 分钟 TTL）
- L2：Redis（可选，10 分钟 TTL）
- 缓存键：`style:{slug}:published`
- 发布新版本时主动清除缓存

## 5. Admin 发布流程

```
Admin 编辑 Profile
       │
       ├─ "保存草稿" → status='draft'，不生效
       │
       └─ "确认发布" → 弹出二次确认弹窗
                        │
                        ├─ 确认 → 生成新版本号
                        │         status='published'
                        │         旧版本 status='archived'
                        │         清除缓存
                        │         触发评测任务
                        │         记录 published_at / published_by
                        │
                        └─ 取消 → 保持 draft 状态
```

### 5.1 发布校验

发布前自动校验：
1. `system_prompt` 不为空
2. `word_range.max > word_range.min`
3. `title_guidelines.forbidden_patterns` 正则可编译
4. `structure.type` 为有效值
5. JSON 格式合法

### 5.2 版本回滚

Admin 可在版本历史中选择任意旧版本"重新发布"，生成新版本号但使用旧配置。

## 6. 灰度与 Profile 的关系

Profile 本身不直接控制灰度——灰度由独立的 `rollout` 配置管理（见灰度路由文档）。

但 Profile 版本发布时可以同时配置灰度策略：
- 发布时选择"全量发布"或"灰度发布"
- 灰度发布时设置灰度范围（UID 白名单 / 百分比）
- 灰度期间新旧版本同时在线
- 灰度验证通过后手动"全量切换"

## 7. 风格选择器 UI 交互

```
用户端（写作工作台）
┌─────────────────────────────────────────┐
│  选择写作风格                             │
│  ┌─────────┐ ┌─────────┐ ┌──────────┐  │
│  │ 预设评论风格  │ │ 应用文风格  │ │ 小红书风格 │  │
│  │ ✓ 已选择  │ │         │ │          │  │
│  │ 1000-1500│ │ 800-1200│ │ 300-800  │  │
│  └─────────┘ └─────────┘ └──────────┘  │
│  [自定义风格 — 敬请期待]                  │
└─────────────────────────────────────────┘
```

用户选择后，Profile slug 随写作请求一起发送，Agent Engine 在 `WriteStep` 中加载对应 Profile。
