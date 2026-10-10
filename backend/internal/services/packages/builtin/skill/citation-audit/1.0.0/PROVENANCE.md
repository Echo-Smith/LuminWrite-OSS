# PROVENANCE

- **来源**：从 MiMo 桌面端（Xiaomi MiMo）内置技能 `super-research`（本机分发于
  `~/.local/share/mimocode/builtin_skills/`，2026-10 提取）中**剥离引用校验（citation
  audit）部分**独立成包。super-research 的其余模式（实验循环/基准对比/根因排查/消融/
  论文复现）面向代码实验工作流，与写作 agent 无关；主题调研模式与已内置的
  `deep-research` 重叠，均未收录。
- **许可证状态**：super-research 未附带 LICENSE（README 自述灵感来自 Karpathy 的
  autoresearch 方法论）。
- **改写范围**：
  - `SKILL.md` —— 仅取论文写作模式的 Part B（引用审计）+ 修复矩阵，中文重写，
    独立成完整工作流（原版需依附写作模式）；
  - `references/api-cheatsheet.md` —— 端点事实保留，中文重写；
  - `scripts/verify_citation.py`、`scripts/paper_search.py` —— 基于原脚本适配：
    移除一切落盘写文件（`--out`），只输出 stdout；补 SSRF 防护（协议白名单 +
    解析 IP 公网校验 + 重定向逐跳校验）、XML DTD/实体拒绝、响应大小上限；
    http 升级 https；UA 换为本包标识。未收录 `fetch_paper.py`（全文抓取由平台
    `read_source` 承担，且其 LaTeX tar 解包有 zip-slip 风险面）。
- **公开前待办**：若本仓库公开发布，需复核原脚本可分发许可；无法确认时可仅保留
  SKILL.md + api-cheatsheet，改用 curl 瀑布手工核验（见 api-cheatsheet「按需选源」）。
