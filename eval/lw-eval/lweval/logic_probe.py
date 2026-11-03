"""逻辑度量的"元评测"：段落打乱灵敏度 harness。

思想：一个声称衡量逻辑/连贯的指标，必须能把原文与其打乱版本区分开。
  sensitivity = P(metric(original) > metric(shuffled))   （对 N 个随机置换取平均）
  mean_drop   = mean(metric(original) - metric(shuffled))
合格线（预注册建议）：在"连贯的人写长文"上 sensitivity ≥ 0.90。

内置两个廉价代理指标（仅作基线/对照，预期会不合格）：
  adjacency_advantage : 相邻段内容二元组 Jaccard 均值 - 非相邻段 Jaccard 均值
  connective_fit      : 以承接词开头的段落，与前一段的 Jaccard 高于其与全文其它段平均值的比例
LLM 论证图指标通过 judge 接口接入（见 judges.py，本原型不调用真实模型）。
"""
import random
import statistics

from .textutil import paragraphs, content_bigrams, jaccard

CONNECTIVES = ("所以", "因此", "但", "可是", "不过", "然而", "这", "这些", "同一", "后来", "至于",
               "于是", "而且", "此外", "另外", "相反", "换句话说", "也就是说")


def _content_paras(text):
    ps = paragraphs(text, keep_markdown=False)
    return [p for p in ps if len(p) >= 15]  # 去掉标题等短段


def adjacency_advantage(paras):
    G = [content_bigrams(p) for p in paras]
    n = len(G)
    if n < 3:
        return 0.0
    adj = [jaccard(G[i], G[i + 1]) for i in range(n - 1)]
    non = [jaccard(G[i], G[j]) for i in range(n) for j in range(i + 2, n)]
    return statistics.mean(adj) - (statistics.mean(non) if non else 0.0)


def connective_fit(paras):
    G = [content_bigrams(p) for p in paras]
    hits, ok = 0, 0
    for i in range(1, len(paras)):
        if paras[i].startswith(CONNECTIVES):
            hits += 1
            prev = jaccard(G[i], G[i - 1])
            others = [jaccard(G[i], G[j]) for j in range(len(paras)) if j not in (i, i - 1)]
            if not others or prev > statistics.mean(others):
                ok += 1
    return ok / hits if hits else 0.0


METRICS = {"adjacency_advantage": adjacency_advantage, "connective_fit": connective_fit}


def shuffle_sensitivity(text, metric="adjacency_advantage", n_perm=200, seed=7, keep_first=False):
    f = METRICS[metric] if isinstance(metric, str) else metric
    paras = _content_paras(text)
    if len(paras) < 3:
        return {"error": "段落少于 3，无法检验"}
    rnd = random.Random(seed)
    orig = f(paras)
    wins, ties, drops = 0, 0, []
    for _ in range(n_perm):
        p = paras[:]
        if keep_first:
            head, tail = p[:1], p[1:]
            rnd.shuffle(tail)
            p = head + tail
        else:
            rnd.shuffle(p)
        if p == paras:
            continue
        v = f(p)
        drops.append(orig - v)
        if orig > v:
            wins += 1
        elif orig == v:
            ties += 1
    m = len(drops) or 1
    return {"metric": metric if isinstance(metric, str) else getattr(metric, "__name__", "custom"),
            "n_paras": len(paras), "original": round(orig, 4),
            "sensitivity": round((wins + 0.5 * ties) / m, 3),
            "mean_drop": round(statistics.mean(drops), 4) if drops else 0.0, "n_perm": len(drops)}
