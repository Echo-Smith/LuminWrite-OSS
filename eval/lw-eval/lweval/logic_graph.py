"""由 LLM 抽取的论证图（prompts/logic_argument_graph.md 的输出）计算确定性逻辑指标。

  support_ratio  非 main 论断中，至少有一条 supports 指向上级的比例
  orphan_rate    孤立论断占比
  warrant_rate   supports 中 warrant_stated=True 的比例（推理依据是否写出）
  leap_rate      每千字跳步数
  depth          从任一 fact 走到 thesis 支撑的最长链（层级感）
  rebuttal       是否有被回应（addressed=True）的反方
  thesis_cov     金标准要点被 claims 覆盖的比例（覆盖判定由 judge 给出：gold_hits 列表）
"""


def graph_metrics(graph, chars, gold_points=None, gold_hits=None):
    claims = {c["id"]: c for c in graph.get("claims", [])}
    sup = graph.get("supports", [])
    out_edges = {}
    for e in sup:
        out_edges.setdefault(e["from"], []).append(e["to"])
    non_main = [c for c in claims.values() if c.get("type") != "main"]
    supported_up = sum(1 for c in non_main if c["id"] in out_edges)
    mains = [c["id"] for c in claims.values() if c.get("type") == "main"]

    def depth(cid, seen=()):
        if cid in seen:
            return 0
        return 1 + max((depth(t, seen + (cid,)) for t in out_edges.get(cid, [])), default=0)
    d = max((depth(c) for c in claims), default=0)
    res = {
        "n_claims": len(claims),
        "support_ratio": round(supported_up / max(len(non_main), 1), 3),
        "orphan_rate": round(len(graph.get("orphans", [])) / max(len(claims), 1), 3),
        "warrant_rate": round(sum(1 for e in sup if e.get("warrant_stated")) / max(len(sup), 1), 3),
        "leap_per_kchar": round(1000 * len(graph.get("leaps", [])) / max(chars, 1), 2),
        "depth": d,
        "has_main": bool(mains),
        "rebuttal": any(a.get("addressed") for a in graph.get("attacks", [])),
    }
    if gold_points:
        res["thesis_cov"] = round(len(set(gold_hits or [])) / len(gold_points), 3)
    return res
