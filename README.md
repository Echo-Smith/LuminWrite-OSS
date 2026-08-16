# 笔润智谈 LuminBuddy V2

[English](README.en.md) · [在线体验](https://luminbuddy2.ericdocmic.top/v2/) · [更新日志](#更新日志)

面向中文内容生产者的 AI 写作工作台：从需求理解、素材检索和提纲确认，到按风格成稿、写后自检、反馈与记忆沉淀，把一次性生成变成可观察、可干预、可迭代的写作流程。

![笔润智谈写作工作台](docs/assets/luminbuddy-workspace.png)

> **当前成熟度：工程 Beta。** 仓库已实现可运行的前后端、Harness 单层 Agent 编排、写作 Pipeline、引导式提纲、风格配置、A/B 评测、反馈系统、分层记忆与监控指标；尚未积累可公开验证的用户规模、留存或业务结果。

## 为什么做

通用对话模型可以很快给出一篇稿件，但真实写作任务通常卡在四个环节：

- 用户的真实意图、篇幅和风格约束没有被稳定理解；
- 素材检索、观点组织和成稿被压进一次不可观察的生成；
- 用户无法在关键决策点确认提纲、暂停或调整方向；
- 好坏反馈没有进入下一次生成，团队也难以定位失败发生在哪一步。

LuminBuddy 将这些问题拆成可见的产品机制，而不是简单增加一个更长的 Prompt。

## 核心体验

```text
写作需求
  → 意图识别与任务归一化（规则优先，毫秒级路由）
  → Harness 单层 LLM 编排（工具自主调用，会话状态跨轮保留）
  → 检索计划与多源素材
  → 相关性过滤与去重
  → 提纲确认（引导模式）
  → 风格化流式写作
  → 写后审校与一次自动修正
  → 分段反馈、A/B 评测与记忆沉淀
```

| 用户阶段 | 产品机制 | 用户获得什么 |
|---|---|---|
| 描述任务 | 规则优先、低置信度再调用 LLM 的意图路由 | 口语化需求也能落到写作、润色、压缩、扩写或自由对话 |
| 补充素材 | Query Plan、多源检索、相关性过滤与语义去重 | 看得到素材从哪里来，减少无关信息进入成稿 |
| 决定结构 | 自动模式或 Guided Mode | 可直接生成，也可先确认、修改或重做提纲 |
| 形成稿件 | Style Profile + 流式输出 + 暂停/恢复 | 风格从代码逻辑中解耦，生成过程可干预 |
| 判断质量 | Post Review、敏感检查、Auto Fix | 低严重度问题自动修正，高风险问题保留人工判断 |
| 持续改进 | 分段反馈、A/B 评测集、分层记忆 | 将偏好和失败信号沉淀为下一轮可复用证据 |

## 关键产品判断

### 1. Harness 单层编排，LLM 自主执行

采用 Harness-LLM 单层持续会话架构：Harness 负责意图路由、工具注册、状态管理和断路保护；LLM 在持续会话中自主决定调用什么工具、何时写作、何时修正。不存在外层 ReAct + 内层 agent loop 的嵌套，降低延迟与输出漂移。

### 2. 模型不负责所有确定性逻辑

意图识别先走规则评分，低置信度场景才交给模型；Pipeline 管理确定步骤，模型负责需要语义判断和生成的环节。

### 3. 把"控制权"放在高代价决策点

Guided Mode 在提纲阶段设置人工门控，支持确认、编辑和最多五次重做。用户不必逐步配置全部参数，但能在成稿前改变最重要的结构方向。

### 4. 风格不是一段藏在代码里的 Prompt

Style Profile 将风格规则、版本和灰度路由独立管理，使不同写作场景可以发布、回滚和评测，而不需要修改整条 Agent Pipeline。

### 5. 记忆必须可解释、可冲突处理

记忆区分为硬偏好、行为模式和反馈信号，并提供质量门、冲突处理、强化与废弃状态。记忆用于辅助写作，不默认把每次输入都永久保存。

### 6. 没有评测和观测，就没有可持续迭代

内置 A/B 测试框架，支持对照组与实验组的自动化评测与指标对比。后端暴露 HTTP、WebSocket、Agent、LLM、数据库、评测、缓存和灰度路由等指标；Trace 保存步骤、耗时和错误。

## 已实现能力与边界

| 状态 | 能力 |
|---|---|
| 已实现 | React 19 写作工作台、Go Harness Agent 引擎、WebSocket 流式事件、暂停/恢复/取消、Guided Mode、Style Profile、Post Review、Auto Fix、用户反馈、A/B 评测框架、分层记忆、Session Event Log、Trace 与 Prometheus 指标、Passkey/WebAuthn 认证、游客模式 |
| 依赖配置 | PostgreSQL 17 + pgvector/paradedb、模型 API、Embedding、外部检索信源、WebAuthn 或账号体系 |
| Demo / 早期能力 | 多信源稳定性、真实用户反馈闭环、评测集标注质量、长期记忆的实际收益 |
| 尚缺公开证据 | 活跃用户、重复使用、质量提升幅度、成本收益、内容采用或业务结果 |

## 系统结构

```text
React 19 + Vite
       │ REST / WebSocket
Go Harness Agent Engine
       ├─ Intent Routing (规则优先) / Memory Gate
       ├─ Harness LLM 编排 (工具自主调用, 会话状态持久化)
       ├─ Query Plan / Search / Relevance / Outline / Write
       ├─ Post Review / Auto Fix / Memory Extract
       ├─ Style Profile / A/B Evaluation / Feedback
       └─ Trace / Metrics / Session Event Log / Grayscale Routing
       │
PostgreSQL 17 + pgvector + paradedb (BM25)
```

技术选型服务于三个目标：让步骤可观察、让关键节点可干预、让反馈可以进入下一轮迭代。详细设计见 [架构蓝图](docs/01-architecture.md) 和 [Harness 架构方案](docs/architecture-c-design.md)。

## 快速启动

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

端到端与 A/B 测试脚本位于 `backend/e2e-*.mjs`，需要可访问的后端、数据库和相应外部服务配置。

### 部署打包

```bash
# 仅源码包
./scripts/pack-for-1panel.sh

# 源码 + Docker 镜像（国内服务器推荐）
./scripts/pack-for-1panel.sh --images
```

## 设计文档

| 文档 | 内容 |
|---|---|
| [架构蓝图](docs/01-architecture.md) | Agent Engine、数据流与系统边界 |
| [Harness 架构方案](docs/architecture-c-design.md) | 单层 LLM 编排器设计与工具粒度 |
| [数据库 Schema](docs/02-database-schema.md) | PostgreSQL、pgvector 与迁移 |
| [API 规范](docs/03-api-specification.md) | REST 与 WebSocket 协议 |
| [Style Profile](docs/04-style-profile.md) | 风格版本、发布与回滚 |
| [灰度路由](docs/05-grayscale-routing.md) | Profile 标记和 UID Hash 分流 |
| [评测系统](docs/06-evaluation.md) | 评测集与触发机制 |
| [反馈系统](docs/07-feedback.md) | 分段反馈与信誉权重 |
| [Admin Dashboard](docs/08-admin-dashboard.md) | 配置、评测与可观测入口 |
| [记忆系统](docs/11-memory-system.md) | 硬偏好、行为模式与反馈信号 |
| [编辑部系统](docs/12-editorial-system.md) | 编辑任务管理与工作流 |

## 项目声明

- 这是可运行的个人产品与工程项目，不代表已完成规模化市场验证。
- 外部模型、检索和数据源的可用性取决于各服务配置与条款。
- 仓库不包含生产环境密钥；请从示例环境文件创建本地配置。

## 更新日志

### v0.3.0 (2026-08-16)

- **Harness 架构**：替换原 UnifiedAgent 嵌套 ReAct 为单层 LLM 持续会话编排，工具自主调用，会话状态跨轮持久化
- **A/B 测试框架**：编辑实验编排器 + 结果存储，支持对照组/实验组自动化评测与指标对比
- **Passkey 认证**：WebAuthn 无密码登录与设备管理，个人中心直接绑定/删除 Passkey
- **Session Event Log**：追加式事件日志，支持会话回放与断线重连
- **Prompt 注入防御**：chat 意图精简注入，Token 降 10.5%
- **SQL 修复**：`session_events.go` PostgreSQL 类型转换 bug
- **打包脚本**：修复 macOS mktemp 兼容性与已删除文件处理

### v0.2.0

- 引导式提纲（Guided Mode）：提纲确认、编辑、最多五次重做
- Style Profile 灰度路由：版本管理、UID Hash 分流
- Post Review + Auto Fix：写后审校与自动修正
- 分层记忆系统：硬偏好、行为模式、反馈信号
- Prometheus 指标与 Trace 链路追踪
- 游客模式与注册升级流程

### v0.1.0

- React 19 写作工作台 + Go Agent Pipeline
- WebSocket 流式事件（暂停/恢复/取消）
- 多源检索与相关性过滤
- Docker Compose 一键部署

## License

[MIT](LICENSE)
