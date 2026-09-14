# T10 AR-012 候选评估接入验收记录

日期：2026-09-12。分支：`feat/ar012-candidate`（基于 `feat/research-review`）。
范围：AR-012 独立综述生成服务以**运行后评估端点 + 持久作业**形态接入
（D-010）。接口合同：`specs/research-review/ar012-sidecar.md`。
2026-09-12 追加：真实模型端到端与镜像构建实测两项完成（见 H/I）。

## 交付物

| 项 | 位置 |
|---|---|
| Go 适配层（client/corpus/exchange/idempotency/metrics） | `backend/internal/arreview/` |
| 持久作业表 + 租约/对账/取消（迁移 111） | `backend/internal/writingstore/ar_review_jobs.go`、`migrations/111_ar_review_jobs.*.sql` |
| 服务与 REST 端点 | `backend/internal/server/arreview_api.go`、`handlers_writing_arreview.go`、`writing_routes.go` |
| Admin 控制台（跨用户作业列表/产物查看/发起/取消） | `backend` admin 组 `/api/v2/admin/ar-review/*`（eval.view）+ `frontend/src/components/admin/ar-review-panel.tsx`（侧边栏「AR-012 候选」） |
| 配置（默认关闭） | `AR012_CANDIDATE_ENABLED` / `AR_REVIEW_*`（`config.go`、`.env.docker.example`） |
| 部署 overlay | `docker-compose.ar012.yml`（profile `ar012`，仅 internal 网络） |
| 上游 fork（不入仓库） | 工作区 `review-sidecar/`（ReviewSpec 参数化 + Dockerfile + 18 项测试） |

## 验收结果

自动化验收（本地，`TEST_DATABASE_URL` 指向临时测试库）：

| # | 用例 | 结果 |
|---|---|---|
| A | `internal/arreview` 全包：client 信封/201-but-failed/超时 outcome_unknown/409/404/截断响应；幂等派生确定性+分离性；corpus 转换（剔除/去重/摘要选择/不足 5 拒绝）；spec 缩放；exchange 冲突与路径逃逸；metrics 未解析引用 | PASS |
| B | `writingstore` TestArReviewJob*：创建/重放身份、租约栅栏、终态不可变、cancel 恒胜（SQL CASE）、A18 隔离（导入后 `ListRunArtifacts(run)` 为空） | PASS |
| C | `server` 单元：usageFromReceipt 求和与"不编造零成本"、视图字段稳定 | PASS |
| D | 回归：`writingstore` + `arreview` + `server` 全包 `go test -p 1`（DB-backed） | PASS |
| E | fork `review-sidecar`：pytest 18 项（preset 一致性、spec 校验拒绝、缩放、教育经济学跨学科双门禁、corpus scope 白名单、章节解析） | PASS |
| F | `go build ./...` + `go vet` 三个触碰包 | PASS |
| G | `docker compose -f docker-compose.yml -f docker-compose.ar012.yml --profile ar012 config` | PASS |
| H | sidecar 镜像构建实测：`PYTHON=python3.12 ./build.sh --image` → `lumin-review-sidecar:local`（217MB）。发现并修复 Dockerfile 缺口：`pip install --no-deps .` 不装运行依赖导致容器起不来，改为完整安装 + `python -m uvicorn` 启动（fork 本地） | PASS |
| I | 真实模型端到端全链路：`TestArReviewLiveSidecarEndToEnd`（`scripts/run-ar012-live-acceptance.sh`）。离线 T06 研究链产出真实冻结包+批准提纲的 completed run → 挂载 arReview 服务 → POST 202 → 轮询 completed → 5 类 artifact 全部 hash 复核导出（manuscript 23932B markdown）→ 幂等重放 200 同 job；usage 实测 input 14909/output 11390（deepseek-chat，measured=true，不编造零成本）；A18 复核：作业全程未新增 run artifact。模型凭据仅注入 sidecar 容器环境 | PASS |

人工/待办（不计通过）：

- [x] 真实模型端到端：完成于 I（见上）。模型凭据由用户临时提供，仅写入工作区
      `env.secrets.local` 并注入 sidecar 容器环境，未进入任何仓库文件、命令
      历史参数或本报告；容器销毁即失效。
- [x] sidecar 镜像 docker build 实测：完成于 H（见上）。
- [x] 盲评材料批次已生成（**真实文献语料版**）：`TestAr012BlindEvalBatch`
      （`AR012_BLIND_BATCH=1`，`AR012_BLIND_ONLY` 定向重跑，每案例最多 3 次
      抽样重试）。12/12 完成：三个真实主题（生成式AI与学术写作 / 公共项目
      评估 / 家长参与与学业成就）× 6-8 篇 × 短/长摘要。冻结包由 **OpenAlex
      真实开放论文**（真实标题/作者/年份/期刊/DOI/英文摘要，共 23 篇）经
      同一研究链产出，候选稿的每条论断都可对照真实摘要核验；研究问题按
      主题参数化（v1.1 研究合同经 `fixtureMutate` 密封）。
      包位于工作区 `output/ar012-blind-eval-20260912/`：每案例 `brief.md`
      （共同问题+批准提纲+编号真实语料）+ 匿名 `A.md`/`B.md`（顺序逐案例
      随机，6A/6B 候选），另有 `盲评记录表.md`、`answer-key.jsonl`、
      `cost-sheet.md`（打分前勿开）与 `build_panel.py` 生成的可视化打分
      面板 `盲评面板.html`（引用上标点弹来源卡、localStorage 自动保存、
      一键导出）。合计 20.4 万 input / 15.0 万 output tokens（deepseek-chat）。
      早期合成语料批次（12/12）已归档至
      `output/ar012-blind-eval-20260912-synthetic-archive/`：其语料为离线
      模板句，无法实质评判引用支持，仅作管线冒烟记录保留。
      已知局限（如实声明）：主稿 baseline 为 T06 离线确定性 stub（首版
      研究链未部署），长度不对称会使可读性维度系统性偏向候选；候选稿按
      合同自带“LuminBuddy AR-012 候选生成器”证据边界标注（诚实性不变量，
      不移除），完全双盲不可能——本次盲评核心是引用支持与校准表达。
- [x] 盲评已执行并计分（2026-09-13 揭盲）：四维打分 + 逐条无支持主张判定，
      见 `output/ar012-blind-eval-20260912/计分对照报告.md`。结果：论点组织
      5.00 / 可读性 4.75（对 stub，不计证据）；**引用支持 3.33 / 校准表达
      3.50；无支持主张 86 处 / 12 篇（7.2 处/篇，case-05 高达 15）**。按
      design.md §8“不因文风分更高而上线”：**候选生成器晋升不通过**——
      机械 grounding 之上暴露系统性语义外推，需校准改造（prompt 收紧 +
      逐 claim 语料核对 / T07 语义复核）后按同协议重评。管线工程验收不受
      影响：12/12 闭环、机械门禁有效、A18 隔离、成本实测每篇约 1.7 万
      input / 1.2 万 output tokens（60–90 秒）。
- [x] 校准改造第一轮已执行（lumin.5，2026-09-13）：(1) Go spec 长度门随
      语料信息量等比 `min_cjk=clamp(摘要字符÷2, 1800, 4500)`，draft 目标
      同步缩放——消除“薄语料被迫注水”这一最大编造来源；(2) fork 生成/修订
      提示词加入逐句校准纪律（禁机制推断/跨域迁移/改写研究设计/数字换算/
      单源升格共识）；(3) quality report 新增引文密度指标（仅报告）。
      重跑 12/12：候选均长 7703→5275 CJK（-32%），引文密度 8.9→9.9/千字，
      覆盖率 1.00 保持，tokens -15%。语义复评（无支持主张条数对比）待
      评审人按新面板执行；判定线：无支持主张较 86 处减半且引用支持/校准
      ≥4.0 方可再议晋升。
- [x] 校准轮语义复评已执行（2026-09-13）：评审人以**原子论断口径**（比第一
      轮更严：逐句拆分、去重、明确标注推论者不计）对 lumin.5 稿件重新计数，
      **无支持论断 151 处 / 12 篇（12.6 处/篇）**，越界模式与第一轮同构
      （能力补写、方法学扩写、调查→行为外推、相关→因果、效应量过度解读）。
      见 `output/ar012-blind-eval-20260912/计分对照报告-round2校准轮.md`。
      **判定：校准轮未通过晋升线，候选维持 compare-only 隔离形态**——问题
      为结构性（仅凭少量摘要写连贯综述与逐句可溯源天然冲突），prompt 级
      校准到收益边界；有效出路是语义级手段（sidecar claim 级自检/修补阶段，
      或等 T07 语义复核落地后接入），归入后续任务再议。
- [x] **第二轮校准：claim 审计阶段已落地并重跑（lumin.6，2026-09-13）**。
      fork 新增独立事实核查阶段 `_repair_unsupported_claims` + `_apply_exact_edits`：
      修订稿在编号前由"裁判"提示词逐句找出超出摘要支撑的原子论断（五类越界
      逐一枚举），逐字匹配改写降格（带 checkpoint、应用数入 receipt），
      审计后 grounding 重验。fork 20 项测试通过。
      重跑 12/12（case 07/09 换样本后通过）：**引文密度 8.9→9.9→17.7/千字
      （审计删除无锚定填充的直接证据）**，均长 5117 CJK，覆盖率 1.00，
      tokens 25.6万/17.3万（+47%，审计调用的代价）。包与面板已更新：
      `output/ar012-blind-eval-20260912/`（盲评面板-v3-lumin6.html；
      R2 版归档于 `…-round2-lumin5/`）。**待评审人以同口径（原子论断）做
      第三轮计数**：对比 151 处基线，显著下降且引用支持/校准 ≥4.0 方再议晋升。
- [x] **第四轮盲评（lumin.7 最终轮）已执行：预注册判定线全部通过**（2026-09-13
      揭盲）。编号错位根治后，评审人以原子论断口径计数：**候选稿无支持主张
      0 处 / 12 篇**（判定线 ≤75）；**引用支持 4.42 / 校准表达 4.42（≥4.0）**；
      论点组织 4.58 / 可读性 3.83；总体更可信 12/12（对手 stub）。
      按 D-010"盲评通过且成本可接受"：**满足立"候选晋升为用户可选生成器"
      任务的条件**（每篇成本约 2.1 万 input / 1.4 万 output tokens）。
      迭代轨迹：R1 79–86 → R2 151（原子口径）→ R3 编号错位假阳性 403（已
      根治）→ R4 **0**。如实声明（不随通过撤销）：baseline 仍为 stub（本轮
      证明达标，非"优于真实 Writer"，真实对照需首版研究链）；claim 审计为
      同源自检，建议 T07 语义复核作异源二道防线；候选稿仍有 30 处残差
      （集中在 case-04/08/11/12 各 7–8 处，非全域归零）。
      完整报告：`output/ar012-blind-eval-20260912/计分对照报告-round4-final.md`。
- [x] **宿主侧异源 claim 复核组件已落地（路线 B，2026-09-13）**：
      `internal/arreview/claimverify`（独立 OpenAI 兼容客户端 + 逐句拆分 +
      supported/partial/unsupported 判定 + claim-check/1 报告），作为第六类
      作业产物 `claim_check` 挂到作业行（report-only，不拦截不改稿）；
      配置 `AR_REVIEW_VERIFY_*` 三项齐备即启用，启动日志明示"必须与生成
      模型不同供应商"（异源性由部署保证，gate-inventory §5 已注明）。
      测试：包内 7 项单测（拆分/判定/越界索引跳过/服务错/围栏剥离/分块索引
      映射/20 句分 2 块真实 GLM-5.3 集成）+ 全包 vet。**分块策略**：单次
      调用最多 10 句（`claimChunkSize`），失败块跳过（优雅降级），重试 5 次 +
      30 秒指数退避，超时 180 秒。**真实模型验证**（2026-09-13）：GLM-5.3
      推理模型 20 句分 2 块，100 秒完成，10 supported / 10 partial / 0
      unsupported，chunk 索引正确映射到全局位置（`TestCheckChunksAndMapsGlobalIndices`
      回归覆盖）。**上线待办**：配置一组与 deepseek 不同供应商的验证模型凭据。
- [ ] 后续任务：立"候选晋升为用户可选生成器"任务（前提：首版研究链部署 +
      真实主稿对照 + T07 异源复核上线并积累一致率数据）；商业版回归（另起
      任务，见 D-010）。

## 真实模型运行暴露并修复的集成缺陷（I 的前置）

离线 mock 路径此前无法暴露这些问题；接真实 deepseek-chat 后才逐一现形，
均已修复并各自带回归测试：

1. **coverage 词表带计数后缀**（Go 侧 `arreview.BuildSpecMapping`）：研究链的
   `Coverage.Topics` 形如 `"自动选择 (6)"`，而 fork 的 coverage 门按子串匹配
   正文——带计数后缀的词永远不可能逐字命中，任何冻结包都必然 `required_topic
   _coverage_missing`。修复：`coverageTerm` 剥离 ` (N)` 计数（纯传输规范化，
   不放松门），计数型空词丢弃。回归：`TestBuildSpecMappingScalesAndDerives`。
2. **裸写来源记号被误判为语料外数字**（fork `agent_review._NUMBER`）：正则
   `(?<![A-Za-z])\d+` 本意放过 `R01`，但回溯让 `R01` 的尾数 `1` 逃逸成
   “数字”，触发 `numbers_outside_source_corpus`。修复：lookbehind 加数字
   `(?<![A-Za-z0-9])`，并在 draft/revision 提示词要求来源只以 `[Rxx]` 引用。
   回归：`test_bare_source_marker_does_not_leak_digits`。
3. **小语料必然撞 4500 字长度门**（fork）：`section_floor = 门禁汉字数/小节
   数 + 100` 注入 draft/revision 提示词，把“每节最低长度”变成生成侧硬约束。
4. **coverage_repair 只传组名不传词表**（fork）：修复阶段拿到的是缺失主题组
   名而非需逐字命中的词，最小编辑无从补词。修复：把 `missing_terms` 词表
   一并传给模型。
5. **sidecar LLM 超时**：draft 一次输出 ~1 万 token，300s 被掐断（
   `incomplete chunked read`）；提到 900s 后通过。compose 侧
   `AR_REVIEW_SIDECAR_TIMEOUT_MS=1500000` 仍覆盖整链。

上述第 2/3/4 项在 fork（`../review-sidecar`，不入仓库），`GeneratorVersion`
随之旋转 `lumin.1→lumin.4`，旧候选缓存与新代际天然分离。

## 语义边界（重申）

- 候选稿是**评估材料**：机械指标不含质量结论；grounding 通过≠正确。
- sidecar 不可取消/无队列：`outcome_unknown` 只对账不重发；cancel 语义
  诚实（running 取消=丢弃导入，sidecar 侧继续）。
- 交换卷为单机传输；跨机需受控上传 API（未做）。
