# LuminBuddy 研究综述路径（research-review）验收记录 — 2026-09-08

任务：T09 后端部分（docs/plans/2026-09-07-research-review-integration.md §T09，161–171 行；验收矩阵 A01–A18，203–222 行）。分支 `feat/research-review`，基线 24d5a7b。本报告只填实际结果；跳过的模型/网络场景写 SKIPPED，不计通过。

## 1. 验收矩阵 A01–A18 对照表

结果取值：PASS（自动化测试通过）/ SKIPPED（本环境无法执行，不计通过）/ 待人工（T09 计划中明确的人工项，不在本任务）。commit 列为覆盖测试所在（或断言所在）的提交。

| 用例 | 场景 | 覆盖测试 | commit | 结果 |
|---|---|---|---|---|
| A01 | 原 fast/sourced/strict 合同：hash/模板不变、正常执行 | `TestContractFixtureRoundTrip` + `TestContractGoldenv1Hashes`（kernel golden）；`TestGovernedP0HTTPTemplates`；`TestLegacyTemplatesUnaffectedByResearchCatalog`；`TestT09LegacyRunResearchViewKeepsShape`（flag 关闭下 legacy run 正常完成） | 1fe514d / 24d5a7b / 9a4aaa8 / da544cd | PASS |
| A02 | 禁外部研究 + 用户文件：只读授权素材，外部调用 0 | **F5 后语义**：`TestF5UserMaterialOnlyResearchChainWithoutExternalResearch`（禁联网+用户材料走完两 gate 与写作，discover/rank/fetch_full_text 计数为 0、parse/read 允许）；「禁联网且无材料 → `RESEARCH_MATERIALS_REQUIRED`」（writingplan 拒绝测试）；联网路径用户材料合并零 fetch（`research_read_test.go` 材料用例）。原 compile-rejection 测试已按新语义重写（d973c94） | b38be0a / d973c94 | PASS |
| A03 | 同 DOI 多写法/同标题不同 DOI：确定合并、歧义保留 | `TestMergePapers`（R03 规则：DOI 相等合并；conflicting-DOI same-title 保留 + possible_duplicate）及 discovery 包测试 | 43e9972 | PASS |
| A04 | 一源失败/全源失败/真无结果：三态可解释 | `TestResearchDiscoverFailsClosedWhenAllQueriesFail`（全源失败=RESEARCH_UNAVAILABLE）；provider 隔离测试（单源失败不吞没）；空结果与 failed 状态区分 | 43e9972 / 66ef42c | PASS |
| A05 | HTML 重定向/内网 IP/超大 PDF：下载失败且未读入内网内容 | downloader SSRF 负例（per-hop DNS/IP screening、重定向环、size cap、内网目标拒绝）+ **F3 连接层 IP 钉死**（rebinding 模拟：解析先公网后私网时实际连接不落私网、SNI/Host/证书校验保留原 hostname、环境代理禁用；`test_downloader_pinning.py` 13 用例） | 43e9972 / a602050 | PASS |
| A06 | 摘要/扫描件/部分块全文：scope 正确，无伪造页码/覆盖 | `TestResearchReadAbstractDegradation`（likely-scanned → abstract scope、独立 content hash、PDF hash 不入证据）；`TestT07ScopeOverclaimNeedsReviewButFinalizes`；worker parser CJK/页码测试 | 66ef42c / 24d5a7b | PASS |
| A07 | quote/offset/hash 被改：拒绝证据入包 | `TestBuildEvidencePackNegatives`（tampered offsets/wrong block hash/ghost block/unread-paper 均拒 EVIDENCE_INVALID）；`TestT07ClassC_BlockHashTamper`、`TestT07ClassC_QuoteTamper` | 66ef42c / 24d5a7b | PASS |
| A08 | reader 中途重启/旧 worker 晚到：缓存安全复用，晚到不覆盖 | `TestResearchReadSubTaskCacheReuse`（新 executor 零 worker 调用、pack 字节相同）；`TestResearchReadFencingNegative`（过期租约回收后晚到完成被 fence） | 66ef42c | PASS |
| A09 | 普通 resume 绕过 gate：409，未调用正文模型 | `TestT02PlainResumeNeverBypassesGate`（409 GATE_APPROVAL_REQUIRED） | 6c83581 / 85ea903 | PASS |
| A10 | 两人并发/跨 owner/重复确认：最多一个决议；越权 404 | `TestT02ResearchGateNegativeCases`（并发双确认、stale revision、跨 owner 404、重复确认幂等） | 6c83581 / 85ea903 | PASS |
| A11 | 修改提纲后确认旧 ref：409；新 ref 确认后才推进 | `TestT02ResearchGateNegativeCases`（stale revision 409 STALE_GATE）；`TestResearchOutlineExecutorRejectsApprovalForDifferentPack`；前端 T08（304ec0c）对应 reducer 测试 | 6c83581 / 948e5fa/304ec0c | PASS |
| A12 | 无效引用/错包/摘要夸大：机械错误阻断；语义可复核 | `TestT07ClassA/B/C/D/E` 五类失败；`TestT07QualityGateBlocksOnBlockerIssue`（机械阻断）、`TestT07ScopeOverclaimNeedsReviewButFinalizes`（语义进复核不阻断） | 561cd75 / 24d5a7b | PASS |
| A13 | 429/超时未知/取消：有界重试，未知不自动重复收费，取消不提交正文 | 429/超时/未知：`TestCall429ParsesRetryAfter`、`TestCallContextTimeoutMidFlight`、`TestCall500OutcomeUnknown` + `TestResearchReadOutcomeUnknownParksRow`（未知结果落库待 owner，不自动重付）。取消路径（本次补）：`TestT09ResearchCancelMidReadStopsScheduling`（运行中 cancel → 终态 cancelled、不再调度新读、无 full_draft/revision_set） | 1ea606d/66ef42c + da544cd | PASS |
| A14 | 确认事务后 worker 宕机：重启找到待恢复运行，仅推进一次 | `TestGateDecisionResumesExactlyOnce`（决议恰好推进一次）；`TestGovernedRunRestartRecovery`（重启恢复） | 6c83581 / 25dd4be | PASS |
| A15 | SSE 断开/旧 GET 晚到：UI 最终与持久 run 一致 | 后端（本次补）：`TestT09SSEReconnectMergesBySequence`（断线重连按 sequence 合并、无 rewind、旧 GET 不覆盖新事件、Last-Event-ID 重放）。前端旧 GET/重复/跨 run 事件：T08 reducer 测试（304ec0c，`fresh GET snapshot wins; stale GET never overwrites newer event state` 等） | da544cd / 304ec0c | PASS（后端自动化；浏览器端到端待人工） |
| A16 | 功能关闭/worker 离线：研究明确不可用，普通路径可用 | `TestT09ResearchReviewDisabledRefusesExplicitly`（flag=false → 503 RESEARCH_UNAVAILABLE、legacy run 不受影响）；`TestT06ResearchDraftWithoutGeneratorPausesUnavailable`（worker 未配置 → UnavailableResearchExecutor，dispatch 报 RESEARCH_UNAVAILABLE）；flag 读取点 `config.WritingRuntime.ResearchReviewEnabled`（RESEARCH_REVIEW_ENABLED，默认 false） | da544cd / 85ea903 | PASS |
| A17 | 完整真实 HTTP 两次确认：正式版本、引用、用量完整可追踪 | 离线全链：`TestT06ResearchReviewFullChainThroughHTTP`（两 gate 各确认一次、citation index 绑定 draft hash、终态 completed）。真实模型全流程：SKIPPED（见 §2） | 85ea903 | PASS（offline E2E）；真实模型 SKIPPED |
| A18 | AR 候选试图写正式版本：权限/路由拒绝 | T10 未实施（计划定位：首版之后）。仅静态确认：正式版本提交只经由 DeliveryProtocol/finalize 路径，AR 候选执行器不存在于任何注册表 | — | 待人工/未实施（T10） |

矩阵外本次新增的守卫/视图回归：`TestT09WallClockBudgetBoundaryPausesAndResumes`、`TestT09WallClockBudgetBoundaryStaysQuietUnderBudget`、`TestT09WallClockBudgetBoundaryRespectsHumanGateWaits`（da544cd + c380225）；视图字段断言 `TestT09ResearchViewExposesSpecAndReadingScopeCounts`（c9c909e）。

## 2. 真实 HTTP 全流程结果

- **离线 E2E：PASS。** `TestT06ResearchReviewFullChainThroughHTTP`（真实 HTTP 面 + fake worker + 确定性 generator）：discover → read → evidence gate（确认一次）→ outline → outline gate（确认一次）→ draft → citations → fact → quality → finalize；12 类正式产物齐备，citation index 绑定 draft hash，run_id 全程可追踪。同族：budget-boundary 暂停/恢复、no-generator 诚实暂停、T07 四个交付门场景全部 PASS。
- **真实模型全流程（rank/read/draft 的真实 LLM）：SKIPPED。** 本环境无任何 LLM 凭据，且任务约束禁止注入 key；凭据不在代码、fixture、命令历史或本报告中。启用方式（未来执行时）：以 TASK13_LLM_* 同型的测试环境注入给 `TestGovernedP0HTTPTemplates` 的 live 通道与研究 worker 的 SCHOLAR_LLM_*，在受预算观测下重跑 A17。**未执行即不计通过。**
- **真实 worker 回环（无模型）：PASS。** `scripts/dev-scholar-smoke.sh`：真实 Python worker 进程 + 真实 Go 客户端，healthz → parse（确定性 TXT，hash-verified blocks）→ rank（无 SCHOLAR_LLM_* 时 fail-closed typed error，符合 T04 合同，静默 0 分视为失败）→ discover（真实 OpenAlex fan-out，3 篇返回、provider 状态 ok）。运行记录：2026-09-08，worker 0.1.0，request_id 371623ab-…，usage.measured=false。
- **Worker 侧 live smoke（真实 OpenAlex）：PASS（未跳过）。** `SCHOLAR_LIVE_TESTS=1 .venv/bin/python -m pytest tests/test_live_smoke.py -v` → 2 passed（openalex 可达、响应 shape 符合文档类型）。同窗口全量 worker pytest `-W error -q`：163 通过、2 skip（live 门控的其余项），exit 0。

## 3. 三个主题人工检查项（不在本任务，待人工）

材料科学 / 计算机科学 / 社会科学各两次真实运行、覆盖一个仅摘要场景与一个混合用户材料场景、人工核对关键主张与原文 —— **待人工**（T09 计划 §T09 明确为人工检查；本任务只交付自动化部分与可自动化回环）。

## 4. 预算守卫实现说明（**F2 重写后语义**，f3e88a5；下文替换 2026-09-08 首版的 fire-once 描述）

- 位置：`backend/internal/server/research_budget.go`（WallClockResearchBudgetBoundary），生产组合注入 research read executor；orchestrator 级 `RESEARCH_BUDGET_BOUNDARY` 干净暂停分支保留。
- **时长口径**：spend = max(attempt 账， 子任务账) —— attempt 账 = Σ 各节点 attempt `actual_duration_ms` + **在途 attempt 活跃时间**（`writing_node_attempts.started_at`→now，仅 running 行计入）；子任务账 = `writing_research_tasks.usage_json.duration_ms` 累计（attempt 行丢失时保持诚实）。两账取 max 避免重叠部分双重计数。人工 gate 等待不记 attempt 时长，天然免费（design.md §7）。
- **上限**：run 持久化 plan budget `MaxDurationMS`；无显式预算默认 30 分钟（`ResearchBudgetDefaultMS`）。上调预算 = 新合同版本 + 新运行。
- **纯函数语义（F2 修正）**：守卫每次检查从账本重算 verdict，无 fire-once/缓存——resume 后预算未变即再次触界，不得继续阅读；账读取失败 **fail-closed**（按触界处理，只能"停"不能"放行"）。
- **触界行为（F2 修正，对齐 design.md §3）**：停止调度新子任务后——可用可引用来源 ≥ min_citable_sources → 冻结**部分包**（未完成篇 unread 入包、coverage.gaps 标注预算截断、provenance 记 `budget_boundary:partial_pack:k/n`）→ 正常进入 evidence gate；不足 → INSUFFICIENT_EVIDENCE 干净暂停。
- 测试：真实账本行驱动（非仅 SetForcedSpentMS，该 API 已删除）——超限后 worker 调用计数不再增长；触界+达标 → 部分包直达 evidence gate 且链条 completed；触界+不足 → INSUFFICIENT_EVIDENCE；恢复后 0 次新调用仍暂停；in-flight 计时、子任务 usage 计时、fail-closed 单测（`research_t09_test.go`、`research_read_test.go`）。

## 5. Feature flag 与 compose 变更说明

- `RESEARCH_REVIEW_ENABLED`：此前仅有前端读取点（VITE_ 前缀，T08/T09b）；后端无读取点。本次在 `config.WritingRuntimeConfig` 新增（默认 false），注入 `persistentWritingAPI.researchReviewEnabled`。flag=false 时 CompilePlan / CreateRun 对 research_review 合同显式 503 RESEARCH_UNAVAILABLE（`errResearchReviewDisabled`），绝不静默降级；旧 mode 的 compile/run 完全不经过该检查（回归测试含 legacy run 正常完成）。gate 决议/只读研究端点不关闭（回滚演练保留证据读能力）。
- **错误码选择说明**：选 503 RESEARCH_UNAVAILABLE 而非 400 INVALID_RESEARCH_SPEC——合同/spec 本身有效，拒绝原因是“功能未启用”，属服务可用性语义；且与 worker 离线共用同一错误码，前端“研究不可用”状态一次覆盖两种部署形态。
- compose：`docker-compose.yml` 新增 `scholar-worker` 服务（profile "research"，默认 `up` 不启动；非 root（Dockerfile USER scholar）、仅 internal 网络、无宿主端口、SCHOLAR_WORKER_TOKEN 必填（缺失即拒绝启动，fail-closed）、SCHOLAR_LLM_* 注释留空不写值、healthz 探针不触网）。backend 服务增加 RESEARCH_REVIEW_ENABLED / SCHOLAR_WORKER_URL / SCHOLAR_WORKER_TOKEN 注释示例。两份 compose 文件均通过 YAML 解析校验。

## 6. 验证记录

### 首版（2026-09-08，实现任务 T09a）

- `go build ./...` 退出 0；`TEST_DATABASE_URL go test ./internal/... -count=1`：22 个包全部 ok；`-race` writingruntime/writingstore/scholar/writingplan 全 ok。
- worker pytest 163 passed / 2 skipped；live smoke 2 passed（真实网络）。
- 提交：b38be0a、c380225、da544cd、c9c909e、7b6d841；前端 T09b 946acb6。

### 审查修复后复验（2026-09-08，F1–F5 落地后）

- 审查记录见 docs/reviews/2026-09-08-research-review.md；修复提交：683acdc（F1）、d69b210（前缀分叉，F1 连带发现）、a602050（F3）、2558285（F4）、f3e88a5（F2）、d973c94（F5）。
- `go build ./...` 退出 0；`TEST_DATABASE_URL go test ./internal/... -count=1`：22 个包全部 ok，0 FAIL（含 T02/T06/T07/T09 既有 E2E 回归、F5 用户材料全链 E2E、F2 真实账本预算测试）。
- 前端 `npm test` 83 passed / 0 fail（含 F1 launch 链路与 `/api/v2` 前缀断言）；`tsc -b` 通过。
- worker `pytest -W error -q` 177 passed / 2 skipped（新增 F3 pinning 13 用例、F4 payload ceiling e2e）。
- API 前缀对齐说明：writing 路由生产挂载于 `/api/v2`（server.go），E2E harness 与前端此前自挂 `/api/v2/writing` 属分叉，d69b210 统一为生产前缀；contracts.md §3 已注明。

## 7. 与完成定义（requirements.md §5）的差距清单

完成定义：R01–R12/R14 自动化验收通过 + 真实 HTTP 全流程至少一次完成（含两个 gate、断线重连）+ 三类主题各两次人工检查 + OSS/商业版构建回归通过。

| 差距 | 状态 | 说明 |
|---|---|---|
| 人工检查 ×6（三类主题各两次） | 待人工 | 需真实模型运行后人工核对关键主张与原文（含一个仅摘要场景、一个混合用户材料场景） |
| 真实模型全流程（A17 live） | SKIPPED | 本环境无凭据且禁止注入 key；未执行不计通过 |
| 商业版同步与回归 | 未开始 | 任务范围限定 OSS 仓库；迁移编号核对（107/108）、flag 语义、测试需在商业版独立执行 |
| docker build 实测 | 未执行 | compose 配置已加并做 YAML 校验，但本环境未跑 `docker compose build`（scholar-worker 镜像未实测构建） |
| 真实 HTTP 全流程的“真实”语义 | 部分满足 | offline 全链（fake worker + 确定性 generator）已 PASS；真实模型 + 真实 worker 的端到端待上两项补齐。F1 后前端可创建真实运行；d69b210 修正前缀分叉后浏览器链路不再 404 |
| 材料分支媒体类型 | 已知限制 | F5 用户材料当前按 text/plain 处理（kb 文本）；材料 PDF/扫描件探测（R05）待真实语料校准 |
| 测试账号 allowlist 灰度演练 / 回滚演练 | 待人工 | flag 关闭入口与暂停在途任务的演练机制已就位（flag 只挡新入口，不关只读端点），演练本身待人工执行 |
