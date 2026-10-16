# 原型运行结果（自动生成，请勿手改）

> samples/ 为原型作者编写的虚构样本（非真实人类文本）：nat_*=平实自然、style_*=高质量印月三谈风格（修辞承载具体内容）、hollow_*=空话套话（含一篇'只有修辞外壳、没有内容'的印月仿写）、control_filler_*=lweval.filler 生成的中性模板空话（负对照）。data/heldout/ 为真实人类文本留出集（阮一峰周刊 3 篇、2024 政府工作报告节选 2 段）。检测器版本 aitone-zh.v0.2。

## 1. 说人话 / AI 腔检测（v0.2）

| 文件 | 字数 | AI腔分(0-100) | 空洞 raw | 修辞 raw(未折减) | 修辞 raw(折减后) | 具体性/百字 | 空话句占比 | 可删句占比(启发式) | 主要命中 |
|---|---|---|---|---|---|---|---|---|---|
| samples/control_filler_ev_county.md | 337 | 89.7 | 90.8 | 0.0 | 0.0 | 0.0 | 0.786 | 0.071 | {'buzzword': 9, 'hollow_summary': 6, 'template_connector': 5, 'hedge': 2} |
| samples/control_filler_village.md | 301 | 92.1 | 101.66 | 0.0 | 0.0 | 0.0 | 0.786 | 0.071 | {'buzzword': 9, 'hollow_summary': 6, 'template_connector': 5, 'hedge': 2} |
| samples/hollow_explainer_hedging.md | 308 | 93.9 | 103.9 | 1.62 | 1.62 | 0.32 | 0.818 | 0.091 | {'hedge': 15, 'hollow_summary': 6, 'template_connector': 3, 'cta': 2} |
| samples/hollow_report_ev_county.md | 421 | 95.5 | 100.48 | 1.19 | 1.19 | 0.0 | 0.522 | 0.087 | {'buzzword': 22, 'bold': 7, 'hollow_summary': 5, 'template_connector': 3} |
| samples/hollow_subtle_template.md | 313 | 80.6 | 51.12 | 14.38 | 14.38 | 0.0 | 0.529 | 0.0 | {'template_connector': 8, 'hollow_summary': 4, 'rhetorical_q': 1, 'parallel_paragraph_openers': 1} |
| samples/hollow_yinyue_imitation.md | 379 | 99.5 | 147.76 | 60.69 | 60.69 | 0.0 | 0.875 | 0.188 | {'slogan': 28, 'buzzword': 5, 'cliche_metaphor': 5, 'rhetorical_q': 3} |
| samples/style_yinyue_good_village.md | 363 | 14.8 | 0.0 | 34.44 | 6.41 | 4.13 | 0.0 | 0.0 | {'rhetoric_frame': 3, 'cliche_metaphor': 1, 'rhetorical_q': 1, 'parallel_clause_run': 1} |
| samples/nat_commentary_village.md | 327 | 0.0 | 0.0 | 0.0 | 0.0 | 3.98 | 0.0 | 0.0 | {} |
| samples/nat_essay_remote_work.md | 305 | 0.0 | 0.0 | 0.0 | 0.0 | 3.61 | 0.0 | 0.0 | {} |
| samples/nat_explainer_electricity.md | 309 | 0.0 | 0.0 | 0.0 | 0.0 | 5.5 | 0.0 | 0.133 | {} |
| samples/nat_report_ev_county.md | 401 | 0.0 | 0.0 | 0.0 | 0.0 | 3.99 | 0.0 | 0.0 | {} |
| data/heldout/real_gov2024_review.md | 1601 | 20.4 | 5.62 | 6.87 | 3.52 | 1.44 | 0.157 | 0.039 | {'buzzword': 7, 'idiom_chain': 7, 'hollow_summary': 1, 'exclaim': 1} |
| data/heldout/real_gov2024_tasks.md | 1619 | 42.2 | 14.52 | 7.41 | 7.41 | 0.06 | 0.316 | 0.175 | {'buzzword': 22, 'idiom_chain': 8, 'slogan': 1} |
| data/heldout/real_weekly100_essay.md | 593 | 11.9 | 0.0 | 2.53 | 2.53 | 0.67 | 0.0 | 0.045 | {'bold': 3, 'idiom_chain': 1} |
| data/heldout/real_weekly150_essay.md | 861 | 2.9 | 0.0 | 0.0 | 0.0 | 1.74 | 0.0 | 0.0 | {'bold': 2} |
| data/heldout/real_weekly300_essay.md | 530 | 20.2 | 3.77 | 5.66 | 0.53 | 6.79 | 0.0 | 0.0 | {'bold': 5, 'idiom_chain': 2, 'hollow_summary': 1} |

## 1b. 修辞校准与两轴（印月风格遵循 × 说人话）

| 文件 | AI腔分 v0.1(修辞不折减) | AI腔分 v0.2(修辞按具体性折减) | 印月遵循度(规则版 0-1) |
|---|---|---|---|
| samples/control_filler_ev_county.md | 89.7 | 89.7 | 0.0 |
| samples/control_filler_village.md | 92.1 | 92.1 | 0.0 |
| samples/hollow_explainer_hedging.md | 93.9 | 93.9 | 0.0 |
| samples/hollow_report_ev_county.md | 95.5 | 95.5 | 0.0 |
| samples/hollow_subtle_template.md | 80.6 | 80.6 | 0.5 |
| samples/hollow_yinyue_imitation.md | 99.5 | 99.5 | 1.0 |
| samples/style_yinyue_good_village.md | 57.7 | 14.8 | 1.0 |
| samples/nat_commentary_village.md | 0.0 | 0.0 | 0.0 |
| samples/nat_essay_remote_work.md | 0.0 | 0.0 | 0.0 |
| samples/nat_explainer_electricity.md | 0.0 | 0.0 | 0.0 |
| samples/nat_report_ev_county.md | 0.0 | 0.0 | 0.0 |
| data/heldout/real_gov2024_review.md | 26.8 | 20.4 | 0.0 |
| data/heldout/real_gov2024_tasks.md | 42.2 | 42.2 | 0.0 |
| data/heldout/real_weekly100_essay.md | 11.9 | 11.9 | 0.0 |
| data/heldout/real_weekly150_essay.md | 2.9 | 2.9 | 0.0 |
| data/heldout/real_weekly300_essay.md | 29.8 | 20.2 | 0.0 |

## 2. 注入 AI 腔短语的灵敏度（5 个随机位置种子平均）

| 文件 | k=0 | 1 | 2 | 4 | 8 |
|---|---|---|---|---|---|
| samples/nat_commentary_village.md | 0.0 | 13.5 | 42.9 | 63.0 | 86.1 |
| samples/nat_essay_remote_work.md | 0.0 | 14.3 | 45.0 | 64.4 | 87.2 |
| samples/nat_explainer_electricity.md | 0.0 | 14.2 | 44.6 | 64.4 | 86.9 |
| samples/nat_report_ev_county.md | 0.0 | 11.2 | 37.1 | 56.3 | 81.4 |
| data/heldout/real_weekly100_essay.md | 11.9 | 18.5 | 35.7 | 50.9 | 74.3 |
| data/heldout/real_weekly150_essay.md | 2.9 | 8.2 | 22.3 | 36.0 | 60.6 |
| data/heldout/real_weekly300_essay.md | 20.2 | 26.6 | 43.5 | 57.1 | 78.4 |

## 3. 风格指纹距离

| 文件 | 距平实范文(留一) | 族分解 | 距印月三谈(以 style_yinyue_good 为参考) |
|---|---|---|---|
| samples/nat_commentary_village.md | 0.397 | {'sentlen': 0.221, 'punct': 0.387, 'funcword': 0.77, 'rhetoric': 0.209} | 1.165 |
| samples/nat_essay_remote_work.md | 0.534 | {'sentlen': 0.559, 'punct': 0.309, 'funcword': 0.959, 'rhetoric': 0.309} | 1.162 |
| samples/nat_explainer_electricity.md | 1.029 | {'sentlen': 0.693, 'punct': 1.117, 'funcword': 0.799, 'rhetoric': 1.505} | 0.984 |
| samples/nat_report_ev_county.md | 0.544 | {'sentlen': 0.86, 'punct': 0.411, 'funcword': 0.666, 'rhetoric': 0.241} | 1.238 |
| samples/hollow_explainer_hedging.md | 1.301 | {'sentlen': 0.803, 'punct': 0.943, 'funcword': 0.961, 'rhetoric': 2.498} | 1.464 |
| samples/hollow_report_ev_county.md | 1.303 | {'sentlen': 0.708, 'punct': 1.169, 'funcword': 0.874, 'rhetoric': 2.459} | 1.495 |
| samples/hollow_subtle_template.md | 1.562 | {'sentlen': 0.983, 'punct': 0.643, 'funcword': 1.174, 'rhetoric': 3.45} | 1.426 |
| samples/hollow_yinyue_imitation.md | 1.51 | {'sentlen': 0.693, 'punct': 1.233, 'funcword': 0.733, 'rhetoric': 3.381} | 1.446 |
| samples/style_yinyue_good_village.md | 1.091 | {'sentlen': 0.433, 'punct': 0.882, 'funcword': 0.866, 'rhetoric': 2.182} | 0.205 |
| samples/control_filler_ev_county.md | 1.093 | {'sentlen': 0.88, 'punct': 0.369, 'funcword': 1.056, 'rhetoric': 2.067} | 1.389 |
| samples/control_filler_village.md | 1.129 | {'sentlen': 1.069, 'punct': 0.327, 'funcword': 1.054, 'rhetoric': 2.067} | 1.464 |
| data/heldout/real_gov2024_review.md | 1.514 | {'sentlen': 1.482, 'punct': 1.225, 'funcword': 0.782, 'rhetoric': 2.566} | 1.642 |
| data/heldout/real_gov2024_tasks.md | 1.516 | {'sentlen': 0.762, 'punct': 1.282, 'funcword': 0.896, 'rhetoric': 3.122} | 1.612 |
| data/heldout/real_weekly100_essay.md | 0.832 | {'sentlen': 0.561, 'punct': 0.591, 'funcword': 0.89, 'rhetoric': 1.286} | 1.32 |
| data/heldout/real_weekly150_essay.md | 0.687 | {'sentlen': 0.891, 'punct': 0.721, 'funcword': 0.572, 'rhetoric': 0.566} | 1.232 |
| data/heldout/real_weekly300_essay.md | 1.208 | {'sentlen': 0.535, 'punct': 1.305, 'funcword': 0.74, 'rhetoric': 2.254} | 1.674 |

## 4. 滑动窗口漂移（窗口 200 字、步长 100 字）

- **splice_exemplar**：序列 [0.656, 0.528, 0.94, 1.078, 0.889, 1.358]，阈值 0.929（exemplar），越线窗口 [2, 3, 5]，变点 {'index': 2, 'stat': 0.548, 'mean_before': 0.592, 'mean_after': 1.066}
- **control_exemplar**：序列 [0.656, 0.528, 0.52, 0.611, 0.596, 0.446]，阈值 0.929（exemplar），越线窗口 []，变点 {'index': 4, 'stat': 0.067, 'mean_before': 0.579, 'mean_after': 0.521}
- **splice_self_anchor**：序列 [0.223, 0.223, 0.828, 1.235, 1.242, 1.731]，阈值 1.0（self_anchor），越线窗口 [3, 4, 5]，变点 {'index': 3, 'stat': 1.198, 'mean_before': 0.425, 'mean_after': 1.403}
- **control_self_anchor**：序列 [0.223, 0.223, 0.506, 1.292, 1.28, 0.938]，阈值 1.0（self_anchor），越线窗口 [3, 4]，变点 {'index': 3, 'stat': 1.044, 'mean_before': 0.317, 'mean_after': 1.17}

## 5. UPCR

- **rev_after_good.md**：UPCR=0.0，加权=0.0，插入率=0.0，锚定编辑成功=True，段落状态=['unchanged', 'unchanged', 'unchanged', 'unchanged', 'modified', 'unchanged']
- **rev_after_drift.md**：UPCR=0.6，加权=0.453，插入率=0.0，锚定编辑成功=True，段落状态=['rewritten', 'modified', 'unchanged', 'unchanged', 'modified', 'rewritten']

## 6. 段落打乱灵敏度（廉价连贯性代理，合格线 ≥0.90）

| 文件 | adjacency_advantage | connective_fit |
|---|---|---|
| samples/nat_commentary_village.md | 0.662 | 0.664 |
| samples/nat_essay_remote_work.md | 0.116 | 0.808 |
| samples/nat_explainer_electricity.md | 0.51 | 0.5 |
| samples/nat_report_ev_county.md | 0.343 | 0.5 |
| samples/hollow_explainer_hedging.md | 0.232 | 0.5 |
| samples/hollow_report_ev_county.md | 0.755 | 0.5 |
| samples/hollow_subtle_template.md | 0.091 | 0.404 |
| samples/hollow_yinyue_imitation.md | 0.205 | 0.5 |
| samples/style_yinyue_good_village.md | 0.914 | 0.5 |
| data/heldout/real_weekly100_essay.md | 0.995 | 0.5 |
| data/heldout/real_weekly150_essay.md | 0.985 | 0.595 |
| data/heldout/real_weekly300_essay.md | 1.0 | 0.233 |

## 7. 样本量（α=0.05 双侧，power=0.8）

- 成对胜率（去平局）：{'0.55': 783, '0.6': 194, '0.65': 85, '0.7': 47}
- 配对连续指标 Cohen d：{'0.2': 199, '0.3': 90, '0.5': 34, '0.8': 15}

## 8. 合成模拟：位置偏差

- {'note': '合成模拟：两系统真实质量相同、judge 有 15% 首位偏好', 'winrate_sysA_without_swap': 0.618, 'winrate_sysA_with_swap_consistency': 0.461, 'tie_rate_with_swap': 0.532}
