# PROVENANCE

- **来源**：移植自 MiMo 桌面端（Xiaomi MiMo）内置技能 `arxiv`（本机分发于
  `~/.local/share/mimocode/builtin_skills/`，2026-10 提取），上游版本 2.0.0。
- **许可证**：MIT（原 SKILL.md frontmatter 声明 `license: MIT`）。SKILL.md 正文
  英文原件保留，另加中文 frontmatter 与「平台适配」小节；`scripts/arxiv.py`
  原件保留并做如下加固。
- **与上游的差异（安全加固，功能等价）**：
  - **移除 `download` 子命令与 `--dest` 参数**：服务端技能不直接落盘写文件；
    PDF 全文改用平台 `read_source` 读 HTML 版或走平台文件导入链路（docreader）；
  - 新增 SSRF 防护：仅允许 http/https；解析目标主机并拒绝私网/环回/链路本地/
    保留地址；重定向逐跳重新校验；
  - XML 解析前全量拒绝 `<!DOCTYPE`/`<!ENTITY` 声明并限制响应大小（实体扩展防护）；
  - arXiv ID / 分类参数按严格白名单正则校验后才拼入 URL；API 返回的 ID/版本号
    在进入任何 URL 前二次校验；
  - User-Agent 换为本包标识。
