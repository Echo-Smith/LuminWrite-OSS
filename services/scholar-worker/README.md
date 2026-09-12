# lumin-scholar (Scholar Worker)

LuminBuddy 研究综述路径的私网 Python worker。提供五个有界 operation
（discover / rank / fetch_full_text / parse / read），统一请求与错误信封见
`specs/research-review/contracts.md` §4。

- 仅私网部署：默认绑定 `127.0.0.1:8971`，生产由编排器注入 `SCHOLAR_WORKER_HOST`
  指向私网接口；**不得**对外发布端口。
- 服务间认证：`Authorization: Bearer $SCHOLAR_WORKER_TOKEN`；未配置 token 时
  服务器拒绝启动，且拒绝一切请求（fail closed）。
- 第三方运行时依赖仅 `httpx`（精确锁版本；超时/流式/transport 注入需要）。

## 实现状态（T04）

- `discover` — 真实实现：OpenAlex / Crossref / Semantic Scholar 三源 fan-out，
  单源失败不影响其他源；全源失败返回聚合错误（区别于"无结果"）。DOI 规范化与
  去重按 R03 口径（`dois.py` / `discovery.py`）：DOI 相同或题名+作者姓氏+年份
  同时匹配才合并；DOI 冲突的同题名记录保留独立并标记 `possible_duplicate`。
- `rank` — 真实实现：OpenAI 兼容 chat/completions（环境变量
  `SCHOLAR_LLM_BASE_URL` / `SCHOLAR_LLM_MODEL` / `SCHOLAR_LLM_API_KEY`）。
  未配置时 fail-closed 报错，绝不静默给全 0 分。摘要按不可信数据分框，缺失/
  多余/重复 paper_id 一律报错。
- `fetch_full_text` — 真实实现（`downloader.py`）：SSRF 加固下载器。逐跳
  DNS 解析 + 全部 IP 校验（拒绝 loopback/private/link-local/云 metadata/
  组播/保留段），手动跟随重定向（上限 5 跳），Content-Length 预检 + 流式
  字节上限双保险，Content-Type 白名单 + octet-stream 魔数嗅探，返回
  SHA-256 内容 hash。blob 以 base64 内联返回（`transport=inline_base64`）；
  T05/T09 切换到 Go 内容服务的任务作用域上传 URL。
  测试可注入 `IpPolicy`（仅 tests/ 内构造；生产路径无任何 bypass）。
- `parse` / `read` — 仍为 T03 离线 mock（T05 落地）。

测试：离线单测全部使用注入 transport / 本地 fixture server，不联网；
`SCHOLAR_LIVE_TESTS=1` 时额外运行真实 OpenAlex 的 live smoke（默认 skip）。

## 本地开发

```bash
cd services/scholar-worker
uv venv .venv
uv pip install -e ".[dev]"
.venv/bin/python -m pytest -W error -q

# 真实网络 live smoke（可选）
SCHOLAR_LIVE_TESTS=1 .venv/bin/python -m pytest tests/test_live_smoke.py -v

# 手动启动（另开终端）
SCHOLAR_WORKER_TOKEN=dev-token .venv/bin/python -m lumin_scholar
```

联调回环脚本：仓库根目录 `scripts/dev-scholar-smoke.sh`（用 Go client 打
`/healthz` 与一个真实 discover operation）。
