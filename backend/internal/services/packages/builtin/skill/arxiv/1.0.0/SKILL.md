---
name: arxiv
description: "arXiv 论文的检索、阅读、引用、追踪与下载：按主题/作者/分类/arXiv ID 搜索；取摘要或完整元数据；生成 BibTeX 引用；下载 PDF；列出领域最新提交（如 cs.AI 每日新论文）；查论文引用影响力；找引用者、参考文献或相关论文推荐。触发于提到 'arXiv'、arXiv ID（如 2601.02780、hep-th/0601001）、arxiv.org 链接、'论文检索'、'文献综述'、'找关于X的论文'、'引用这篇论文'、'cs.LG 有什么新文章'。"
description_zh: arXiv 文献检索与引用：搜索/元数据/BibTeX/下载/引用图，免费 API 零密钥。
description_en: Find, read, cite, track, download, and analyze academic papers on arXiv via the free arXiv and Semantic Scholar APIs.
version: 1.0.0
author: LuminBuddy (upstream skill, MIT)
license: MIT
tags: [arxiv, papers, 论文, literature-review, bibtex, citations]
---

# arXiv Research（arXiv 文献检索）

Search, read, cite, and analyze academic papers using the free arXiv API and Semantic Scholar API. No API keys, no dependencies — the bundled script uses only the Python stdlib.

## 平台适配

- 原文的 `webfetch` 工具在本平台对应 `read_source`（网页全文读取）；其他 agent 环境对应 WebFetch 类工具。
- 脚本调用方式：`python3 scripts/arxiv.py <子命令>`；脚本仅需 Python 3 标准库，无需安装依赖。
- 下载的 PDF 如需抽取文本，走平台已有的文件解析链路（docreader）或 PDF 处理技能。

## Quick Start: the `arxiv.py` script

Prefer `scripts/arxiv.py` over raw `curl` — it handles Atom XML parsing, versioned IDs, withdrawn-paper detection, and produces clean readable (or `--json`) output.

| Goal | Command |
|------|---------|
| Search papers | `python3 scripts/arxiv.py search "GRPO reinforcement learning" --max 10 --sort date` |
| Filtered search | `python3 scripts/arxiv.py search "attention" --author vaswani --category cs.CL` |
| Full metadata + abstract | `python3 scripts/arxiv.py get 2601.02780,1706.03762` |
| BibTeX citation | `python3 scripts/arxiv.py bibtex 2601.02780` |
| Latest in a category | `python3 scripts/arxiv.py new cs.CL --max 10` |
| Who cites this paper | `python3 scripts/arxiv.py cites 2601.02780 --max 20` |
| What this paper cites | `python3 scripts/arxiv.py refs 2601.02780` |
| Related-paper recommendations | `python3 scripts/arxiv.py similar 2601.02780` |
| Machine-readable output | append `--json` to any command |

Common flags: `--max N` (result count), `--sort relevance|date|updated`, `--start N` (pagination offset, search only), `--json`.

> **与上游版本的差异**：本包移除了 `download` 子命令（服务端技能不直接落盘写文件）。需要 PDF 全文时用 `read_source` 读 `https://arxiv.org/html/<id>` 的 HTML 版，或走平台已有的文件导入链路（docreader）。

## Reading Paper Content

After finding a paper, read it with the platform's web-reading tool (`read_source`):

- Abstract page (fast, metadata + abstract): `https://arxiv.org/abs/2601.02780`
- Full paper as HTML (best for reading, when available): `https://arxiv.org/html/2601.02780`
- PDF: `https://arxiv.org/pdf/2601.02780`

If HTML is unavailable and the PDF must be processed locally, `download` it first, then use a PDF-processing skill.

## Recommended Research Workflows

**Literature review on a topic（文献综述）**
1. `search "topic" --sort date --max 15` — recent work
2. `search "topic" --max 15` — seminal work (relevance-sorted)
3. Cross-check impact: `python3 scripts/arxiv.py get ID` then `cites ID --max 5` for citation counts
4. Read the top candidates via `read_source` on the abs/html URLs
5. `bibtex ID1,ID2,...` for the papers you keep

**Deep-dive a single paper（单篇深读）**
1. `get ID` — full abstract, versions, journal ref, DOI
2. `refs ID` — what it builds on
3. `cites ID` — follow-up work (sorted by citation count)
4. `similar ID` — related papers you might have missed
5. `read_source` the HTML/PDF for full text

**Stay current in a field（追踪领域动态）**
- `new cs.AI --max 20` — latest submissions in a category
- Category taxonomy: https://arxiv.org/category_taxonomy — common ones: `cs.AI`, `cs.CL` (NLP), `cs.CV`, `cs.LG`, `cs.CR`, `stat.ML`, `math.OC`

## Raw API Reference (when the script isn't enough)

The script covers most needs; use the raw APIs for advanced queries.

### arXiv API (Atom XML)

```bash
curl -s "https://export.arxiv.org/api/query?search_query=all:transformer&max_results=5"
```

Field prefixes: `all:` (everything), `ti:` (title), `au:` (author), `abs:` (abstract), `cat:` (category), `co:` (comment, e.g. `co:accepted+NeurIPS`).

Boolean syntax (URL-encode spaces as `+`):

```
all:GPT+OR+all:BERT              # OR
all:language+model+ANDNOT+all:vision   # AND NOT
ti:"chain+of+thought"            # exact phrase
au:hinton+AND+cat:cs.LG          # combined
```

Parameters: `sortBy` (`relevance`|`lastUpdatedDate`|`submittedDate`), `sortOrder`, `start`, `max_results`, `id_list` (comma-separated IDs).

### Semantic Scholar API (JSON)

arXiv has no citation data — Semantic Scholar fills that gap (free, ~1 req/sec unauthenticated).

```bash
# Paper details with citation counts
curl -s "https://api.semanticscholar.org/graph/v1/paper/arXiv:2601.02780?fields=title,citationCount,influentialCitationCount,tldr"

# Author profile
curl -s "https://api.semanticscholar.org/graph/v1/author/search?query=Yann+LeCun&fields=name,hIndex,citationCount,paperCount"

# Keyword search returning JSON (alternative to arXiv search)
curl -s "https://api.semanticscholar.org/graph/v1/paper/search?query=GRPO&limit=5&fields=title,year,citationCount,externalIds"
```

Useful fields: `title`, `authors`, `year`, `abstract`, `tldr` (AI summary), `citationCount`, `influentialCitationCount`, `isOpenAccess`, `openAccessPdf`, `fieldsOfStudy`, `publicationVenue`, `externalIds` (arXiv ID, DOI).

## Important Details

**Rate limits** — arXiv: ~1 request / 3 seconds; Semantic Scholar: ~1 request / second. Space out consecutive calls; the script exits with a clear message on HTTP 429.

**ID formats** — new style `2402.03300`, old style `hep-th/0601001`. Both work everywhere.

**Versioning** — `arxiv.org/abs/2601.02780` resolves to the latest version; `...v2` is a specific immutable version. `bibtex` and `download` preserve the version suffix so citations match the content you actually read (later versions can change substantially).

**Withdrawn papers** — the script flags entries whose abstract indicates withdrawal/retraction with `[WITHDRAWN]`. Don't cite these without noting the status.

**Listings caveat** — `new CATEGORY` sorts by submission date via the search API; the official "new today" listing at `https://arxiv.org/list/cs.AI/new` may group slightly differently (cross-lists, replacements).
