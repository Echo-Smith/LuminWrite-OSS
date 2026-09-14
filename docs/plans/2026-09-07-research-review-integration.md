# LuminBuddy 研究综述路径 Implementation Plan

**Goal:** 在现有工作台交付一条可追溯、可确认、可恢复的研究综述路径，首版复用现有 Writer，后续独立评估 AR-012。

**Architecture:** Go WritingRuntime 统一持有合同、预算、状态与正式版本；私网 Python Scholar Worker 提供有界检索/解析/阅读能力。使用版本化证据 Artifact 和两个持久 gate；不引入第二套项目/写作编排。

**Tech Stack:** 现有 Go/Postgres/React/TypeScript 与 Python 3.12 独立服务。依赖在实际实现提交锁版本；本文为基于源码的设计，不是第三方 API 最新用法说明。

日期：2026-09-07。状态：**待实施**；这次仅交付开发方案，不代表功能已接入、测试已完成或已部署。

## 开发入口

先读 [需求与范围](../../specs/research-review/requirements.md) → [技术设计](../../specs/research-review/design.md) → [接口合同](../../specs/research-review/contracts.md)。验收用例在本文末尾。原始 PR 证据在工作区 `output/autoresearch-ar012-analysis/`；参考源码在 `references/AutoResearch-pr6`、`references/AutoResearch-pr7`、`references/AutoResearch-ar012`。

实施以 OSS 仓库为主，每个里程碑将公共代码同步至商业版并各自跑验证。**当前两仓库都有既有未提交模型测试/限速修复，先记录 git status，不覆盖、不 reset，不把它们误算为本功能提交。** 开发前从确认过的基线准备隔离分支，迁移编号同时查两仓库；当前 OSS 最大编号为106。

## 推荐顺序与工程量

人日为一名熟悉代码的开发者约一天有效工作量，含对应测试与评审；不含许可等待、外部接口不可用和生产审批等待。是估算，不是已完成工作量。

| 任务 | 优先级 | 人日 | 收益 | 依赖 |
|---|---|---:|---|---|
| T00 运行终态与恢复基线 | P0 | 1–2 | 避免长流程不可恢复/界面一直等待 | 无 |
| T01 合同、证据类型与 schema | P0 | 2–3 | 三方数据接通，防证据链在适配时丢失 | 无 |
| T02 子任务与 gate 持久机制 | P0 | 3–4 | 真正支持确认、重启与幂等 | T00,T01 |
| T03 Worker 骨架与安全边界 | P0 | 1–2 | 隔离 Python 依赖，建立可替换能力接口 | T01；代码复用需许可 |
| T04 学术发现与全文获取 | P1 | 2–3 | 补齐论文来源和全文入口 | T03 |
| T05 Reader 与证据包 | P1 | 3–4 | 从普通摘录升级为可核查证据 | T02,T04 |
| T06 模板、提纲与现有 Writer | P1 | 2–3 | 完成研究到写作的纵向闭环 | T01,T02,T05 |
| T07 引用与交付质量门 | P1 | 2–3 | 防伪造引用和证据范围夸大进入正式稿 | T06 |
| T08 工作台入口/证据/确认交互 | P1 | 2–3 | 用户能看懂并控制流程 | T01可先 mock；联调依赖T02,T06 |
| T09 联合回归与测试用户灰度 | P1 | 2–3 | 用真实完整路径验证可靠性 | T00–T08 |
| T10 AR-012 参数化与对照实验 | P2 | 5–8 | 判断新生成器是否真正增加质量 | 首版通过；独立许可检查 |

**首版合计 20–30 人日；AR-012 对照另加 5–8 人日。** 两名开发可并行协议/前端和后端/worker，首版建议预留约3–4周日历时间，具体取决于熟悉度和接口稳定性；一名开发约4–6周。若许可要求重新实现整个 Reader，而非复用，经 T03 技术验证后重估，不能沿用此复用估算。

最快可评审切片：T00/T01/T02 + 手工论文固定 fixture + mock Reader → 两个确认点 → 现有 Writer。这个切片仅证明宿主链路，不标记真实学术研究完成。随后接真实检索与阅读，最后做 AR-012。

## T00：把运行状态基线修通

**现有文件**：backend/internal/server/governed_worker.go、governed_runtime.go、handlers_writing_run.go、governed_runners_test.go；backend/internal/writingruntime/orchestrator.go、recovery.go；backend/internal/writingstore/runs.go、runtime.go、execution_lock.go。

- [ ] 保存此前 HTTP E2E 的失败日志；用无外部模型的可控 runner 重现“node_outline 已暂停但 GET 未见终态/暂停”。
- [ ] 加 `TestGovernedRunTerminalConsistency`：主动失败/暂停/取消/完成四种状态，对比持久 run、checkpoint、事件和 HTTP 投影；断开请求后 worker 仍需写最终状态。
- [ ] 加进程重启/执行锁竞争样本，确认故障属于状态提交、worker 调度还是测试轮询；按证据修复，不能只增加超时掩盖问题。
- [ ] 运行下方后端定向测试；只有能稳定复现并修复才关闭任务。

验收：上述状态均在测试限定窗口内可见；GET 与事件最终一致；重启不丢已完成节点。提交范围：运行可靠性基线，独立于学术业务。

## T01：先落协议与兼容性

**修改**：backend/internal/writingkernel/contract.go、enums.go、contract_test.go；backend/internal/writingplan/compiler.go、capability.go；frontend/src/lib/writing-runtime-types.ts。

**新增**：backend/internal/writingruntime/research_contracts.go、research_contracts_test.go；specs/lcp/v1.1/writing-contract.schema.json；specs/research-review/schemas/ 下的严格 Artifact schemas 与 fixtures（实施时生成）。

- [ ] 写历史 v1 合同 JSON/hash 金样本测试，再新增 v1.1 ResearchSpec 验证测试（越界、未知字段、非法年份、模式不匹配）。
- [ ] 扩展严格解码、版本校验、hash 与 FieldValueHash/source attribution 支持；旧合同 omitempty 不影响 hash。
- [ ] 定义 pack、locator、claim、outline、citation_index 类型和跨引用校验函数；Go/Python/TS 共享相同 fixture。
- [ ] 明确源文档字节 hash、解析块 hash、码点 offset；加中文/emoji/CRLF 的一致性样本。
- [ ] 注册新增 Artifact/权限白名单及初始材料 manifest；无上传文件时也生成合法空 manifest，避免模板所需 materials 缺失。

验收：历史合同字节/hash 不变；篡改 quote/hash/ID 失败；示例经严格 schema 与 Go 解码双验证。提交范围：协议与纯验证，无外部调用。

## T02：持久子任务与两个确认点

**修改**：backend/internal/writingruntime/orchestrator.go、recovery.go；backend/internal/writingstore/runtime.go；backend/internal/server/writing_routes.go、writing_api.go、handlers_writing_run.go、governed_worker.go。

**新增**：backend/internal/writingstore/research_tasks.go、research_gates.go 及 _test.go；backend/internal/writingruntime/research_gate.go、research_gate_test.go；backend/internal/server/handlers_writing_research.go；backend/internal/database/migrations/107_research_workflow.up.sql 和 .down.sql（编号开工确认）。同一迁移扩展 schema_version（lcp/1.1）/ node_kind（human_gate）/ event_type 三处 CHECK（见 design.md §6）；gate 决议后节点恰好完成一次、主循环正常退出作为显式验收。

- [ ] 用数据库测试先表达 gate pending/确认/重复确认/过期确认/越权/重启六条行为。
- [ ] 建增量表、索引、fencing 租约与 store 事务入口。decision、成功 gate 记录、checkpoint、状态事件与恢复标识必须原子提交。
- [ ] 扩展 checkpoint waiting_gate_id；到达 gate 不写 UnsafeInFlight。恢复消费已持久的决议，不重新阻塞同一个 gate。
- [ ] 新增 GET 研究进度、GET gate、POST decision、POST outline revision 和受限 Artifact 读取接口，所有权规则沿用现有 API。
- [ ] 将确认后的恢复交给可重启的 worker；POST 返回202，不同步等待正文。普通 resume 不能跳过 pending gate。
- [ ] 为子任务 lease expiry、旧 worker 晚到结果、outcome_unknown 加负例；不把所有模型调用标记 IdempotencySafe。

验收：并发确认只生成一份批准产物；确认事务后宕机仍能恢复；不出现 gate 循环暂停；未知付费结果不自动重复调用。

## T03：可独立部署的 Scholar Worker

**新增**：services/scholar-worker/pyproject.toml、依赖锁文件、src/lumin_scholar/{api,contracts,operations}.py、tests/test_api_contract.py、Dockerfile；backend/internal/scholar/{client,errors}.go 及测试。

- [ ] 确认并记录上游代码许可；若许可未明确，先实现 mock 和自有协议，不复制 Proprietary 模块。
- [ ] 搭建五类 operation 白名单接口、结构化错误、deadline/context 传播、服务认证、输入/输出 hash 校验。
- [ ] 实现受任务范围约束的文件传输，不接受本地路径；超大、过期、跨 owner 内容请求失败。
- [ ] Go 客户端严格验证 request_id/input_hash/输出类型，配置统一 provider 调用限速和预算钩子。
- [ ] 写假 HTTP server 测试成功、429、超时、畸形 JSON、断连和旧 worker 晚到结果；容器健康探针不调用收费模型。

验收：worker 独立启动，不依赖 AutoResearch 项目文件结构/LangGraph/SQLite；对外没有公共访问端口。提交范围：服务骨架与模拟接口。

## T04：PR #7 学术发现与全文

**参考源码**：references/AutoResearch-pr7/src/autoresearch/{search_service,open_access_service,full_text_service}.py 与 agents/orchestrator.py；实际采用固定 SHA 见 design.md。

**新增**：services/scholar-worker/src/lumin_scholar/{discovery,ranking,download}.py 与对应 tests；backend/internal/writingruntime/research_discover.go 及测试。

- [ ] 抽取/重实现 query 规划与三源查询，先测试原始研究问题传到 rank，不误用改写 query 当用户意图。
- [ ] 规范 DOI 与别名，保留 provider 状态；落实 60 候选/20 阅读限制和明确 fallback。
- [ ] 将 OA发现、下载、PDF验证分开持久记录；补受约束重定向/DNS目标检查、25MiB大小限制与 landing page 负例。
- [ ] 用户材料优先并通过已有 owner manifest 纳入候选；禁联网合同不触发外部调用。
- [ ] 真实检索 smoke 只验证可访问性与返回结构，回归用固定响应，避免网络波动使 CI 随机失败。

验收：无结果、全源故障、未评分、待补全文状态不同；重复论文与元数据歧义可解释；下载不访问内网目标。

## T05：PR #6 Reader 与冻结证据包

**参考源码**：references/AutoResearch-pr6/src/autoresearch/{reader_parser,reader_service,evidence,contracts,handoffs}.py；不移植整套 agent orchestration。

**新增**：services/scholar-worker/src/lumin_scholar/{parser,reader,evidence}.py、tests/test_evidence_integrity.py；backend/internal/writingruntime/research_read.go、research_pack.go 与对应测试。

- [ ] 先用固定 PDF/TXT/MD/摘要生成解析块及 hash，验证位置；扫描件返回清晰 typed gap。
- [ ] 接 Reader 结构化输出，校验 quote 区间/文档归属/claim绑定，分离 source_assertion 与 interpretation/hypothesis。
- [ ] 每篇/每阶段通过 T02 子任务 ledger 记录进度、用量、缓存键；重启只复用已提交且 hash 有效的产物。
- [ ] 构建 ResearchEvidencePack、覆盖与缺口报告，写 ContentGateway；满足要求后到 evidence gate。
- [ ] 加原始文件修改、模型版本改变、parser版本改变、非法证据绑定、摘要伪装全文等负例。

验收：至少一组含中文/英文和摘要降级的混合材料可回溯每条证据；中断后无需重读已完成且相同输入论文。

## T06：模板到现有 Writer

**修改**：backend/internal/writingplan/templates.go、compiler.go、capability.go 及测试；backend/internal/server/governed_runtime.go、governed_runners.go。

**新增**：backend/internal/writingruntime/research_executor.go、research_outline.go、research_writer.go、research_context.go 及测试。

- [ ] 注册 design.md 节点表；将 contracts.md 的 research_citation_index 加到 draft output/validator input，将 research_validation_details 加到 validator output。每项都进 plan/compiler 检查。
- [ ] 接入直接 runtime executor 与权限/预算检查；不硬塞入仅 action/validate 的 legacy gate runner。
- [ ] 新 outline runner 只生成综合提纲，不使用旧的一秒超时自动确认；确认产物进入 draft 必需输入。
- [ ] 研究 Writer adapter 复用现有模型/写作能力，以专用上下文接收 pack 和批准提纲；记录实际使用证据清单与截断缺口。
- [ ] 正文与 citation index 一致提交候选；provisional 流式文本不被当正式稿。
- [ ] 新模式 unavailable 时明确失败；旧模式模板与权限测试保持不变。

验收：mock 全链路可以按两个 gate 手动推进到候选；未批准、过期批准、上下文关键证据丢失均不能直接进入正文。

## T07：引用校验与正式交付

**修改**：backend/internal/server/governed_runners.go；backend/internal/writingruntime/material_adapter.go、delivery_protocol.go（先确认实际质量投影入口）；backend/internal/writingstore/quality.go。

**新增**：backend/internal/writingruntime/research_validation.go、research_validation_test.go。

- [ ] 编写不存在引用、错包引用、hash篡改、摘要过度归因、没有证据的强断言五类失败样本。
- [ ] 校验正文标记与 citation index、pack 一致，生成研究详情并映射到现有 evidence_report/fact_report 的 blocker。
- [ ] 复用质量交付协议，明确机械引用失败阻断、语义待核查需要人工复核；不让模型自评分直接覆盖 blocker。
- [ ] 生成文献表和点击定位数据；渲染编号变化不破坏 evidence ID。

验收：无效引用无法 finalize；用户看到可检查的具体句子和证据，不只有总分。

## T08：现有工作台交互

**修改**：frontend/src/pages/writing-workspace.tsx、components/composer/writing-composer.tsx、stores/writing-runtime-store.ts、lib/writing-runtime-types.ts。

**新增**：frontend/src/components/writing/research-settings.tsx、research-progress.tsx、research-evidence-panel.tsx、research-gate-panel.tsx、research-citation-popover.tsx；frontend/tests/research-runtime.test.ts。

- [ ] 入口新增研究综述，字段默认值与合同协议一致，disabled 原因可见；保持当前视觉系统。
- [ ] 用 mock API 先完成进度、来源阅读范围、缺口、待确认和失败状态；不把 max20 显示成“已读20”。
- [ ] 证据确认按钮绑定 ref/hash/revision；提纲编辑保存新版本再确认；pending gate 不展示可绕过的继续按钮。
- [ ] 引用展示 quote/来源/页码/范围；摘要和部分全文覆盖明确标注。
- [ ] reducer 测试旧事件/旧 GET、重复事件、跨 run 事件；刷新恢复待确认页面。

验收：完整操作可在原工作台完成；无额外 AutoResearch 项目页面；键盘可操作确认与错误恢复。

## T09：联合验收与灰度

**新增**：backend/internal/server/research_workflow_test.go；docs/releases/2026-09-XX-research-review-acceptance.md（实施时用实际日期）；按现有部署结构增加 worker compose 配置。

- [ ] 跑下方矩阵全部离线场景，再做真实 HTTP 全流程；保留run ID、commit、模型、脱敏日志与用量。
- [ ] 三类主题（材料科学/计算机科学/社会科学）各两次，覆盖至少一个仅摘要场景和一个混合用户材料场景；人检查关键主张与原文。
- [ ] OSS 与商业版独立跑后端和前端回归，核对迁移与功能开关；不将商业支付逻辑同步进 OSS。
- [ ] flag 默认关闭，先启用测试账号 allowlist；观察错误/恢复/引用失败/成本分布，达到完成定义再扩大。
- [ ] 演练关闭入口、暂停在途任务、保留证据读能力的回滚，不通过删除表“回滚”。

验收报告只填实际结果；跳过的模型/网络场景写 SKIPPED，不计通过。

## T10：AR-012 对照候选（首版之后）

**参考**：references/AutoResearch-ar012/src/autoresearch/{review_workflow_service,agent_review,storm_review}.py 与 packages/scientific-review-service/。

**实施记录（2026-09-12，分支 feat/ar012-candidate）**：以**运行后评估端点 +
持久作业**形态接入（用户决议），接口合同见 `specs/research-review/ar012-sidecar.md`。
上游 Proprietary fork 部署于工作区 `review-sidecar/`（不入仓库/镜像）；
Go 适配层 `backend/internal/arreview`；持久作业表 `writing_ar_review_jobs`
（迁移 111）；端点 `POST/GET /runs/{runId}/research/ar012-candidate[/cancel|/artifacts/{kind}]`。
同日完成真实模型端到端与镜像构建实测（验收记录 §H/I）：`deepseek-chat` 下
POST→202→completed→5 类 artifact hash 复核导出→幂等重放全链通过，
usage 实测入账；过程中修复 coverage 计数后缀、裸 Rxx 数字误判、小语料长度门、
coverage_repair 词表缺失、Dockerfile 依赖共 5 项集成缺陷，
`GeneratorVersion` 旋转至 `lumin.4`。盲评批次 `TestAr012BlindEvalBatch` 以
**真实 OpenAlex 文献语料**产出 12/12 材料并已完成人工盲评与揭盲计分
（2026-09-13，`output/ar012-blind-eval-20260912/计分对照报告.md`）：管线工程
全部通过；候选生成器**引用支持 3.33 / 校准 3.50 / 无支持主张 7.2 处/篇**，
按 §8 判定**暂不晋升**为用户可选生成器，需校准改造后按同协议重评。

- [x] 参数化题目/章节/阈值：fork 新增 `review_spec.py`（REVIEW_SPEC.json，
      提纲标题即规定章节、coverage 词表随包、阈值按语料等比放低）+ Go 侧
      `BuildSpecMapping`；跨学科反例见 fork `tests/test_gates_with_spec.py`
      （教育经济学 fixture 过双门禁）。（typed claims/locator 升级未做——
      上游 corpus 仅摘要级，属于 compare_only/深校验后续项。）
- [x] 输入内容 hash 进幂等键（owner+contract+pack+outline+generator+mode，
      design.md §8）；内容寻址 surrogate 目录隔离；持久作业
      （writing_ar_review_jobs 租约栅栏）+ 诚实取消 + ListRuns 对账
      （outcome_unknown 绝不盲重发）。
- [x] 同一冻结包与批准提纲：主稿（full_draft）与候选（manuscript）的机械
      对比指标落 `metrics`（review-metrics/1）；**12 案例人工盲评未执行**
      （本轮只产出盲评材料与成本记录，属人工步骤）。
- [x] 候选只能写隔离 Artifact：导入仅写内容寻址 blob + 作业行引用，
      不写 writing_artifacts/attempt ledger/文档版本；A18 断言见
      `writingstore/ar_review_jobs_test.go TestArReviewJobA18Isolation`。
      「提升才开正式生成选项」另立任务，未启动。

## 验证命令与验收矩阵

下列新增测试名是本计划要求实现的目标，**目前不存在的测试不得用 `-run` 空匹配成功充当通过**。先 `go test -list` / test runner 确认列出，再运行。命令在各仓库相应目录执行；数据库集成测试需按仓库测试说明提供临时测试库，不能指向生产。

```bash
# backend 目录；T00–T07 定向验证
 go test ./internal/writingkernel ./internal/writingplan ./internal/writingruntime ./internal/writingstore ./internal/server
# 新增 scholar 包存在后
 go test ./internal/scholar
# T09 后端回归
 go test ./...
 go test -race ./internal/writingruntime ./internal/writingstore ./internal/scholar
# frontend 目录，使用该仓库锁文件安装完成后
 npm test
 npm run build
# services/scholar-worker 目录，已创建并按锁文件安装的 Python 3.12 环境
 python -m pytest -W error -q
```

预期：相关测试实际执行并 PASS、构建退出0、无竞态；外部依赖缺失造成 skip 必须列入报告。真实模型测试使用测试环境注入凭据，禁止写入代码、fixture、命令历史或报告；不把现有含 key 的旧请求复制进新文档。

| 用例 | 场景 | 必须观察到的结果 | 主要任务 |
|---|---|---|---|
| A01 | 原fast/sourced/strict合同 | hash/模板不变，正常执行 | T01,T06 |
| A02 | 禁外部研究 + 用户文件 | 只读取授权素材，外部调用0 | T04 |
| A03 | 同DOI多写法/同标题不同DOI | 确定重复合并，歧义保留 | T04 |
| A04 | 一源失败/全源失败/真无结果 | 三种状态不同，可解释 | T04 |
| A05 | HTML重定向/内网IP/超大PDF | 下载失败且未读入内网内容 | T03,T04 |
| A06 | 摘要/扫描件/部分块全文 | scope正确，无伪造页码/覆盖 | T05 |
| A07 | quote/offset/hash被改 | 拒绝证据入包 | T01,T05 |
| A08 | reader中途重启/旧worker晚到 | 缓存安全复用，晚到不覆盖 | T02,T05 |
| A09 | 普通resume绕过gate | 409，未调用正文模型 | T02,T06 |
| A10 | 两人并发/跨owner/重复确认 | 最多一个决议；越权404 | T02 |
| A11 | 修改提纲后确认旧ref | 409；新ref确认后才推进 | T02,T08 |
| A12 | 无效引用/错包/摘要夸大 | 机械错误阻断；语义问题可复核 | T07 |
| A13 | 429/超时未知/取消 | 有界重试，未知不自动重复收费，取消不提交正文 | T02,T03 |
| A14 | 确认事务后worker宕机 | 重启找到待恢复运行，仅推进一次 | T00,T02 |
| A15 | SSE断开/旧GET晚到 | UI最终与持久run一致 | T00,T08 |
| A16 | 功能关闭/worker离线 | 研究明确不可用，普通路径可用 | T09 |
| A17 | 完整真实HTTP两次确认 | 正式版本、引用、用量完整可追踪 | T09 |
| A18 | AR候选试图写正式版本 | 权限/路由拒绝 | T10 |

## 开工第一天建议

1. 用 git status 与现有测试报告锁定基线；复现 T00，不改部署参数。
2. 建立 T01 最小合同/证据 fixture 与历史 hash 保护测试。
3. 将 T02 gate 的六种行为写成数据库/API失败测试，以 mock Reader/Writer 验证协议。

到这一步即可评审具体接口和运行闭环；无须先把 AutoResearch 三条分支强行合并。
