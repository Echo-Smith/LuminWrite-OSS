# Writing Agent V2 — 运维手册 (Runbook)

> **适用对象**：后端开发者、运维人员、值班工程师
> **更新日期**：2026-08-03
> **关联文档**：`PROJECT_LEDGER.md`（项目台账）、`agentops-health-check-2026-08-03.md`（审计报告）

---

## 目录

1. [服务架构与端点](#1-服务架构与端点)
2. [健康检查与监控](#2-健康检查与监控)
3. [常见事故处理](#3-常见事故处理)
4. [数据库运维](#4-数据库运维)
5. [部署与回滚](#5-部署与回滚)
6. [配置变更](#6-配置变更)
7. [安全事件响应](#7-安全事件响应)
8. [应急联系人](#8-应急联系人)

---

## 1. 服务架构与端点

### 1.1 服务清单

| 服务 | 容器名 | 端口 | 健康检查 |
|---|---|---|---|
| Backend (Go) | writing-agent-backend | 8080 | `GET /health` |
| Frontend (Nginx) | writing-agent-frontend | 3002→8080 | `GET /health` (Nginx) |
| PostgreSQL | writing-agent-pg | 5432 | `pg_isready -U postgres` |
| Docreader | writing-agent-docreader | 50051 (gRPC) | 容器启动即健康 |

### 1.2 关键端点

| 端点 | 用途 |
|---|---|
| `GET /health` | 后端健康状态（LLM/DB/Search/Embedding 配置检查） |
| `GET /metrics` | Prometheus 指标导出 |
| `GET /api/v2/ws/agent` | WebSocket Agent 通信 |
| `GET /api/v2/sse/topics` | SSE 热点话题推送 |
| `GET /api/v2/admin/stats` | 管理后台统计（需 Admin Token） |

### 1.3 关键配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `AGENT_MODE` | `pipeline` | Agent 执行模式 (`pipeline` / `unified`) |
| `AGENT_TIMEOUT` | `5m` | Agent 全局超时 |
| `AGENT_MAX_TOKENS` | `300000` | Token 预算上限 |
| `AGENT_MAX_CONCURRENT` | `10` | 全局并发上限 |
| `AGENT_MAX_CONCURRENT_PER_USER` | `3` | 每用户并发上限 |
| `AGENT_CIRCUIT_BREAKER_FAILS` | `3` | LLM 连续失败断路器阈值 |
| `WS_AUTH_ENABLED` | `false` | WebSocket 认证开关 |
| `RATE_LIMIT_ENABLED` | `true` | 速率限制开关 |

---

## 2. 健康检查与监控

### 2.1 日常健康检查

```bash
# 1. 检查后端健康
curl -s http://localhost:8080/health | jq .

# 2. 检查容器状态
docker compose ps

# 3. 检查 Prometheus 指标
curl -s http://localhost:8080/metrics | grep -E "agent_executions_total|websocket_connections_active|llm_errors_total"

# 4. 检查数据库连接
docker exec writing-agent-pg pg_isready -U postgres -d writing_agent_v2
```

### 2.2 关键指标告警阈值

| 指标 | 告警阈值 | 严重级别 | 处理方式 |
|---|---|---|---|
| `agent_executions_total{status="failed"}` 5分钟内 > 10 | P1 | 严重 | 检查 LLM API 状态、DB 连接 |
| `llm_errors_total` 5分钟内 > 20 | P1 | 严重 | 检查 AI_API_KEY、额度余额 |
| `websocket_errors_total{type="accept"}` > 5 | P2 | 警告 | 检查 WS 连接数、内存使用 |
| `agent_execution_duration_seconds` P95 > 120s | P2 | 警告 | 检查 LLM 延迟、搜索源响应时间 |
| DB 连接数 > 20 | P2 | 警告 | 检查 DB_MAX_OPEN_CONNS、慢查询 |
| 磁盘使用 > 80% | P1 | 严重 | 清理日志、扩展存储 |

### 2.3 日志查看

```bash
# 后端日志（最近 100 行）
docker logs --tail 100 writing-agent-backend

# 按日志级别过滤
docker logs writing-agent-backend 2>&1 | grep '"level":"ERROR"'

# 搜索特定 trace
docker logs writing-agent-backend 2>&1 | grep "trace_abc12345"

# PostgreSQL 慢查询日志
docker logs writing-agent-pg 2>&1 | grep "duration:"
```

---

## 3. 常见事故处理

### 3.1 Agent 执行卡住 (Stuck Run)

**症状**：用户报告写作任务长时间无响应，前端显示"进行中"但不更新。

**诊断步骤**：

```bash
# 1. 查找长时间运行的 Agent 会话
# 服务器内存中的会话无法直接查询，但可以通过 DB trace 查看状态
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT trace_id, user_id, style_slug, status, current_step, started_at,
         EXTRACT(EPOCH FROM (NOW() - started_at))::int AS elapsed_seconds
  FROM agent_traces
  WHERE status IN ('running', 'paused')
    AND started_at < NOW() - INTERVAL '10 minutes'
  ORDER BY started_at;
"

# 2. 检查是否有 panic 日志
docker logs writing-agent-backend 2>&1 | grep "panic" | tail -20

# 3. 检查 LLM API 是否可达
curl -s -o /dev/null -w "%{http_code}" https://api.deepseek.com/v1/models \
  -H "Authorization: Bearer $AI_API_KEY"
```

**恢复操作**：

```bash
# 情况 A：LLM API 不可达 → 等待恢复后用户重试
# 情况 B：LLM API 可达但 Agent 卡住 → 需要重启后端清理内存中的卡住会话

# 重启后端（优雅关闭，等待 30s）
docker compose restart backend

# 重启后，卡住的会话会从内存清除，用户需要重新发起写作请求
# DB 中的 trace 记录会保留，状态为 'running'（需要手动标记为 'failed'）

# 手动标记卡住的 trace 为 failed
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  UPDATE agent_traces
  SET status = 'failed', error = 'manually marked as failed due to stuck run'
  WHERE status IN ('running', 'paused')
    AND started_at < NOW() - INTERVAL '30 minutes';
"
```

**预防**：
- `AGENT_TIMEOUT` 默认 5 分钟会自动超时
- `AGENT_CIRCUIT_BREAKER_FAILS` 默认 3 次连续失败会触发断路器
- 如果频繁卡住，考虑降低 `AGENT_TIMEOUT`

### 3.2 LLM API 错误 / 额度耗尽

**症状**：Agent 执行失败，日志中出现 "quota exceeded" 或 "rate limit"。

**诊断步骤**：

```bash
# 1. 检查后端日志中的 LLM 错误
docker logs writing-agent-backend 2>&1 | grep -E "quota|rate.limit|insufficient.balance" | tail -20

# 2. 检查 LLM 指标
curl -s http://localhost:8080/metrics | grep "llm_errors_total"

# 3. 检查 DeepSeek API 余额（需要登录控制台或调用 API）
curl -s https://api.deepseek.com/user/balance \
  -H "Authorization: Bearer $AI_API_KEY"
```

**恢复操作**：

```bash
# 情况 A：额度耗尽 → 充值后自动恢复
# 情况 B：Rate limit → 降低并发或增加间隔
# 情况 C：API 服务故障 → 等待恢复或切换到备用模型

# 切换备用模型（通过管理后台 API）
curl -X PUT http://localhost:8080/api/v2/admin/models/{model_id} \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"is_default": true, "is_active": true}'

# 或者通过 DB 直接切换
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  UPDATE admin_model_configs SET is_default = true WHERE model_name = 'deepseek-chat';
  UPDATE admin_model_configs SET is_default = false WHERE model_name != 'deepseek-chat';
"
```

**预防**：
- 设置额度告警（DeepSeek 控制台）
- 配置备用模型并在管理后台注册
- 断路器（`AGENT_CIRCUIT_BREAKER_FAILS`）会自动在连续失败时停止

### 3.3 数据库连接耗尽

**症状**：后端日志出现 "too many connections" 或 DB 查询超时。

**诊断步骤**：

```bash
# 1. 检查当前连接数
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT count(*), state, application_name
  FROM pg_stat_activity
  GROUP BY state, application_name;
"

# 2. 检查慢查询
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT pid, now() - pg_stat_activity.query_start AS duration, query
  FROM pg_stat_activity
  WHERE state = 'active' AND now() - pg_stat_activity.query_start > interval '5 seconds'
  ORDER BY duration DESC;
"

# 3. 检查后端配置的连接池大小
docker exec writing-agent-backend env | grep DB_MAX
```

**恢复操作**：

```bash
# 1. 终止空闲连接
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT pg_terminate_backend(pid)
  FROM pg_stat_activity
  WHERE state = 'idle'
    AND application_name = ''
    AND now() - state_change > interval '10 minutes';
"

# 2. 如果连接数持续过高，重启后端
docker compose restart backend

# 3. 调整连接池大小（修改 .env.docker）
# DB_MAX_OPEN_CONNS=15  # 降低默认 25
# DB_MAX_IDLE_CONNS=3   # 降低默认 5
```

### 3.4 客户端 WebSocket 断连后无法恢复

**症状**：用户刷新页面后，之前的写作进度丢失。

**诊断步骤**：

```bash
# 检查 trace 是否仍在内存中（如果后端未重启，会话应仍在）
docker logs writing-agent-backend 2>&1 | grep "session resumed" | tail -10

# 检查前端是否发送了 session.resume 消息
docker logs writing-agent-backend 2>&1 | grep "session.resume" | tail -10
```

**恢复操作**：

```bash
# 情况 A：后端未重启，会话仍在内存 → 前端发送 session.resume 即可恢复
# 情况 B：后端已重启，会话丢失 → 从 DB 恢复最后状态
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT trace_id, status, article_title, current_step, article IS NOT NULL AS has_article
  FROM agent_traces
  WHERE user_id = '<user_id>'
  ORDER BY started_at DESC LIMIT 5;
"

# 如果 trace 状态是 'completed'，文章已保存，用户可在历史记录中查看
# 如果 trace 状态是 'running' 但后端已重启，标记为 'failed' 并通知用户重试
```

### 3.5 记忆系统故障

**症状**：记忆检索/提取失败，日志中出现 memory 相关错误。

**诊断步骤**：

```bash
# 1. 检查记忆服务是否初始化
docker logs writing-agent-backend 2>&1 | grep "memory: service initialized" | tail -5

# 2. 检查 embedding 服务是否可用
docker logs writing-agent-backend 2>&1 | grep "embedding client configured" | tail -5

# 3. 检查记忆表是否有数据
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT 'memories' AS table_name, count(*) FROM memories
  UNION ALL
  SELECT 'memory_entities', count(*) FROM memory_entities
  UNION ALL
  SELECT 'short_term_messages', count(*) FROM short_term_messages
  UNION ALL
  SELECT 'working_summaries', count(*) FROM working_summaries;
"

# 4. 检查记忆文件目录
ls -la data/memory/
```

**恢复操作**：

```bash
# 情况 A：Embedding 服务不可用 → 检查 DashScope API Key
docker exec writing-agent-backend env | grep DASHSCOPE

# 情况 B：记忆文件损坏 → 从 DB 重新导出
# 通过 API 重新导出记忆文件
curl -X POST http://localhost:8080/api/v2/memories/file/export \
  -H "Authorization: Bearer $JWT_TOKEN"

# 情况 C：DB 记忆数据异常 → 清理并重新提取
# 谨慎操作！先备份
docker exec writing-agent-pg pg_dump -U postgres writing_agent_v2 memories > /tmp/memories_backup.sql

# 清理异常记忆（例如空内容或无 embedding 的记忆）
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  DELETE FROM memories WHERE content IS NULL OR content = '';
"

# 重新触发记忆提取（需要用户重新提交反馈）
```

### 3.6 编辑部 Agent 死锁

**症状**：编辑部任务卡在某个状态，Agent 不执行。

**诊断步骤**：

```bash
# 1. 检查编辑部任务状态
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT id, title, status, assignee_type, owner_id, updated_at,
         EXTRACT(EPOCH FROM (NOW() - updated_at))::int AS stale_seconds
  FROM editorial_tasks
  WHERE status IN ('research', 'writing', 'review')
  ORDER BY updated_at DESC;
"

# 2. 检查是否有未释放的 Lease
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT task_id, agent_role, acquired_at, expires_at,
         NOW() > expires_at AS is_expired
  FROM editorial_leases
  WHERE released_at IS NULL
  ORDER BY acquired_at;
"
```

**恢复操作**：

```bash
# 1. 释放过期的 Lease
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  UPDATE editorial_leases
  SET released_at = NOW(), release_reason = 'manual_cleanup'
  WHERE released_at IS NULL AND NOW() > expires_at;
"

# 2. 如果 Lease 已释放但任务仍卡住，手动推进任务状态
# 例如：将 research 阶段的任务推进到 writing
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  UPDATE editorial_tasks
  SET status = 'writing', assignee_type = 'writing_agent', updated_at = NOW()
  WHERE status = 'research' AND updated_at < NOW() - INTERVAL '15 minutes';
"

# 3. 如果 Agent 执行器未注册，检查日志
docker logs writing-agent-backend 2>&1 | grep "orchestrator: executor registered" | tail -5
```

---

## 4. 数据库运维

### 4.1 迁移操作

```bash
# 查看当前迁移版本
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT version, name, applied_at FROM schema_migrations ORDER BY version DESC LIMIT 10;
"

# 手动执行迁移（通常后端启动时自动执行）
docker exec writing-agent-backend /app/server -migrate

# 回滚最近一次迁移（谨慎！先备份）
# 查看回滚 SQL
cat backend/internal/database/migrations/$(ls backend/internal/database/migrations/ | sort -V | tail -2 | head -1 | sed 's/.up.sql/.down.sql/')
```

### 4.2 备份与恢复

```bash
# 完整备份
docker exec writing-agent-pg pg_dump -U postgres writing_agent_v2 > backup_$(date +%Y%m%d).sql

# 仅备份关键表
docker exec writing-agent-pg pg_dump -U postgres writing_agent_v2 \
  -t agent_traces -t memories -t memory_entities -t editorial_tasks \
  > backup_critical_$(date +%Y%m%d).sql

# 恢复
docker exec -i writing-agent-pg psql -U postgres -d writing_agent_v2 < backup_20260803.sql

# 备份记忆文件
tar -czf memory_backup_$(date +%Y%m%d).tar.gz data/memory/
```

### 4.3 常用查询

```sql
-- 用户写作统计
SELECT user_id, count(*) AS total, 
       count(*) FILTER (WHERE status = 'completed') AS completed,
       count(*) FILTER (WHERE status = 'failed') AS failed,
       avg(EXTRACT(EPOCH FROM (completed_at - started_at)))::int AS avg_duration_sec
FROM agent_traces
WHERE started_at > NOW() - INTERVAL '7 days'
GROUP BY user_id ORDER BY total DESC LIMIT 20;

-- 搜索源质量统计
SELECT source, count(*) AS total,
       avg(credibility_score) FILTER (WHERE credibility_score > 0) AS avg_credibility
FROM search_results_cache  -- 如果有缓存表
GROUP BY source ORDER BY total DESC;

-- 编辑部任务流转统计
SELECT status, count(*) FROM editorial_tasks GROUP BY status ORDER BY count DESC;

-- 敏感词命中统计
SELECT word, category, severity, count(*) AS hit_count
FROM sensitive_word_hits  -- 如果有记录表
GROUP BY word, category, severity ORDER BY hit_count DESC LIMIT 20;
```

---

## 5. 部署与回滚

### 5.1 部署流程

```bash
# 1. 拉取最新代码
git pull origin main

# 2. 检查 .env.docker 配置
diff .env.docker.example .env.docker

# 3. 构建并启动
docker compose up -d --build

# 4. 验证健康
sleep 10
curl -s http://localhost:8080/health | jq .
docker compose ps

# 5. 检查迁移是否成功
docker logs writing-agent-backend 2>&1 | grep -E "migration|migrate" | tail -10
```

### 5.2 回滚流程

```bash
# 1. 回滚代码
git log --oneline -5  # 找到上一个稳定版本
git checkout <stable_commit>

# 2. 回滚数据库迁移（如果需要）
# 查看迁移文件
ls backend/internal/database/migrations/ | sort -V | tail -5

# 执行回滚 SQL（手动）
docker exec -i writing-agent-pg psql -U postgres -d writing_agent_v2 < \
  backend/internal/database/migrations/0XX_xxx.down.sql

# 更新迁移记录
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  DELETE FROM schema_migrations WHERE version = XXX;
"

# 3. 重新构建并启动
docker compose up -d --build

# 4. 验证
curl -s http://localhost:8080/health | jq .
```

### 5.3 蓝绿部署（单机）

```bash
# 1. 启动新版本（不同端口）
PORT=8081 docker compose -p writing-agent-v2-new up -d --build

# 2. 验证新版本
curl -s http://localhost:8081/health | jq .

# 3. 切换 Nginx 上游
# 编辑 frontend/nginx.conf，将 proxy_pass 改为 8081
docker compose -p writing-agent-v2-new restart frontend

# 4. 关闭旧版本
docker compose -p writing-agent-v2 down

# 5. 重命名新版本
docker compose -p writing-agent-v2-new rename writing-agent-v2  # 不支持，需手动
```

---

## 6. 配置变更

### 6.1 切换 Agent 模式

```bash
# 切换到 Unified (ReAct) 模式
# 编辑 .env.docker
AGENT_MODE=unified

# 重启后端
docker compose restart backend

# 验证
docker logs writing-agent-backend 2>&1 | grep "using unified agent" | tail -5
```

### 6.2 添加/修改搜索源 API Key

```bash
# 通过管理后台 API（推荐，无需重启）
curl -X POST http://localhost:8080/api/v2/admin/api-keys \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "provider": "tavily",
    "api_key": "tvly-xxx",
    "base_url": "https://api.tavily.com/search"
  }'

# 或者修改 .env.docker 后重启
# TAVILY_API_KEY=tvly-xxx
docker compose restart backend
```

### 6.3 启用/禁用 MCP Server

```bash
# 编辑 .env.docker 中的 MCP_SERVERS
MCP_SERVERS='[{"name":"filesystem","transport":"stdio","command":"npx","args":["-y","@anthropic/mcp-filesystem"]}]'

# 启用进程内 MCP 服务端
MCP_SERVER_ENABLED=true
MCP_SERVER_HTTP_ADDR=:9090

# 重启后端
docker compose restart backend

# 验证 MCP 连接
docker logs writing-agent-backend 2>&1 | grep "MCP server registered" | tail -5
```

### 6.4 调整 Agent 并发限制

```bash
# 修改 .env.docker
AGENT_MAX_CONCURRENT=20        # 全局并发上限
AGENT_MAX_CONCURRENT_PER_USER=5  # 每用户并发上限

# 重启后端
docker compose restart backend
```

---

## 7. 安全事件响应

### 7.1 Prompt Injection 攻击

**症状**：Agent 行为异常，输出了与用户请求无关的内容，或执行了非预期操作。

**诊断步骤**：

```bash
# 1. 检查 guardrails 日志
docker logs writing-agent-backend 2>&1 | grep "guardrail" | tail -20

# 2. 检查红队评估结果
curl -s http://localhost:8080/api/v2/evaluation/runs \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq '.[] | select(.trigger_type == "redteam")'

# 3. 检查可疑 trace
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT trace_id, user_id, user_input, status, started_at
  FROM agent_traces
  WHERE started_at > NOW() - INTERVAL '1 hour'
    AND (user_input ILIKE '%ignore%previous%instruction%'
      OR user_input ILIKE '%system%prompt%'
      OR user_input ILIKE '%disregard%above%')
  ORDER BY started_at DESC;
"
```

**响应操作**：

```bash
# 1. 如果检测到活跃攻击，临时禁用可疑用户
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  UPDATE users SET role = 'suspended' WHERE id = '<suspicious_user_id>';
"

# 2. 查看 guardrails 拦截记录
docker logs writing-agent-backend 2>&1 | grep "injection_pattern_detected" | tail -20

# 3. 如果 guardrails 被绕过，更新防护规则
# 编辑 backend/internal/engine/guardrails.go 中的 injectionPatterns
# 添加新的攻击模式
# 重新部署
```

### 7.2 敏感信息泄露

**症状**：Agent 输出中包含 API Key、密码、个人隐私等信息。

**响应操作**：

```bash
# 1. 立即撤销泄露的 API Key
# 通过管理后台更新 API Key
curl -X PUT http://localhost:8080/api/v2/admin/api-keys/{id} \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"api_key": "new-key-value"}'

# 2. 检查是否在日志中泄露
docker logs writing-agent-backend 2>&1 | grep -i "api.key\|secret\|password" | tail -20

# 3. 添加敏感词到过滤列表
curl -X POST http://localhost:8080/api/v2/admin/sensitive-words \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"word": "leaked-key-pattern", "category": "security", "severity": "high", "action": "block"}'
```

### 7.3 恶意用户滥用

**症状**：某用户高频发起写作请求，消耗大量 Token。

**响应操作**：

```bash
# 1. 查看用户 Token 使用统计
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT user_id, count(*) AS requests, sum(total_tokens) AS total_tokens
  FROM agent_traces
  WHERE started_at > NOW() - INTERVAL '24 hours'
  GROUP BY user_id ORDER BY total_tokens DESC LIMIT 10;
"

# 2. 降低该用户的并发限制（当前全局限制，需要代码层面实现 per-user 限制）

# 3. 紧急情况：封禁用户
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  UPDATE users SET role = 'banned' WHERE id = '<abusive_user_id>';
"

# 4. 临时降低全局速率限制
# 编辑 .env.docker
# RATE_LIMIT_REQUESTS=60  # 从 120 降低
docker compose restart backend
```

---

## 8. 应急联系人

| 角色 | 联系方式 | 职责 |
|---|---|---|
| 值班工程师 | (待填写) | 第一响应，执行恢复操作 |
| 后端负责人 | (待填写) | 代码层面问题排查 |
| DBA | (待填写) | 数据库事故处理 |
| 安全负责人 | (待填写) | 安全事件响应 |

---

## 附录 A: 快速诊断脚本

```bash
#!/bin/bash
# quick-diagnose.sh — 一键诊断所有服务状态
echo "=== Docker Container Status ==="
docker compose ps

echo -e "\n=== Backend Health ==="
curl -s http://localhost:8080/health | jq .

echo -e "\n=== Key Metrics ==="
curl -s http://localhost:8080/metrics | grep -E \
  "agent_executions_total|websocket_connections_active|llm_errors_total|llm_calls_total"

echo -e "\n=== Database Connections ==="
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT state, count(*) FROM pg_stat_activity GROUP BY state;
"

echo -e "\n=== Recent Errors ==="
docker logs --since 10m writing-agent-backend 2>&1 | grep '"level":"ERROR"' | tail -10

echo -e "\n=== Stuck Agents ==="
docker exec writing-agent-pg psql -U postgres -d writing_agent_v2 -c "
  SELECT trace_id, status, current_step, started_at,
         EXTRACT(EPOCH FROM (NOW() - started_at))::int AS age_sec
  FROM agent_traces
  WHERE status = 'running' AND started_at < NOW() - INTERVAL '10 minutes';
"
```

---

*最后更新：2026-08-03*
*维护者：Writing Agent V2 Team*
