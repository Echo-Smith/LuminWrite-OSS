# M1-01：PGlite、pg-boss、pgvector

日期：2026-10-16。对照 M1 规格 v2.1：本机默认用嵌入式 PGlite，正式 Postgres 由配置选择，同一套 Drizzle schema。这一轮实际跑了 pg-boss 12.38.0 和 `@electric-sql/pglite-pgvector` 0.0.11。

## 运行时数据库

`openDatabase` 默认在 `${LW_DATA_DIR}/pglite` 打开 PGlite（`LW_DATA_DIR` 未设时是 `~/.luminwrite`）。只有 `LW_DATABASE_URL` 才走 `openPostgres`。不读环境里的 `DATABASE_URL`，避免用到别的项目留下的连接串。测试和演示仍用内存 PGlite（`openMemoryDatabase`）。

## pgvector

能加载。PGlite 构造时传入 `{ extensions: { vector } }`，再执行 `CREATE EXTENSION vector`。`vector(1024)` 列、余弦距离 `<=>`、`hnsw` + `vector_cosine_ops` 都在本机跑通。测试在 `packages/db/src/pglite-capabilities.test.ts`。

扩展必须在构造时挂上。`openDatabase` / `openMemoryDatabase` 现在在构造 PGlite 时传入 `{ extensions: { vector } }`，迁移里再 `CREATE EXTENSION IF NOT EXISTS vector`。换嵌入模型仍然要重嵌入。

## pg-boss

能跑，不需要换成进程内队列。pg-boss 12 自带 `backend: "pglite"` 和 `fromPglite`。在挂了本仓库迁移的同一个 PGlite 上，`start`、`createQueue`、`send`、`work`、`schedule("*/15 * * * *")` 都成功。它用自己的 schema，不碰业务表。

所以 M1-08 的 `JobPort` 可以有两个实现，接口不变：

- 运行时（PGlite 或 `LW_DATABASE_URL` 的 Postgres）用 pg-boss。PGlite 走 `fromPglite`，Postgres 走连接串。
- 单元测试仍用进程内实现（规格 §6.3：同步执行、可手动推进时间），不另起数据库。

M1-08 已把进程内 `JobPort` 落在 `@luminwrite/jobs`。它保住三件事：`singletonKey` 合并还没开始的同名任务并保留最新数据，`startAfterSeconds` 延迟，`schedule` 按 cron 触发。运行时 `startServer` 仍用 pg-boss。如果以后某版 pg-boss 在 PGlite 上起不来，把运行时适配器换成这套进程内实现即可，调用方不用改。

## M0 的定稿字段

有列，没有定稿状态。不在这一轮迁移。

- `documents.final_version_id` 是可空的 `text`。建文档和派生文档都写成 `null`，没有别的写入。
- `documents.status` 是不带检查约束的 `text`。迁移默认值是 `'draft'`，代码也只写 `'draft'`。没有 `'final'`，也没有 `'archived'`。

M1-06 已删掉 `final_version_id`，并把 `draft` / `final` / `archived` 收成 `active`。丢弃不用状态位，用 `discarded_at`。
