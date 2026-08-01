# DeepSeek V4 Flash Responses API 迁移 — 测试报告

## 概述

本报告记录了将 Writing Agent V2 从 DeepSeek Chat Completions API 迁移到 Responses API 的 A/B 测试方案、预期收益分析和实施细节。

## 一、变更清单

### 1.1 IMA 知识库删除

已完全移除 IMA (ima.qq.com) 知识库集成，由内置知识库（PostgreSQL + pgvector + paradedb BM25 + GraphRAG）替代。

| 删除项 | 文件 |
|--------|------|
| `IMAConfig` 结构体 | `config/config.go` |
| `IMAClient` 结构体及方法 | `tools/search.go` |
| `cronIMASync()` 函数 | `handlers_admin_mgmt.go` |
| `ima_sync` cron job 分支 | `handlers_admin_mgmt.go` |
| `IMAClient()` 方法 | `tools/search.go` |
| IMA 搜索分支 | `tools/search.go` Search() |
| IMA 环境变量 | `.env`, `.env.example`, `.env.docker`, `.env.docker.example` |
| IMA 下拉选项 | `frontend/src/pages/admin/api-keys.tsx` |
| `handleHealth` 中的 `ima_configured` | `server.go` |
| 搜索工具描述中的 IMA | `server.go` buildToolRegistry |

### 1.2 模型降级策略 (V4 Flash → Pro 按需切换)

默认模型保持 `deepseek-v4-flash`，仅在需要深度推理的步骤切换到 `deepseek-v4-pro`：

| Step | 模型 | Thinking | 原因 |
|------|------|----------|------|
| IntentStep | Flash | ❌ | 分类任务，低要求 |
| QueryPlanStep | Flash | ❌ | 查询生成 |
| SearchStep (mock) | Flash | ❌ | 仅降级时使用 |
| CompressStep | Flash | ❌ | 摘要压缩 |
| ChatStep | Flash | ❌ | 快速对话 |
| **OutlineStep** | **Pro** | ✅ high | 提纲需要深度推理 |
| **WriteStep (writing)** | **Pro** | ✅ high | 核心写稿，最高质量要求 |
| WriteStep (polish等) | Flash | ❌ | 机械性文本操作 |
| **PostReviewStep (主评审)** | **Pro** | ✅ high | 质量评审需要判断力 |
| **PostReviewStep (标题评审)** | **Pro** | ✅ high | 标题质量评估 |
| AutoFixStep | Flash | ❌ | 指令执行 |
| factCheckArticle | Flash | ❌ | 信息提取 |

**预期成本节省**：约 60-70%（机械性任务从 Pro 降为 Flash）

### 1.3 Responses API 客户端

新增文件：
- `backend/internal/tools/deepseek_responses.go` — Responses API 实现
- `backend/internal/tools/deepseek_chatcompletions.go` — 提取出的 Chat Completions 实现
- `backend/internal/tools/ab_metrics.go` — A/B 测试指标收集

关键能力：
- `responsesChat()` — 非流式 Responses API 请求
- `responsesStream()` — 流式 Responses API，解析结构化 SSE 事件
- `toResponsesRequest()` — 将 LLMRequest 转换为 Responses API 格式
  - 系统消息提取为 `instructions` 参数（独立缓存）
  - `max_tokens` → `max_output_tokens`
  - `response_format` → `text.format`
- 结构化 SSE 事件解析（`event: <type>` + `data: <json>`）
  - `response.output_text.delta` → onDelta
  - `response.reasoning.delta` → onReasoning
  - `response.completed` → token 用量（含 cached_tokens）
  - `response.error` → 错误处理

### 1.4 A/B 测试基础设施

配置：
```env
# .env
DEEPSEEK_RESPONSES_API_RATIO=0.5  # 50% 流量走 Responses API
```

路由逻辑：
- `Chat()` 和 `ChatStreamWithReasoning()` 按比例随机路由
- 每个请求记录：request_count, prompt_tokens, cache_hit_tokens, completion_tokens, latency
- Admin API: `GET /api/v2/admin/ab-metrics` 返回对比指标

指标对比维度：
| 指标 | Chat Completions | Responses API |
|------|-----------------|---------------|
| `cache_hit_rate` | `cache_hit_tokens / prompt_tokens` | 同左 |
| `avg_latency_ms` | `total_latency / request_count` | 同左 |
| `completion_tokens` | 总输出 token | 同左 |

### 1.5 Instructions 分层

重构了 3 个关键步骤的 system prompt：

| Step | 静态 Instructions | 动态 Input |
|------|------------------|------------|
| OutlineStep | "你是写作提纲生成器。根据话题和素材生成文章提纲。只返回 JSON。" | 话题 + 素材 |
| PostReviewStep (主评审) | "你是文章正文质量评审员。只评审正文，不评审标题。只返回 JSON。" | 文章 + profile规则 + 事实核查 + 日期 |
| PostReviewStep (标题评审) | "你是文章标题质量评审员。只返回 JSON，不要解释。" | 标题 + 正文 + 标题规则 |

**Chat Completions 兼容**：当走 Chat Completions API 时，`instructions` 自动作为 `messages[0]` (system role) 插入。
**Responses API 优势**：`instructions` 作为顶层参数发送，DeepSeek 服务端可独立缓存，即使 `input` 每次变化也能命中。

## 二、预期收益量化

### 2.1 缓存命中率提升

以 `PostReviewStep` 为例（系统提示 ~500 tokens，用户消息 ~3000 tokens）：

| 场景 | Chat Completions | Responses API |
|------|-----------------|---------------|
| instructions tokens | 0 (混在 messages 中) | 500 (独立缓存) |
| input tokens | 3500 | 3000 |
| 第二次请求 cache_hit | 0 (system 因日期变化) | 500 (instructions 不变) |
| 缓存命中率 | **0%** | **~14%** |

### 2.2 成本节省

DeepSeek 缓存命中 token 价格为正常价格的 1/4：

| 模型 | 正常输入价格 | 缓存命中价格 | 节省比例 |
|------|------------|------------|---------|
| V4 Flash | 1 元/百万 token | 0.02 元/百万 token | 98% |
| V4 Pro | 3 元/百万 token | 0.025 元/百万 token | 99% |

假设 14% 缓存命中率 → prompt 成本降低约 `14% × 98% ≈ 13.7%`

### 2.3 模型降级成本节省

| Step | 之前 | 之后 | 节省 |
|------|------|------|------|
| IntentStep | Pro (3元) | Flash (1元) | 66% |
| CompressStep | Pro (3元) | Flash (1元) | 66% |
| factCheckArticle | Pro (3元) | Flash (1元) | 66% |
| ChatStep | Pro (3元) | Flash (1元) | 66% |
| AutoFixStep | Pro (3元) | Flash (1元) | 66% |

**总体预估**：每次写作任务的 LLM 调用成本降低约 50-60%

## 三、测试计划

### 3.1 启动 A/B 测试

```bash
# .env.docker 中设置
DEEPSEEK_RESPONSES_API_RATIO=0.5
```

### 3.2 收集指标

```bash
# 查看 A/B 测试指标
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:8080/api/v2/admin/ab-metrics
```

### 3.3 决策标准

| 指标 | 目标 | 行动 |
|------|------|------|
| Responses API cache_hit_rate > Chat Completions +10% | ✅ 确认收益 | `DEEPSEEK_RESPONSES_API_RATIO=1.0` |
| avg_latency_ms 降低 > 15% | ✅ 确认收益 | 全量切换 |
| error rate > 1% | ❌ 异常 | 回滚到 0.0 |
| 无明显质量回退 | ✅ 通过 | 全量切换 |

### 3.4 测试周期

```
Day 1: ratio=0.1 (10% 验证兼容性)
Day 2: ratio=0.5 (50/50 对比收集指标)
Day 3: 分析数据，确认收益
Day 4+: ratio=1.0 (全量切换) 或回滚
```

## 四、新增文件

| 文件 | 用途 |
|------|------|
| `backend/internal/tools/deepseek_responses.go` | Responses API 客户端 |
| `backend/internal/tools/deepseek_chatcompletions.go` | 提取的 Chat Completions 实现 |
| `backend/internal/tools/ab_metrics.go` | A/B 测试指标收集器 |
| `docs/responses-api-migration-report.md` | 本测试报告 |

## 五、修改文件

| 文件 | 变更 |
|------|------|
| `backend/internal/tools/deepseek.go` | 添加 ModelV4Pro 常量、Instructions 字段、A/B 路由 |
| `backend/internal/tools/search.go` | 删除 IMA 客户端 |
| `backend/internal/config/config.go` | 删除 IMAConfig、添加 ResponsesAPIRatio |
| `backend/internal/server/server.go` | 删除 IMA 接线、添加 A/B 配置和端点 |
| `backend/internal/server/handlers_admin_mgmt.go` | 删除 cronIMASync、添加 AB metrics 端点 |
| `backend/internal/engine/steps/steps.go` | 4 处 LLM 调用添加 WithModel+WithInstructions |
| `frontend/src/pages/admin/api-keys.tsx` | 删除 IMA 选项 |
| `.env`, `.env.example`, `.env.docker`, `.env.docker.example` | 删除 IMA 变量、添加 ResponsesAPIRatio |

## 六、风险评估

| 风险 | 等级 | 缓解 |
|------|------|------|
| Responses API 兼容性问题 | 低 | A/B 双轨并行，可随时回滚 |
| V4 Flash 质量不足 | 低 | 仅机械性任务使用 Flash，核心步骤仍用 Pro |
| 缓存命中率低于预期 | 中 | A/B 测试验证后再全量切换 |
| Responses API 不支持某些功能 | 低 | 已确认不支持 store/previous_response_id，无影响 |
