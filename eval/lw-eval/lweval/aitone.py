"""说人话 / AI 腔检测器（规则 + 词表，可解释）。

v0.2 关键变化：区分"有意的修辞"与"空话套话"。
  每条命中带 role：hollow（空话套话，全额计分）/ rhetoric（排比、比喻、设问、首在-重在-贵在等，
  按所在段落的具体性折减 w_eff = w / (1 + c_para)，c_para = 该段每百字具体锚点数）/ format（不折减）。
  理由：修辞承载真实内容时是风格，不是腔调；同样的修辞出现在零信息段落里才是"AI 味"。

输出：
  aitone_score   0-100，越高越"AI 腔"（=100*(1-exp(-raw/K))，raw = hollow + 折减后 rhetoric + format 的每千字密度）
  hollow_raw     仅空话套话类的每千字加权密度
  rhetoric_raw   修辞类未折减的每千字加权密度（风格特征：反映修辞浓度，不代表好坏）
  rhetoric_gated 修辞类折减后的每千字加权密度
  concreteness   每百字"具体锚点"数（数字、量词短语、引语、专名、实例标记）
  hollow_ratio   空话句占比：无具体锚点 且 命中至少一条 AI 腔规则 的句子比例
  deletable_ratio 可删句占比（启发式代理）：无锚点且内容二元组被全文其余部分覆盖 ≥ 阈值
  hits           每条命中的类别、文本、权重，便于人工复核
"""
import json
import math
import os
import re
import statistics
from dataclasses import dataclass, field, asdict

from .textutil import (strip_markdown, paragraphs, sentences, clauses, count_chars,
                       content_bigrams)

LEXICON_PATH = os.path.join(os.path.dirname(__file__), "lexicon", "aitone_zh.json")
K_SCALE = 40.0  # raw=40 时得分约 63；raw=0 → 0

_NUM_UNIT = r"[一二三四五六七八九十百千万两几半]+(?:多)?(?:个|斤|家|位|天|年|月|周|小时|分钟|公里|度|元|块|次|人|岁|倍|成|档|匹|台|条|辆|户|亩|吨|页|篇|晚|趟|来回)"
ANCHOR_PATTERNS = {
    "number": re.compile(r"\d+(?:\.\d+)?\s*(?:%|％|万|亿|千|元|度|公里|km|个|家|位|天|年|月|日|号|周|小时|分钟|斤|匹|人|辆|档)?"),
    "num_unit_cn": re.compile(_NUM_UNIT),
    # 引语：仅计"说/问/讲/写着/称 + 引号"的直接引语；修辞性的引号（如"如椽之笔"）不计
    "quote": re.compile(r'(?:说|问|讲|道|称|写着|回答)[：:，,]?\s*[“"「][^”"」]{2,60}[”"」]'),
    "latin": re.compile(r"[A-Za-z]{2,}"),
    "title": re.compile(r"《[^》]{1,30}》"),
    "example": re.compile(r"比如|例如|举个例子|拿[^，。]{1,8}来说"),
}

EMOJI_RE = re.compile("[\U0001F300-\U0001FAFF\u2600-\u27BF\u2B50\u2B06\u2194-\u21AA]")
IDIOM_CHAIN_RE = re.compile(r"(?:[\u4e00-\u9fff]{2,4}、){2,}[\u4e00-\u9fff]{2,4}")


def load_lexicon(path: str = LEXICON_PATH):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


@dataclass
class Hit:
    category: str
    text: str
    weight: float
    role: str = "hollow"
    para: int = -1
    eff_weight: float = None


@dataclass
class AIToneReport:
    chars: int
    aitone_raw: float
    aitone_score: float
    hollow_raw: float
    rhetoric_raw: float
    rhetoric_gated: float
    concreteness: float
    hollow_ratio: float
    deletable_ratio: float
    category_density: dict
    hits: list = field(default_factory=list)

    def to_dict(self, max_hits: int = 40):
        d = asdict(self)
        d["hits"] = d["hits"][:max_hits]
        return d


def _compile_category(cat):
    pats = []
    for it in cat["items"]:
        pats.append(re.compile(it if cat.get("regex") else re.escape(it)))
    return pats


class AIToneDetector:
    def __init__(self, lexicon=None, coverage_threshold: float = 0.6, gate_rhetoric: bool = True):
        """gate_rhetoric=False 复现 v0.1 行为（修辞与空话同等计分），用于校准对比。"""
        self.gate_rhetoric = gate_rhetoric
        self.lex = lexicon or load_lexicon()
        self.cats = {name: (cat, _compile_category(cat)) for name, cat in self.lex["categories"].items()}
        self.rules = self.lex["structure_rules"]
        self.coverage_threshold = coverage_threshold

    # ---------- 基础命中（逐段，便于按段落具体性折减修辞） ----------
    def _paras(self, text):
        return [strip_markdown(p) for p in paragraphs(text)]

    def lexical_hits(self, text, para_idx=-1):
        plain = strip_markdown(text)
        hits = []
        for name, (cat, pats) in self.cats.items():
            for p in pats:
                for m in p.finditer(plain):
                    hits.append(Hit(name, m.group(0), cat["weight"], cat.get("role", "hollow"), para_idx))
        return hits

    def _rule(self, name):
        r = self.rules[name]
        return r["weight"], r.get("role", "hollow")

    def structure_hits(self, text):
        r = self.rules
        hits = []
        paras = self._paras(text)
        plain = "\n".join(paras)
        # 1) 序数模板（文档级，hollow）
        found = sorted({m.group(1) for m in re.finditer(
            r"(?:^|[。！？；，：\n])\s*(" + "|".join(r["ordinal_sequence"]["markers"]) + r")(?=[，、,:：])", plain, flags=re.M)})
        if len(found) >= r["ordinal_sequence"]["min_distinct"]:
            w, role = self._rule("ordinal_sequence")
            hits.append(Hit("ordinal_sequence", "/".join(found), w, role, -1))
        mp = r["parallel_clause_run"]["min_prefix"]
        for pi, para in enumerate(paras):
            # 2) 排比分句（段内）
            for s_ in sentences(para):
                cl = [c for c in clauses(s_) if len(c) >= mp + 2]
                run = 1
                for a, b in list(zip(cl, cl[1:])) + [(None, None)]:
                    if a is not None and a[:mp] == b[:mp]:
                        run += 1
                        continue
                    if run >= r["parallel_clause_run"]["min_run"]:
                        w, role = self._rule("parallel_clause_run")
                        hits.append(Hit("parallel_clause_run", s_[:40], w, role, pi))
                    run = 1
            # 4) 顿号堆砌（段内）
            for m in IDIOM_CHAIN_RE.finditer(para):
                if len(m.group(0).split("、")) >= r["idiom_chain"]["min_items"]:
                    w, role = self._rule("idiom_chain")
                    hits.append(Hit("idiom_chain", m.group(0), w, role, pi))
            for _ in re.finditer(r"[！!]", para):
                w, role = self._rule("exclaim")
                hits.append(Hit("exclaim", "！", w, role, pi))
        # 3) 排比段落（文档级：按这些段落的平均具体性折减）
        po = r["parallel_paragraph_openers"]
        heads = {}
        for pi, p in enumerate(paras):
            if len(p) >= po["min_prefix"]:
                heads.setdefault(p[:po["min_prefix"]], []).append(pi)
        for h, idx in heads.items():
            if len(idx) >= po["min_run"]:
                w, role = self._rule("parallel_paragraph_openers")
                hits.append(Hit("parallel_paragraph_openers", f"{h}…×{len(idx)}", w, role, tuple(idx)))
        # 5) 格式（format，不折减）
        for m in re.finditer(r"\*\*[^*]+\*\*", text):
            w, role = self._rule("bold")
            hits.append(Hit("bold", m.group(0)[:20], w, role, -1))
        for m in EMOJI_RE.finditer(text):
            w, role = self._rule("emoji")
            hits.append(Hit("emoji", m.group(0), w, role, -1))
        # 6) 段落均匀度（仅诊断）
        up = r["uniform_paragraphs"]
        lens = [count_chars(p) for p in paras if count_chars(p) >= 30]
        if len(lens) >= up["min_paragraphs"]:
            cv = statistics.pstdev(lens) / (statistics.mean(lens) or 1)
            if cv < up["cv_below"]:
                w, role = self._rule("uniform_paragraphs")
                hits.append(Hit("uniform_paragraphs", f"cv={cv:.2f}", w, role, -1))
        return hits

    # ---------- 具体性 ----------
    IDIOM_STOP = re.compile(r"千家万户|一篇大文章|一系列|一蹴而就|一池春水|一方面|另一方面|第一资源")

    @staticmethod
    def anchors(text):
        plain = AIToneDetector.IDIOM_STOP.sub("", strip_markdown(text))
        found = []
        for name, p in ANCHOR_PATTERNS.items():
            for m in p.finditer(plain):
                found.append((name, m.group(0)))
        return found

    def para_concreteness(self, text):
        out = []
        for p in self._paras(text):
            n = max(count_chars(p), 1)
            out.append(100.0 * len(self.anchors(p)) / n)
        return out

    def analyze(self, text: str) -> AIToneReport:
        n = max(count_chars(strip_markdown(text)), 1)
        paras = self._paras(text)
        conc_p = self.para_concreteness(text)
        hits = []
        for pi, p in enumerate(paras):
            hits += self.lexical_hits(p, pi)
        hits += self.structure_hits(text)
        k = n / 1000.0
        hollow = rhet = rhet_g = fmt = 0.0
        for h in hits:
            if h.role == "rhetoric":
                if isinstance(h.para, tuple):
                    c = statistics.mean(conc_p[i] for i in h.para)
                elif h.para >= 0:
                    c = conc_p[h.para]
                else:
                    c = 0.0
                h.eff_weight = round(h.weight / (1.0 + c), 3) if self.gate_rhetoric else h.weight
                rhet += h.weight
                rhet_g += h.eff_weight
            else:
                h.eff_weight = h.weight
                if h.role == "format":
                    fmt += h.weight
                else:
                    hollow += h.weight
        raw = (hollow + rhet_g + fmt) / k
        score = 100.0 * (1 - math.exp(-raw / K_SCALE))
        dens = {}
        for h in hits:
            if h.weight > 0:
                dens[h.category] = dens.get(h.category, 0.0) + 1
        dens = {c: round(v / k, 2) for c, v in sorted(dens.items())}
        conc = 100.0 * len(self.anchors(text)) / n
        sents = sentences(text)
        hollow_s = deletable = 0
        for i, s_ in enumerate(sents):
            has_anchor = bool(self.anchors(s_))
            if not has_anchor and any(h.role == "hollow" for h in self.lexical_hits(s_)):
                hollow_s += 1
            if not has_anchor:
                g = content_bigrams(s_)
                rest = content_bigrams("".join(sents[:i] + sents[i + 1:]))
                if g and len(g & rest) / len(g) >= self.coverage_threshold:
                    deletable += 1
        ns = max(len(sents), 1)
        return AIToneReport(chars=n, aitone_raw=round(raw, 2), aitone_score=round(score, 1),
                            hollow_raw=round(hollow / k, 2), rhetoric_raw=round(rhet / k, 2),
                            rhetoric_gated=round(rhet_g / k, 2),
                            concreteness=round(conc, 2), hollow_ratio=round(hollow_s / ns, 3),
                            deletable_ratio=round(deletable / ns, 3), category_density=dens,
                            hits=[asdict(h) for h in hits])


# ---------- 扰动：注入 AI 腔短语，检验检测器灵敏度 ----------
INJECTIONS = [
    "值得注意的是，这一点具有重要的现实意义。",
    "总而言之，前景广阔、未来可期。",
    "我们要以问题为导向，全方位、多维度、立体化推进。",
    "这既是机遇也是挑战，需要多方协同发力。",
    "随着技术的不断发展，相关领域迎来了新的增长极。",
    "唯有久久为功，方能行稳致远。",
    "让我们携手共进，奋力书写崭新篇章！",
    "在一定程度上，这或许并非绝对，仅供参考。",
]


def inject(text: str, k: int, seed: int = 0) -> str:
    """在句末确定性地插入 k 条 AI 腔短语（按 seed 轮换位置）。"""
    import random
    rnd = random.Random(seed)
    sents = re.split(r"(?<=[。！？])", text)
    sents = [s for s in sents if s]
    for j in range(k):
        pos = rnd.randrange(1, len(sents) + 1)
        sents.insert(pos, INJECTIONS[j % len(INJECTIONS)])
    return "".join(sents)
