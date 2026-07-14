# Writing Agent V2

> 笔润智谈 V2 — 模块化、高性能的 AI 写作平台

## 项目定位

V1（`writing-assistant`）是一个基于 Node.js 的单体写作管道，V2 将重构为 Go + PostgreSQL + React 的模块化写作平台，核心升级包括：

- **Agent Engine**：状态机式写作引擎，Steps 可插拔、可编排
- **Style Profiles**：用户端可选的写作风格热插拔系统（预设评论风格、申论、小红书等）
- **Topic Center**：选题中心，解耦热点抓取与写作流程
- **Guided Mode**：引导模式，用户可选择/定制论点提纲后再生成
- **Feedback & Evaluation**：分段反馈 + 信誉加权 + 评测集基准
- **Admin Dashboard**：热插拔管理、模型配置、自动化、评测面板

## 技术栈

| 层 | 选型 | 版本 |
|---|---|---|
| 后端 | Go | 1.24+ |
| 路由 | chi | v5 |
| WebSocket | coder/websocket | latest |
| 数据库 | PostgreSQL | 17 |
| 向量扩展 | pgvector | latest |
| Embedding | 通义 text-embedding-v3 | — |
| LLM | DeepSeek V4 (flash/pro + thinking) | — |
| 前端 | React + Vite | 19 |
| UI 组件 | shadcnUI + assistantUI | latest |
| 定时任务 | robfig/cron | v3 |

## 项目结构

```
writing-agent-v2/
├── README.md                     # 本文件
├── docker-compose.yml            # 一键启动 PG + Backend + Frontend
├── .env.docker                   # Docker 环境变量配置
├── docs/                         # 设计文档
│   ├── 01-architecture.md        # 架构蓝图
│   ├── 02-database-schema.md     # 数据库 Schema
│   ├── 03-api-specification.md   # API 规范
│   ├── 04-style-profile.md       # Style Profile 热插拔系统
│   ├── 05-grayscale-routing.md   # 灰度路由
│   ├── 06-evaluation.md          # 评测系统
│   ├── 07-feedback.md            # 反馈与信誉系统
│   ├── 08-admin-dashboard.md     # Admin Dashboard
│   ├── 09-go-project.md          # Go 后端项目结构
│   └── 10-execution-plan.md      # 分阶段执行计划
├── frontend/                     # React 前端
│   ├── Dockerfile                # 多阶段构建 (Vite + Nginx)
│   ├── nginx.conf                # Nginx 配置 + API 代理
│   └── src/
│       ├── pages/                # 页面组件
│       ├── components/           # 通用组件
│       ├── layouts/              # 布局组件
│       ├── hooks/                # 自定义 Hooks
│       └── lib/                  # 工具函数
└── backend/                      # Go 后端
    ├── Dockerfile                # 多阶段构建 (Go static binary)
    └── internal/
        ├── database/             # PostgreSQL 连接 + 迁移
        ├── profile/              # Style Profile 加载器 (DB + fallback)
        ├── engine/               # Agent 执行引擎
        └── server/               # HTTP 路由 + 处理函数
```

## 快速启动

### Docker Compose 一键启动（推荐）

```bash
# 1. 复制环境变量配置，填入 API Key
cp .env.docker .env.docker.local
# 编辑 .env.docker.local，至少配置 AI_API_KEY

# 2. 一键启动
docker compose up -d

# 3. 查看日志
docker compose logs -f backend

# 4. 访问
#   前端: http://localhost:3000
#   后端 API: http://localhost:8080/api/v2/health
#   PostgreSQL: localhost:5432
```

### 本地开发

```bash
# 启动 PostgreSQL (Docker)
docker run -d --name writing-agent-pg \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=writing_agent_v2 \
  -p 5432:5432 \
  pgvector/pgvector:pg16

# 后端
cd backend
cp .env.example .env  # 编辑配置
make run              # 或: go run ./cmd/server/

# 前端
cd frontend
npm install
npm run dev           # http://localhost:5173
```

### 数据库迁移

迁移文件位于 `backend/internal/database/migrations/`，后端启动时自动执行。

启动顺序：
1. PostgreSQL + pgvector 启动
2. 后端连接数据库 → 自动执行迁移 → 种子内置 Style Profiles
3. 前端启动，通过 Nginx 代理 API 请求到后端

## V1 与 V2 的关系

- **V1**：先进行完善优化，打包为 LTS 版本，通过 1Panel 继续部署运行
- **V2**：独立开发，稳定后决定是否将 V1 下线
- V1 和 V2 可并行运行，V2 上线后逐步迁移流量

## 文档索引

| 文档 | 内容 |
|---|---|
| [架构蓝图](docs/01-architecture.md) | 整体架构、Agent Engine、数据流、技术选型 |
| [数据库 Schema](docs/02-database-schema.md) | PostgreSQL 表设计、pgvector、迁移 |
| [API 规范](docs/03-api-specification.md) | REST API + WebSocket 协议 |
| [Style Profile](docs/04-style-profile.md) | 风格热插拔、用户端选择、Admin 发布流程 |
| [灰度路由](docs/05-grayscale-routing.md) | Profile 标记 + UID Hash 两级路由 |
| [评测系统](docs/06-evaluation.md) | 评测集框架、人工标注、变更触发 |
| [反馈系统](docs/07-feedback.md) | 分段反馈、信誉加权、workbuddy 录用标记 |
| [Admin Dashboard](docs/08-admin-dashboard.md) | 热插拔管理、模型配置、自动化面板 |
| [Go 项目结构](docs/09-go-project.md) | 后端目录结构、包划分 |
| [执行计划](docs/10-execution-plan.md) | Phase 0-4 分阶段路线图 |
