"""中文文本基础工具：去 Markdown、分段、分句、计字、二元组。"""
import re

CJK = r"\u4e00-\u9fff"
_CJK_RE = re.compile(f"[{CJK}]")
_COUNT_RE = re.compile(f"[{CJK}A-Za-z0-9]")


def strip_markdown(text: str) -> str:
    t = re.sub(r"^\s{0,3}#{1,6}\s*", "", text, flags=re.M)
    t = t.replace("**", "").replace("__", "")
    t = re.sub(r"^\s*(?:[-*+]|\d+[.、)])\s+", "", t, flags=re.M)
    return t


def paragraphs(text: str, keep_markdown: bool = True):
    """按空行分段；单行标题也算一段。"""
    paras = [p.strip() for p in re.split(r"\n\s*\n", text.strip()) if p.strip()]
    return paras if keep_markdown else [strip_markdown(p) for p in paras]


_SENT_SPLIT = re.compile(r"(?<=[。！？!?；;])|\n+")


def sentences(text: str):
    t = strip_markdown(text)
    out = []
    for s in _SENT_SPLIT.split(t):
        s = s.strip()
        if s and count_chars(s) > 0:
            out.append(s)
    return out


def clauses(sentence: str):
    return [c.strip() for c in re.split(r"[，,；;。！？!?：:]", sentence) if c.strip()]


def count_chars(text: str) -> int:
    """计数：汉字 + 字母数字（不含标点、空白）。"""
    return len(_COUNT_RE.findall(text))


def cjk_count(text: str) -> int:
    return len(_CJK_RE.findall(text))


STOP_CHARS = set("的了是在和与及就也都而但把被让给对从向这那其之以于为个们有着过地得很更最又才却并或")


def content_bigrams(text: str):
    chars = [c for c in strip_markdown(text) if _CJK_RE.match(c)]
    grams = set()
    for a, b in zip(chars, chars[1:]):
        if a in STOP_CHARS and b in STOP_CHARS:
            continue
        grams.add(a + b)
    return grams


def jaccard(a: set, b: set) -> float:
    if not a and not b:
        return 0.0
    return len(a & b) / len(a | b)
