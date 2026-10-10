---
name: deep-research
description: 多源深度调研，产出带引用编号的研究报告。只用搜索/网页读取工具与免费公开 API，无需任何 API Key。当用户要求"深度调研X"、"deep research"、"帮我全面研究一下"、"多方求证"、"写一份调研报告"、"专题研究"、写作前需要系统性的素材研究时使用。简单查询（一两次搜索即可回答）不适用。
description_zh: 深度调研：拆角度并行取证、交叉核验、单点成稿，产出带引用的研究报告；零 API Key 依赖。
description_en: Deep research on any topic via independent research angles and free public sources (no API keys), producing a coherent cited report.
version: 1.0.0
author: LuminBuddy
tags: [research, deep-research, 调研, 研究报告, 素材, citations]
---

# 深度调研（Deep Research）

核心纪律：**研究并行，写作单点**。取证阶段把问题拆成互相独立的角度分头查证；成稿阶段由单一执行者一次写就——绝不让多个执行者分头写报告正文。

## 工具映射（平台适配）

| 能力 | 本写作平台 | 通用 agent 环境 |
|---|---|---|
| 联网搜索 | `search_web` | WebSearch 类工具 |
| 网页全文读取 | `read_source` | WebFetch 类工具 |
| 免费 API 取数 | curl（bash） | curl（bash） |
| 并行子任务 | 环境支持则按角度派发；否则按角度**串行**执行 | 并行子代理 |

串行模式下纪律不变：每个角度先把结构化发现写入 findings 文件、只带 3-5 行摘要回主上下文，再开始下一个角度；原始网页内容永远不进主上下文。

## Step 0 — 分诊与定档（永远最先做）

1. 先用 `date +%Y-%m-%d` 确认当天日期，绝不凭训练数据猜年份。
2. 分诊：
   - 一两次搜索就能回答？→ 停，直接用搜索工具作答，**不要**进入本流程。
   - 枚举型任务（N 个对象 × M 个字段，如"对比 20 个框架"）？→ 仍用本流程，但按对象分批拆角度（表格导向）。
   - 开放式调查？→ 继续。
3. 定深度档位（默认 **standard**；用户说"快速"/"彻底"时覆盖）：

| 档位 | 首轮角度数 | 追问轮数上限 | 来源目标 |
|---|---|---|---|
| quick | 2-3 | 0 | 8+ |
| standard | 3-5 | 1 | 15+ |
| deep | 5-8 | 2 | 25+ |

以上是硬预算：补漏（Phase 4）可以花，但绝不能超。

## 工作区

所有状态落盘到 `./research/<slug>/`，绝不只存在上下文里（可抵御上下文压缩）：

```
research/<slug>/
├── brief.md         # 调研简报 —— 所有阶段共同遵守的唯一契约
├── findings/        # F1.md, F2.md ... 每个角度一份结构化证据
└── REPORT.md        # 最终交付物
```

中断恢复：重读 `brief.md` + 列出 `findings/`，跳过已完成的角度，继续未完成的。

## Phase 1 — 立契约（Scope）

仅在确实含糊时，向用户提**至多一轮**澄清问题（受众、时间范围、地域、要支撑的决策）。用户说"直接开跑"或意图已清晰时，跳过提问，把假设写进简报。

然后写 `brief.md`：精炼后的问题、范围边界（做/不做）、假设、深度档位、当天日期。后续所有阶段对照的是这份契约，不是原始对话。

## Phase 2 — 拆角度（Plan）

把契约拆成 3-8 个**互相独立**的研究角度。可选的切分视角：核心事实与定义 · 近 12 个月的新进展 · 量化数据与基准 · 反方观点与失败案例 · 一线实践者经验（论坛、issue） · 学术工作 · 关键玩家与替代方案。

角度清单写入 `brief.md` 的 `## Angles` 小节。deep 档或争议性话题，先把角度清单给用户快速确认一次再花预算。

## Phase 3 — 并行取证（Research）

每个角度派一个子任务（支持并行的环境在一条消息里并行派发）。子任务提示词按
[references/research-prompt.md](references/research-prompt.md) 的**锁定模板**逐字复用，只替换 `{变量}`。每个子任务：

- 只研究这一个角度，使用搜索/网页读取工具和 [references/sources.md](references/sources.md) 的免费端点；
- 把结构化发现写入 `findings/F<n>.md`（每条：论断 / 原文引句 / URL / 日期 / 置信度）；
- 只向主流程返回 3-5 行摘要——原始网页内容绝不进入主上下文。

某个角度失败或结果单薄：记下来继续推进其他角度，不要阻塞全局。

## Phase 4 — 补漏（Reflect）

通读全部 `findings/*.md`，对照 `brief.md` 自问：契约的哪些部分没有证据？哪些关键论断只有单一来源？哪些来源互相冲突？

- 有缺口且追问预算未用完 → 用同一模板派差距追问（角度更窄）。每档位按上限最多追问相应轮数。
- 预算用尽或覆盖已足够 → 进入成稿。未解决的缺口如实记录，进报告的"开放问题"。

## Phase 5 — 单点成稿（Write）

由主流程**独自**一次写就 `REPORT.md`，格式遵循 [references/report.md](references/report.md)。核心规则：

- 每条非显而易见的论断带行内引用 `[n]`，映射到文末 Sources；引用 URL 只能来自 findings 文件，绝不能凭记忆补。
- 来源冲突时，两说并列并给日期；新者、一手来源优先。
- 单一来源的论断标注 `[单一来源]`，超出证据的推断标注 `[推测]`。
- 文末收尾：开放问题 · Sources（编号，含访问日期）。

deep 档在定稿前做一轮自我批判：以怀疑型审稿人的视角重读全文（有无未支撑论断？数据是否过时？反方视角是否缺席？），就地修完。

最后向用户交付 5-10 行关键结论摘要与报告路径。

## References

- [references/research-prompt.md](references/research-prompt.md) — 取证子任务的锁定提示词模板
- [references/sources.md](references/sources.md) — 免费公开来源端点清单（零 API Key）
- [references/report.md](references/report.md) — 报告结构与写作规则
