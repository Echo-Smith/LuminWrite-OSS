---
name: citation-audit
description: 引用校验：对文章/论文的参考文献做两段式审计——先用免费学术 API（Crossref/Semantic Scholar/OpenAlex/arXiv，零密钥）机械核验每条引用的元数据真伪（VERIFIED/MISMATCH/NOT_FOUND/UNCERTAIN），再逐条核对"所引文献是否真的支撑正文论断"（SUPPORTS/WEAK/WRONG/UNKNOWN），按修复矩阵处理。防编造引用、防真实文献被张冠李戴。当用户要求"查引用"、"校验引用"、"引用核查"、"参考文献真实吗"、"citation audit"、"detect fabricated references"、"这条引用对不对"，或成稿将引用文献时使用。
description_zh: 引用审计：机械元数据核验 + 语境支撑审计 + 修复矩阵，防编造引用与张冠李戴。
description_en: Two-stage citation audit — mechanical metadata verification via free scholarly APIs, then context audit of whether each cited paper actually supports the claim it is cited for.
version: 1.0.0
author: LuminBuddy
tags: [citation, audit, 引用, 参考文献, fact-check, scholarly-apis]
---

# 引用校验（Citation Audit）

本技能针对两类失效模式，它们比普通事实错误更致命——因为稿子看起来完美无缺：

1. **编造引用**：参考文献里有一条 API 里根本查不到的文献；
2. **张冠李戴**：引用了一篇真实存在的论文，但它根本没说过正文声称它说的话。

铁律：**终稿里的每一条引用都必须能追溯到 API 返回的记录，绝不允许"凭记忆补元数据"。**

适用于：学术论文投稿前自查、深度稿引用文献的核验、对他人成稿的引用审计（独立可用，
不需要参与写作过程）。

## 平台适配

- 全文获取（语境审计要读论文摘要/正文）用平台的 `read_source`（对应通用环境的
  WebFetch 类工具）；脚本只负责检索与元数据核验，**只输出到 stdout，不落盘**——
  审计产物由平台的文件工具链持久化。
- 脚本为纯 Python 标准库、零密钥：`python3 scripts/verify_citation.py`、
  `python3 scripts/paper_search.py`。

## 审计输入（契约字段）

1. **文献来源**：`refs.bib` 路径；或从成稿（.tex/.md/PDF 导出的文本）中提取的参考文献列表。
2. **成稿来源**：正文文件（语境审计需要逐条抽取 `\cite{key}` 所在句子）。
3. **交付物**：`citation_audit.json`（逐条判定）+ `audit_report.md`（汇总与修复记录）。
4. **修复权限**：哪些修复可以自动做、哪些必须问用户（见修复矩阵）。

## Step 1 — 机械审计（元数据核验）

先对全部文献做纯机械核验，不做任何语义分析：

```bash
python3 scripts/verify_citation.py --bib refs.bib
```

单条核验：`python3 scripts/verify_citation.py --title "..." --author ... --year ...`

核验瀑布：Crossref → Semantic Scholar → OpenAlex → arXiv；标题相似度
（SequenceMatcher）+ 作者姓氏重叠 + 年份容错（±1）联合判定。逐条判定：

| 判定 | 含义 |
|---|---|
| `VERIFIED` | 标题/作者/年份与四个 API 之一的真实记录吻合 |
| `MISMATCH` | 找到了论文但元数据不符（年份、venue、一作不对）——细节在 `issues` |
| `NOT_FOUND` | 四个 API 均无可信匹配——**疑似编造** |
| `UNCERTAIN` | 多个弱匹配，无法裁决 |

每条文献必须有判定，**不允许静默跳过**。审计 JSON 即本次运行的审计日志。
补查检索用 `python3 scripts/paper_search.py "关键词" --sources arxiv,s2,openalex,crossref`。

## Step 2 — 语境审计（真正要命的失效模式）

真实存在的论文被引去支撑它没说过的话，能通过一切机械检查——所以必须逐条核：

1. 从正文抽取含 `\cite{key}`（或 `[n]`/作者-年份引用）的句子，识别它支撑的**具体论断**。
2. 用 `read_source` 读被引论文的摘要页（`https://arxiv.org/abs/<id>`、DOI 落地页，
   或 Step 1 审计记录 `matched` 字段里的 URL）。
3. 判定四选一：
   - `SUPPORTS` —— 摘要明确包含该论断；
   - `WEAK` —— 摘要有所暗示但没有直接这么说；
   - `WRONG` —— 摘要不含此论断；
   - `UNKNOWN` —— 需要读全文才能判定——**标记出来，留待下一步**。
4. **承重引用**（论文的核心贡献所依赖的论断）必须读全文，不能只看摘要——把抓取预算花在这里。

把"句子 + 摘要 + 引用键"交给独立子任务批量判定；**不要**把你预期的判定告诉它——
新鲜视角防确认偏误。环境不支持子任务时串行执行，同样先抽取句子和摘要、再独立判定。
语境判定追加进 `citation_audit.json` 的对应条目。

## Step 3 — 修复矩阵

| 组合判定 | 动作 | 可自动执行？ |
|---|---|---|
| VERIFIED + SUPPORTS | 保留 | 是 |
| VERIFIED + WEAK | 复核（必要时把正文论断措辞弱化） | 问用户 |
| MISMATCH + SUPPORTS | 用 `matched` 记录修正元数据 | 是 |
| MISMATCH + WRONG | 替换 | 问用户 |
| NOT_FOUND | 替换（用 paper_search 找真实替代文献）或删除 | 问用户 |
| 任意 + WRONG | 替换（找真正支撑该论断的论文）或删除 + 弱化论断 | 问用户 |
| UNCERTAIN / UNKNOWN | 保持标记状态，**绝不猜一个判定** | — |

**删除引用后绝不留悬空 `\cite`**：删 `.bib` 条目和删正文 `\cite` 是同一个操作。
若某条论断只靠被删的引用支撑，该论断现在失去了支撑——弱化它或删掉它。

**修正只能来自 API 返回的记录，绝不能来自记忆**：API 说年份是 2023，`.bib` 写 2022，
以 API 为准。

## 审计报告（audit_report.md）

```
总条目：47
VERIFIED + SUPPORTS：39（保留）
MISMATCH（已修正）：5   → 元数据已按 API 记录纠正
NOT_FOUND：2           → 1 条已替换，1 条已删除并弱化论断
WRONG 语境：1          → 已替换（@brown2020 被用于只有 @wei2022 支撑的论断）
UNCERTAIN：0           → 复核后清零
```

收尾汇报包含：审计对象一段话概述、各判定条数与处置、残留风险（带 hedge 进入终稿的
WEAK/UNKNOWN 条目及原因）、未决的 UNCERTAIN 条目。

## 反模式（绝不触碰）

- 往有利方向四舍五入数字。
- 没有统计检验就写 "significantly"。
- 引用一篇摘要里根本没有该论断的论文（Step 2 就是为它设的）。
- 修订过程中把论断越改越强（修订只能收紧，不能膨胀）。
- 凭记忆填参考文献元数据而不是用 API 结果。
- 仅凭标题匹配就给承重引用标 VERIFIED，不读摘要。

## References

- [references/api-cheatsheet.md](references/api-cheatsheet.md) — 免费学术 API 速查（零密钥）
