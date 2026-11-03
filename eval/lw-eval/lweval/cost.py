"""成本估算（参数化）。价格单位：元/百万 tokens（缓存未命中输入, 输出）。
价格来源（2026-10-16 查询，可能变动）：DeepSeek 官方定价页（V4 系列峰谷定价，空闲=高峰半价）；阿里云百炼定价页。"""
PRICES = {
    "deepseek-v4-flash@idle": (1.5, 4.5), "deepseek-v4-flash@peak": (3.0, 9.0),
    "deepseek-v4-pro@idle": (4.5, 13.5), "deepseek-v4-pro@peak": (9.0, 27.0),
    "qwen3.7-plus@list": (2.0, 8.0), "qwen-plus@nonthinking": (0.8, 2.0),
}


def plan(n_tasks=120, n_arms=7, retrieval_frac=1 / 3, claims_per_doc=30,
         gen_in=15_000, gen_out=6_000, judges=2):
    outputs = n_tasks * n_arms
    items = {
        "generation": (outputs, gen_in, gen_out),
        # 每个消融臂 vs full：一对，两种顺序 × judges 家
        "pairwise_naturalness+style": (n_tasks * (n_arms - 1) * 2 * judges * 2, 6_000, 300),
        "logic_graph_extract": (outputs, 4_000, 1_500),
        "logic_checklist": (outputs * judges, 5_000, 800),
        "claim_extract": (int(outputs * retrieval_frac), 4_000, 1_500),
        "claim_verify": (int(outputs * retrieval_frac * claims_per_doc), 3_000, 200),
        "plan_compare": (int(outputs * retrieval_frac), 2_000, 400),
        "deletion_test(30%样本×10句)": (int(outputs * 0.3 * 10), 3_000, 80),
    }
    rows = {}
    for k, (calls, i, o) in items.items():
        rows[k] = {"calls": calls, "in_M": round(calls * i / 1e6, 2), "out_M": round(calls * o / 1e6, 2)}
    return rows


def price(rows, gen_model, judge_model):
    tot = {}
    for k, r in rows.items():
        pi, po = PRICES[gen_model if k == "generation" else judge_model]
        tot[k] = round(r["in_M"] * pi + r["out_M"] * po, 1)
    tot["TOTAL"] = round(sum(tot.values()), 1)
    return tot
