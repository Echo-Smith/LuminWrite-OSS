# 免费学术 API 速查（零密钥）

以下端点全部无需 API key。脚本（`scripts/verify_citation.py`、`scripts/paper_search.py`）
已覆盖主流程；需要自定义查询时直接用原始端点。请求头带 mailto 的 User-Agent（部分
API 对"礼貌客户端"给更好的限速）。限流时退避一次，仍失败则换源，绝不硬等。

## arXiv

- 检索：`https://export.arxiv.org/api/query?search_query=all:<terms>&max_results=20&sortBy=relevance`（Atom XML）
  - 字段前缀：`ti:` 标题、`au:` 作者、`abs:` 摘要、`cat:cs.LG` 分类；组合：`au:vaswani+AND+cat:cs.CL`
- 按 ID：`?id_list=1706.03762`
- PDF：`https://arxiv.org/pdf/<id>.pdf` · HTML 全文：`https://ar5iv.labs.arxiv.org/html/<id>`
- 限速：约 1 次 / 3 秒；无需认证。

## Semantic Scholar（S2）

- 检索：`https://api.semanticscholar.org/graph/v1/paper/search?query=<q>&limit=20&fields=title,authors,year,abstract,externalIds,citationCount,venue,url`
- 论文详情：`/graph/v1/paper/<id>`，id 形如 `arXiv:2504.17192`、`DOI:...` 或 S2 hash
- **引文滚雪球**：`/graph/v1/paper/<id>/citations?fields=title,year` 与 `/references` —— 最好的免费引文图谱 API
- 限速：未认证池很紧（429 常见——脚本已做退避；持续限流就放弃 S2，改用 OpenAlex）。

## OpenAlex

- 检索：`https://api.openalex.org/works?search=<q>&per-page=25`
- 过滤：`&filter=from_publication_date:2022-01-01,cited_by_count:>50,open_access.is_oa:true`
- 按 DOI：`/works/doi:10.xxxx/yyy` —— 响应含 `best_oa_location`（免费 OA-PDF 解析器，内含 Unpaywall 数据）
- 摘要以 `abstract_inverted_index`（词 → 位置）给出；反转即可重建。
- 限速：10 万次/天，非常稳定。加 `&mailto=you@example.org` 进礼貌池。

## Crossref

- 检索：`https://api.crossref.org/works?query.bibliographic=<title+author>&rows=5` —— **引用核验首选**（DOI 权威源）
- 按 DOI：`/works/<doi>`
- 字段：`title[]`、`author[].family`、`issued.date-parts`、`container-title[]`、`is-referenced-by-count`
- 限速：礼貌 UA 下很宽松。

## dblp（CS 会议/作者）

- `https://dblp.org/search/publ/api?q=<q>&format=json&h=10` —— CS 论文的精确 venue/年份；适合核验 venue 声明。

## PubMed（生物医学）

- 检索：`https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esearch.fcgi?db=pubmed&term=<q>&retmax=20&retmode=json`
- 摘要：`efetch.fcgi?db=pubmed&id=<pmid>&rettype=abstract&retmode=text`
- 限速：无 key 约 3 次/秒。

## 全文获取阶梯

1. 知道 arXiv id → 用 `read_source` 读 `https://arxiv.org/abs/<id>`（摘要）或 `https://ar5iv.org/abs/<id>`（HTML 全文）
2. 知道 DOI → OpenAlex `best_oa_location` → 用 `read_source` 读落地页
3. Europe PMC（生物医学 OA）：`https://www.ebi.ac.uk/europepmc/webservices/rest/search?query=<q>&format=json`
4. 无开放版本 → 只依据摘要工作，分析标注 `[仅摘要]`。

## 按需选源

| 需求 | 用哪个 |
|---|---|
| CS/ML 新预印本 | arXiv、S2 |
| 引用数 / 引文图谱 | S2、OpenAlex |
| 核验一条引用是否存在 | Crossref → S2 → OpenAlex → arXiv（瀑布） |
| CS 会议元数据 | dblp |
| 生物医学 | PubMed、Europe PMC |
| 跨学科覆盖 | OpenAlex |
