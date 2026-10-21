# LuminWrite TypeScript

这里是 LuminWrite 的 TypeScript 重写，只放在 `ts/`。仓库里原有的 Go 后端和 React 前端不动。

M0 打的是地基：块锚点修订、只追加的版本链。会话属于文档：一篇文档有一个主会话，也可以有分叉、接口和协同会话。派生文档是另一篇独立文档，不跟分叉会话混在一起。不需要模型密钥就能安装、构建、测试和跑演示。

本机默认一个所有者。首次 `lw init` 或 `lw serve` 会在数据目录写下 `owner.token`（权限 0600），库里只留令牌的 SHA-256。`/api/v1` 的调用者由这把令牌推出，请求体里的 `actor` 和 `ownerId` 会被忽略。默认只监听 `127.0.0.1`。

外部 Agent 用所有者签发的 API Key（`lw keys create` 或 `POST /api/v1/me/api-keys`）。明文只在创建时出现一次，库里只存 SHA-256。密钥带 scope 和策略；不能管理凭据、写作笔记、模型 slot、订阅源，也不能再签发密钥。按密钥的请求速率是进程内令牌桶，重启清零。外部 Agent 提交写作走 `POST /api/v1/tasks`，必须带 `Idempotency-Key`。回合在后台跑，进度用 `GET /api/v1/tasks/:id/events` 的 SSE 续传（`Last-Event-ID` 或 `after`）。`GET /api/v1/openapi.json` 由 `@hono/zod-openapi` 生成。`GET /.well-known/agent-card.json` 只声明 HTTP+JSON 和 API Key。人改过的文档上，外部 Agent 的回合和 `POST /ops` 默认挂成提案。提案按块接受或拒绝。锚点还能接到当前正文时，接受会把旧锚点重放上去；接不上，或者文档回滚之后，这块标成过期。每接受一块就新开一个版本。`GET /documents/:id/proposals` 列出提案和块。

模型供应商的 BYOK 密钥用 AES-256-GCM 存在 `provider_credentials`。没有 `secrets.key` 时用 `LW_SECRETS_KEY`（32 字节 base64）；都没有就首次启动生成数据目录里的 `secrets.key`（权限 0600）。轮换之后以密钥文件为准。`lw secrets rotate` 会换主密钥并重加密。接口和 `lw credentials` 只返回指纹和元数据，不返回密钥。凭据库不读取供应商环境变量。只有 `LW_LLM_ENABLE=1` 时，启动才会把 `LW_LLM_API_KEY`（或 `OPENAI_API_KEY`）加密导入成 `label=env` 的凭据；否则这些变量不会被读。

模型目录用 pi-ai 自带的 models.dev 快照，所有者的 `model_registry` 条目可以覆盖它。价格优先级是用户手填、注册表声明、目录价。一把凭据对应一个 `byok_<credentialId>` provider。回退只发生在第一个 token 之前，成功后钉在 `sessions.model_pin`。换了槽位之后，新回合走新链；槽位没变就沿用钉住的模型。`turns` 记下 `model_ref` / `tier_ref`。用量带上 `user_id` 和 `api_key_id`。所有者的 `LW_DAILY_COST_CAP_USD` 和密钥的 `daily_cost_cap_usd` 都会挡住回合。`GET /api/v1/models` 只返回引用和是否可用。测试里的探测和写作回合走 `test/faux`，不访问真实模型。真实供应商只在库里有对应凭据时才会被调用。

文档没有定稿。所有者新建的文档要 `POST /api/v1/documents/:id/save` 才进列表；之后的人工修订自动保存，同一 actor 在 30 秒内的连续 manual 改动合成一个版本。文章库是这些已保存文档：`GET /api/v1/library/articles` 按标题、正文、标签、体裁、来源和是否归档过滤。标签挂在文档上。归档走原来的丢弃（`discarded_at`），`POST /api/v1/library/purge` 和 `POST /admin/purge-discarded` 都只删过了保留期的丢弃文档。AI 回合、接受提案和回滚各自成版。`GET /documents/:id/versions`、`GET /versions/:id` 和 `GET /documents/:id/diff?from=&to=` 看历史。`POST /documents/:id/restore`（`/rollback` 仍可用）用旧内容新建 `origin=restore` 的版本。外部 Agent、导入和派生在创建时就算已保存。丢弃是 `discarded_at`：列表和知识库检索都跳过它，保留期内可以撤销。到期后进程内清退任务才删版本，普通写入仍然只追加。保留天数是 `LW_DISCARD_RETENTION_DAYS`，默认 30。会话也可以单独丢弃：`POST /documents/:id/sessions/:sessionId/discard` 只记 `sessions.discarded_at` 并关掉会话，同样的保留期到期后才删会话行。API Key 不能丢弃会话。

写作笔记是所有者的短句偏好，不是从稿子里学出来的。`POST /api/v1/me/writing-notes` 和 `lw notes add` 写的条目马上生效；Agent 的 `propose_writing_note` 只生成 `candidate`，人确认后才生效。`api` 会话不向模型提供这个工具。只有 `active`、范围匹配、且不和说人话冲突的句子会编译成 `StyleLayer { origin:'personal' }`，放进回合提示词的 `<notes>`。冲突的条目仍可列出。改措辞会新建一条并把旧条标成 `superseded`。`DELETE` 删掉条目，并把 `preference_audit` 里的原文清空，只留一条没有内容的删除记录。删除说明会告诉所有者：已有聊天里可能还留着原文，需要另外丢弃会话。API Key 调这些接口是 403。

pi-durable 1.3.0 把一个进程的对话放在同一个 SQLite 文件里，没有删除对话的 API。表是 `durable_schema`、`durable_metadata`（只增的 `next_id`）、`record_ids`、`conversations`（`record` 里的 `parent` 指向被 fork 的父对话）、`entries`（用户消息和模型回复）、`tasks`、`submissions`、`documents`、`document_revisions`。聊天原文在 `entries.record` 和 `document_revisions.content`。保留期清退会话时，先删掉目标对话以及 `owner_conversation_id` 落在它上面的任务子对话的这些行和对应 `record_ids`。还要留下的 fork 会去掉 `parent`，保留它自己已经复制的文档和之后的消息，然后旧对话整段删除。`durable_metadata` 和别的对话不动。接着 `wal_checkpoint(TRUNCATE)` 和 `VACUUM`，避免原文留在空闲页；Harness 仍打开着这个文件时 VACUUM 可能失败，行已经删掉。

知识库接受 MD、TXT、PDF、DOCX，也可以用 `safeFetch` 导入网页。正文用 jieba 切开，大约 600–800 字一块，再用全文和向量做 RRF。`kb.embed` 槽绑了 BYOK 凭据时才请求 OpenAI 兼容的 `/embeddings`；没有凭据就只做全文，不报错。重复上传返回 `duplicate`。`search_kb` 和 `read_source` 只读，结果标明是资料而不是指令。已保存文档按最新版本进知识库，后续改动经 `doc.index` 每 60 秒最多索引一次。未保存、已丢弃或 `kb_excluded` 的文档不参与检索。`search_kb` 默认不含文档类来源，`library=true` 才放开，并且排除正在写的这篇和它的派生血缘。引用钉住当时索引的版本。检索计划由模型写入 `research_plans`，服务端只校验问题 id 和证据钉。`GET /api/v1/documents/:id/plans` 给 H2 读这份计划。正文里的 `[[cite:证据id]]` 和档案引用在回合结束时核对：对得上是 `verified` 或 `fuzzy`，对不上会记一条 `claim.flags`。RSS 条目改稿后，旧引用判 `mismatch`；钉住文档版本的引用仍是 `verified`，并标出来源已有新版本。编辑部角色写在 `roles/builtin`：研究员、事实核查、引用审计、审校。主写作代理自己决定何时 `delegate`。服务端只卡住工具名单、块范围、预算和同一任务的重复运行。子会话挂在这一次工具任务上，不带走父对话里范围外的稿。`GET /api/v1/documents/:id/subagent-runs` 列出这些运行。

RSS 按所有者订阅，条目进同一套知识库，这个用户的所有会话和文档都能检索。抓取会解析 DNS，拒绝私网、环回和链路本地，并限制重定向、体积和时长。`lw feeds` 和 `/api/v1/feeds` 只有所有者能改。带 `sources:read` 的 API key 可以检索这些条目，不能加订阅。开源版没有中文热榜。轮询走 pg-boss，默认每 15 分钟。

## 环境

- Node.js 24（见 `.nvmrc`）
- pnpm 12.11.2（`packageManager` 字段）

```bash
cd ts
pnpm install
pnpm -r build
pnpm -r test
pnpm demo
```

`pnpm demo` 会建一篇文档。主会话只改第 3 段；从旧版本分出的会话再改第 3 段会被拒绝，改第 5 段可以落下。然后从旧版本派生一篇新文档，派生文档自己的修改不会回到源文档。把段落写回源文档必须用源文档当前的块锚点。成功时输出 `DEMO_OK`。

真实模型不会在测试或演示里被调用。`lw serve` 的回合先走注册表；没有凭据、又没打开 `LW_ALLOW_FAUX` 时，回合失败，不会去读环境里的供应商密钥。测试和 `pnpm demo` 没有注册表解析器时，仍用进程内的 `test/faux` 计划函数提出修订，服务端负责校验锚点和说人话。`LW_LIVE=1` 的冒烟测试才会碰真实模型。

## 包

| 包 | M0 状态 |
|---|---|
| `contracts` `doc-model` `plainspeak` | 浏览器和 Node 共用的文档模型与空话检测 |
| `db` | Drizzle schema。运行时默认是数据目录里的 PGlite，设置 `LW_DATABASE_URL` 才连 Postgres |
| `models` | 档位路由、可重试回退、用量账本接口 |
| `harness` | `HarnessPort` 后面的 pi-durable 1.3.0。一个会话一个 SQLite 对话，不按文档复用 |
| `style` | 默认文风 `plain-professional`，说人话地板不可关 |
| `eval-adapter` | 确定性说人话冒烟，负对照排最后 |
| `edition` | 开源扩展口：授权、提案策略、实时通道 |
| `server` `cli` | HTTP（`/api/v1`，所有者令牌或 API Key）、`lw init` / `lw owner rotate-token` / `lw keys` / `lw credentials` / `lw secrets rotate` / `lw feeds` / `lw notes` / `lw serve`，以及演示 |
| `kb` | 上传、分块、全文与向量混合检索。没有嵌入凭据时只做全文 |
| `jobs` | `JobPort`。运行时是 pg-boss，测试用进程内队列 |
| `sources` | `safeFetch` 和按所有者隔离的 RSS。没有中文热榜 |
| `research` | 检索计划校验、引用核对（`verified` / `fuzzy` / `not_found` / `mismatch`） |
| `roles` | 编辑部角色：提示词、工具名单、模型槽。`delegate` 由主写作代理决定 |
| `library` | 文章库：已保存文档的列表、搜索、标签和归档。归档就是丢弃，清退沿用保留期 |
| `prefs` | 写作笔记：人写的偏好和待确认候选，编译成个人风格层 |

M0 的验证记录见 `docs/m0-verification.md`。会话和派生文档见 `docs/m1-sessions.md`。PGlite 上的 pg-boss 和 pgvector 见 `docs/m1-pglite-findings.md`。

## 密钥扫描

仓库根目录的 `.gitleaks.toml` 在默认规则之外增加了裸 `sk-` 密钥规则。提交前钩子在 `.githooks/pre-commit`：

```bash
git config core.hooksPath .githooks
```

本机需要 gitleaks v8.30.1。钩子命令是 `gitleaks git --pre-commit --redact --staged`。CI 只扫工作区，不扫历史，避免已作废、但还留在历史里的密钥把检查打红。
