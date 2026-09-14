# AR-012 候选评估 · 门槛与规则清单（权威参考）

状态：2026-09-13 整理。覆盖对象：从"一个已完成的研究 run"到"一张候选综述稿
被导入"全链路上所有**硬门槛**（代码强制、二元判定）、**软信号**（只报告、
不拦截）、**部署旋钮**（可配置）以及它们之间的**冲突与交互**。任何 AI 或人
在判断"一次候选生成为什么失败/成功/该不该信"时，以本文为准。

关联：`ar012-sidecar.md`（接口合同）、`design.md` §8（对照协议）、
`docs/releases/2026-09-12-ar012-candidate-acceptance.md`（两轮盲评）、
`../../output/ar012-blind-eval-20260912/计分对照报告*.md`。

## 1. 全链路阶段图

```
已完成研究 run（evidence gate + outline gate 双确认，主稿交付）
  → POST ar012-candidate：准入门槛（§2 硬门槛 H1–H8）
  → 持久作业（幂等 H9，租约/取消 H10）
  → worker：语料转换门槛（H11）+ 预算门槛（H12，可配）
  → sidecar 五阶段生成：计划→起草→修订→grounding 门（H13）→成稿门（H14）
       ↳ 修补通道：数字类（R1）、覆盖类（R2）
  → 导入：五类产物重算 hash（H15）→ A18 隔离（H16）
  → 组织门槛：盲评 + 成本（§4，决定是否晋升，非单次生成的门）
```

## 2. 硬门槛（代码强制，违反即失败）

| # | 门槛 | 位置 | 违反结果 | 来源/依据 |
|---|---|---|---|---|
| H1 | 特性开关 + sidecar 地址已配 | server 装配 | 503 `AR_REVIEW_UNAVAILABLE` | 默认关闭纪律 |
| H2 | run 存在且 caller 是 owner | handler | 404（owner-first） | 越权防护 |
| H3 | run 状态 = completed | `loadFrozenInputs` | 409 `AR_REVIEW_RUN_NOT_COMPLETE` | 只评完成稿 |
| H4 | 三类冻结 artifact 齐备且过 `Validate()`（pack/outline/全文） | 同上 | 422 `AR_REVIEW_INPUTS_MISSING` | 合同 §2 |
| H5 | 提纲 3–24 个唯一标题、≤200 字符 | `BuildSpecMapping` | 422 协议错 | spec 门 |
| H6 | 语料结构下限：≥5 可用来源；每来源有 DOI、验证引用 ≥120 字符；DOI 去重 | `BuildCorpus` | 422 `AR_REVIEW_INSUFFICIENT_CORPUS` | 上游按 DOI 去重，空串假碰撞 |
| H7 | **部署预算门槛**：可用来源 ≥ `AR_REVIEW_MIN_SOURCES`（默认 5）；语料总摘要字符 ≥ `AR_REVIEW_MIN_ABSTRACT_RUNES`（默认 6000；0=关闭） | `CheckCorpusBudget` | 422 `AR_REVIEW_INSUFFICIENT_CORPUS`（附具体欠额） | 24 案例盲评：~3600 字符薄语料是无支持论断峰值区 |
| H8 | 交换卷内容寻址一致（同目录字节不同=篡改/冲突） | `PrepareProject` | 409 `AR_REVIEW_EXCHANGE_CONFLICT` | fail-closed |
| H9 | 幂等：`(owner, input_hash)` 唯一；同输入重放原作业 | `CreateArReviewJob` | 200 重放（绝不二次驱动） | design §8 |
| H10 | 租约栅栏 + 终态不可变 + cancel 恒胜（SQL CASE） | `writingstore` | 竞态拒绝 | 每篇一次语义 |
| H11 | sidecar grounding 门：引用 ID 全解析、数字全部出自语料（年份/标题/摘要 token+篇数）、汉字数 ≥ `min_cjk`、无占位符、无过程性语言 | fork `agent_review` | job failed（诚实落账） | 防编造 |
| H12 | 成稿门：被引来源数、正文引用次数、**coverage 词表逐组命中**、边界标注在场、无密钥样文本 | fork `storm_review` | job failed | 上游门 + spec 参数化 |
| H13 | 长度下限：`min_cjk = clamp(语料摘要字符÷2, 1800, 4500)`；draft 目标 7000/11000 同比缩放 | Go spec 生成 + fork 提示词 | 门禁拦截（不足即 fail） | 盲评：薄语料强制注水=编造峰值区 |
| H14 | 修补通道只认两类**单一失败**：仅数字越界→数字修补；仅覆盖缺失→覆盖修补 | fork `agent_review` | 修补后再验，仍败即 fail | 上游设计 |
| H15 | 导入重算 sha256：五类产物逐一 hash 入内容寻址 blob | `importCompleted` | 失败落账 | 可信引用 |
| H16 | **A18 隔离**：候选只写 blob + 作业行引用，绝不写 run artifact/attempt/文档版本 | `importCompleted` + 断言测试 | 结构上不可能进入交付 | 隔离不变量 |
| H17 | 双人门（evidence/outline gate）先于候选：候选只消费已批准的冻结输入 | 研究链 | —— | 人在环 |
| H18 | License 纪律：fork 不入仓库/镜像；镜像只本地构建 | 仓库布局 + CI 习惯 | —— | R13 |

## 3. 软信号（只报告、不拦截——判断时"看但不自动决定"）

| # | 信号 | 位置 | 用途 |
|---|---|---|---|
| S1 | `corpus_warnings`：每篇被剔除论文的原因（无 DOI/引用过短/重复 DOI/unread） | 作业行 | 评判语料质量与代表性 |
| S2 | `citation_density_per_1k`（每千字引用次数） | fork quality report | 密度异常低≈未归属的综合写作 |
| S3 | metrics（review-metrics/1）：汉字数、标题数、引用出现/唯一数、可解析率、来源覆盖率、双稿对照 | 作业产物 | 机械可复核对照，**不含质量结论** |
| S4 | usage 诚实性：receipt 无用量则保持为空，绝不编造零成本 | 作业行 | 成本审计 |
| S5 | `outcome_unknown` / 对账：超时/断连不盲重发，靠 sidecar 账本对账 | worker | 诚实不確定态 |
| S6 | 盲评四维（论点组织/引用支持/校准/可读性）+ 无支持主张条数 | 人工 | 唯一的质量判定面 |
| S7 | **异源 claim 复核报告**（claim-check/1）：独立供应商模型逐句判定候选稿引用句是否被所引摘要支撑（supported/partial/unsupported） | 宿主侧 `arreview/claimverify`（report-only，第六类作业产物） | 第二轮盲评 151 处越界的机械化自查；同源审计（lumin.6）之外的异源验证；不拦截 |

## 4. 组织门槛（决定"晋升"，不是单次生成的门）

- D-010 再议条件：盲评通过 + 成本可接受 → 才立"用户可选生成器"任务。
- 两轮实测：第一轮（lumin.4）无支持主张 79–86 处；校准轮（lumin.5，原子
  口径更严）151 处 → **均未通过**，候选维持 compare-only。
- 晋升前还差：语义级校验（claim 自检或 T07 复核接入）+ 主稿 baseline 的
  公平对照（需首版研究链部署）。

## 5. 部署旋钮（env，均可热改配置文件后重启）

| 旋钮 | 默认 | 说明 |
|---|---|---|
| `AR012_CANDIDATE_ENABLED` | false | 总开关 |
| `AR_REVIEW_SIDECAR_URL` | 空 | 未配即 503 |
| `AR_REVIEW_SIDECAR_TIMEOUT_MS` | 1500000 | 单次 sidecar 全程上限 |
| `AR_REVIEW_EXCHANGE_DIR` | /data/review-exchange | 交换卷 |
| `AR_REVIEW_MIN_SOURCES` | 5 | 候选来源数下限（<5 无效） |
| `AR_REVIEW_MIN_ABSTRACT_RUNES` | 6000 | 语料信息预算下限（0=关闭） |
| `AR_REVIEW_LLM_PROVIDER/API_KEY/BASE_URL/MODEL/TIMEOUT_SECONDS` | offline/空/…/180 | sidecar 模型（timeout 建议 ≥900） |
| `AR_REVIEW_VERIFY_BASE_URL/API_KEY/MODEL/TIMEOUT_MS` | 空（关闭） | 宿主侧异源 claim 复核模型；三项齐备即启用，**必须与生成模型不同供应商** |

## 6. 冲突与交互（判断时最容易踩的）

| # | 冲突双方 | 机制 | 现状/解法 |
|---|---|---|---|
| C1 | 长度下限 ↔ 语料信息量 | 语料薄于下限所需要素时，模型被迫注水→编造（实测 15 处/篇峰值） | **已解**：H13 随语料等比 + H7 预算门槛拒薄 |
| C2 | 校准纪律（只复述可验证内容）↔ 长度下限（写满） | "多写"与"只写有据的"天然对立，floor>语料承载即复发 | **缓解未根治**：等比缩小降低压力，但任何 floor>0 都保留张力；根治需语义级逐句核验（后续任务） |
| C3 | 数字门（禁止计算/换算）↔ 分析性表达 | 综述想说"相当于/合计"即被拒 | 有意为之（防编造）；代价是分析深度；数字类有修补通道 |
| C4 | coverage 词表逐字命中 ↔ 语料实际措辞 | 词表来自 claim token，语料不含该词时必须修补插入 | 修补通道可插入"证据空白"表述；主题契合的词表天然缓解 |
| C5 | 预算门槛 ↔ 评估工具 | 盲评需测薄语料格，生产默认拒薄 | 已解：评估工具显式 `MinAbstractRunes: 0` |
| C6 | `MIN_SOURCES` 可配 ↔ 结构下限 5 | 配置低于 5 不生效 | 已文档化（防误配） |
| C7 | 预算门槛 ↔ 主题契合度 | 门槛挡"薄"，不挡"跑题"（治理组高预算仍 16–20 处） | **边界**：跑题属检索相关性排序（T04），需另治 |
| C8 | 超时嵌套：LLM 单次 900s × 5 次 ↔ 宿主 25 分钟 | 最坏情况超宿主上限 → `outcome_unknown` | 靠对账兜底（不丢失、不重发）；实测全程 45–90s，未触发 |
| C9 | generator_version 轮换 ↔ 幂等重放 | 换版本即换键：旧缓存绝不跨代复用 | 有意设计；代价是升级后同输入也要重跑 |
| C10 | 取消语义 ↔ sidecar 不可取消 | running 取消=丢弃导入，sidecar 继续跑完（成本已花） | 诚实设计；不可中断是其上游限制 |
| C11 | A18 隔离 ↔ admin 控制台便利性 | owner 路径与 RBAC 路径读同一 blob，写入路径不存在 | 双读单写，结构保证 |
| C12 | **机械门禁 ↔ 语义越界** | 门禁查数字/编号/覆盖/长度，查不出"这句话真是这篇论文的意思吗" | **最大已知盲区**：两轮盲评的主要发现都在此；出路=claim 级自检/T07 语义复核 |
| C13 | 长度下限 ↔ 模型输出上限（deepseek ~8K tokens/次） | floor 过高时 JSON 截断→解析失败 | 等比缩放后 floor≤4500 已安全；若再调高需同步评估 |
| C14 | 评估环境门槛关闭 ↔ 生产默认开启 | 盲评要测薄语料格 | 已解：评估工具显式 `MinSources:0/MinAbstractRunes:0` |
| C15 | 修补通道只认单一失败 ↔ 多失败并发 | 数字+字数双失败时修补不触发，直接 fail | 上游设计；第一轮 case-05 即此情形（重试换样本解决） |

## 7. 门槛数值的出处（可追溯）

| 数值 | 出处 |
|---|---|
| 5 来源 | 上游按 DOI 去重的安全下限（防空串假碰撞） |
| 120 字符/引用 | 上游摘要可用性下限 |
| 4500 CJK | 上游材料科学 preset（富语料时保留） |
| 1800 CJK 下限 | 盲评：低于此更无可复述内容，纯编造 |
| 6000 rune 预算 | 24 案例：~3600 是编造峰值区，≥6000 为实测安全带 |
| 7000/11000 draft 目标 | 预算充足时的成文区间（随 min_cjk 等比） |
| 900s LLM 超时 | 实测 draft 单轮 300s+，300 被掐断 |
| 3 次抽样重试 | 门禁失败多为模型抽样方差，换样本（salt）有效 |

## 8. 已知的"门禁查不出"清单（判定时必须靠人/语义级手段）

1. 论断与所引摘要的**蕴含关系**（说了 A 推出 B）。
2. **跨来源归因错位**（把甲的发现安到乙头上）。
3. 研究设计细节的**改写**（随机化层级、样本构成）。
4. **因果/中介升格**（相关→因果、路径→机制）。
5. **跨域迁移**（A 领域结论套 B 领域场景）。
6. 效应量的**重要性解读**（"显著提升"≠"重要"）。

——两轮盲评的 86/151 处无支持主张全部落在这一清单内。这就是"候选维持
compare-only"的根本原因，也是 T07 语义复核与 claim 级自检的靶子。

**当前防线状态（2026-09-13）**：同源自检已在生成管线内（fork claim_audit
阶段，lumin.6 起）；异源复核已落地为宿主侧 report-only 组件
（`arreview/claimverify`，S7）——异源验证与人工盲评的一致率积累足够数据
后，再决定是否升格为硬门。
