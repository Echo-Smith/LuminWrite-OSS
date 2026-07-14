# API 规范

## REST API

### 基础信息

- 基础路径：`/api/v2`
- 认证：`Authorization: Bearer <JWT>`
- 内容类型：`application/json`
- 字符集：UTF-8

### 1. 写作

#### POST `/api/v2/agent/start`

启动写作 Agent（非流式，返回 TraceID 后通过 WebSocket 接收实时更新）。

```jsonc
// Request
{
  "message": "请基于热搜写一篇关于外卖骑手闯红灯的评论",
  "style": "yinyue",          // 风格 slug，可选，默认用户上次选择
  "mode": "guided",            // auto | writing | guided | polish
  "session_id": "sess_xxx",    // 会话 ID
  "user_materials": [],        // 用户素材
  "word_limit": 1200           // 字数限制，可选
}

// Response 200
{
  "trace_id": "trace_xxx",
  "ws_url": "/api/v2/ws/agent?trace_id=trace_xxx",
  "style": "yinyue",
  "mode": "guided"
}
```

#### POST `/api/v2/agent/trace/:trace_id`

获取某次写作的完整 Trace（用于断线恢复）。

```jsonc
// Response 200
{
  "trace_id": "trace_xxx",
  "status": "completed",
  "current_step": "post_review",
  "article": "## 标题\n\n正文...",
  "review": {
    "scores": { "factuality": 0.92, "structure": 0.85, "style": 0.90 },
    "issues": [],
    "passed": true
  },
  "step_history": [...],
  "token_usage": { "total_tokens": 8420 }
}
```

#### GET `/api/v2/agent/traces`

获取用户历史写作记录。

```
GET /api/v2/agent/traces?page=1&page_size=20&status=completed
```

### 2. 风格

#### GET `/api/v2/styles`

获取所有已发布的风格 Profile（用户端选择）。

```jsonc
// Response 200
{
  "styles": [
    {
      "slug": "yinyue",
      "name": "预设评论风格",
      "description": "植根于杭州时评专栏的深度评论风格",
      "version": 3,
      "word_range": [1000, 1500],
      "tags": ["政论", "民生", "深度评论"]
    },
    {
      "slug": "shenlun",
      "name": "应用文风格",
      "description": "[style description removed]",
      "version": 1,
      "word_range": [800, 1200],
      "tags": ["申论", "公考"]
    },
    {
      "slug": "xiaohongshu",
      "name": "小红书风格",
      "description": "轻松种草风格",
      "version": 1,
      "word_range": [300, 800],
      "tags": ["社交媒体", "种草"]
    }
  ]
}
```

#### GET `/api/v2/styles/:slug`

获取单个风格详情。

### 3. 选题

#### GET `/api/v2/topics`

获取选题列表。

```
GET /api/v2/topics?source=hotlist&page=1&page_size=20
```

```jsonc
// Response 200
{
  "topics": [
    {
      "id": "uuid",
      "title": "外卖骑手闯红灯",
      "description": "...",
      "source": "system",
      "platform": "zhihu",
      "hot_rank": 1,
      "fetched_at": "2026-07-07T08:00:00Z"
    }
  ],
  "total": 50
}
```

#### POST `/api/v2/topics`

用户上传自定义选题。

```jsonc
// Request
{
  "title": "我的自定义选题",
  "description": "想写关于这个话题的评论"
}
```

#### GET `/api/v2/topics/:id/detail`

获取选题详情（含 AI 拓展的写作角度建议）。

### 4. 反馈

#### POST `/api/v2/feedback`

提交分段反馈。

```jsonc
// Request
{
  "trace_id": "trace_xxx",
  "segments": [
    {
      "segment_type": "title",
      "segment_index": 0,
      "segment_text": "外卖骑手的红灯困境",
      "rating": 4,
      "feedback_type": "good",
      "comment": "标题切中要害但不够吸引人"
    },
    {
      "segment_type": "paragraph",
      "segment_index": 2,
      "segment_text": "在杭州的大街小巷...",
      "rating": 5,
      "feedback_type": "good",
      "comment": ""
    }
  ]
}
```

#### POST `/api/v2/feedback/adopt`

标记文章被录用（workbuddy 回调）。

```jsonc
// Request
{
  "trace_id": "trace_xxx",
  "author_name": "张三",
  "adopted_source": "workbuddy",
  "article_url": "https://..."
}
```

#### GET `/api/v2/feedback/aggregate/:style_slug`

获取某风格的反馈聚合数据。

```
GET /api/v2/feedback/aggregate/yinyue?version=3
```

### 5. 知识库检索

#### POST `/api/v2/knowledge/search`

```jsonc
// Request
{
  "query": "外卖骑手交通安全",
  "top_k": 5,
  "source": "ima"  // ima | user_upload | all
}

// Response 200
{
  "results": [
    {
      "id": "uuid",
      "title": "外卖骑手交通安全的治理路径",
      "content": "片段...",
      "similarity": 0.89,
      "source": "ima"
    }
  ]
}
```

### 6. Admin API

所有 Admin API 前缀：`/api/v2/admin`，需要 Admin Token 认证。

#### 6.1 Style Profile 管理

```
GET    /api/v2/admin/styles              // 所有 Profile（含 draft）
GET    /api/v2/admin/styles/:slug        // Profile 详情
POST   /api/v2/admin/styles              // 创建 Profile
PUT    /api/v2/admin/styles/:slug        // 编辑 Profile (保存草稿)
POST   /api/v2/admin/styles/:slug/publish // 发布 Profile (二次确认)
POST   /api/v2/admin/styles/:slug/archive // 归档 Profile
GET    /api/v2/admin/styles/:slug/versions // 版本历史
```

#### 6.2 模型配置

```
GET    /api/v2/admin/models
POST   /api/v2/admin/models
PUT    /api/v2/admin/models/:id
DELETE /api/v2/admin/models/:id
POST   /api/v2/admin/models/:id/default  // 设为默认
```

#### 6.3 API 密钥

```
GET    /api/v2/admin/keys
POST   /api/v2/admin/keys
PUT    /api/v2/admin/keys/:id
DELETE /api/v2/admin/keys/:id
POST   /api/v2/admin/keys/:id/test       // 测试连通性
```

#### 6.4 定时任务

```
GET    /api/v2/admin/cron-jobs
POST   /api/v2/admin/cron-jobs
PUT    /api/v2/admin/cron-jobs/:id
DELETE /api/v2/admin/cron-jobs/:id
POST   /api/v2/admin/cron-jobs/:id/run    // 手动触发
```

#### 6.5 评测管理

```
GET    /api/v2/admin/eval-sets
POST   /api/v2/admin/eval-sets
PUT    /api/v2/admin/eval-sets/:id
GET    /api/v2/admin/eval-sets/:id/samples
POST   /api/v2/admin/eval-sets/:id/samples       // 添加样本
PUT    /api/v2/admin/eval-samples/:id             // 编辑样本
POST   /api/v2/admin/eval-sets/:id/run            // 触发评测
GET    /api/v2/admin/eval-runs                    // 评测记录
GET    /api/v2/admin/eval-runs/:id                // 评测详情
POST   /api/v2/admin/eval-runs/:id/export         // 导出第三方平台
```

#### 6.6 Token 用量

```
GET /api/v2/admin/usage?start=2026-07-01&end=2026-07-07&group_by=day
GET /api/v2/admin/usage/models?start=2026-07-01&end=2026-07-07
```

#### 6.7 敏感词管理

```
GET    /api/v2/admin/sensitive-words
POST   /api/v2/admin/sensitive-words
PUT    /api/v2/admin/sensitive-words/:id
DELETE /api/v2/admin/sensitive-words/:id
PUT    /api/v2/admin/sensitive-words/config       // 全局严格程度配置
```

#### 6.8 灰度配置

```
GET    /api/v2/admin/styles/:slug/rollout         // 灰度配置
PUT    /api/v2/admin/styles/:slug/rollout          // 更新灰度配置
POST   /api/v2/admin/styles/:slug/rollout/preview  // 预览灰度命中情况
```

---

## WebSocket API

### 连接

```
WS /api/v2/ws/agent?trace_id=trace_xxx&token=<JWT>
```

使用 `coder/websocket` 库，支持双向通信。

### 消息格式

所有消息均为 JSON，包含 `type` 和 `payload` 两个字段。

### 客户端 → 服务端

| type | 说明 | payload |
|---|---|---|
| `agent.start` | 启动 Agent | `{ message, style, mode, session_id, user_materials, word_limit }` |
| `agent.pause` | 暂停 | `{ trace_id }` |
| `agent.resume` | 恢复 | `{ trace_id }` |
| `agent.cancel` | 取消 | `{ trace_id }` |
| `agent.confirm` | 确认（引导模式） | `{ trace_id, step, data }` |
| `agent.edit` | 编辑中间结果 | `{ trace_id, step, data }` |
| `feedback.submit` | 提交反馈 | `{ trace_id, segments }` |

### 服务端 → 客户端

| type | 说明 | payload |
|---|---|---|
| `agent.created` | Agent 已创建 | `{ trace_id, style, mode }` |
| `agent.step.start` | Step 开始 | `{ trace_id, step, step_index }` |
| `agent.step.complete` | Step 完成 | `{ trace_id, step, result, duration_ms }` |
| `agent.stream` | 流式输出 | `{ trace_id, delta }` |
| `agent.stream.done` | 流式完成 | `{ trace_id, full_text }` |
| `agent.paused` | 已暂停 | `{ trace_id, step, saved_state }` |
| `agent.resumed` | 已恢复 | `{ trace_id, step }` |
| `agent.await_input` | 等待用户输入（引导模式） | `{ trace_id, step, data, options }` |
| `agent.completed` | 完成 | `{ trace_id, article, review, token_usage }` |
| `agent.error` | 错误 | `{ trace_id, code, message, step }` |
| `agent.cancelled` | 已取消 | `{ trace_id }` |

### 引导模式交互示例

```
1. 客户端: agent.start { message: "写一篇关于外卖骑手的评论", mode: "guided" }
2. 服务端: agent.created { trace_id: "..." }
3. 服务端: agent.step.start { step: "intent" }
4. 服务端: agent.step.complete { step: "intent", result: { taskMode: "writing" } }
5. 服务端: agent.step.start { step: "search" }
6. 服务端: agent.step.complete { step: "search", result: { count: 8 } }
7. 服务端: agent.step.start { step: "outline" }
8. 服务端: agent.await_input {
     step: "outline",
     data: {
       title: "外卖骑手的红灯困境",
       outline: [
         { point: "现象切入：外卖骑手闯红灯的普遍性", type: "opening" },
         { point: "首在认知：平台算法对时间的极致压缩", type: "argument" },
         { point: "重在制度：交通法规执行的弹性空间", type: "argument" },
         { point: "贵在行动：多方协同治理的可能路径", type: "argument" },
         { point: "总结升华：城市温度与规则意识的平衡", type: "conclusion" }
       ]
     },
     options: ["confirm", "edit", "regenerate"]
   }
9. 客户端: agent.confirm { trace_id: "...", step: "outline", data: { ... 修改后的提纲 ... } }
10. 服务端: agent.step.complete { step: "outline" }
11. 服务端: agent.step.start { step: "write" }
12. 服务端: agent.stream { delta: "## 外卖骑手的红灯困境\n\n" }
13. 服务端: agent.stream { delta: "在杭州的大街小巷..." }
14. ... (持续流式输出)
15. 服务端: agent.stream.done { full_text: "完整文章..." }
16. 服务端: agent.step.start { step: "post_review" }
17. 服务端: agent.step.complete { step: "post_review", result: { scores: {...}, passed: true } }
18. 服务端: agent.completed { article: "...", review: {...}, token_usage: {...} }
```

---

## SSE API（选题推送）

### GET `/api/v2/topics/stream`

SSE 连接，推送选题更新。

```
event: topic_new
data: {"id":"uuid","title":"新热搜话题","platform":"weibo","hot_rank":3}

event: topic_update
data: {"id":"uuid","hot_rank":1}
```
