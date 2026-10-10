# PROVENANCE

- **来源**：移植自 MiMo 桌面端（Xiaomi MiMo）内置技能 `research-paper-writing`
  （本机分发于 `~/.local/share/mimocode/builtin_skills/`，2026-10 提取）。
- **许可证状态**：原始文件未附带 LICENSE（包内 `docx-official` 等相邻技能为 Apache 2.0，
  本技能无同等声明）。
- **改写范围**：
  - `SKILL.md` —— 方法论保留，中文重写，并新增「本平台适配说明」（与写作 agent
    标准链路的衔接、LaTeX 环境缺失时的降级行为）；
  - `references/`（9 份章节指南 + 33 份带批注示例）—— **英文原件保留**：示例为真实
    论文摘录的批注教学材料（InstantNGP、Neural Body 等），翻译或改写会破坏其教学价值。
- **公开前待办**：若本仓库公开发布，需先复核 `references/` 原件的可分发许可；无法确认时，
  可仅保留 SKILL.md 与自撰章节指南、移除示例库。
