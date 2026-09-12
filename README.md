# 笔润智谈丨LuminWrite OSS

![License](https://img.shields.io/badge/License-MIT-green.svg)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)
![React](https://img.shields.io/badge/React-19-61DAFB?logo=react)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-17%20%2B%20pgvector-4169E1?logo=postgresql)

**笔润智谈丨LuminWrite OSS** 是一款面向中文内容创作场景的**自托管 AI 写作工作台**。
它不追求「一键生成」的魔法，而是把写作拆成**可观察、可干预、可迭代**的工程流程：
素材检索 → 提纲确认 → 风格化成稿 → 写后自检 → 记忆沉淀，关键决策始终由创作者掌控。

![写作工作台](docs/assets/luminbuddy-workspace.png)

[English](README.en.md)

---

## 解决了什么问题

| 痛点 | 具体表现 | LuminWrite 的方案 |
|---|---|---|
| **意图理解不稳定** | 用户的真实需求、篇幅和风格约束没有被准确捕捉 | 规则优先的意图路由 + 低置信度 LLM fallback |
| **生成过程黑盒** | 素材检索、观点组织和成稿被压进一次不可观察的生成 | Harness 单层编排，每一步可见、可暂停、可恢复 |
| **关键节点失控** | 用户无法在提纲确认、风格调整等高代价决策点干预 | 引导模式：提纲确认后再成稿 |
| **反馈无法沉淀** | 好坏反馈没有进入下一次生成，难以定位失败环节 | 分段反馈 + A/B 评测 + 分层记忆系统 |

## 功能特性

- **多模式写作执行**：单层 LLM 持续会话（Harness，默认）、经典步骤流水线（Pipeline）、
  编辑部多 Agent DAG（研究/写作/审校三角色）三套执行体系并存；
- **治理型写作运行时**（实验性）：文档为主对象、聊天只修改合约——
  WritingContract → ExecutablePlan → Artifact → 质量门（Candidate/Accepted/Verified）
  的版本化交付协议，支持 off/shadow/allowlist 灰度放量与断点恢复
  （见 [docs/19-governed-writing-runtime.md](docs/19-governed-writing-runtime.md)）；
- **研究综述路径**（实验性）：学术检索（OpenAlex/CrossRef/Semantic Scholar）→
  证据门（人工审核证据包）→ 提纲门（人工确认提纲）→ 引用可校验成稿，
  全链路 fail-closed（`RESEARCH_REVIEW_ENABLED`，默认关闭）；
- **AR-012 候选评估**（实验性）：对完成的综述运行调用外部综述生成 sidecar，
  产出隔离的对比候选稿与机械对比指标，用于内部评估（`AR012_CANDIDATE_ENABLED`，默认关闭；
  sidecar 为私有组件，不随本仓库分发）；
- **分层记忆系统**：会话记忆 → 工作记忆 → 长期记忆 → 项目记忆，写后自动沉淀行为模式；
- **知识库（本地 RAG）**：PostgreSQL 上的 BM25（ParadeDB）+ 向量（pgvector）+ RRF 融合
  + GraphRAG，无需外部向量库服务；
- **素材体系**：用户素材最高优先级（P0），网络检索结果不得覆盖用户原始表达；
- **风格系统**：风格 Profile 热插拔 + 风格构建器（上传范文自动提炼）+ 灰度发布。
  出于内容资产考虑，本仓库**不内置任何风格内容**，风格目录由部署方自建
  （见 [docs/04-style-profile.md](docs/04-style-profile.md)）；
- **评测中心**：WABench 契约驱动的写作质量评测（数据集/候选/盲评/发布）+ 红队评估集；
- **Admin 后台**：模型配置热更新（Key 加密入库）、MCP 管理、审计中心、RBAC 角色权限。

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.25 · chi · coder/websocket · pgx/v5 · go-redis（依赖面刻意克制） |
| 数据库 | PostgreSQL 17（ParadeDB 镜像：pgvector + pg_bm25）· Redis 7 |
| 文档解析 | docreader sidecar（markitdown，TCP 协议，~150MB） |
| 模型 | OpenAI 兼容 `/chat/completions` 协议（DeepSeek / SenseNova 已验证；切换其他提供方见 [docs/provider-configuration.md](docs/provider-configuration.md)） |
| Embedding | 阿里 DashScope text-embedding-v3（可选，未配置时自动降级） |
| 前端 | React 19 · Vite 7 · TypeScript · Tailwind CSS · Tiptap/ProseMirror · zustand |
| 研究侧车 | scholar-worker（Python 3.12，仅依赖 httpx，research profile 时启用） |

## 快速开始

### 方式一：Quickstart Compose（推荐，3 分钟）

预构建镜像 + 内置 SearXNG 搜索（**无需任何搜索源 API key**）：

```bash
git clone https://github.com/Echo-Smith/luminbuddy-writing-agent-v2.git
cd luminbuddy-writing-agent-v2
cp .env.docker.example .env.docker
vi .env.docker        # 至少填写 DEEPSEEK_API_KEY
docker compose -f docker-compose.quickstart.yml up -d
```

打开 `http://localhost:3002`，完成注册即可使用。
镜像来自 GitHub Packages（`ghcr.io/echo-smith/luminbuddy-v2-*`，发布流水线见
`.github/workflows/docker-publish.yml`）；也可以用 `docker compose build` 本地构建。

### 方式二：主 Compose（自构建）

```bash
cp .env.docker.example .env.docker && vi .env.docker
docker compose up -d
```

### 方式三：本地开发

```bash
cd backend && cp .env.example .env && go run ./cmd/server/
cd frontend && npm ci && npm run dev
```

### 验证

```bash
cd backend && go test ./...
cd frontend && npm ci && npm test && npm run build
```

### 生产部署（1Panel）

见 [DEPLOY.md](DEPLOY.md)：镜像包/命令行/源码三种部署方式、域名 + HTTPS 反向代理、
`docker-compose.scale.yml` 多实例扩展。数据备份与恢复见
[docs/ops-backup-restore.md](docs/ops-backup-restore.md)。

## 搜索源

写作流的素材检索由多源并发框架驱动（[适配器开发指南](docs/search-provider-adapter.md)）：

| 源 | OSS 版 | 说明 |
|---|---|---|
| **SearXNG** | ✅ 完整实现 | 自托管元搜索，零 API key，quickstart 栈内置 |
| Tavily / 知乎 / 腾讯新闻 / 微博 / Bing / AnySearch | stub | 完整实现在商业版；接口公开，可按[指南](docs/search-provider-adapter.md)自行接入 |

## 关键配置

完整清单见 [.env.docker.example](.env.docker.example)（含注释）。

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `DEEPSEEK_BASE_URL` / `DEEPSEEK_API_KEY` / `DEEPSEEK_DEFAULT_MODEL` | — | LLM 后端（OpenAI 兼容协议，可切换提供方） |
| `SEARXNG_BASE_URL` | 空 | SearXNG 实例地址（唯一开箱可用的搜索源，强烈建议配置） |
| `WRITING_RUNTIME_MODE` | off | 治理运行时：off / shadow / allowlist |
| `RESEARCH_REVIEW_ENABLED` | false | 研究综述路径开关（需配合 `--profile research` 启动 scholar-worker） |
| `AR012_CANDIDATE_ENABLED` | false | AR-012 候选评估开关（sidecar 需另行部署） |
| `DASHSCOPE_API_KEY` | 空 | Embedding（可选，未配置时语义去重/记忆检索自动降级） |

> 日常换 Key 不必改文件：Admin 后台「模型配置」与「MCP 管理 → 服务密钥」
> 热更新（加密入库，优先级高于环境变量）。`API_KEY_ENCRYPTION_KEY`
> 一经使用必须保持稳定。

## 风格目录

本仓库遵循「引擎开源、内容自有」的边界：风格 Profile 的**系统**（数据结构、
构建器、热插拔、灰度）完全开源，但**不附带任何预置风格内容**。新建部署请通过
工作台的风格构建器或 Admin API 建立自己的风格目录。

## 文档

| 文档 | 内容 |
|---|---|
| [docs/01-architecture.md](docs/01-architecture.md) | 架构总览 |
| [docs/04-style-profile.md](docs/04-style-profile.md) | 风格系统 |
| [docs/11-memory-system.md](docs/11-memory-system.md) | 分层记忆 |
| [docs/19-governed-writing-runtime.md](docs/19-governed-writing-runtime.md) | 治理型写作运行时 |
| [docs/search-provider-adapter.md](docs/search-provider-adapter.md) | 搜索源适配器开发 |
| [docs/provider-configuration.md](docs/provider-configuration.md) | 模型提供方切换 |
| [docs/ops-backup-restore.md](docs/ops-backup-restore.md) | 备份与恢复 |
| [specs/](specs/) | research-review 契约、验收记录 |

## License

MIT
