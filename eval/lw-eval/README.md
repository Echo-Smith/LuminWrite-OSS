# lw-eval —— LuminWrite 试验场评测原型

纯 Python 标准库（3.10+），**无需 API Key**。LLM 评审部分为桩 + 提示词模板。

```bash
cd eval/lw-eval                              # 在仓库根目录下
python3 -m unittest discover -s tests        # 35 项单测（或 python3 -m pytest tests，需另装 pytest）
python3 run_demo.py                          # 复现 results/DEMO_RESULTS.md（留出集各行需自备原文）
python3 -m lweval aitone samples/*.md        # 说人话 / AI 腔检测
python3 -m lweval inject samples/nat_report_ev_county.md     # 注入灵敏度
python3 -m lweval style samples/hollow_*.md samples/style_*.md --ref samples/nat_commentary_village.md samples/nat_essay_remote_work.md
python3 -m lweval stylepack samples/style_*.md --voice yinyue --genre report   # 文风遵循 + 文体合规
python3 -m lweval drift results/synthetic_splice_nat_then_ai.md --ref samples/nat_commentary_village.md samples/nat_essay_remote_work.md samples/nat_explainer_electricity.md --window 200 --step 100
python3 -m lweval upcr samples/rev_before.md samples/rev_after_drift.md --anchor 售后是第二个 --must-contain 不能说明普遍 --rows
python3 -m lweval shuffle samples/nat_essay_remote_work.md   # 真人长文需先按 data/heldout/SOURCES.md 自行获取
python3 -m lweval power
python3 -m lweval cost --tasks 120 --arms 7 --judge qwen3.7-plus@list
```

| 目录 | 内容 |
|---|---|
| `lweval/aitone.py` + `lexicon/aitone_zh.json` | 说人话检测 v0.2：9 类词表 + 8 条结构规则，每项带角色（hollow 全额计分 / rhetoric 按段落具体性折减 w/(1+c) / format）；具体性锚点、空话句、可删句；注入扰动。`gate_rhetoric=False` 复现 v0.1 |
| `lweval/filler.py` | 中性负对照：同主题合成模板空话（每次运行必带，说人话必须排末位） |
| `lweval/stylepack.py` | 风格包规则版检查 v0.3，**文风 × 文体分开打分**：文风包（`yinyue`、`plain_professional`）按修辞手法的每千字密度区间判"文风遵循"，"首在-重在-贵在"是可选手法（只记录、不计分）；文体包（`commentary`、`report`）判结构、篇幅、标题、分节的"文体合规"。与说人话轴一起组成多轴报告 |
| `lweval/stylometry.py` | 风格指纹（句长/标点/虚词/修辞 4 族）、稳健 z 距离、滑动窗口漂移、留一零分布阈值、变点 |
| `lweval/upcr.py` | UPCR / 加权 UPCR / 非请求插入率 / 锚定编辑成功 |
| `lweval/logic_probe.py` | 段落打乱灵敏度 harness（指标元评测）+ 两个廉价代理 |
| `lweval/logic_graph.py` | 由论证图 JSON 计算 support/orphan/warrant/leap/depth |
| `lweval/retrieval_metrics.py` | 计划覆盖、证据召回、引文精确/召回、无支持率、来源权威/多样性、诱饵率、规划效率、移除来源扰动判定 |
| `lweval/stats.py` | Bradley-Terry + bootstrap、Cohen κ、Krippendorff α、样本量、符号检验 |
| `lweval/judges.py` + `prompts/` | 评审桩、位置互换一致性；10 个提示词模板（`pairwise_style` 只评文风，`pairwise_genre` 只评文体） |
| `lweval/cost.py` | 成本估算 |
| `schemas/` | task / source_pack / output / judgment / prereg 的 JSON Schema |
| `data/examples/` | 任务、来源包、预注册示例 |
| `samples/` | **虚构**样本：4 平实（nat_）、2 高质量印月文风（style_：评论 1 篇、调研报告 1 篇）、4 空话套话（hollow_，含 1 篇空壳印月仿写）、2 中性负对照（control_filler_）、3 修订对，见 `samples/manifest.json` |
| `data/heldout/` | 真实人类文本留出集：**公开仓库不分发原文**，只保留来源清单 `SOURCES.md`。不放原文时 `run_demo.py` 照常运行，只是输出里少了留出集各行；`results/DEMO_RESULTS.md` 保留的是作者本地含留出集运行的数字 |

**关于「印月三谈」**：它是风格市场中一个正当的**文风包**，不是反例。v0.3 起它只管文风（比喻、排比、设问的密度，修辞要承载具体信息），不再绑定评论文体；篇幅、三段式、标题长度归文体包。原型用它做"修辞 vs 空话"的校准：高质量印月文 v0.1 得 57.7（修辞被过度惩罚），v0.2 得 14.8；空壳仿写在两版中都是 99.5。

**重要局限**：词表与权重是对着 `samples/` 调的（开发集），样本上的完美分离不代表泛化；留出集结果见 `results/DEMO_RESULTS.md`。
