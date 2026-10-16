"""UPCR（Unrequested-Paragraph Change Rate，非请求段落改动率）与锚定编辑成功率。

算法：
 1. 按空行分段，规范化（去空白/Markdown 标记）后用 difflib 对段落序列做对齐；
 2. 对齐中的 replace 块内，再用字符级相似度做贪心一对一匹配（≥ MATCH_MIN 视为"同一段被改"）；
 3. 每个原段落状态 ∈ {unchanged(相似度≥UNCHANGED), modified, rewritten(同位整段重写), deleted}；未匹配的新段落为 inserted；
 4. 目标段落由 --target 指定（原文段落序号，0 起）或 --anchor（原文中包含该子串的段落）；
 UPCR          = 非目标原段落中 modified+deleted 的个数 / 非目标原段落总数
 UPCR_weighted = Σ_非目标 (1 - 相似度) / 非目标原段落总数   （删除记 1）
 insert_rate   = 非请求插入段数 / 非目标原段落总数（插入位置不紧邻目标段即视为非请求）
 anchored_success = 每个目标段均被修改，且满足 must_contain / must_not_contain（在其对应新段落上检查）
"""
import difflib
import re

from .textutil import paragraphs

UNCHANGED = 0.98
MATCH_MIN = 0.45


def _norm(p):
    p = re.sub(r"\s+", "", p)
    return p.replace("**", "").lstrip("#")


def _sim(a, b):
    return difflib.SequenceMatcher(None, _norm(a), _norm(b), autojunk=False).ratio()


def align(before, after):
    B, A = paragraphs(before), paragraphs(after)
    nb, na = [_norm(p) for p in B], [_norm(p) for p in A]
    sm = difflib.SequenceMatcher(None, nb, na, autojunk=False)
    mapping = {}  # before idx -> (after idx | None, sim)
    used_after = set()
    for tag, i1, i2, j1, j2 in sm.get_opcodes():
        if tag == "equal":
            for k in range(i2 - i1):
                mapping[i1 + k] = (j1 + k, 1.0)
                used_after.add(j1 + k)
        elif tag in ("replace", "delete", "insert"):
            cands = []
            for i in range(i1, i2):
                for j in range(j1, j2):
                    cands.append((_sim(B[i], A[j]), i, j))
            cands.sort(reverse=True)
            for s, i, j in cands:
                if s < MATCH_MIN:
                    break
                if i in mapping or j in used_after:
                    continue
                mapping[i] = (j, s)
                used_after.add(j)
            # 同一 replace 块内剩余的段落按位置配对，视为"整段重写"（相似度低于 MATCH_MIN）
            left_i = [i for i in range(i1, i2) if i not in mapping]
            left_j = [j for j in range(j1, j2) if j not in used_after]
            if tag == "replace":
                for i, j in zip(left_i, left_j):
                    mapping[i] = (j, _sim(B[i], A[j]))
                    used_after.add(j)
            for i in range(i1, i2):
                mapping.setdefault(i, (None, 0.0))
    inserted = [j for j in range(len(A)) if j not in used_after]
    return B, A, mapping, inserted


def upcr(before, after, targets=None, anchors=None, must_contain=None, must_not_contain=None):
    B, A, mapping, inserted = align(before, after)
    tset = set(targets or [])
    for a in anchors or []:
        for i, p in enumerate(B):
            if a in p:
                tset.add(i)
    rows = []
    for i, p in enumerate(B):
        j, s = mapping[i]
        status = ("deleted" if j is None else "unchanged" if s >= UNCHANGED
                  else "modified" if s >= MATCH_MIN else "rewritten")
        rows.append({"before_idx": i, "after_idx": j, "sim": round(s, 3), "status": status,
                     "requested": i in tset, "preview": p[:24]})
    non = [r for r in rows if not r["requested"]]
    changed = [r for r in non if r["status"] != "unchanged"]
    # 插入是否紧邻目标段（目标段对应新段落 ±1）
    target_after = {mapping[i][0] for i in tset if mapping[i][0] is not None}
    unreq_ins = [j for j in inserted if not any(abs(j - t) <= 1 for t in target_after)]
    denom = max(len(non), 1)
    succ = []
    for i in sorted(tset):
        j, s = mapping[i]
        ok = j is not None and s < UNCHANGED
        txt = A[j] if j is not None else ""
        for m in must_contain or []:
            ok = ok and (m in txt)
        for m in must_not_contain or []:
            ok = ok and (m not in txt)
        succ.append({"target": i, "success": ok, "sim": round(s, 3)})
    return {
        "n_before": len(B), "n_after": len(A), "targets": sorted(tset),
        "UPCR": round(len(changed) / denom, 3),
        "UPCR_weighted": round(sum(1 - r["sim"] for r in non) / denom, 3),
        "insert_rate": round(len(unreq_ins) / denom, 3),
        "anchored_success": all(x["success"] for x in succ) if succ else None,
        "targets_detail": succ, "rows": rows, "inserted_after_idx": inserted,
    }
