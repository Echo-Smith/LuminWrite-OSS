"""LLM 评审接口（原型中为桩，不调用任何模型、不需要 API Key）。

真实实现需：
 - 至少 2 家不同供应商的模型组成 ensemble（例如 DeepSeek + Qwen + GLM），与被测生成模型不同源；
 - 成对比较每对跑两次（A/B 与 B/A 互换），两次不一致记为平局；
 - 所有判定写入 judgments.jsonl（schema 见 schemas/judgment.schema.json），
   基础设施失败（超时/JSON 损坏/限流）写 status=infra_error，不计入写作失败。
"""
import json
import os

PROMPT_DIR = os.path.join(os.path.dirname(os.path.dirname(__file__)), "prompts")


def load_prompt(name: str) -> str:
    with open(os.path.join(PROMPT_DIR, name + ".md"), encoding="utf-8") as f:
        return f.read()


def render(name: str, **kw) -> str:
    t = load_prompt(name)
    for k, v in kw.items():
        t = t.replace("{{" + k + "}}", v if isinstance(v, str) else json.dumps(v, ensure_ascii=False))
    return t


class StubJudge:
    """占位评审：返回渲染后的提示词，标记 status=not_run。用于检查流水线与提示词。"""
    vendor = "stub"

    def pairwise(self, template, task, text_a, text_b, **kw):
        return {"status": "not_run", "vendor": self.vendor,
                "prompt": render(template, TASK=task, TEXT_A=text_a, TEXT_B=text_b, **kw)}

    def checklist(self, template, task, text, **kw):
        return {"status": "not_run", "vendor": self.vendor,
                "prompt": render(template, TASK=task, TEXT=text, **kw)}


def swap_consistent(first: str, second_swapped: str) -> str:
    """位置互换一致性：first ∈ {A,B,tie}（原顺序），second_swapped 为互换后的判定。
    一致则返回胜者（A/B），否则 tie。"""
    flip = {"A": "B", "B": "A", "tie": "tie"}
    return first if first == flip.get(second_swapped) else "tie"
