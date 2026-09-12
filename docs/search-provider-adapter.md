# 搜索源适配器开发指南

LuminWrite OSS 的搜索层是多源并发框架（`backend/internal/tools/search.go`）：
一次查询并发打到所有已配置的源，跨源去重后按相关性（可选叠加来源可信度）
截断返回。本文说明如何接入你自己的搜索源。

## 内置源

| 源 | OSS 版状态 | 配置 |
|---|---|---|
| **SearXNG** | ✅ 完整实现（自托管，零 key） | `SEARXNG_BASE_URL` |
| Tavily | stub（商业版完整） | `TAVILY_API_KEY` |
| 知乎 | stub（商业版完整） | `ZHIHU_*` |
| 腾讯新闻 | stub（商业版完整；仅热搜通道） | `TENCENT_*` |
| 微博 | stub（商业版完整） | `WEIBO_*` |
| Bing | stub（商业版完整） | `BING_*` |
| AnySearch | stub（商业版完整） | `ANYSEARCH_*` |

SearXNG 是 OSS 的默认可用源：任何 OpenAI 兼容 LLM key + 一个 SearXNG 实例
即可跑通完整写作流。quickstart 栈（`docker-compose.quickstart.yml`）已内置
SearXNG 服务并默认启用（`SEARXNG_BASE_URL=http://searxng:8080`）。

自托管 SearXNG 必须开启 JSON 输出，参考 `docker/searxng/settings.yml`：

```yaml
search:
  formats:
    - html
    - json
```

## 写一个新适配器

一个源 = 一个实现 `Search(ctx, query, maxResults) ([]engine.SearchResult, error)`
的 client 结构体（约 100 行）。

1. **新建 `backend/internal/tools/search_<name>.go`**：

```go
package tools

type MySourceClient struct{ /* endpoint, key, http client … */ }

func NewMySourceClient(endpoint, apiKey string, timeout time.Duration) *MySourceClient { … }

func (c *MySourceClient) Search(ctx context.Context, query string, maxResults int) ([]engine.SearchResult, error) {
    // 调你的 API，把每条结果映射为 engine.SearchResult：
    // {Title, Snippet, URL, Source: "mysource"}；Source 用于日志与可信度关联。
}
```

要点（以 `search_searxng.go` 为范本）：
- `ctx` 必须贯穿 HTTP 请求（取消/超时语义由框架统一控制）；
- `maxResults` 是硬上限，宁缺勿滥——框架会做跨源去重；
- 单源失败返回 error 即可，框架记 warning 后继续其他源，不会拖垮整次搜索。

2. **注册进 `SearchClient`**（`search.go`）：加字段、`NewSearchClient` 加参数、
   `Search()` 加并发分支、`HasSources()` / `activeSources()` 加条件。

3. **配置**：`internal/config/config.go` 加配置块 + env 读取；
   `server.go` 的 `NewSearchClient(...)` 调用点传参；
   `.env.docker.example` 加注释示例。

4. **测试**：仿 `search_searxng_test.go`——`httptest.Server` mock 上游 JSON、
   字段映射断言、上限截断、错误分支（如 403/超时）。

## 设计边界

- 框架不负责鉴权与配额，client 自理（含 key 泄漏防护：不打印到日志）。
- 结果会被 `DedupSearchResults` 按 URL 精确去重 + 标题 Jaccard>0.7 模糊去重，
  不要在单源内部再做一次。
- `SearchResult.IsMock=true` 仅供测试注入，生产路径禁止。
- 商业版独有源的实现位于商业仓库（`.commercial-files` 清单），不要把真实
  key 或实现提交到本仓库。
