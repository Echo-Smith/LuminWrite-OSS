"""风格包遵循度（规则版，原型）。正式版用 pairwise_style judge；此处只做可解释的必需手法检查。

印月三谈风格包的必需手法见 docs/06-evaluation.md 中的风格评测示例：
  argument_pattern "首在-重在-贵在"、required_metaphor、required_parallelism、required_rhetorical_question。
注意：遵循度只回答"像不像这个风格"，不回答"好不好"；好坏由说人话轴（空洞度）和逻辑/事实轴回答。
"""
import re

from .aitone import AIToneDetector
from .textutil import strip_markdown

_det = AIToneDetector()

METAPHOR_RE = re.compile(
    r"如同|犹如|宛如|好比|仿佛|恰似|像[^，。]{1,12}一样"
    r"|(?:^|[。！？，])[\u4e00-\u9fff]{1,4}是[\u4e00-\u9fff]{1,3}，[\u4e00-\u9fff]{1,4}是[\u4e00-\u9fff]{1,3}[。！]"
    r"|不是[^，。]{1,12}的[“\"][^”\"]{1,6}[”\"]")

PACKS = {
    "yinyue": {
        "label": "印月三谈（评论/杂文：递进格 + 比喻 + 排比 + 设问）",
        "checks": ["frame_shou_zhong_gui", "metaphor", "parallelism", "rhetorical_question"],
    },
    "plain_professional": {
        "label": "专业平实风（默认推荐）",
        "checks": ["no_ordinal_template", "no_format_show", "concrete"],
    },
}


def check(text: str, pack: str = "yinyue") -> dict:
    plain = strip_markdown(text)
    rep = _det.analyze(text)
    cats = {h["category"] for h in rep.hits}
    res = {}
    for c in PACKS[pack]["checks"]:
        if c == "frame_shou_zhong_gui":
            res[c] = all(x in plain for x in ("首在", "重在", "贵在"))
        elif c == "metaphor":
            res[c] = bool(METAPHOR_RE.search(plain)) or "cliche_metaphor" in cats
        elif c == "parallelism":
            res[c] = bool(cats & {"parallel_clause_run", "parallel_paragraph_openers"})
        elif c == "rhetorical_question":
            res[c] = "rhetorical_q" in cats or bool(re.search(r"[^。！？]{4,}[吗呢][？?]", plain))
        elif c == "no_ordinal_template":
            res[c] = "ordinal_sequence" not in cats
        elif c == "no_format_show":
            res[c] = not (cats & {"bold", "emoji"})
        elif c == "concrete":
            res[c] = rep.concreteness >= 1.0
    return {"pack": pack, "adherence": round(sum(res.values()) / len(res), 3), "checks": res}
