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
  "name": "我的风格",
  "description": "一句话描述",
  "version": 1,
  "tags": ["自定义"],

  // 篇幅配置
  "word_range": { "min": 1000, "max": 1500, "hard_limit": true },

  // 结构框架
  "structure": {
    "type": "three_part",              // three_part | free_form | custom
    "opening": "…",                     // 开头类型
    "body": "…",
    "conclusion": "…",
    "argument_pattern": "…",           // 分论点递进模式
    "argument_count": { "min": 2, "max": 4 }
  },

  // 修辞要求
  "rhetoric": {
    "required_metaphor": false,
    "required_parallelism": false,
    "required_rhetorical_question": false,
    "metaphor_description": ""
  },

  // 价值导向
  "value_orientation": {
    "type": "custom",                   // people_livelihood | governance | policy | custom
    "emotional_gradient": "…",
    "keywords": ["…"]
  },

  // 标题规范
  "title_guidelines": {
    "length": { "min": 10, "max": 25 },
    "style": "…",
    "forbidden_patterns": ["…"],
    "examples": ["…"]
  },

  // 系统提示词与写作规范（风格的核心内容，随 Profile 存储）
  "system_prompt": "…",
  "writing_standard": "…",

  // 事实与时态红线
  "fact_guard": {
    "future_tense_required": ["将", "预计", "计划"],
    "forbidden_results": ["…"],
    "user_material_priority": true
  },

  // 输出格式
  "output_format": {
    "use_markdown": true,
    "title_prefix": "## ",
    "separator": "---MODIFICATIONS---",
    "include_modification_notes": true,
    "note_label": "成文说明"
  },

  // 篇幅配置（按任务类型）
  "length_profiles": {
    "writing": { "min": 1000, "max": 1500 },
    "polish_short": { "min": 100, "max": 600 },
    "polish_long": { "min": 600, "max": 1200 }
  }
}
```

## 3. 风格目录

**LuminWrite OSS 不内置任何风格内容资产。** 编辑风格指南属于第一方内容资产，
有意不进入开源仓库。全新安装仅内置一个引擎级通用骨架 `default`（「通用写作
风格」：三段式结构 + 事实约束，无栏目定位、无范文语料），保证开箱可用；
除此之外的风格目录为空。

建立风格目录的三种方式：

1. **风格工作台（推荐）**：工作台 → 风格 → 风格构建器，上传范文自动提炼
   或手工填写结构/修辞/标题规范；
2. **Admin 风格 API**：`POST /api/v2/admin/styles` 直接写入 Profile JSON
   （数据结构见上节），支持版本与发布流程；
3. **自行 seed**：参考本节字段结构构造 JSON，经 Admin API 导入。

商业部署可将自有风格目录随部署包分发；开源部署请使用自己的风格内容。
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
       └─ 未命中 → 降级到默认 Profile（当前已发布版本的 latest）
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
│  ┌─────────┐ ┌─────────┐              │
│  │ 风格 A    │ │ 风格 B   │              │
│  │ ✓ 已选择  │ │         │              │
│  │ 1000-1500│ │ 800-1200│              │
│  └─────────┘ └─────────┘              │
│  （目录内容由部署方自行建立）              │
└─────────────────────────────────────────┘
```

用户选择后，Profile slug 随写作请求一起发送，Agent Engine 在 `WriteStep` 中加载对应 Profile。
