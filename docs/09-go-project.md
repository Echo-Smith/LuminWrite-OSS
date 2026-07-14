# Go 后端项目结构

## 目录结构

```
writing-agent-v2/backend/
├── go.mod
├── go.sum
├── cmd/
│   └── server/
│       └── main.go                    # 程序入口
├── internal/
│   ├── config/
│   │   └── config.go                  # 配置加载 (env + DB)
│   │
│   ├── database/
│   │   ├── postgres.go                # PostgreSQL 连接池
│   │   ├── migrations/                # SQL 迁移文件
│   │   │   ├── 001_extensions.up.sql
│   │   │   ├── 001_extensions.down.sql
│   │   │   ├── 002_users.up.sql
│   │   │   ├── ...
│   │   │   └── 016_sensitive_words.up.sql
│   │   └── seed.go                    # 初始数据
│   │
│   ├── engine/
│   │   ├── engine.go                  # AgentEngine 核心编排
│   │   ├── context.go                 # ExecutionContext
│   │   ├── step.go                    # Step 接口定义
│   │   ├── steps/
│   │   │   ├── intent.go              # IntentStep
│   │   │   ├── query_plan.go          # QueryPlanStep
│   │   │   ├── search.go              # SearchStep
│   │   │   ├── relevance.go           # RelevanceStep
│   │   │   ├── outline.go             # OutlineStep (引导模式)
│   │   │   ├── write.go               # WriteStep
│   │   │   ├── post_review.go         # PostReviewStep
│   │   │   └── auto_fix.go            # AutoFixStep
│   │   ├── state.go                   # 状态管理 (暂停/恢复)
│   │   └── trace.go                   # Trace 记录
│   │
│   ├── tools/
│   │   ├── tool.go                    # Tool 接口
│   │   ├── deepseek.go                # DeepSeek LLM Client
│   │   ├── zhihu.go                   # 知乎 Client
│   │   ├── ima.go                     # IMA 知识库 Client
│   │   ├── tavily.go                  # Tavily 搜索 Client
│   │   ├── tencent_news.go            # 腾讯新闻 Client
│   │   ├── weibo.go                   # 微博 Client
│   │   ├── dashscope_embedding.go     # 通义 text-embedding-v3
│   │   ├── jiaozhen.go                # 较真核查 Client
│   │   └── sensitive.go               # 敏感词检测
│   │
│   ├── profile/
│   │   ├── profile.go                 # StyleProfile 结构定义
│   │   ├── loader.go                  # Profile 加载器 (DB + 缓存)
│   │   └── publisher.go               # Profile 发布流程 (二次确认)
│   │
│   ├── routing/
│   │   ├── grayscale.go               # 灰度路由 (Profile标记 + UID Hash)
│   │   └── hash.go                    # FNV-1a UID Hash
│   │
│   ├── evaluation/
│   │   ├── runner.go                  # 评测执行器
│   │   ├── scorer.go                  # 评分模块 (规则 + LLM-as-Judge)
│   │   ├── dataset.go                 # 评测集管理
│   │   └── exporter.go                # 第三方平台导出
│   │
│   ├── feedback/
│   │   ├── service.go                 # 反馈服务
│   │   ├── reputation.go              # 信誉计算
│   │   └── aggregation.go             # 反馈聚合 + 迭代阈值
│   │
│   ├── topics/
│   │   ├── service.go                 # 选题服务
│   │   ├── hotlist.go                 # 热搜抓取
│   │   └── sse.go                     # SSE 选题推送
│   │
│   ├── knowledge/
│   │   ├── store.go                   # pgvector 知识库存储
│   │   ├── search.go                  # 语义检索
│   │   ├── dedup.go                   # 语义去重
│   │   └── ima_sync.go                # IMA 同步 (定时任务)
│   │
│   ├── admin/
│   │   ├── styles.go                  # 风格管理
│   │   ├── models.go                  # 模型配置
│   │   ├── keys.go                    # API 密钥管理
│   │   ├── cron.go                    # 定时任务管理
│   │   ├── evaluation.go              # 评测管理
│   │   ├── usage.go                   # 用量统计
│   │   └── sensitive.go               # 敏感词管理
│   │
│   ├── websocket/
│   │   ├── handler.go                 # WebSocket 连接处理
│   │   ├── hub.go                     # 连接管理 Hub
│   │   └── protocol.go                # 消息协议定义
│   │
│   ├── middleware/
│   │   ├── auth.go                    # JWT 认证
│   │   ├── ratelimit.go               # 速率限制
│   │   ├── cors.go                    # CORS
│   │   └── logging.go                 # 请求日志
│   │
│   ├── models/
│   │   ├── user.go                    # User 模型
│   │   ├── trace.go                   # Trace 模型
│   │   ├── topic.go                   # Topic 模型
│   │   ├── feedback.go                # Feedback 模型
│   │   ├── evaluation.go              # Evaluation 模型
│   │   └── ...                        # 其他模型
│   │
│   └── server/
│       ├── router.go                  # chi 路由注册
│       ├── handler_agent.go           # 写作 Agent handler
│       ├── handler_styles.go          # 风格 handler
│       ├── handler_topics.go          # 选题 handler
│       ├── handler_feedback.go        # 反馈 handler
│       ├── handler_knowledge.go       # 知识库 handler
│       ├── handler_admin.go           # Admin handler
│       └── handler_ws.go              # WebSocket handler
│
├── pkg/                               # 可复用的公共包
│   ├── logger/
│   │   └── logger.go                  # 结构化日志 (slog)
│   ├── crypto/
│   │   └── aes.go                     # AES-256 加密 (API Key 存储)
│   └── response/
│       └── response.go                # 统一 API 响应格式
│
├── migrations/                        # golang-migrate 迁移文件
│
├── config.yaml                        # 配置文件
├── Dockerfile
└── Makefile
```

## 核心依赖

```go
// go.mod 核心依赖
module github.com/luminbuddy/luminbuddy-writing-agent-v2

go 1.24

require (
    github.com/go-chi/chi/v5 v5.x.x          // 路由
    github.com/coder/websocket v1.x.x        // WebSocket
    github.com/jackc/pgx/v5 v5.x.x           // PostgreSQL 驱动
    github.com/pgvector/pgvector-go v0.x.x   // pgvector Go 绑定
    github.com/golang-jwt/jwt/v5 v5.x.x      // JWT 认证
    github.com/robfig/cron/v3 v3.x.x         // 定时任务
    github.com/google/uuid v1.x.x            // UUID 生成
    github.com/joho/godotenv v1.x.x          // .env 加载
    github.com/redis/go-redis/v9 v9.x.x      // Redis (可选缓存)
)
```

## main.go 骨架

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
    "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
    "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/server"
)

func main() {
    // 加载配置
    cfg := config.Load()

    // 初始化日志
    logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
    }))
    slog.SetDefault(logger)

    // 连接数据库
    db, err := database.NewPostgres(cfg.DatabaseURL)
    if err != nil {
        slog.Error("数据库连接失败", "error", err)
        os.Exit(1)
    }
    defer db.Close()

    // 运行迁移
    if err := database.Migrate(db); err != nil {
        slog.Error("数据库迁移失败", "error", err)
        os.Exit(1)
    }

    // 启动服务器
    srv := server.New(cfg, db)

    ctx, cancel := signal.NotifyContext(context.Background(),
        syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    slog.Info("Writing Agent V2 启动中", "port", cfg.Port)
    if err := srv.Start(ctx); err != nil {
        slog.Error("服务器启动失败", "error", err)
        os.Exit(1)
    }
}
```

## router.go 骨架

```go
package server

import (
    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/handlers"
    "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/middleware"
)

func NewRouter(h *handlers.Handlers, mw *middleware.Middlewares) *chi.Mux {
    r := chi.NewRouter()

    // 全局中间件
    r.Use(middleware.RequestID)
    r.Use(middleware.RealIP)
    r.Use(mw.Logging)
    r.Use(mw.Recoverer)
    r.Use(mw.CORS)

    // 健康检查
    r.Get("/health", h.Health)

    // API v2
    r.Route("/api/v2", func(r chi.Router) {
        // 认证
        r.Use(mw.Auth)

        // 写作 Agent
        r.Post("/agent/start", h.Agent.Start)
        r.Get("/agent/trace/{traceID}", h.Agent.GetTrace)
        r.Get("/agent/traces", h.Agent.ListTraces)

        // 风格
        r.Get("/styles", h.Styles.List)
        r.Get("/styles/{slug}", h.Styles.Get)

        // 选题
        r.Get("/topics", h.Topics.List)
        r.Post("/topics", h.Topics.Create)
        r.Get("/topics/{id}/detail", h.Topics.Detail)
        r.Get("/topics/stream", h.Topics.Stream) // SSE

        // 反馈
        r.Post("/feedback", h.Feedback.Submit)
        r.Post("/feedback/adopt", h.Feedback.Adopt)
        r.Get("/feedback/aggregate/{styleSlug}", h.Feedback.Aggregate)

        // 知识库
        r.Post("/knowledge/search", h.Knowledge.Search)

        // WebSocket
        r.Get("/ws/agent", h.WS.HandleAgent)

        // Admin (需要 Admin Token)
        r.Group(func(r chi.Router) {
            r.Use(mw.AdminAuth)
            r.Route("/admin", func(r chi.Router) {
                // 风格管理
                r.Get("/styles", h.Admin.Styles.List)
                r.Post("/styles", h.Admin.Styles.Create)
                r.Put("/styles/{slug}", h.Admin.Styles.Update)
                r.Post("/styles/{slug}/publish", h.Admin.Styles.Publish)
                r.Post("/styles/{slug}/archive", h.Admin.Styles.Archive)
                r.Get("/styles/{slug}/versions", h.Admin.Styles.Versions)
                r.Get("/styles/{slug}/rollout", h.Admin.Styles.GetRollout)
                r.Put("/styles/{slug}/rollout", h.Admin.Styles.UpdateRollout)
                r.Post("/styles/{slug}/rollout/preview", h.Admin.Styles.PreviewRollout)

                // 模型配置
                r.Get("/models", h.Admin.Models.List)
                r.Post("/models", h.Admin.Models.Create)
                r.Put("/models/{id}", h.Admin.Models.Update)
                r.Delete("/models/{id}", h.Admin.Models.Delete)
                r.Post("/models/{id}/default", h.Admin.Models.SetDefault)

                // API 密钥
                r.Get("/keys", h.Admin.Keys.List)
                r.Post("/keys", h.Admin.Keys.Create)
                r.Put("/keys/{id}", h.Admin.Keys.Update)
                r.Delete("/keys/{id}", h.Admin.Keys.Delete)
                r.Post("/keys/{id}/test", h.Admin.Keys.Test)

                // 定时任务
                r.Get("/cron-jobs", h.Admin.Cron.List)
                r.Post("/cron-jobs", h.Admin.Cron.Create)
                r.Put("/cron-jobs/{id}", h.Admin.Cron.Update)
                r.Delete("/cron-jobs/{id}", h.Admin.Cron.Delete)
                r.Post("/cron-jobs/{id}/run", h.Admin.Cron.Run)

                // 评测
                r.Get("/eval-sets", h.Admin.Eval.ListSets)
                r.Post("/eval-sets", h.Admin.Eval.CreateSet)
                r.Get("/eval-sets/{id}/samples", h.Admin.Eval.ListSamples)
                r.Post("/eval-sets/{id}/samples", h.Admin.Eval.AddSample)
                r.Post("/eval-sets/{id}/run", h.Admin.Eval.Run)
                r.Get("/eval-runs", h.Admin.Eval.ListRuns)
                r.Get("/eval-runs/{id}", h.Admin.Eval.GetRun)
                r.Post("/eval-runs/{id}/export", h.Admin.Eval.Export)

                // 用量统计
                r.Get("/usage", h.Admin.Usage.Summary)
                r.Get("/usage/models", h.Admin.Usage.ByModel)

                // 敏感词
                r.Get("/sensitive-words", h.Admin.Sensitive.List)
                r.Post("/sensitive-words", h.Admin.Sensitive.Create)
                r.Put("/sensitive-words/{id}", h.Admin.Sensitive.Update)
                r.Delete("/sensitive-words/{id}", h.Admin.Sensitive.Delete)
                r.Put("/sensitive-words/config", h.Admin.Sensitive.UpdateConfig)
            })
        })
    })

    return r
}
```

## 配置文件 (config.yaml)

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  read_timeout: 30s
  write_timeout: 120s

database:
  url: "postgres://user:pass@localhost:5432/writing_agent_v2?sslmode=disable"
  max_open_conns: 25
  max_idle_conns: 5

redis:
  url: "redis://localhost:6379/0"
  enabled: false

jwt:
  secret: "${JWT_SECRET}"
  expiry: 24h

admin:
  token: "${ADMIN_TOKEN}"

deepseek:
  base_url: "https://api.deepseek.com"
  api_key: "${AI_API_KEY}"
  default_model: "deepseek-v4-flash"
  timeout: 120s

dashscope:
  api_key: "${DASHSCOPE_API_KEY}"
  model: "text-embedding-v3"
  dimension: 1024

ima:
  base_url: "https://ima.qq.com"
  client_id: "${IMA_CLIENT_ID}"
  api_key: "${IMA_API_KEY}"
  kb_id: "${IMA_KB_ID}"
  timeout: 15s

zhihu:
  enabled: true
  base_url: "https://developer.zhihu.com"
  access_secret: "${ZHIHU_ACCESS_SECRET}"
  timeout: 15s

tavily:
  api_key: "${TAVILY_API_KEY}"
  endpoint: "https://api.tavily.com/search"
  timeout: 20s

cron:
  ima_sync: "0 17 * * *"
  timezone: "Asia/Shanghai"

log:
  level: "info"
  format: "json"
```
