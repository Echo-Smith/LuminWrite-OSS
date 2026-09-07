# lumin-scholar (Scholar Worker)

LuminBuddy 研究综述路径的私网 Python worker（T03 骨架）。只提供五个有界
operation（discover / rank / fetch_full_text / parse / read），统一请求与错误
信封见 `specs/research-review/contracts.md` §4。

- 仅私网部署：默认绑定 `127.0.0.1:8971`，生产由编排器注入 `SCHOLAR_WORKER_HOST`
  指向私网接口；**不得**对外发布端口。
- 服务间认证：`Authorization: Bearer $SCHOLAR_WORKER_TOKEN`；未配置 token 时
  服务器拒绝启动，且拒绝一切请求（fail closed）。
- 零第三方运行时依赖（标准库 `http.server`）；dev 依赖仅 pytest。
- 当前全部为离线 mock 实现（合成样例数据，原创），真实检索/解析/阅读分别在
  T04/T05 落地。

## 本地开发

```bash
cd services/scholar-worker
uv venv .venv
uv pip install -e ".[dev]"
.venv/bin/python -m pytest -q

# 手动启动（另开终端）
SCHOLAR_WORKER_TOKEN=dev-token .venv/bin/python -m lumin_scholar
```

联调回环脚本：仓库根目录 `scripts/dev-scholar-smoke.sh`（用 Go client 打
`/healthz` 与一个 mock operation）。
