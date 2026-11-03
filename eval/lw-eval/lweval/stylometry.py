"""风格指纹 + 漂移计算（无需分词器）。

指纹 = 4 个特征族：
  sentlen   句长分布：均值、标准差、p10/p50/p90、8 桶直方图
  punct     标点每千字频率（，。；：？！、“”（）——…《》）
  funcword  60 个虚词/口语词每千字频率
  rhetoric  空洞度（hollow_raw）与修辞浓度（rhetoric_raw，未折减）分开计入，另含具体性、问句率
距离：各标量特征对参考分布做稳健 z 分数（|x-μ| / max(σ, 0.25|μ|+ε)），族内取均值、族间取均值；
      另报告句长直方图 JS 散度。漂移：对滑动窗口逐窗算距离，并用均值偏移法找变点。
"""
import math
import statistics

from .textutil import sentences, count_chars, strip_markdown, paragraphs
from .aitone import AIToneDetector

PUNCT = ["，", "。", "；", "：", "？", "！", "、", "“", "（", "——", "…", "《"]
FUNC_WORDS = ["的", "了", "着", "过", "地", "得", "就", "也", "都", "还", "又", "才", "却", "而",
              "但", "可", "然而", "因为", "所以", "如果", "虽然", "其实", "这", "那", "这个", "那个",
              "我们", "你", "我", "他", "它", "是", "有", "在", "把", "被", "让", "给", "对", "从",
              "比", "很", "更", "最", "非常", "吧", "呢", "啊", "吗", "嘛", "说", "大概", "一下",
              "进行", "实现", "推动", "不断", "具有", "以及", "通过"]
BINS = [0, 8, 14, 20, 28, 38, 50, 70, 10_000]

_det = None


def _detector():
    global _det
    if _det is None:
        _det = AIToneDetector()
    return _det


def _hist(lengths):
    h = [0] * (len(BINS) - 1)
    for L in lengths:
        for i in range(len(BINS) - 1):
            if BINS[i] <= L < BINS[i + 1]:
                h[i] += 1
                break
    s = sum(h) or 1
    return [x / s for x in h]


def _q(xs, q):
    xs = sorted(xs)
    if not xs:
        return 0.0
    k = (len(xs) - 1) * q
    lo, hi = math.floor(k), math.ceil(k)
    return xs[lo] + (xs[hi] - xs[lo]) * (k - lo)


def fingerprint(text: str) -> dict:
    plain = strip_markdown(text)
    n = max(count_chars(plain), 1)
    lens = [count_chars(s) for s in sentences(text)] or [0]
    sentlen = {
        "mean": statistics.mean(lens), "std": statistics.pstdev(lens),
        "p10": _q(lens, .1), "p50": _q(lens, .5), "p90": _q(lens, .9),
    }
    punct = {p: 1000 * plain.count(p) / n for p in PUNCT}
    func = {w: 1000 * plain.count(w) / n for w in FUNC_WORDS}
    rep = _detector().analyze(text)
    rhet = {"hollow_log": math.log1p(rep.hollow_raw), "rhetoric_log": math.log1p(rep.rhetoric_raw), "concreteness": rep.concreteness,
            "question_rate": 1000 * (plain.count("？") + plain.count("?")) / n}
    return {"chars": n, "sentlen": sentlen, "hist": _hist(lens), "punct": punct,
            "funcword": func, "rhetoric": rhet}


def reference(fps):
    """由若干指纹（例如目标风格范文的窗口）求每个标量特征的 μ、σ。"""
    ref = {}
    for fam in ("sentlen", "punct", "funcword", "rhetoric"):
        ref[fam] = {}
        for k in fps[0][fam]:
            vals = [fp[fam][k] for fp in fps]
            ref[fam][k] = (statistics.mean(vals), statistics.pstdev(vals) if len(vals) > 1 else 0.0)
    m = len(fps[0]["hist"])
    ref["hist"] = [statistics.mean(fp["hist"][i] for fp in fps) for i in range(m)]
    return ref


def _jsd(p, q):
    def kl(a, b):
        return sum(x * math.log(x / y) for x, y in zip(a, b) if x > 0 and y > 0)
    p = [x + 1e-9 for x in p]
    q = [x + 1e-9 for x in q]
    mm = [(a + b) / 2 for a, b in zip(p, q)]
    return 0.5 * kl(p, mm) + 0.5 * kl(q, mm)


Z_CAP = 5.0  # 单特征 z 分数截断，防止单一特征主导


def distance(fp, ref, eps=0.5):
    fam_d = {}
    for fam in ("sentlen", "punct", "funcword", "rhetoric"):
        zs = []
        for k, (mu, sd) in ref[fam].items():
            denom = max(sd, 0.25 * abs(mu) + eps)
            zs.append(min(abs(fp[fam][k] - mu) / denom, Z_CAP))
        fam_d[fam] = statistics.mean(zs)
    total = statistics.mean(fam_d.values())
    return {"distance": round(total, 3), "families": {k: round(v, 3) for k, v in fam_d.items()},
            "hist_jsd": round(_jsd(fp["hist"], ref["hist"]), 4)}


def windows(text: str, size: int = 300, step: int = 150):
    """按句子边界切滑动窗口（size/step 以计数字符计）。"""
    sents = sentences(text)
    lens = [count_chars(s) for s in sents]
    out = []
    i = 0
    while i < len(sents):
        acc, j = 0, i
        while j < len(sents) and acc < size:
            acc += lens[j]
            j += 1
        if acc >= size * 0.6:
            out.append("".join(sents[i:j]))
        if j >= len(sents):
            break
        adv, k = 0, i
        while k < j and adv < step:
            adv += lens[k]
            k += 1
        i = max(k, i + 1)
    return out


def change_point(series):
    """单变点：最大化 |μL-μR|·sqrt(nL·nR/n)。返回 (切分位置, 统计量, μL, μR)。"""
    n = len(series)
    best = (None, 0.0, None, None)
    for t in range(2, n - 1):
        L, R = series[:t], series[t:]
        stat = abs(statistics.mean(L) - statistics.mean(R)) * math.sqrt(len(L) * len(R) / n)
        if stat > best[1]:
            best = (t, stat, statistics.mean(L), statistics.mean(R))
    return best


DEFAULT_SELF_THRESHOLD = 1.0  # 自锚模式的暂定阈值（须用真实语料的零分布重新标定）


def null_threshold(ref_texts, size, step, q=0.95):
    """留一文本法零分布：每次用其余参考文本建参考，计算被留出文本各窗口的距离；阈值取 p95。"""
    null = []
    for i, t in enumerate(ref_texts):
        others = [w for j, o in enumerate(ref_texts) if j != i for w in windows(o, size, step)]
        if len(others) < 2:
            continue
        ref = reference([fingerprint(w) for w in others])
        null += [distance(fingerprint(w), ref)["distance"] for w in windows(t, size, step)]
    return (_q(null, q) if null else DEFAULT_SELF_THRESHOLD), null


def drift(text, ref_texts=None, self_anchor=2, size=300, step=150):
    """逐窗距离序列。ref_texts 给出时以其窗口为参考、阈值来自留一零分布；
    否则以本文前 self_anchor 个窗口为参考、阈值用暂定常数。"""
    wins = windows(text, size, step)
    fps = [fingerprint(w) for w in wins]
    if ref_texts:
        ref = reference([fingerprint(w) for t in ref_texts for w in windows(t, size, step)])
        thr, null = null_threshold(ref_texts, size, step)
        mode = "exemplar"
    else:
        ref = reference(fps[:self_anchor])
        thr, null, mode = DEFAULT_SELF_THRESHOLD, [], "self_anchor"
    series = [distance(f, ref)["distance"] for f in fps]
    cp = change_point(series) if len(series) >= 4 else (None, 0, None, None)
    return {"mode": mode, "n_windows": len(wins), "series": series, "threshold": round(thr, 3),
            "null_n": len(null),
            "drift_windows": [i for i, d in enumerate(series) if d > thr],
            "change_point": {"index": cp[0], "stat": round(cp[1], 3),
                             "mean_before": cp[2] and round(cp[2], 3), "mean_after": cp[3] and round(cp[3], 3)}}
