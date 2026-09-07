# LuminBuddy 研究综述路径（research-review）验收记录 — 2026-09-08

任务：T09 后端部分（docs/plans/2026-09-07-research-review-integration.md §T09，161–171 行；验收矩阵 A01–A18，203–222 行）。分支 `feat/research-review`，基线 24d5a7b。本报告只填实际结果；跳过的模型/网络场景写 SKIPPED，不计通过。

## 1. 验收矩阵 A01–A18 对照表

结果取值：PASS（自动化测试通过）/ SKIPPED（本环境无法执行，不计通过）/ 待人工（T09 计划中明确的人工项，不在本任务）。commit 列为覆盖测试所在（或断言所在）的提交。

| 用例 | 场景 | 覆盖测试 | commit | 结果 |
|---|---|---|---|---|
| A01 | 原 fast/sourced/strict 合同：hash/模板不变、正常执行 | `TestContractFixtureRoundTrip` + `TestContractGoldenv1Hashes`（kernel golden）；`TestGovernedP0HTTPTemplates`；`TestLegacyTemplatesUnaffectedByResearchCatalog`；`TestT09LegacyRunResearchViewKeepsShape`（flag 关闭下 legacy run 正常完成） | 1fe514d / 24d5a7b / 9a4aaa8 / da544cd | PASS |
| A02 | 禁外部研究 + 用户文件：只读授权素材，外部调用 0 | `TestResearchReviewCompileRejectsContractForbiddingExternalResearch`（compile 拒绝）；`TestResearchExecutorsNeverCallWorkerWhenExternalResearchForbidden`（executor 计数断言：discover/rank/fetch/parse/read 全 0） | b38be0a | PASS |
| A03 | 同 DOI 多写法/同标题不同 DOI：确定合并、歧义保留 | `TestMergePapers`（R03 规则：DOI 相等合并；conflicting-DOI same-title 保留 + possible_duplicate）及 discovery 包测试 | 43e9972 | PASS |
| A04 | 一源失败/全源失败/真无结果：三态可解释 | `TestResearchDiscoverFailsClosedWhenAllQueriesFail`（全源失败=RESEARCH_UNAVAILABLE）；provider 隔离测试（单源失败不吞没）；空结果与 failed 状态区分 | 43e9972 / 66ef42c | PASS |
| A05 | HTML 重定向/内网 IP/超大 PDF：下载失败且未读入内网内容 | downloader SSRF 负例（per-hop DNS/IP screening、重定向环、size cap、内网目标拒绝） | 43e9972 | PASS |
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

## 4. 预算守卫（T06 遗留 #2）实现说明

- 位置：`backend/internal/server/research_budget.go`（WallClockResearchBudgetBoundary），接入点 `governed_runners.go governedResearchSpecs`（生产组合注入 research read executor）。
- **时长来源的选择**：主动执行时长 = 该 run 各节点 attempt 的 `actual_duration_ms` 之和（`writing_node_attempts` 终态行），每次 paper 间检查实时读账。人工 gate 等待不记 attempt 时长，天然免费。这一来源是持久的：跨进程重启后 resume 天然计入已花时间。
- **上限的选择**：run 的持久化 plan budget `MaxDurationMS`（CreateRun 时校验入库；上调预算 = 新合同版本 + 新运行，不做运行中改预算）。计划合同无显式预算时默认 30 分钟（`ResearchBudgetDefaultMS = 30*60*1000`，design.md §7 “整个主动执行预算 30 分钟”）。这与 T06 E2E harness 的 `researchRunBudget()`（2 小时）一致：模板节点上限总和已超 50 分钟默认天花板，plan budget 是实际生效的墙钟上限。
- 语义：触界 → `RESEARCH_BUDGET_BOUNDARY` 干净暂停（checkpoint 无 UnsafeInFlight、可 resume/decide/cancel）；fire-once 持久化 —— 暂停本身落 `node.paused(RESEARCH_BUDGET_BOUNDARY)` 事件作为标记，owner 的 resume 即“继续剩余篇”的决定，不会中途重新施加同一上限。账读取失败 fail-open（不因账错误失败节点）。
- 测试：触界暂停→恢复续跑不重读（E2E，注入强制时长）；预算内不误触发；Σ actual_duration_ms 口径 + 人工 gate 免费属性 + 新进程凭持久标记不重触发（`TestT09WallClockBudgetBoundaryRespectsHumanGateWaits`）。

## 5. Feature flag 与 compose 变更说明

- `RESEARCH_REVIEW_ENABLED`：此前仅有前端读取点（VITE_ 前缀，T08/T09b）；后端无读取点。本次在 `config.WritingRuntimeConfig` 新增（默认 false），注入 `persistentWritingAPI.researchReviewEnabled`。flag=false 时 CompilePlan / CreateRun 对 research_review 合同显式 503 RESEARCH_UNAVAILABLE（`errResearchReviewDisabled`），绝不静默降级；旧 mode 的 compile/run 完全不经过该检查（回归测试含 legacy run 正常完成）。gate 决议/只读研究端点不关闭（回滚演练保留证据读能力）。
- **错误码选择说明**：选 503 RESEARCH_UNAVAILABLE 而非 400 INVALID_RESEARCH_SPEC——合同/spec 本身有效，拒绝原因是“功能未启用”，属服务可用性语义；且与 worker 离线共用同一错误码，前端“研究不可用”状态一次覆盖两种部署形态。
- compose：`docker-compose.yml` 新增 `scholar-worker` 服务（profile "research"，默认 `up` 不启动；非 root（Dockerfile USER scholar）、仅 internal 网络、无宿主端口、SCHOLAR_WORKER_TOKEN 必填（缺失即拒绝启动，fail-closed）、SCHOLAR_LLM_* 注释留空不写值、healthz 探针不触网）。backend 服务增加 RESEARCH_REVIEW_ENABLED / SCHOLAR_WORKER_URL / SCHOLAR_WORKER_TOKEN 注释示例。两份 compose 文件均通过 YAML 解析校验。

## 6. 验证记录（2026-09-08）

- `go build ./...` 退出 0。
- `TEST_DATABASE_URL go test ./internal/... -count=1`：22 个包全部 ok（含 server 36.7s、writingruntime 18.8s、scholar 7.3s），0 FAIL。
- `-race` 关键包：writingruntime / writingstore / scholar / writingplan 全 ok；server 包 -race 定向 T06/T07/T09 全 ok。
- 改动文件 gofmt 干净（server 包内既有的 import 排序问题为基线遗留，git stash 验证，非本任务引入）；`go vet` 对改动包无告警（仓库既有 capability.go tag 告警为基线遗留）。
- worker：`pytest -W error -q` 163 passed / 2 skipped；live smoke 2 passed（真实网络）。
- 提交列表（本任务）：b38be0a（A02 缺口）、c380225（预算守卫）、da544cd（T09 场景 + flag）、c9c909e（视图增强）。前端 T09b 提交 946acb6 属并行任务，未在本任务内改动。

## 7. 与完成定义（requirements.md §5）的差距清单

完成定义：R01–R12/R14 自动化验收通过 + 真实 HTTP 全流程至少一次完成（含两个 gate、断线重连）+ 三类主题各两次人工检查 + OSS/商业版构建回归通过。

| 差距 | 状态 | 说明 |
|---|---|---|
| 人工检查 ×6（三类主题各两次） | 待人工 | 需真实模型运行后人工核对关键主张与原文（含一个仅摘要场景、一个混合用户材料场景） |
| 真实模型全流程（A17 live） | SKIPPED | 本环境无凭据且禁止注入 key；未执行不计通过 |
| 商业版同步与回归 | 未开始 | 任务范围限定 OSS 仓库；迁移编号核对（107/108）、flag 语义、测试需在商业版独立执行 |
| docker build 实测 | 未执行 | compose 配置已加并做 YAML 校验，但本环境未跑 `docker compose build`（scholar-worker 镜像未实测构建） |
| 真实 HTTP 全流程的“真实”语义 | 部分满足 | offline 全链（fake worker + 确定性 generator）已 PASS；真实模型 + 真实 worker 的端到端待上两项补齐 |
| 测试账号 allowlist 灰度演练 / 回滚演练 | 待人工 | flag 关闭入口与暂停在途任务的演练机制已就位（flag 只挡新入口，不关只读端点），演练本身待人工执行 |
