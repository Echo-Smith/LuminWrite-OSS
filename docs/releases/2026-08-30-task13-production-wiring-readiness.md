# Task13 生产接线门禁记录 — 2026-08-30

## 结论

**代码与构建门禁通过；Commercial 真实 Provider 已部分验证，但 LLM 认证失败，生产流量门禁未通过。**

当前两版可以进入“带真实凭证的隔离 staging 验收”，不能据此直接声明已经生产上线，也不能开启 allowlist、percentage 或全量流量。默认 rollout 继续保持 `off/shadow`。

本记录取代此前“两个 compose 栈已运行且 Task13 已闭环”的结论。那一结论混合了旧镜像运行状态、单元测试和尚未执行的真实 Provider 验收，证据不足。

## 本轮纳入的实现

### 受治理写作运行时

- writingstore 是 Contract、Plan、Run、Artifact、Decision、Ledger、canonical/shadow 内容的唯一事实源。
- 生产组合根统一装配 capability registry、MaterialAdapter、ExecutorAdapter、调度、恢复、质量验证与 rollout。
- 初始 `contract/materials`、中间产物和最终正文均进入 typed Artifact/lineage；canonical body 持久化后逐次校验 hash。
- 服务重启只恢复 `planned/running`，不会越过 `awaiting_approval/paused`。
- required validator 失败时 fail-closed，不能自动生成通过结论。
- rollout 默认关闭；shadow candidate 使用独立 namespace/TTL，不进入 canonical 文档。

### Provider、MCP 与版本边界

- `/health` 仅表示进程存活；`/ready` 按 `installed/configured/reachable/ready` 判断生产依赖。
- 管理员可显式执行 `POST /api/v2/admin/provider-preflight`；自动预检默认关闭，所有探测有界且只返回稳定错误码。
- MCP 连接状态进入 readiness；SSE endpoint 与 JSON-RPC 响应写入已串行化，消除跨 handler 的 ResponseWriter 数据竞争。
- OSS 只保留通用检索契约、本地知识检索和共享网页抓取；商业搜索 Provider 不注册、凭证变量不公开，运行镜像不包含付费信息源 CLI。
- Commercial 保留 Tavily、知乎、微博 OpenAPI、Bing、AnySearch 等显式付费源入口；热榜类来源不能冒充受治理写作检索能力。
- MCP 框架与通用检索接口共享，但两版不共享付费搜索源实现和配置。

## 已执行门禁

| 门禁 | OSS | Commercial | 结果 |
|---|---:|---:|---|
| `go test ./... -count=1`（仓库根挂载，Go 1.25） | 通过 | 通过 | PASS |
| `go vet ./...` | 通过 | 通过 | PASS |
| `go test -race ./internal/writingruntime ./internal/writingstore ./internal/mcp ./internal/server -count=1` | 通过 | 通过 | PASS |
| 空库 migration 与幂等校验 | 通过 | 通过 | PASS |
| writingstore PostgreSQL 集成测试（含 canonical/shadow/recovery） | 通过 | 通过 | PASS |
| `docker compose config --quiet` | 通过 | 通过（含本地 override） | PASS |
| 当前分支 backend 镜像构建 | 通过 | 通过 | PASS |
| OSS 最终镜像付费 CLI 扫描 | 无 `tencent-news-cli` | 不适用 | PASS |
| 受信环境隔离 Provider preflight | OSS 不导入 Commercial 付费凭证 | Embedding、Tavily、知乎、微博、Bing、AnySearch 可达；LLM 被认证拒绝 | BLOCKED |

数据库测试必须先执行 migration 包，再执行 writingstore 包；两个独立 `go test` 进程同时对同一个空库创建 `schema_migrations` 会产生测试编排竞争，不应并行共享数据库。

本轮 race 门禁最初稳定发现 MCP SSE 并发写；修复后两版完整选定包 race 测试通过。相关提交：

- OSS：`fa5ab96`（运行时生产缺口）、`08031b1` + `8f2e94b`（Provider preflight）、`2a5966e`（MCP SSE 并发）、`bef6f45`（移除付费 CLI）。
- Commercial：`95e2648`（运行时生产缺口）、`6a8eb86`（Provider preflight 与付费检索边界）、`9d53b2b`（MCP SSE 并发）；后续补充并发搜索预检，避免慢源耗尽其他源的观测预算。

## 尚未通过的生产门禁

Commercial 已在隔离网络中读取受信环境配置并执行一次真实 preflight；密钥、URL 和响应正文均未记录。Embedding 与五个受治理写作检索源均可达，但 `AI_API_KEY` 返回 `PROVIDER_AUTH_REJECTED`；它与遗留 `LLM_API_KEY` 为同一值，因此没有可切换的本地备份主模型密钥。`/ready` 的唯一 required 阻断项是 LLM。

Tencent News CLI/Jiaozhen 在启动时也拒绝了现有 API Key；公开热榜抓取的成功不能视为该付费事实核验凭证已验证。支付宝证书未挂入隔离容器，不作为本轮写作验收结论。

因此以下证据尚不存在：

1. 有效 LLM 的真实 preflight 成功快照；
2. 通过生产 HTTP API 完成的长文创作、多材料综合、忠实改写三条真实模型链路；
3. 三条链路的 Candidate Draft → Accepted Draft → Verified Deliverable 状态、引用/快照、token/cost、pause/cancel 与 BLOCKER 证据；
4. 使用当前 Task13 分支镜像进行的隔离 staging 部署、重启恢复和回滚演练；
5. 基于持久化 evidence 作出的 allowlist/percentage 放量决定。

旧环境中正在运行的容器没有被本轮替换，也不作为当前提交的部署证据。

## 凭证就绪后的执行顺序

1. 替换失效的 DeepSeek 主模型凭证，并为 Tencent News CLI/Jiaozhen 提供有效凭证或显式关闭该能力；不把密钥写入仓库、日志或验收文档。
2. 保持 `PROVIDER_PREFLIGHT_ENABLED=false`，由管理员手动执行一次 preflight；确认 LLM、Embedding 和 Commercial 搜索 readiness。
3. 部署当前两版候选镜像，验证 `/health` 与 `/ready` 的预期差异。
4. 运行 `cmd/writingacceptance` 的三条写作场景，检查完整 Artifact、Decision、Ledger、质量报告和费用证据。
5. 在 run 进行中重启 backend，验证只恢复允许恢复的状态；测试 pause/cancel。
6. 仅开启 shadow，验证 candidate 隔离、TTL 清理、canonical 不受污染。
7. 完成回滚演练并归档证据后，另行评审 allowlist；percentage 和全量放量仍需独立决策。

## 发布判定

- 本地开发/继续集成：**允许**。
- 隔离 staging + 真实凭证验收：**允许**。
- 生产部署：**暂不批准**。
- 生产写作流量、allowlist、percentage：**保持关闭**。
