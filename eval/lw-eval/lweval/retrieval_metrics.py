"""检索规划指标（给定 LLM/人工判定后的确定性计算）。

输入：
  plan_cov   {gold_id: covered|partial|missed}，gold_critical {gold_id: bool}
  used_eids  Agent 在正文中实际引用到的证据单元 id 集合（由 claim_verify 的 best_source+quote 反查）
  gold_eids  来源包中标注为"本任务应使用"的证据单元集合
  verdicts   [{claim_id, verdict, cited: [sid], best_source}]（claim_verify 输出）
  queries    Agent 发出的检索查询数；useful_evidence = 被正文引用且 supported 的不同证据单元数
  sources    {sid: authority}
"""
import math
from collections import Counter

AUTH_W = {"primary_official": 1.0, "peer_reviewed": 1.0, "industry_data": 0.8, "major_media": 0.6,
          "expert_blog": 0.5, "self_media": 0.2, "marketing": 0.0}


def plan_coverage(plan_cov, gold_critical):
    def s(v):
        return {"covered": 1.0, "partial": 0.5}.get(v, 0.0)
    allc = sum(s(v) for v in plan_cov.values()) / max(len(plan_cov), 1)
    crit = [g for g, c in gold_critical.items() if c]
    critc = sum(s(plan_cov.get(g, "missed")) for g in crit) / max(len(crit), 1)
    return {"plan_coverage": round(allc, 3), "critical_coverage": round(critc, 3)}


def evidence_recall(used_eids, gold_eids):
    return round(len(set(used_eids) & set(gold_eids)) / max(len(set(gold_eids)), 1), 3)


def citation_metrics(verdicts):
    """precision：带引用的论断中，被所引来源 supported 的比例（partial 计 0.5）；
    recall：需要引用的事实型论断中，带了至少一个正确引用的比例；
    unsupported_rate：全部事实型论断中 unsupported/contradicted 的比例（AR-012 口径）。"""
    fact = [v for v in verdicts if v.get("type", "fact") != "opinion"]
    cited = [v for v in fact if v.get("cited")]
    sc = {"supported": 1.0, "partial": 0.5}
    prec = sum(sc.get(v["verdict"], 0) for v in cited if v.get("best_source") in v["cited"]) / max(len(cited), 1)
    rec = sum(1 for v in fact if v.get("cited") and v["verdict"] in ("supported", "partial")
              and v.get("best_source") in v["cited"]) / max(len(fact), 1)
    uns = sum(1 for v in fact if v["verdict"] in ("unsupported", "contradicted")) / max(len(fact), 1)
    return {"citation_precision": round(prec, 3), "citation_recall": round(rec, 3),
            "unsupported_rate": round(uns, 3), "n_fact_claims": len(fact)}


def source_quality(cited_sids, sources):
    """authority：被引来源权威度均值；diversity：被引出版方的归一化香农熵；distractor_rate 另算。"""
    if not cited_sids:
        return {"authority": 0.0, "diversity": 0.0}
    auth = sum(AUTH_W.get(sources[s]["authority"], 0) for s in cited_sids) / len(cited_sids)
    pubs = Counter(sources[s].get("publisher", s) for s in cited_sids)
    n = sum(pubs.values())
    H = -sum(c / n * math.log(c / n) for c in pubs.values())
    div = H / math.log(len(pubs)) if len(pubs) > 1 else 0.0
    return {"authority": round(auth, 3), "diversity": round(div, 3)}


def distractor_rate(cited_sids, distractors):
    return round(len(set(cited_sids) & set(distractors)) / max(len(set(cited_sids)), 1), 3)


def planning_efficiency(n_queries, useful_evidence):
    """每条有用证据所需查询数（越低越好）；无有用证据记为 inf。"""
    return round(n_queries / useful_evidence, 2) if useful_evidence else float("inf")


def ablation_sensitivity(full_cov, dropped_cov, admitted_gap):
    """移除关键来源扰动：好的规划应 (1) 覆盖率下降（说明确实依赖该来源），(2) 正文明确承认证据缺口（admitted_gap=True），
    而不是编造。返回 'honest_gap' / 'fabricated_fill' / 'insensitive'。"""
    if dropped_cov < full_cov and admitted_gap:
        return "honest_gap"
    if not admitted_gap and dropped_cov >= full_cov:
        return "fabricated_fill"
    return "insensitive"
