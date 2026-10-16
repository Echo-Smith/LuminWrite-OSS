"""一键复现原型结果：python3 run_demo.py → results/demo_results.json + results/DEMO_RESULTS.md
所有数字均为在 samples/（原型作者编写的虚构样本）与 data/heldout/（真实人类文本留出集）上实际运行得到。"""
import glob
import json
import os
import random

from lweval import aitone, stylometry, upcr, logic_probe, stats, judges, stylepack

ROOT = os.path.dirname(os.path.abspath(__file__))
os.chdir(ROOT)
R = lambda p: open(p, encoding="utf-8").read()  # noqa: E731
det = aitone.AIToneDetector()
res = {}

# 1) 说人话检测
rows = []
files = (sorted(glob.glob("samples/control_*.md")) + sorted(glob.glob("samples/hollow_*.md")) + sorted(glob.glob("samples/style_*.md"))
         + sorted(glob.glob("samples/nat_*.md")) + sorted(glob.glob("data/heldout/real_*.md")))
for f in files:
    r = det.analyze(R(f))
    top = {}
    for h in r.hits:
        if h["weight"] > 0:
            top[h["category"]] = top.get(h["category"], 0) + 1
    rows.append({"file": f, "chars": r.chars, "aitone_score": r.aitone_score, "aitone_raw": r.aitone_raw,
                 "hollow_raw": r.hollow_raw, "rhetoric_raw": r.rhetoric_raw, "rhetoric_gated": r.rhetoric_gated,
                 "concreteness": r.concreteness, "hollow_ratio": r.hollow_ratio,
                 "deletable_ratio": r.deletable_ratio, "top_categories": dict(sorted(top.items(), key=lambda x: -x[1])[:4])})
res["aitone"] = rows

# 1b) 修辞校准：v0.1（修辞与空话同等计分） vs v0.2（修辞按段落具体性折减），以及两轴（印月风格遵循 × 说人话）
v01 = aitone.AIToneDetector(gate_rhetoric=False)
calib = []
for f in files:
    t = R(f)
    sp = stylepack.check(t, "yinyue")
    calib.append({"file": f, "aitone_v01": v01.analyze(t).aitone_score, "aitone_v02": det.analyze(t).aitone_score,
                  "yinyue_adherence": sp["adherence"], "checks": sp["checks"]})
res["calibration"] = calib

# 2) 注入灵敏度
inj = {}
for f in sorted(glob.glob("samples/nat_*.md")) + sorted(glob.glob("data/heldout/real_weekly*.md")):
    t = R(f)
    inj[f] = {k: round(sum(det.analyze(aitone.inject(t, k, s)).aitone_score for s in range(5)) / 5, 1) for k in (0, 1, 2, 4, 8)}
res["injection"] = inj

# 3) 风格距离：留一法（目标=自然范文集），以及 目标=印月三谈
nat = sorted(glob.glob("samples/nat_*.md"))
style = {}
for f in (sorted(glob.glob("samples/nat_*.md")) + sorted(glob.glob("samples/hollow_*.md")) + sorted(glob.glob("samples/style_*.md"))
          + sorted(glob.glob("samples/control_*.md")) + sorted(glob.glob("data/heldout/real_*.md"))):
    refs = [x for x in nat if x != f]
    ref = stylometry.reference([stylometry.fingerprint(w) for x in refs for w in stylometry.windows(R(x), 200, 100)])
    yref = stylometry.reference([stylometry.fingerprint(w) for w in stylometry.windows(R("samples/style_yinyue_good_village.md"), 200, 100)])
    fp = stylometry.fingerprint(R(f))
    style[f] = {"to_natural_LOO": stylometry.distance(fp, ref), "to_yinyue": stylometry.distance(fp, yref)}
res["style"] = style

# 4) 漂移：拼接文本（前半自然、后半 AI 腔） vs 对照（两篇自然文本拼接）
splice = R("samples/nat_report_ev_county.md") + "\n\n" + R("samples/hollow_report_ev_county.md").split("\n", 1)[1]
ctrl = R("samples/nat_report_ev_county.md") + "\n\n" + R("samples/nat_explainer_electricity.md").split("\n", 1)[1]
open("results/synthetic_splice_nat_then_ai.md", "w").write(splice)
open("results/synthetic_control_nat_then_nat.md", "w").write(ctrl)
exrefs = [R("samples/nat_commentary_village.md"), R("samples/nat_essay_remote_work.md"), R("samples/nat_explainer_electricity.md")]
res["drift"] = {
    "splice_exemplar": stylometry.drift(splice, exrefs, 2, 200, 100),
    "control_exemplar": stylometry.drift(ctrl, exrefs, 2, 200, 100),
    "splice_self_anchor": stylometry.drift(splice, None, 2, 200, 100),
    "control_self_anchor": stylometry.drift(ctrl, None, 2, 200, 100),
}

# 5) UPCR
b = R("samples/rev_before.md")
res["upcr"] = {}
for name in ("rev_after_good.md", "rev_after_drift.md"):
    u = upcr.upcr(b, R("samples/" + name), anchors=["售后是第二个"], must_contain=["不能说明普遍"])
    res["upcr"][name] = {k: u[k] for k in ("UPCR", "UPCR_weighted", "insert_rate", "anchored_success")}
    res["upcr"][name]["statuses"] = [r["status"] for r in u["rows"]]

# 6) 段落打乱灵敏度（廉价代理指标的元评测）
sh = {}
for f in (sorted(glob.glob("samples/nat_*.md")) + sorted(glob.glob("samples/hollow_*.md")) + sorted(glob.glob("samples/style_*.md"))
          + sorted(glob.glob("data/heldout/real_weekly*.md"))):
    sh[f] = {m: logic_probe.shuffle_sensitivity(R(f), m, 200)["sensitivity"] for m in ("adjacency_advantage", "connective_fit")}
res["shuffle"] = sh

# 7) 功效
res["power"] = {"pairwise_winrate": {str(p): stats.n_pairs_for_winrate(p) for p in (0.55, 0.6, 0.65, 0.7)},
                "paired_d": {str(d): stats.n_for_paired_d(d) for d in (0.2, 0.3, 0.5, 0.8)}}

# 8) 模拟：位置偏差对成对评测的影响（合成数据，仅演示为何必须互换位置）
rnd = random.Random(3)
true = {"sysA": 1.0, "sysB": 1.0}  # 两系统真实质量相同
bias = 0.15  # judge 偏好放在 A 位的文本
raw, swapped = [], []
for _ in range(400):
    p_first = 0.5 + bias  # 原顺序 sysA 在 A 位
    o1 = "A" if rnd.random() < p_first else "B"
    o2 = "A" if rnd.random() < p_first else "B"  # 互换后 sysB 在 A 位
    raw.append(1.0 if o1 == "A" else 0.0)
    sw = judges.swap_consistent(o1, o2)
    swapped.append({"A": 1.0, "B": 0.0, "tie": 0.5}[sw])
res["sim_position_bias"] = {"note": "合成模拟：两系统真实质量相同、judge 有 15% 首位偏好",
                            "winrate_sysA_without_swap": round(sum(raw) / len(raw), 3),
                            "winrate_sysA_with_swap_consistency": round(sum(swapped) / len(swapped), 3),
                            "tie_rate_with_swap": round(sum(1 for x in swapped if x == 0.5) / len(swapped), 3)}

json.dump(res, open("results/demo_results.json", "w"), ensure_ascii=False, indent=2)

# ---- Markdown ----
L = ["# 原型运行结果（自动生成，请勿手改）", "",
     "> samples/ 为原型作者编写的虚构样本（非真实人类文本）：nat_*=平实自然、style_*=高质量印月三谈风格（修辞承载具体内容）、hollow_*=空话套话（含一篇'只有修辞外壳、没有内容'的印月仿写）、control_filler_*=lweval.filler 生成的中性模板空话（负对照）。data/heldout/ 为真实人类文本留出集（阮一峰周刊 3 篇、2024 政府工作报告节选 2 段）。检测器版本 aitone-zh.v0.2。", ""]
L += ["## 1. 说人话 / AI 腔检测（v0.2）", "", "| 文件 | 字数 | AI腔分(0-100) | 空洞 raw | 修辞 raw(未折减) | 修辞 raw(折减后) | 具体性/百字 | 空话句占比 | 可删句占比(启发式) | 主要命中 |", "|---|---|---|---|---|---|---|---|---|---|"]
for r in rows:
    L.append(f"| {r['file']} | {r['chars']} | {r['aitone_score']} | {r['hollow_raw']} | {r['rhetoric_raw']} | {r['rhetoric_gated']} | {r['concreteness']} | {r['hollow_ratio']} | {r['deletable_ratio']} | {r['top_categories']} |")
L += ["", "## 1b. 修辞校准与两轴（印月风格遵循 × 说人话）", "", "| 文件 | AI腔分 v0.1(修辞不折减) | AI腔分 v0.2(修辞按具体性折减) | 印月遵循度(规则版 0-1) |", "|---|---|---|---|"]
for c in calib:
    L.append(f"| {c['file']} | {c['aitone_v01']} | {c['aitone_v02']} | {c['yinyue_adherence']} |")
L += ["", "## 2. 注入 AI 腔短语的灵敏度（5 个随机位置种子平均）", "", "| 文件 | k=0 | 1 | 2 | 4 | 8 |", "|---|---|---|---|---|---|"]
for f, v in inj.items():
    L.append(f"| {f} | " + " | ".join(str(v[k]) for k in (0, 1, 2, 4, 8)) + " |")
L += ["", "## 3. 风格指纹距离", "", "| 文件 | 距平实范文(留一) | 族分解 | 距印月三谈(以 style_yinyue_good 为参考) |", "|---|---|---|---|"]
for f, v in style.items():
    L.append(f"| {f} | {v['to_natural_LOO']['distance']} | {v['to_natural_LOO']['families']} | {v['to_yinyue']['distance']} |")
L += ["", "## 4. 滑动窗口漂移（窗口 200 字、步长 100 字）", ""]
for k, v in res["drift"].items():
    L.append(f"- **{k}**：序列 {v['series']}，阈值 {v['threshold']}（{v['mode']}），越线窗口 {v['drift_windows']}，变点 {v['change_point']}")
L += ["", "## 5. UPCR", ""]
for k, v in res["upcr"].items():
    L.append(f"- **{k}**：UPCR={v['UPCR']}，加权={v['UPCR_weighted']}，插入率={v['insert_rate']}，锚定编辑成功={v['anchored_success']}，段落状态={v['statuses']}")
L += ["", "## 6. 段落打乱灵敏度（廉价连贯性代理，合格线 ≥0.90）", "", "| 文件 | adjacency_advantage | connective_fit |", "|---|---|---|"]
for f, v in sh.items():
    L.append(f"| {f} | {v['adjacency_advantage']} | {v['connective_fit']} |")
L += ["", "## 7. 样本量（α=0.05 双侧，power=0.8）", "", f"- 成对胜率（去平局）：{res['power']['pairwise_winrate']}", f"- 配对连续指标 Cohen d：{res['power']['paired_d']}",
      "", "## 8. 合成模拟：位置偏差", "", f"- {res['sim_position_bias']}"]
open("results/DEMO_RESULTS.md", "w").write("\n".join(L) + "\n")
print("\n".join(L))
