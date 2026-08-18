# 笔润智谈 LuminBuddy V2

[English](README.en.md) · [在线体验](https://luminbuddy2.ericdocmic.top/v2/) · [更新日志](#更新日志)

> **面向中文内容创作者的 AI 写作工作台**：从需求理解、素材检索、提纲确认，到按风格成稿、写后自检、反馈与记忆沉淀，把一次性生成变成可观察、可干预、可迭代的写作流程。

![笔润智谈写作工作台](docs/assets/luminbuddy-workspace.png)

---

## 关于笔润智谈

**笔润智谈**（LuminBuddy）是一款面向中文内容生产场景的 AI 写作助手。它不追求"一键生成"的魔法，而是将写作过程拆解为可观察、可干预、可迭代的工程流程——让创作者在关键决策点保持控制权，同时让 AI 在素材搜集、结构规划、风格适配和质量检查等环节提供有效辅助。

**当前成熟度：工程 Beta**。已实现完整前后端、Harness 单层 Agent 编排、写作 Pipeline、引导式提纲、风格配置、A/B 评测、反馈系统、分层记忆与监控指标；持续迭代中。

---

## 解决了什么问题

通用对话模型可以快速给出一篇稿件，但真实写作任务通常卡在四个环节：

| 痛点 | 具体表现 | 笔润智谈的解决方案 |
|------|---------|------------------|
| **意图理解不稳定** | 用户的真实需求、篇幅和风格约束没有被准确捕捉 | 规则优先的意图路由 + 低置信度 LLM fallback |
| **生成过程黑盒** | 素材检索、观点组织和成稿被压进一次不可观察的生成 | Harness 单层编排，每一步可见、可暂停、可恢复 |
| **关键节点失控** | 用户无法在提纲确认、风格调整等高代价决策点干预 | Guided Mode 引导模式，提纲确认后再成稿 |
| **反馈无法沉淀** | 好坏反馈没有进入下一次生成，团队难以定位失败环节 | 分段反馈 + A/B 评测 + 分层记忆系统 |

---

## 如何解决

### 核心架构：Harness-LLM 单层持续会话

借鉴 DeepSeek Harness (dsh) 的编排理念，采用**单层架构**：

```
用户请求
  → Harness（意图路由、工具注册、状态管理、断路保护）
    → LLM 持续会话（自主决定调用什么工具、何时写作、何时修正）
      ←→ 工具执行（搜索/知识库/写作/评审/修正）
  → 流式输出到前端
```

**关键设计**：
- **不存在外层 ReAct + 内层 agent loop 的嵌套**，降低延迟与输出漂移
- **1 次 LLM 持续会话**替代传统 Pipeline 的 10+ 次独立调用，首字延迟从 30-60s 降至 3-5s
- **会话状态跨轮持久化**：文章、素材、搜索记录在同一对话框内累积复用

### 智能上下文管理

**Compaction（对话压缩）**：当对话历史超过阈值（10 条消息 / 6000 tokens），自动将旧消息压缩为摘要，前端显示节省的 Token 数。

**按需获取（retrieve_context）**：LLM 可以根据当前任务需要，主动查询：
- `article` — 当前文章的特定段落
- `memory` — 用户的写作偏好和历史记忆
- `history` — 本轮对话历史
- `search` — 已收集的搜索素材
- `profile` — 当前风格配置详情

System Prompt 从全量注入（3000+ tokens）精简为常驻层（500-800 tokens），信息更精准，上下文窗口更充裕。

### 写作流程

```text
写作需求
  → 意图识别（规则优先，毫秒级路由）
  → 记忆检索（用户偏好注入）
  → 素材检索（多源搜索 + 知识库 + 相关性过滤）
  → 提纲确认（引导模式：确认/编辑/重做）
  → 风格化流式写作（按 Profile 规则生成）
  → 写后审校（质量评分 + 敏感检查）
  → 自动修正（低严重度问题自动修复）
  → 分段反馈 + A/B 评测 + 记忆沉淀
```

---

## 功能特性

### 写作核心

| 功能 | 说明 |
|------|------|
| **意图识别** | 规则优先路由，支持写作/润色/压缩/扩写/自由对话 |
| **多源检索** | 网络搜索（Tavily/Bing/腾讯新闻）+ 内部知识库（BM25 + Dense + GraphRAG） |
| **引导模式** | 提纲确认、编辑、最多五次重做 |
| **风格配置** | Style Profile 独立管理，支持版本、灰度发布和回滚 |
| **流式输出** | WebSocket 实时推送，支持暂停/恢复/取消 |
| **质量评审** | 6 维度评分（事实/结构/风格/修辞/篇幅/安全） |
| **自动修正** | 评审未通过时自动修正，最多 3 次尝试 |

### 写作工具集

LLM 在持续会话中自主调用的工具：

| 工具 | 用途 |
|------|------|
| `search_web` | 搜索互联网获取最新信息 |
| `search_knowledge` | 检索内部知识库范文和风格规范 |
| `read_source` | 读取搜索结果的详细内容 |
| `generate_outline` | 生成文章提纲供用户确认 |
| `write_article` | 开始流式输出完整文章 |
| `review_article` | 对文章进行质量评审 |
| `revise_section` | 定向修改文章的某一部分 |
| `word_count_check` | 检查字数是否符合风格要求 |
| `rewrite_title` | 生成 3 个备选标题及推荐理由 |
| `fact_check` | 提取事实声明并通过搜索验证 |
| `retrieve_context` | 按需获取会话上下文 |

### 在线编辑与导出

- **实时编辑**：在聊天框内直接编辑文章（Markdown 格式）
- **多格式导出**：Markdown (.md) / Word (.doc) / PDF（打印模式）
- **纯前端实现**：无需后端 API，浏览器直接生成文件

### 记忆系统

四层记忆架构：

| 层级 | 类型 | 用途 |
|------|------|------|
| Tier 1 | 硬偏好 | 用户明确设置的写作偏好 |
| Tier 2 | 行为模式 | 自动提取的写作习惯 |
| Tier 3 | 反馈信号 | 用户反馈驱动的改进信号 |
| Tier 4 | 实体网络 | 话题、人物、概念的关系图谱 |

支持文件层双向同步（Markdown 文件 ↔ 数据库），人类可读、可编辑。

### 管理后台

- **风格管理**：Profile 创建、编辑、版本控制、灰度发布
- **模型配置**：多模型接入（DeepSeek/OpenAI 兼容接口）、密钥管理
- **A/B 评测**：对照组/实验组自动化评测与指标对比
- **反馈分析**：分段反馈统计、质量趋势
- **审计日志**：操作追踪、安全审计
- **Token 监控**：用量统计、成本分析

### 认证与安全

- **Passkey/WebAuthn**：无密码登录，设备级安全
- **游客模式**：无需注册即可体验，支持后续升级
- **Prompt Injection 防御**：输入清洗 + System Prompt 防御指令
- **敏感词检查**：内置敏感内容过滤

---

## 技术架构

```text
┌─────────────────────────────────────────────────────────────┐
│                    Frontend (React 19 + Vite)                 │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌────────────────┐  │
│  │ 写作工作台 │ │ 选题中心  │ │ 个人中心  │ │ Admin Dashboard │  │
│  └─────┬────┘ └─────┬────┘ └─────┬────┘ └───────┬────────┘  │
│        └────────────┴────────────┘              │           │
│                     │                           │           │
│              WebSocket + REST                   │           │
└─────────────────────┼───────────────────────────┼───────────┘
                      │                           │
┌─────────────────────┼───────────────────────────┼───────────┐
│              Go Backend (chi router)             │           │
│                     │                           │           │
│  ┌──────────────────┴───────────────────────────┴────────┐  │
│  │                   Harness Agent Engine                 │  │
│  │  ┌────────┐  ┌─────────┐  ┌────────┐  ┌────────────┐  │  │
│  │  │ Intent │→ │ Search  │→ │ Write  │→ │PostReview  │  │  │
│  │  │Routing │  │  Plan   │  │ Step   │  │   Step     │  │  │
│  │  └────────┘  └─────────┘  └────────┘  └────────────┘  │  │
│  │                                                        │  │
│  │  ┌────────┐  ┌─────────┐  ┌────────┐  ┌────────────┐  │  │
│  │  │ Memory │  │  Style  │  │  A/B   │  │  Feedback  │  │  │
│  │  │  Gate  │  │ Profile │  │  Eval  │  │  System    │  │  │
│  │  └────────┘  └─────────┘  └────────┘  └────────────┘  │  │
│  └────────────────────────────────────────────────────────┘  │
│                     │                                       │
│  ┌──────────┬──────────┬──────────┬──────────┬──────────┐  │
│  │ DeepSeek │  Tavily  │ 知乎搜索 │ IMA KB   │ DashScope│  │
│  │  Client  │  Client  │  Client  │  Client  │ Embedding│  │
│  └──────────┴──────────┴──────────┴──────────┴──────────┘  │
└─────────────────────┬───────────────────────────────────────┘
                      │
┌─────────────────────┼───────────────────────────────────────┐
│              PostgreSQL 17 + pgvector + paradedb             │
│  ┌─────────┬──────────┬──────────┬──────────┬────────────┐  │
│  │ 用户数据 │ 风格配置  │ 知识库   │ 记忆系统  │ 会话日志   │  │
│  │会话状态  │ A/B评测  │ GraphRAG │ 实体网络  │ 审计日志   │  │
│  └─────────┴──────────┴──────────┴──────────┴────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### 技术栈

| 层级 | 技术 |
|------|------|
| 前端 | React 19, Vite, TypeScript, Tailwind CSS, shadcn/ui |
| 后端 | Go 1.22+, chi router, coder/websocket |
| 数据库 | PostgreSQL 17 + pgvector + paradedb (BM25) |
| LLM | DeepSeek API (默认), 支持 OpenAI 兼容接口 |
| Embedding | DashScope text-embedding-v3 (1024维) |
| 部署 | Docker Compose |
| 监控 | Prometheus 指标 + slog 结构化日志 + Trace 链路追踪 |

---

## 快速开始

### Docker Compose（推荐）

```bash
cp .env.docker.example .env.docker
# 编辑 .env.docker，填入模型 API Key 和数据库密码
docker compose up -d
```

默认入口：前端 `http://localhost:3000`，后端健康检查 `http://localhost:8080/api/v2/health`。

### 本地开发

```bash
# 后端
cd backend
cp .env.example .env
go run ./cmd/server/

# 前端
cd frontend
npm ci
npm run dev
```

### 验证

```bash
cd backend && go test ./...
cd frontend && npm ci && npm run build
```

### 部署打包

```bash
# 仅源码包
./scripts/pack-for-1panel.sh

# 源码 + Docker 镜像（国内服务器推荐）
./scripts/pack-for-1panel.sh --images
```

---

## 设计文档

| 文档 | 内容 |
|------|------|
| [架构蓝图](docs/01-architecture.md) | Agent Engine、数据流与系统边界 |
| [Harness 架构方案](docs/architecture-c-design.md) | 单层 LLM 编排器设计与工具粒度 |
| [数据库 Schema](docs/02-database-schema.md) | PostgreSQL、pgvector 与迁移 |
| [API 规范](docs/03-api-specification.md) | REST 与 WebSocket 协议 |
| [Style Profile](docs/04-style-profile.md) | 风格版本、发布与回滚 |
| [灰度路由](docs/05-grayscale-routing.md) | Profile 标记和 UID Hash 分流 |
| [评测系统](docs/06-evaluation.md) | 评测集与触发机制 |
| [反馈系统](docs/07-feedback.md) | 分段反馈与信誉权重 |
| [管理后台](docs/08-admin-dashboard.md) | 配置、评测与可观测入口 |
| [记忆系统](docs/11-memory-system.md) | 硬偏好、行为模式与反馈信号 |
| [编辑部系统](docs/12-editorial-system.md) | 编辑任务管理与工作流 |
| [运维手册](docs/runbook.md) | 部署、监控与故障排查 |

---

## 更新日志

### v0.4.0 (2026-08-18)

- **智能上下文管理**：`retrieve_context` 工具让 LLM 按需获取信息，System Prompt Token 减少 60%+
- **对话历史 Compaction**：借鉴 dsh 模式自动压缩历史，前端显示节省 Token 数
- **写作工具集扩展**：新增 `word_count_check`、`rewrite_title`、`fact_check`
- **在线编辑与导出**：支持 Markdown/Word/PDF 格式导出
- **管理后台重构**：统一权限/轮询/资源管理 hooks，新增审计日志
- **精简 docreader 镜像**：~150MB 替代旧 5.53GB

### v0.3.0 (2026-08-16)

- **Harness 架构**：单层 LLM 持续会话编排，工具自主调用
- **A/B 测试框架**：对照组/实验组自动化评测
- **Passkey 认证**：WebAuthn 无密码登录
- **Session Event Log**：追加式事件日志，支持断线重连
- **Prompt 注入防御**：chat 意图精简注入，Token 降 10.5%

### v0.2.0

- 引导式提纲（Guided Mode）
- Style Profile 灰度路由
- Post Review + Auto Fix
- 分层记忆系统
- Prometheus 指标与 Trace 链路追踪

### v0.1.0

- React 19 写作工作台 + Go Agent Pipeline
- WebSocket 流式事件
- 多源检索与相关性过滤
- Docker Compose 一键部署

---

## 项目声明

- 这是可运行的个人产品与工程项目，不代表已完成规模化市场验证。
- 外部模型、检索和数据源的可用性取决于各服务配置与条款。
- 仓库不包含生产环境密钥；请从示例环境文件创建本地配置。

## License

[MIT](LICENSE)
