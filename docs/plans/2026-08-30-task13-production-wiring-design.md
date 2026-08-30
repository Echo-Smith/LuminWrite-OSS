# Task13 生产接线与真实就绪设计

## 目标

Task13 将“代码存在、容器健康”升级为“真实写作链路可执行、外部能力可验证、降级状态可治理”。系统必须区分 installed、configured、reachable、ready 四层状态；任何付费检索源、模型或 MCP 服务都不能仅因对象已构造或环境变量非空而被报告为可用。

Task13 不开放 percentage 或 enabled 流量。交付终点是两版均具备可审计的本地能力与 MCP 框架，Commercial 可在有效凭证下启用付费信息源，governed runtime 在 shadow-only 模式下完成真实业务接线。

## 版本边界

OSS 与 Commercial 共享以下能力：

- `SearchProvider`、`Crawler`、检索结果、引用和健康状态契约；
- 本地 PostgreSQL/BM25/Dense/GraphRAG 检索；
- 通用网页抓取、正文抽取、材料化、去重、压缩和注入防御；
- MCP client/server/registry/sandbox 及 MCP 搜索适配接口；
- 搜索预算、超时、熔断、来源分类和 evidence 记录；
- governed runtime 的合约、计划、Artifact、Orchestrator 和持久化门禁。

OSS 不包含付费搜索 API 的实现、协议细节、信息源配置或凭证管理。未安装商业 Provider 时返回稳定的 `PROVIDER_NOT_INSTALLED`，不得返回成功的空结果。用户可以通过 MCP 或自行实现共享 Provider SDK 扩展搜索能力。

Commercial 在共享接口上实现 Tavily、AnySearch、微博、知乎、腾讯新闻、较真等付费或受限信息源，并独立承担凭证、配额、计费、健康探测和服务条款。商业来源的实现不得回流 OSS。

## 真实就绪模型

每项外部能力使用统一状态：

1. `installed`：当前 edition 含具体实现；
2. `configured`：配置完整且不是占位值；
3. `reachable`：最近一次受控探测成功；
4. `ready`：前三项成立且熔断器未打开、依赖未降级。

`/health` 只保留进程存活信息；新增详细 readiness 数据，至少覆盖 LLM、Embedding、Search、MCP、Knowledge、Writing Runtime、Evidence Store 和 Shadow Content。健康接口不得执行付费请求；探测由启动预检、管理端显式测试或低频后台任务更新，并记录 `last_checked_at`、稳定错误码和非敏感摘要。

能力不可用时，执行计划根据合约降级：禁止外部研究的任务继续使用用户材料和本地知识库；要求外部研究的任务必须 fail-closed 或进入人工确认，不能生成看似完成但没有来源的成稿。

## 检索与 MCP 数据流

写作计划只声明 `external_research`、`local_knowledge`、`url_fetch` 等能力，不绑定具体品牌。Capability Registry 将其解析到当前 edition 的 Provider Registry。检索结果统一经过来源规范化、注入防御、去重、可信度排序和引用材料化，再作为 typed Artifact 进入 writingstore。

MCP 工具先经过 registry 名称隔离和 sandbox policy，再通过与内置工具相同的权限、预算和 evidence 边界。MCP 搜索服务的结果必须转换为通用 SearchResult，不能绕过材料快照直接进入提示词。外部 MCP 连接为空是合法配置，但 readiness 必须显示 `disabled` 或 `unconfigured`，不能显示 ready。

Evidence 只记录来源类别（`local`、`crawler`、`mcp`、`commercial_api`）、Provider ID、请求/结果哈希、引用数量、耗时、成本和稳定错误码；不得记录 API Key、Authorization header 或原始私密材料。

## Governed Runtime 生产组合根

当前生产服务只创建 writingstore API，未构造 ExecutorRegistry、MaterialAdapter、Orchestrator、RolloutExecutor，控制器也为空。Task13 新增单一 composition root：

- 从 PostgreSQL 构造 writingstore、材料来源和 canonical content gateway；
- 注册真实 Engine、Editorial、Harness adapter；
- 将 Search/MCP/Knowledge 能力通过 ExecutionRequest 注入，而不是由旧算子直接写业务状态；
- 使用 writingstore-backed `RolloutEvidenceStore`；
- 使用独立、可清理的持久化 ShadowContentSink；
- 将 Orchestrator 注入 Writing API controller，并在普通任务创建后按合约自动调度；
- 默认 rollout 为 `off`，只有显式 shadow 策略才运行 candidate，禁止自动进入 authoritative lane。

启动时依赖不完整必须 fail-closed：写作文档和合约仍可读取，但创建可执行 run 返回稳定的 runtime-not-ready 详情，不能创建永远停在 planned 的僵尸 run。

## 错误处理与安全

- 付费 Provider 未安装：`PROVIDER_NOT_INSTALLED`；
- 配置缺失或占位：`PROVIDER_NOT_CONFIGURED`；
- 凭证拒绝：`PROVIDER_AUTH_FAILED`；
- 超时/限流：`PROVIDER_TIMEOUT` / `PROVIDER_RATE_LIMITED`；
- MCP 未配置：`MCP_UNCONFIGURED`；
- runtime 组合根不完整：`WRITING_RUNTIME_NOT_READY`；
- evidence 或 shadow 持久化失败：BLOCKER，不允许 candidate-authoritative 提交。

所有错误对用户展示可行动的简化信息，对审计保留完整非敏感上下文。凭证探测不得在日志中输出响应正文或请求 header。

## 验收与放量

验收分四层：单元测试固定 edition 边界和稳定错误码；契约测试用 fake HTTP/MCP server 验证真实协议；数据库测试验证 evidence、shadow TTL、幂等和重启恢复；部署验收用真实 LLM 和 Commercial 搜索凭证跑长文、多材料综合、忠实改写三条链路。

Task13 完成标准：OSS 本地搜索、Crawler、MCP 扩展路径可用且不暴露商业源；Commercial 付费 Provider 在有效凭证下通过预检；两版 health/readiness 真实；governed runtime 完成 shadow-only 业务接线；重启后 evidence 与 shadow 样本仍可审计和清理。达到这些条件后才讨论 allowlist。
