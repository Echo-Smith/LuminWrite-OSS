# WP4 试点用户上手指南（Pilot User Onboarding）

> 本文与商业版同源，仅检索提供方表述按 OSS 版边界调整。

**日期**：2026-09-21
**状态**：Ready for pilot
**适用对象**：WP4 试点用户
**Context Compiler**：Enabled（B 候选已按消融决策晋级）

本文是操作手册：说明试点是什么、三大写作流程怎么选、从哪个入口开始、运行过程里能看到什么，以及怎么反馈问题。术语保留英文标识符，便于与系统界面对照。

## 1. 试点是什么

- **4 周试点**：在真实写作任务中使用三大写作流程（长文创作 / 多材料综合 / 忠实改写）。
- **目标**：走通「合约 → 计划 → 运行 → 产物 → 质量门」的完整链路，验证三大流程可用、过程可查、结果可信。
- **成功指标（4-week pilot）**：

| 指标 | 目标 | 度量方式 |
|--------|--------|-------------|
| 任务完成率 Task completion rate | ≥95% | completed_runs / total_runs |
| 质量门通过率 Quality gate pass rate | ≥80% | runs with all gates passing |
| ContextEnvelope 编译成功率 | ≥99% | envelopes / node_attempts |
| 离线回放成功率 Offline replay success | 100% | replay_runs / sampled_runs |
| 用户满意度 User satisfaction (1-5) | ≥4.0 | pilot feedback form |

### 核心概念速查

| 概念 | 一句话说明 |
|---|---|
| WritingContract（写作合约） | 版本化的写作约定：文体、受众、篇幅目标、风格与证据要求；每次运行绑定一个确定的合约版本（contract_hash） |
| RunPlan（运行计划） | 由合约编译出的可执行计划；运行开始后锁定版本，重试与恢复沿用同一版本 |
| Run（运行） | 计划的一次完整执行；过程记录在 RunLedger，运行身份由 RunFingerprint 绑定 |
| Artifact（产物） | 各节点产出的类型化结果（outline / draft / synthesis / rewrite / revision_set 等），串成 artifact 链 |
| Quality Gate（质量门） | 对产物的五维评分与硬性检查，≥80 通过；决定稿件能否推进到 Accepted / Verified |

## 2. 三大写作流程速览

### 2.1 长文创作（Long-Form Creation）

- **适合什么任务**：需要「先结构、后成稿」的成篇长文。试点场景示例：根据选题简报和 3–5 份材料，写一篇 3000 字行业分析报告。
- **流程**：Brief → Materials → Structure → Segmented Writing → Full Consolidation
- **编排模式**：`outline_first`（tpl_outline_first_v1）或 `strict_research`（tpl_strict_research_v1）
- **证据策略**：`long_form` —— 受治理节点为 index 1（`draft`）
- **输入要准备什么**：
  - 选题简报（Brief）：主题、核心问题、目标受众、篇幅目标
  - 材料：3–5 份来源材料
- **产出什么（artifact 链）**：

  ```text
  contract → outline → draft → quality → finalize → revision_set
  ```

- **验收要点**（用户视角自查）：
  - 合约里写清了文档类型、受众、篇幅目标和风格约束
  - 大纲（outline）至少 3 个层级
  - 草稿（draft）覆盖大纲全部章节，术语前后一致
  - 质量门五个维度全部 ≥80
  - 终稿（revision_set）保留了来源材料中的事实主张
  - 每个节点尝试都有 ContextEnvelope 编译结果（确定性哈希），运行身份由 RunFingerprint 完整绑定

### 2.2 多材料综合（Multi-Material Synthesis）

- **适合什么任务**：多份材料放在一起读、要输出一份统一结论的任务。试点场景示例：把 3 份数据互相矛盾的市场调研报告综合成一份统一分析，并解释数据差异。
- **流程**：Parallel Understanding → Conflict Identification → Synthesized Expression → Source Tracing
- **编排模式**：`sourced`（tpl_sourced_v1）
- **证据策略**：`multi_material` —— 受治理节点为 index 1（`synthesis`）
- **输入要准备什么**：
  - 材料：2 份以上来源材料（含交叉或冲突数据更佳，便于检验冲突识别）
- **产出什么（artifact 链）**：

  ```text
  contract + materials → synthesis → quality → finalize → revision_set
  ```

- **验收要点**（用户视角自查）：
  - 每份输入材料都在 EvidenceView 中留有 source_ref 溯源
  - 互相矛盾的主张被识别并标记出来
  - 每条事实主张至少能追溯到一个来源（EvidenceViewItem）
  - 每条主张都记录了矛盾状态（`supports` / `contradicts` / `neutral`）
  - 质量门 sourceFidelity 维度 ≥80
  - EvidenceView 回放结果一致（确定性投影）

### 2.3 忠实改写（Faithful Rewrite）

- **适合什么任务**：已有成稿、只做润色或结构调整，且必须保住事实、观点和作者声音。试点场景示例：润色一篇 2000 字企业公告，同时排除内部人事信息、保留对外口径。
- **流程**：Polish / Restructure，保持事实、观点与作者声音
- **编排模式**：`fast`（tpl_fast_v1）
- **证据策略**：`faithful_rewrite` —— 受治理节点为 index 0（`rewrite`）
- **输入要准备什么**：
  - 原文文章（会作为 `article` context field 保存）
  - 需要排除的内容标记（PII、内部信息等）
- **产出什么（artifact 链）**：

  ```text
  contract + materials + article → rewrite → quality → finalize → revision_set
  ```

- **验收要点**（用户视角自查）：
  - 原文以 `article` context field 形式完整保存
  - 事实主张全部保留（改写前后编辑距离比 ≤0.3）
  - 被标记排除的内容（PII、内部信息）不出现在输出里
  - 质量门 styleConsistency 维度 ≥80
  - 没有新增捏造的事实主张
  - 确定性检查确认标记的私密内容零泄漏

### 2.4 补充素材与联网检索

流程的主材料永远是你上传的材料（用户材料优先级最高，`user_material_priority: highest`，见 docs/19）。需要补充联网检索素材时：OSS 版开箱可用的检索实现是 **SearXNG**（自托管元搜索，零 API key，quickstart 栈内置）；其余检索提供方（Tavily、知乎、腾讯新闻、微博、Bing、AnySearch）在 OSS 版仅为 **stub**——接口公开但调用即返回 `not-installed`，可按 docs/search-provider-adapter.md 自行接入完整实现。OSS 版不包含任何付费搜索 Provider 实现、商业凭证变量或商业 CLI，付费搜索不构成内置能力。

## 3. 三种入口怎么用

### 3.1 前端写作工作台（最常用）

1. 打开写作工作台。
2. 点「新建文档（New Document）」。
3. 选择流程类型：**长文创作 / 多材料综合 / 忠实改写**。
4. 按所选流程准备输入（见第 2 节），提交后在右侧详情面板（大纲｜材料｜运行｜质量｜版本）跟踪进度、查看质量评分与产物链。

### 3.2 REST API（适合脚本化 / 批量）

写作命令按顺序走 REST：`POST /api/v2/documents` → `POST /api/v2/contracts` → `POST /api/v2/plans` → `POST /api/v2/runs`；运行事件走 SSE（`GET /api/v2/runs/{runId}/events`，支持 `Last-Event-ID` / `?after=` 断线续传）。认证统一为 `Authorization: Bearer <JWT>`。

以下 curl 骨架中 token 与地址一律使用占位符（`$API_TOKEN` 等），**严禁把真实 token / 密钥写进脚本、工单或文档**：

```bash
# 环境准备（全部是占位符，替换成你自己的部署地址与令牌）
export API_TOKEN="<你的访问令牌 JWT>"
BASE_URL="https://<部署地址>/api/v2"

# ① 新建文档
curl -sS -X POST "$BASE_URL/documents" \
  -H "Authorization: Bearer $API_TOKEN" \
  -H "Content-Type: application/json" \
  -d @document.json
# → 记下返回的文档 ID：<DOCUMENT_ID>

# ② 创建写作合约（WritingContract，字段结构见 docs/19-governed-writing-runtime.md）
curl -sS -X POST "$BASE_URL/contracts" \
  -H "Authorization: Bearer $API_TOKEN" \
  -H "Content-Type: application/json" \
  -d @contract.json
# → 记下返回的合约 ID：<CONTRACT_ID>

# ③ 创建运行计划
curl -sS -X POST "$BASE_URL/plans" \
  -H "Authorization: Bearer $API_TOKEN" \
  -H "Content-Type: application/json" \
  -d @plan.json
# → 记下返回的计划 ID：<PLAN_ID>

# ④ 启动运行
curl -sS -X POST "$BASE_URL/runs" \
  -H "Authorization: Bearer $API_TOKEN" \
  -H "Content-Type: application/json" \
  -d @run.json
# → 记下返回的运行 ID：<RUN_ID>

# ⑤ 订阅运行事件（SSE；断线续传：带 Last-Event-ID 头，或改用 ?after=<事件 ID>）
curl -N "$BASE_URL/runs/<RUN_ID>/events" \
  -H "Authorization: Bearer $API_TOKEN" \
  -H "Last-Event-ID: <上次收到的事件 ID>"
```

说明：

- 四步按顺序串联：上一步返回的 ID 作为下一步请求体的输入。
- `@document.json` / `@contract.json` / `@plan.json` / `@run.json` 的请求体字段以部署版本的接口实现为准；`contract.json` 即 WritingContract。
- 基础路径 `/api/v2`，内容类型 `application/json`，详见 [03-api-specification.md](03-api-specification.md)。

### 3.3 CLI（造测试数据）

```bash
go run ./cmd/seed-ablation-cases/ --seed-cases
```

一条命令造出试点 / 回归测试用例数据，适合在手头没有现成材料时先走通流程、熟悉各阶段产物。

## 4. 试点配置说明

```yaml
contextCompiler:
  enabled: true          # B 候选已按消融决策晋级
  compilerVersion: 2
  persistEnvelopes: true # 支持离线回放

userMemory:
  enabled: false         # C 候选按消融决策暂缓

projectMemory:
  enabled: false         # D 候选按消融决策暂缓
```

用户可感知含义：

- **contextCompiler 开启 + persistEnvelopes=true**：每次运行、每个节点尝试的上下文编译结果都会留存，可以离线回放，逐节点核对「模型当时到底看到了什么」。
- **userMemory / projectMemory 暂关闭**（C / D 候选按消融决策暂缓）：试点期间不会把个人记忆或项目记忆注入写作上下文——行为更可预期，回放结果更干净。

## 5. 运行过程里能看到什么

- **RunFingerprint（运行指纹）**：每次运行都有完整身份绑定——contract_hash、plan、capability、envelope、model、output 记在一起，事后可以精确回答「这个结果是哪个合约、哪个计划、哪个模型产出的」。
- **ContextEnvelope（上下文信封）**：每个节点每次尝试的上下文编译结果（确定性哈希），留存后可离线回放，逐节点核对上下文。
- **EvidenceView（证据视图）**：材料来源追踪；由 5 类来源做确定性投影生成，多材料综合流程里每条事实主张都能点回来源。
- **质量五维评分**：每份产物按五个 rubric 维度打分，全部 ≥80 才算通过质量门（多材料综合看 sourceFidelity ≥80，忠实改写看 styleConsistency ≥80）。
- **终态保护（Terminal State Guards）**：运行进入终态后在数据库层面禁止再写入；已结束的运行不会被后续操作改动。

## 6. 反馈渠道与反馈表要点

### 反馈渠道

- **试点反馈表**：每类任务完成后填写（满意度 1–5 打分 + 场景记录）。
- **产品内分段反馈**：对标题 / 段落级内容打分与评论（`POST /api/v2/feedback`）。

### 反馈表要点

1. **满意度 1–5 打分**：按任务逐次打分，对应成功指标「用户满意度 ≥4.0」。
2. **什么场景用了哪个流程**：记录任务类型与所选流程（长文创作 / 多材料综合 / 忠实改写）的匹配情况——合适就说明为什么合适，不合适就说明你会期待怎么改进或换用哪个流程。
3. 遇到的问题与期望改进（可选）：附上 RunFingerprint 或运行 ID，便于定位回放。

### FAQ

**Q1：质量门没过怎么办？**
未达 ≥80 的维度会以质量 Finding 的形式定位到具体文档块；稿件保持 Candidate Draft 状态，不会冒充完成。你可以让系统局部修复或重新生成该章节，也可以修改 WritingContract 后重新验收。涉及 BLOCKER（如润色改变核心语义、用户锁定内容被破坏）不可豁免，只能修复、重新生成、恢复版本、修改合约后重验或终止候选稿。

**Q2：材料太多，或材料之间有冲突数据会怎样？**
材料多不会一次性塞进模型上下文：每个节点只拿到受控的 ContextEnvelope（合约版本、不可违反的约束、允许读取的 Artifact、相关 Claim/Evidence 等），可离线回放核对。材料间有冲突时，多材料综合流程会识别并标记冲突主张，为每条主张记录矛盾状态（`supports` / `contradicts` / `neutral`）并在 EvidenceView 中保留来源；关键冲突属于高风险情形，需要你确认后才继续。

**Q3：忠实改写会不会改动事实，或泄露我标记的内部信息？**
不会。忠实改写以语义保持为验收标准：事实主张全部保留（改写前后编辑距离比 ≤0.3），不新增捏造主张；被标记排除的内容（PII、内部信息）不得出现在输出中，并有确定性检查核验标记的私密内容零泄漏。若润色改变了核心语义，属于不可豁免的 BLOCKER。

**Q4：什么是离线回放？我怎么用它核对上下文？**
试点配置 `persistEnvelopes=true` 会留存每个节点尝试的 ContextEnvelope。你可以对已完成的运行做离线回放，逐节点核对模型当时收到的上下文（合约版本、约束、允许读取的产物、相关 Claim/Evidence 等），确认与运行时一致；EvidenceView 的投影在回放中确定一致。

## 7. 参考文档

- [28-wp4-pilot-scenarios.md](28-wp4-pilot-scenarios.md) — WP4 三大流程、试点场景与验收标准（权威来源）
- [19-governed-writing-runtime.md](19-governed-writing-runtime.md) — 治理型写作运行时（Contract / Plan / Run / Artifact / Quality Gate）
