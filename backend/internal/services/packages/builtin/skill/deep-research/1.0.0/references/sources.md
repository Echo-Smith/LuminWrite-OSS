# 免费公开来源端点（零 API Key）

默认用平台的搜索与网页读取工具（见 SKILL.md 工具映射）。特定角度类型用下列免费端点能拿到更结构化的结果。全部无需密钥；遇 429/5xx 退避一次，仍失败则放弃该源继续推进。

## 通用网页搜索（搜索工具不可用时的兜底）

通过网页读取工具抓 DuckDuckGo 的 HTML 版，可返回完整搜索结果：

```
抓取 https://html.duckduckgo.com/html/?q=<URL 编码后的查询>
```

- 结果链接包裹为 `//duckduckgo.com/l/?uddg=<URL 编码后的真实地址>`——抓取前先解码 `uddg` 参数。
- 支持运算符：`site:github.com`、`"精确短语"`，以及查询参数 `df=y`（近一年）/ `df=m`（近一月）。
- 中文话题可加试 Bing：`https://www.bing.com/search?q=<查询>&setlang=zh`（免key，质量不稳定）。

## 学术 / 论文

```bash
# arXiv（Atom XML）
curl -s "http://export.arxiv.org/api/query?search_query=all:\"deep+research+agent\"&sortBy=submittedDate&sortOrder=descending&max_results=10"

# Semantic Scholar（JSON；未认证限速约 1 次/秒）
curl -s "https://api.semanticscholar.org/graph/v1/paper/search?query=deep+research+agent&fields=title,year,abstract,citationCount,externalIds,url&limit=10"

# OpenAlex（JSON，额度宽松；带 mailto 表示礼貌抓取）
curl -s "https://api.openalex.org/works?search=deep%20research%20agent&per-page=10&mailto=research@example.com"
```

论文全文：网页读取工具抓 `https://arxiv.org/abs/<id>` 看摘要，或 `https://ar5iv.org/abs/<id>` 看 HTML 全文。

## 代码 / GitHub

```bash
# 仓库搜索（未认证 60 次/小时）
curl -s "https://api.github.com/search/repositories?q=deep+research+agent&sort=stars&per_page=10"

# 仓库 README
curl -s "https://raw.githubusercontent.com/<owner>/<repo>/HEAD/README.md"
```

## 社区 / 讨论

- Hacker News：`https://hn.algolia.com/api/v1/search?query=<q>&tags=story`（JSON，免key）
- Reddit：网页读取工具抓 `https://www.reddit.com/search/?q=<q>`，或给任意帖子 URL 加 `.json`
- Stack Overflow：`https://api.stackexchange.com/2.3/search/advanced?q=<q>&site=stackoverflow`（JSON）

## 数据 / 事实

- Wikipedia REST：`https://en.wikipedia.org/api/rest_v1/page/summary/<title>`（JSON）
- Wikidata：`https://www.wikidata.org/w/api.php?action=wbsearchentities&search=<q>&language=en&format=json`

## 可选增强（环境已配置才用，绝不要求用户安装）

若运行环境已具备以下能力，在对应场景优先使用；没有则静默跳过：

- Firecrawl 类抓取 → JS 渲染严重的页面（网页读取工具渲染不佳时）
- arxiv / 文献检索类工具 → 论文下载 + PDF 文本提取
- 其他搜索服务（Tavily/Exa 等）→ 更高质量的搜索结果

调研进行中绝不让用户安装或配置任何东西。
