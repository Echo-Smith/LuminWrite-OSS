"""统计工具：Bradley-Terry（MM 算法，平局计半胜）+ bootstrap 置信区间；
Cohen κ、Krippendorff α（nominal / interval）；配对符号检验与样本量估计。纯标准库。"""
import math
import random
import statistics
from collections import defaultdict
from statistics import NormalDist

Z = NormalDist()


# ---------------- Bradley-Terry ----------------
def bradley_terry(comparisons, iters=500, tol=1e-9):
    """comparisons: [(a, b, outcome)]，outcome ∈ {1: a 胜, 0: b 胜, 0.5: 平}。
    返回 {system: strength}，按几何均值归一为 1；另返回 Elo 尺度分（400·log10）。"""
    items = sorted({x for a, b, _ in comparisons for x in (a, b)})
    wins = defaultdict(float)
    pair_n = defaultdict(float)
    for a, b, o in comparisons:
        wins[a] += o
        wins[b] += 1 - o
        pair_n[frozenset((a, b))] += 1
    p = {i: 1.0 for i in items}
    for _ in range(iters):
        new = {}
        for i in items:
            denom = 0.0
            for j in items:
                if i == j:
                    continue
                n = pair_n.get(frozenset((i, j)), 0)
                if n:
                    denom += n / (p[i] + p[j])
            new[i] = (wins[i] + 0.5) / (denom + 1.0 / p[i])  # 加 0.5 伪计数防止全胜发散
        g = math.exp(statistics.mean(math.log(v) for v in new.values()))
        new = {k: v / g for k, v in new.items()}
        if max(abs(new[k] - p[k]) for k in items) < tol:
            p = new
            break
        p = new
    elo = {k: round(400 * math.log10(v) + 1000, 1) for k, v in p.items()}
    return p, elo


def bt_bootstrap(comparisons, n_boot=300, seed=0, alpha=0.05):
    rnd = random.Random(seed)
    _, base = bradley_terry(comparisons)
    samples = defaultdict(list)
    for _ in range(n_boot):
        bs = [comparisons[rnd.randrange(len(comparisons))] for _ in comparisons]
        _, elo = bradley_terry(bs, iters=200)
        for k in base:
            samples[k].append(elo.get(k, 1000.0))
    ci = {}
    for k, v in samples.items():
        v.sort()
        ci[k] = (v[int(alpha / 2 * len(v))], v[int((1 - alpha / 2) * len(v)) - 1])
    return base, ci


# ---------------- 一致性 ----------------
def cohen_kappa(r1, r2):
    cats = sorted(set(r1) | set(r2))
    n = len(r1)
    po = sum(a == b for a, b in zip(r1, r2)) / n
    pe = sum((r1.count(c) / n) * (r2.count(c) / n) for c in cats)
    return (po - pe) / (1 - pe) if pe < 1 else 1.0


def krippendorff_alpha(data, level="nominal"):
    """data: 评审×条目 的矩阵（list of lists），缺失用 None。"""
    units = list(zip(*data))
    pairs = []
    values = []
    for u in units:
        vals = [v for v in u if v is not None]
        m = len(vals)
        if m < 2:
            continue
        values.extend(vals)
        for i in range(m):
            for j in range(m):
                if i != j:
                    pairs.append((vals[i], vals[j], 1.0 / (m - 1)))
    if not pairs:
        return float("nan")

    def d(a, b):
        return float(a != b) if level == "nominal" else (a - b) ** 2
    n = len(values)
    Do = sum(w * d(a, b) for a, b, w in pairs) / n
    De = sum(d(a, b) for i, a in enumerate(values) for j, b in enumerate(values) if i != j) / (n * (n - 1))
    return 1.0 - Do / De if De > 0 else 1.0


# ---------------- 功效 / 样本量 ----------------
def n_pairs_for_winrate(p1, alpha=0.05, power=0.8):
    """配对偏好（去平局后）检验胜率 p1 vs 0.5 所需的对数（正态近似，双侧）。"""
    za, zb = Z.inv_cdf(1 - alpha / 2), Z.inv_cdf(power)
    return math.ceil(((za * 0.5 + zb * math.sqrt(p1 * (1 - p1))) / (p1 - 0.5)) ** 2)


def n_for_paired_d(d, alpha=0.05, power=0.8):
    """配对连续指标，检测标准化效应 d（Δ均值/Δ标准差）所需样本量（正态近似 + 小样本修正 +2）。"""
    za, zb = Z.inv_cdf(1 - alpha / 2), Z.inv_cdf(power)
    return math.ceil(((za + zb) / d) ** 2) + 2


def sign_test_p(wins, losses):
    """双侧精确符号检验 p 值。"""
    n = wins + losses
    k = min(wins, losses)
    p = sum(math.comb(n, i) for i in range(0, k + 1)) / 2 ** n
    return min(1.0, 2 * p)
