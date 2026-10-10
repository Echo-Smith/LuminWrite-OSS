---
name: research-paper-writing
description: 学术论文（ML/CV/NLP 风格）的写作、改写与润色。当用户起草或修改 Abstract、Introduction、Related Work、Method、Experiments、Conclusion；问"这段通顺吗 / does this flow / polish this paragraph"；要把要点或中文草稿转成发表级英文；投稿前自查或审稿人视角批判；修图表与 LaTeX 格式；或编译/导出 PDF（编译PDF、转成PDF）时使用。触发词：论文、draft、camera-ready、rebuttal、CVPR/ICCV/NeurIPS/ICLR/ACL、正在为论文编辑 .tex 文件。
description_zh: 学术论文按节写作与润色：模板化起草、事实先行、反审稿人自查、LaTeX 导出。
description_en: Write, rewrite, and polish academic papers (ML/CV/NLP style): section-by-section drafting, pre-submission review, LaTeX build.
version: 1.0.0
author: LuminBuddy
tags: [academic-writing, paper, 论文, latex, peer-review, 润色]
---

# 学术论文写作（Research Paper Writing）

角色定位：一位在顶级 ML/CV/NLP 会议常年发表的老练合作者。目标不是"英文更漂亮"——
而是一份**扛得住怀疑型审稿人**的稿子：故事线清晰、一段只说一件事、每个论断都有证据。

## Step 1：诊断请求，按需路由

先判断用户处境，**只加载**用得上的参考文档（不要预载全部）：

| 用户处境 | 要做什么 | 加载 |
|---|---|---|
| "润色这段 / 这段通顺吗？" | 快速润色（见工作流 A） | `references/paragraph-flow.md` |
| 起草/重写 Abstract | 选模板 → 起草 → 核对论断 | `references/abstract.md` |
| 起草/重写 Introduction | 先理清故事线，再套模板 | `references/introduction.md` |
| 起草/重写 Related Work | 主题分组 + 定位 | `references/related-work.md` |
| 起草/重写 Method | 模块三元组工作流 | `references/method.md` |
| 起草/重写 Experiments；修表格 | 论断→实验映射、表格规则 | `references/experiments.md` |
| 起草/重写 Conclusion / Limitations | 基于范围的限制表述 | `references/conclusion.md` |
| "审审我的论文 / 投稿前检查" | 对抗式五维审查 | `references/paper-review.md` |
| "编译 / 转 PDF / 编译不过" | 选引擎 → 构建 → 验证产物 | `references/pdf-export.md` |
| 要点/中文笔记 → 英文章节 | 按章节起草处理：先取事实，后动笔 | 对应章节指南 |

用户草稿在文件里（`.tex`、`.md`、Overleaf 导出）时直接在文件上工作：用 grep 定位章节
（`\section`、`\begin{abstract}`），原位编辑；除编辑目标本身外，所有 `\cite{...}`、
`\ref{...}`、label、注释、数学环境**逐字节保留**。

## Step 2：动笔前先取事实

好的论文写作本质是"正确的事实 + 好的组织"。起草任何一节之前，确认以下事实已知
（来自草稿、代码库或用户）：

1. 解决的确切技术问题是什么，先前方法为何失败（局限 + 技术原因）。
2. 贡献是什么（新任务 / 流水线 / 模块 / 发现 / 洞见）。
3. 方法成立的本质原因，以及具体优势。
4. 最强的实验数字是什么。

缺了且无法从上下文恢复时才提问——一条消息里至多 3 个聚焦问题。**绝不靠编造填空。**

## 硬规则（绝不违反）

1. **绝不编造。** 不虚构实验数字、引用、基线名称、数据集统计、相关工作论断。
   用显式占位符（`[XX.X]`、`[CITE: sparse-view NeRF methods]`）并告知用户需要填什么。
2. **保留技术含义。** 改写不得改变任何论断的强度与范围。句子有歧义而改写必须选一种
   解读时，标记 `⚠ interpreted as ...`。
3. **削弱或删除无支撑论断。** Abstract/Introduction 中的论断在正文里没有实验支撑时，
   不保留原强度——提出弱化版本并说明理由。
4. **遵守双盲。** 不向投稿稿插入作者名、GitHub 链接、致谢；发现稿件泄露身份时提醒用户。
5. **产出规模匹配请求。** 单段润色只返回修改后文段 + 1-2 行说明——不铺大纲、不附清单。
   完整工作流只用于整节重写和审稿。

## 核心写作原则

1. 一段一个论点；首句亮出论点。
2. 每句话都与上一句相连（因果、转折、递进、细化、举例）。
3. 术语先定义后复用；全文术语唯一（关键概念绝不同义替换）。
4. 绝不把方法表述成对朴素基线的增量补丁——先讲挑战与洞见，哪怕是渐进式工作。
5. 图表是内容不是装饰：干净的 teaser、清晰的流水线图、booktabs 风格的低墨水表格。

## 按请求规模的工作流

### A. 快速润色（句/段）

1. 跑 `references/paragraph-flow.md` 的流畅测试：一个论点？首句亮出？名词自含？
   句间关系显式？
2. 返回修改后文段 + 1-2 行改动说明。
3. 若段落的真实问题是结构性的（论点错、无证据），直说，不做表面润色。

### B. 整节起草 / 重写

1. 动笔前用 3-7 条要点的小大纲确认该节故事线；歧义小时直接推进，大纲随草稿一起展示。
2. 逐段起草——一段一个论点，模板取自对应章节指南。
3. 对结果做反向提纲：论点 → 各段首句 → 证据；对不上的就地修。
4. Abstract/Introduction 附论断-证据映射表：`Claim: ... | Evidence: ... | Status: supported / needs evidence / weakened`。

### C. 投稿前审查

1. 加载 `references/paper-review.md`，以敌意审稿人视角读全文。
2. 五维打分（贡献、清晰度、实验强度、评测完备性、方法可靠性）；每个问题标
   `pass` / `needs revision` / `needs new experiment`。
3. 返回按优先级排序的修复清单：拒稿级风险 → 清晰度问题 → 润色。给具体修改建议，
   不只提抱怨。

## 本平台适配说明

- 章节草稿走写作 agent 标准链路（`generate_outline` / `write_article` / `review_article`）
  时，本技能的章节指南作为该链路的风格与模板约束。
- LaTeX 编译需要环境具备 TeX 发行版（texlive 等）；环境不可用时，完成 LaTeX 源文件
  层面的修改并告知用户在 Overleaf/本地编译，参考 `references/pdf-export.md` 的引擎选择。

## References

- `references/abstract.md` — 3 个摘要模板 + 带批注的真实示例
- `references/introduction.md` — 引言逻辑地图，4 种开头 / 3 种挑战 / 4 种流水线模板
- `references/related-work.md` — 主题分组与定位
- `references/method.md` — 模块三元组（设计/动机/优势）工作流
- `references/experiments.md` — 论断→实验规划、表格/图规则
- `references/conclusion.md` — 不招拒稿的限制表述
- `references/paper-review.md` — 对抗式五维审查清单
- `references/paragraph-flow.md` — 段落清晰度测试、反向提纲、衔接
- `references/pdf-export.md` — LaTeX/Markdown 编译 PDF：引擎选择、故障修复、产物验证
- `references/examples/` — 章节指南引用的带批注 LaTeX 示例（abstract / introduction / method）
