"""风格包遵循度（规则版，原型 v0.3）。正式版用 pairwise_style judge；这里只做可解释的确定性检查。

v0.3 按"文风 × 文体"拆分：
  - 文风包（voice）只管怎么说话：修辞手法的**密度区间**（每千字次数）、用词、格式习惯。
    密度落在区间内得 1 分；低于下限按比例给分；高于上限也按比例扣分（修辞过密同样不像）。
  - 文体包（genre）只管文章长什么样：结构骨架、篇幅区间、标题长度、分节方式。
  - 两者分开打分：voice_adherence（文风遵循）和 genre_conformance（文体合规），任意文风可配任意文体。
「印月三谈」是纯文风包：比喻、排比、设问按密度区间判定，不再要求"必须出现"；
"首在-重在-贵在"递进格是**可选手法**：只记录是否使用，不计入得分（不加分也不扣分）。
原先绑定在印月上的篇幅、三段式、标题长度等文体约束，移到 GENRE_PACKS["commentary"]。
密度区间和文体阈值是原型初值，未经语料校准。
注意：遵循度只回答"像不像"，不回答"好不好"；好坏由说人话轴（空洞度）和逻辑/事实轴回答。
"""
import re

from .aitone import AIToneDetector
from .textutil import count_chars, sentences, strip_markdown

_det = AIToneDetector()

METAPHOR_RE = re.compile(
    r"如同|犹如|宛如|好比|仿佛|恰似|像[^，。]{1,12}一样"
    r"|(?:^|[。！？，])[\u4e00-\u9fff]{1,4}是[\u4e00-\u9fff]{1,3}，[\u4e00-\u9fff]{1,4}是[\u4e00-\u9fff]{1,3}[。！]"
    r"|不是[^，。]{1,12}的[“\"][^”\"]{1,6}[”\"]")

# 文风包：devices = {手法: (每千字下限, 上限)}；flags = 布尔型习惯检查；optional = 可选手法（只记录，不计分）
VOICE_PACKS = {
    "yinyue": {
        "label": "印月三谈（文风：比喻、排比、设问，修辞承载具体信息；不绑定文体）",
        "devices": {"metaphor": (2.0, 15.0), "parallelism": (2.0, 15.0), "rhetorical_question": (1.5, 12.0)},
        "flags": [],
        "optional": ["progression"],
    },
    "plain_professional": {
        "label": "专业平实风（默认推荐）",
        "devices": {"metaphor": (0.0, 3.0), "parallelism": (0.0, 3.0), "rhetorical_question": (0.0, 3.0)},
        "flags": ["no_ordinal_template", "no_format_show", "concrete"],
        "optional": [],
    },
}
PACKS = VOICE_PACKS  # 兼容 v0.2 的名字

# 文体包：只放结构、篇幅、标题、分节类检查。core = 骨架检查：骨架不对就不是这个文体，合规分封顶 0.5
GENRE_PACKS = {
    "commentary": {
        "label": "评论（开头-主体-结尾三段式，散文式论证，不分节）",
        "length": (300, 1500),   # 原型的短篇区间；正式版按 length_profiles 配置（如 1000-1500）
        "title_len": (6, 24),
        "checks": ["length", "title_len", "three_part", "prose_no_sections", "short_closing", "no_lists"],
        "core": ["three_part", "prose_no_sections"],
    },
    "report": {
        "label": "调研报告（交代方法与局限、分节、有结论）",
        "length": (300, 3000),
        "title_len": (6, 30),
        "checks": ["length", "title_len", "sectioned", "method_disclosed", "scope_limited", "has_conclusion"],
        "core": ["sectioned", "has_conclusion"],
    },
}

LABEL_RE = re.compile(r"^(?:#{2,}\s*\S+|[一二三四五六七八九十]+、\S+|[\u4e00-\u9fff]{2,8}[。：:])")
CONCLUSION_RE = re.compile(r"判断|结论|建议|结语|对策")
METHOD_RE = re.compile(r"样本|调研|走访|访谈|聊过|跑了|问卷|数据来源")
LIMIT_RE = re.compile(r"样本小|只能说明|局限|不代表|仅代表")


def _density(n, chars):
    return round(n * 1000 / max(chars, 1), 2)


def _range_score(d, lo, hi):
    if lo <= d <= hi:
        return 1.0
    if d < lo:
        return round(d / lo, 3) if lo > 0 else 1.0
    return round(hi / d, 3) if d > 0 else 1.0


def device_counts(text: str) -> dict:
    plain = strip_markdown(text)
    rep = _det.analyze(text)
    cnt = {}
    for h in rep.hits:
        cnt[h["category"]] = cnt.get(h["category"], 0) + 1
    questions = sum(1 for s in sentences(plain) if s.rstrip().endswith(("？", "?")))
    return {
        "metaphor": len(METAPHOR_RE.findall(plain)) + cnt.get("cliche_metaphor", 0),
        "parallelism": cnt.get("parallel_clause_run", 0) + cnt.get("parallel_paragraph_openers", 0),
        "rhetorical_question": max(questions, cnt.get("rhetorical_q", 0)),
        "_report": rep, "_cats": set(cnt), "_chars": count_chars(plain), "_plain": plain,
    }


def check_voice(text: str, voice: str = "yinyue") -> dict:
    pack = VOICE_PACKS[voice]
    dc = device_counts(text)
    chars, cats, rep, plain = dc["_chars"], dc["_cats"], dc["_report"], dc["_plain"]
    scores, densities = {}, {}
    for dev, (lo, hi) in pack["devices"].items():
        densities[dev] = _density(dc[dev], chars)
        scores[dev] = _range_score(densities[dev], lo, hi)
    for f in pack["flags"]:
        if f == "no_ordinal_template":
            scores[f] = float("ordinal_sequence" not in cats)
        elif f == "no_format_show":
            scores[f] = float(not (cats & {"bold", "emoji"}))
        elif f == "concrete":
            scores[f] = float(rep.concreteness >= 1.0)
    optional = {}
    for o in pack["optional"]:
        if o == "progression":  # 首在-重在-贵在：可选，只记录
            optional[o] = all(x in plain for x in ("首在", "重在", "贵在"))
    return {"voice": voice, "voice_adherence": round(sum(scores.values()) / len(scores), 3),
            "checks": scores, "per1k": densities, "optional_devices": optional}


def _paras(text):
    body = [p.strip() for p in re.split(r"\n\s*\n", text) if p.strip()]
    title = body[0].lstrip("#").strip() if body and body[0].startswith("# ") else ""
    if title:
        body = body[1:]
    return title, body


def check_genre(text: str, genre: str) -> dict:
    pack = GENRE_PACKS[genre]
    title, paras = _paras(text)
    chars = count_chars(strip_markdown(text))
    labels = [p for p in paras if LABEL_RE.match(p)]
    res = {}
    for c in pack["checks"]:
        if c == "length":
            lo, hi = pack["length"]
            res[c] = lo <= chars <= hi
        elif c == "title_len":
            lo, hi = pack["title_len"]
            res[c] = bool(title) and lo <= count_chars(title) <= hi
        elif c == "three_part":
            res[c] = len(paras) >= 4
        elif c == "prose_no_sections":
            # 评论里偶有一两段以短句开头（"这不是个例。"），连续三段以上才算分节
            res[c] = len(labels) <= 2 and not re.search(r"^#{2,}\s", text, flags=re.M)
        elif c == "short_closing":
            mid = [count_chars(p) for p in paras[1:-1]]
            res[c] = bool(mid) and count_chars(paras[-1]) <= sum(mid) / len(mid)
        elif c == "no_lists":
            res[c] = not re.search(r"^\s*(?:[-*]|\d+[.、])\s", text, flags=re.M)
        elif c == "sectioned":
            res[c] = len(labels) >= 3
        elif c == "method_disclosed":
            res[c] = bool(paras) and bool(re.search(r"\d", paras[0])) and bool(METHOD_RE.search(paras[0]))
        elif c == "scope_limited":
            res[c] = bool(LIMIT_RE.search(text))
        elif c == "has_conclusion":
            res[c] = any(CONCLUSION_RE.search(p[:12]) for p in labels)
    conf = sum(res.values()) / len(res)
    core_ok = all(res[c] for c in pack["core"])
    if not core_ok:
        conf = min(conf, 0.5)
    return {"genre": genre, "genre_conformance": round(conf, 3), "core_ok": core_ok, "checks": res}


def score(text: str, voice: str, genre: str) -> dict:
    """两项分开报告，不合成一个总分。"""
    v, g = check_voice(text, voice), check_genre(text, genre)
    return {"voice": voice, "genre": genre, "voice_adherence": v["voice_adherence"],
            "genre_conformance": g["genre_conformance"], "voice_detail": v, "genre_detail": g}


def check(text: str, pack: str = "yinyue") -> dict:
    """v0.2 兼容接口：只返回文风遵循度。"""
    v = check_voice(text, pack)
    return {"pack": pack, "adherence": v["voice_adherence"], "checks": v["checks"],
            "per1k": v["per1k"], "optional_devices": v["optional_devices"]}


def legacy_required_v02(text: str) -> float:
    """仅供 DEMO 对照：v0.2 的"必需手法"判法（首在-重在-贵在 + 比喻 + 排比 + 设问，四项全有才满分）。v0.3 不再使用。"""
    plain = strip_markdown(text)
    cats = {h["category"] for h in _det.analyze(text).hits}
    res = [all(x in plain for x in ("首在", "重在", "贵在")),
           bool(METAPHOR_RE.search(plain)) or "cliche_metaphor" in cats,
           bool(cats & {"parallel_clause_run", "parallel_paragraph_openers"}),
           "rhetorical_q" in cats or bool(re.search(r"[^。！？]{4,}[吗呢][？?]", plain))]
    return round(sum(res) / len(res), 3)
