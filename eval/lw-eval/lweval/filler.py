"""中性负对照：合成"模板填充"空话文本（与任何风格包无关）。

用途：每次运行都加入一个 filler 臂（同一任务主题的模板空话），它在"说人话"轴上必须排末位，
否则说明评测（检测器或 judge）失效，本次运行作废。模板刻意不含任何具体信息，也不模仿任何特定文体。
"""
import random

TEMPLATES = [
    "{T}是当前社会普遍关注的重要话题。",
    "随着{T}的不断发展，其重要性日益凸显。",
    "在新的形势下，{T}面临着新的机遇和挑战。",
    "首先，{T}有助于提升整体效率，具有重要的现实意义。",
    "其次，{T}能够促进多方协同，为相关领域注入新动能。",
    "最后，{T}对于推动高质量发展具有深远影响。",
    "值得注意的是，{T}在推进过程中仍存在一些不容忽视的问题。",
    "一方面，相关机制有待进一步完善；另一方面，各方认识仍需不断提升。",
    "因此，需要从多个角度综合考虑，全方位、多维度地推进{T}。",
    "只有坚持问题导向，持续推进，才能实现{T}的长远发展。",
    "总的来说，{T}前景广阔、潜力巨大。",
    "我们有理由相信，{T}一定会迈上新台阶。",
]


def generate(topic: str, n_paras: int = 5, seed: int = 0, title: bool = True) -> str:
    rnd = random.Random(seed)
    body = [t.format(T=topic) for t in TEMPLATES]
    # 保持"开头-中段-结尾"的模板结构，只打乱中段句子分配
    head, mid, tail = body[:3], body[3:10], body[10:]
    rnd.shuffle(mid)
    chunks = [head] + [mid[i::n_paras - 2] for i in range(n_paras - 2)] + [tail]
    paras = ["".join(c) for c in chunks if c]
    out = ("# 关于" + topic + "的思考\n\n" if title else "") + "\n\n".join(paras) + "\n"
    return out
