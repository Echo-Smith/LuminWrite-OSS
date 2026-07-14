# 分阶段执行计划

## 总览

| Phase | 目标 | 预估周期 | 产出 |
|---|---|---|---|
| Phase 0 | 基础设施搭建 | 1-2 周 | Go 项目骨架 + DB 迁移 + 基础配置 |
| Phase 1 | Agent Engine 核心 | 2-3 周 | Engine + Steps + WebSocket 控制 |
| Phase 2 | 风格系统 + 灰度 | 1-2 周 | Profile 热插拔 + 灰度路由 |
| Phase 3 | 前端 + Admin | 2-3 周 | React 工作台 + Admin Dashboard |
| Phase 4 | 评测 + 反馈 | 1-2 周 | 评测框架 + 反馈系统 + 信誉加权 |

> V1 优化和 LTS 打包与 V2 开发并行进行，互不阻塞。

---

## Phase 0: 基础设施搭建

### 0.1 Go 项目初始化

- [ ] 创建 `backend/` 目录，`go mod init`
- [ ] 搭建 `cmd/server/main.go` 入口
- [ ] 配置 chi 路由框架
- [ ] 配置结构化日志 (slog)
- [ ] 配置加载 (config.yaml + env)
- [ ] 健康检查端点 `/health`

### 0.2 数据库

- [ ] PostgreSQL 17 + pgvector 安装
- [ ] 编写所有迁移文件 (16 个表)
- [ ] 数据库连接池配置 (pgx)
- [ ] 初始数据 seed (默认模型、默认 cron job)
- [ ] 迁移自动化 (golang-migrate)

### 0.3 基础中间件

- [ ] JWT 认证中间件
- [ ] Admin Token 认证中间件
- [ ] CORS 中间件
- [ ] 请求日志中间件
- [ ] 速率限制中间件
- [ ] 错误恢复中间件

### 0.4 基础设施验证

- [ ] 服务器可启动并监听
- [ ] 数据库迁移可执行
- [ ] `/health` 返回正常
- [ ] 基础认证链路通畅

---

## Phase 1: Agent Engine 核心

### 1.1 Engine 框架

- [ ] `ExecutionContext` 结构定义
- [ ] `Step` 接口定义
- [ ] `AgentEngine` 编排器实现
- [ ] 状态管理（运行/暂停/恢复/取消）
- [ ] Trace 记录（DB 持久化）

### 1.2 Steps 实现

- [ ] `IntentStep` — 移植 V1 意图分类 + 三级路由
- [ ] `QueryPlanStep` — 移植 V1 检索 Query 规划
- [ ] `SearchStep` — 并发多源检索 (goroutine + errgroup)
- [ ] `RelevanceStep` — 素材相关性过滤 + pgvector 去重
- [ ] `OutlineStep` — 引导模式提纲生成
- [ ] `WriteStep` — 风格 Profile + DeepSeek 流式生成
- [ ] `PostReviewStep` — 质量评分 + 敏感检查
- [ ] `AutoFixStep` — 自动修正

### 1.3 Tools 实现

- [ ] `DeepSeekClient` — LLM 调用 (flash/pro + thinking)
- [ ] `DashscopeEmbedding` — 通义 text-embedding-v3
- [ ] `ZhihuClient` — 知乎站内/全网搜索
- [ ] `IMAClient` — IMA 知识库检索
- [ ] `TavilyClient` — 通用搜索
- [ ] `TencentNewsClient` — 腾讯新闻
- [ ] `SensitiveCheck` — 敏感词检测

### 1.4 WebSocket

- [ ] WebSocket 连接管理 Hub
- [ ] 消息协议实现 (JSON)
- [ ] 流式输出推送
- [ ] 暂停/恢复/取消控制
- [ ] 引导模式 await_input / confirm
- [ ] 断线重连 + 上下文恢复

### 1.5 知识库

- [ ] pgvector 向量存储
- [ ] 语义检索 (cosine similarity)
- [ ] 语义去重
- [ ] IMA 同步任务 (定时 17:00)

### 1.6 验证

- [ ] 完整写作流程端到端测试
- [ ] WebSocket 流式输出验证
- [ ] 暂停/恢复正常
- [ ] 引导模式交互正常
- [ ] 多源检索并发正常
- [ ] 敏感检查生效

---

## Phase 2: 风格系统 + 灰度

### 2.1 Style Profile

- [ ] Profile JSON 结构定义
- [ ] Profile 加载器 (DB + LRU 缓存)
- [ ] Profile 发布流程 (草稿 → 二次确认 → 发布)
- [ ] 版本管理 (发布 → 归档旧版)
- [ ] 从 V1 提取"预设评论风格"Profile
- [ ] 创建"申论"和"小红书"初始 Profile

### 2.2 灰度路由

- [ ] FNV-1a UID Hash 实现
- [ ] 灰度配置读取 (rollout_type / whitelist / percentage)
- [ ] 两级路由逻辑 (Profile 标记 → UID Hash)
- [ ] 灰度预览 (输入 UID 查看命中版本)
- [ ] 灰度监控指标采集

### 2.3 验证

- [ ] 用户选择不同风格生成不同结果
- [ ] 灰度分流正确 (同一 UID 稳定命中)
- [ ] Profile 发布后即时生效
- [ ] 灰度版本与旧版本并行正常

---

## Phase 3: 前端 + Admin

### 3.1 前端基础设施

- [ ] Vite + React 19 项目初始化
- [ ] shadcnUI 组件库安装
- [ ] assistantUI 集成
- [ ] WebSocket Hook (`useAgentWebSocket`)
- [ ] 路由配置 (React Router)
- [ ] 全局状态管理

### 3.2 写作工作台

- [ ] 写作输入区 (含素材上传)
- [ ] 风格选择器
- [ ] 模式选择 (auto / writing / guided)
- [ ] Agent Trace 可视化 (步骤进度)
- [ ] 流式输出渲染 (Markdown)
- [ ] 引导模式提纲编辑器
- [ ] 暂停/恢复/取消按钮
- [ ] 写后反馈面板

### 3.3 选题中心

- [ ] 热搜列表展示
- [ ] 选题详情查看
- [ ] 自定义选题上传
- [ ] SSE 选题实时推送
- [ ] 一键开始写作

### 3.4 Admin Dashboard

- [ ] 概览面板 (指标卡片 + 趋势图)
- [ ] 风格管理 (列表 + 编辑 + 发布确认 + 版本历史)
- [ ] 灰度配置 (白名单/百分比 + 预览)
- [ ] 模型配置 (CRUD + 设默认)
- [ ] API 密钥管理 (CRUD + 测试)
- [ ] 定时任务管理 (CRUD + 手动运行)
- [ ] 评测面板 (报告 + 版本对比)
- [ ] 反馈分析 (评分分布 + 问题分析)
- [ ] 用量统计 (Token 趋势 + 模型分布)
- [ ] 敏感词库 (CRUD + 严格程度配置)

### 3.5 验证

- [ ] 写作工作台完整流程可用
- [ ] WebSocket 流式输出实时渲染
- [ ] Admin 各页面功能正常
- [ ] 响应式布局 (桌面/平板)

---

## Phase 4: 评测 + 反馈

### 4.1 评测系统

- [ ] 评测集管理 (CRUD)
- [ ] 样本管理 (CRUD + 人工标注)
- [ ] 评测执行器 (并发跑样本)
- [ ] 评分模块 (规则评分 + LLM-as-Judge)
- [ ] 评测报告生成
- [ ] 版本对比
- [ ] 第三方平台导出
- [ ] Profile 变更自动触发评测

### 4.2 反馈系统

- [ ] 分段反馈接收
- [ ] 反馈聚合计算 (定时/异步)
- [ ] 信誉分计算 (base + adoption_bonus)
- [ ] workbuddy 录用回调接口
- [ ] 迭代阈值检测
- [ ] 反馈分析报告

### 4.3 初始评测集

- [ ] 预设评论风格: 25 条样本 (人工标注)
- [ ] 申论: 20 条样本 (人工标注)
- [ ] 小红书: 20 条样本 (人工标注)

### 4.4 验证

- [ ] Profile 发布自动触发评测
- [ ] 评测报告正确展示
- [ ] 用户反馈正确记录和聚合
- [ ] workbuddy 录用回调正常
- [ ] 信誉分计算正确
- [ ] 迭代阈值触发提醒

---

## V1 优化（与 V2 并行）

### V1 优化项

- [ ] 标题质量修复 (禁止用伤亡数字做标题)
- [ ] 内容安全严格程度可调 (避免误拦)
- [ ] Agent Loop MVP 完善 (trace 持久化)
- [ ] 其他已知 bug 修复

### V1 LTS 打包

- [ ] 使用 `scripts/package-1panel-node.sh` 打包
- [ ] 部署到 1Panel 作为 LTS 版本运行
- [ ] V2 稳定后决定是否下线 V1

---

## 里程碑

| 里程碑 | 目标 | 预计完成 |
|---|---|---|
| M0 | 基础设施就绪 | Phase 0 完成 |
| M1 | 写作流程可用 | Phase 1 完成 |
| M2 | 多风格 + 灰度可用 | Phase 2 完成 |
| M3 | 前端 + Admin 可用 | Phase 3 完成 |
| M4 | 评测 + 反馈闭环 | Phase 4 完成 |
| M5 | V2 可上线灰度 | 全部完成 |
| M6 | V1 LTS 上线 | V1 优化完成 |
