# 迁移并发串行化（advisory lock，独立小修）

**日期**：2026-09-03
**范围**：OSS + Commercial 双仓同步；消除 M4b 记录、M5/M6 引用的遗留已知问题——全树并行测试对同一全新 `TEST_DATABASE_URL` 并发跑迁移导致 `schema_migrations` 主键冲突。

## 问题

`database.Migrate` 的读-改-写不是原子的：两个 migrator（并行测试二进制，或多实例后端启动）同时读到空 `schema_migrations`，都判定同一迁移未应用，都去执行——要么 `INSERT schema_migrations` 撞主键，要么非幂等 DDL（如 `CREATE EXTENSION`）撞唯一索引。历史上以 `-p 1` 串行规避，掩盖了它。

## 修复（`internal/database/migrator.go`）

1. **会话级 advisory lock 串行化整个迁移**：`MigrateDB` 开头取 `pg_advisory_lock(migrationAdvisoryLockKey)`，结尾 `pg_advisory_unlock`。锁在读取已应用快照**之前**获取，因此第二个 migrator 阻塞等待，醒来后重新读到完整 schema、全部跳过。锁键是固定常量 `7258234`（应用级唯一）。
2. **单条专用连接承载锁与全部迁移**：`db.Conn(ctx)` 钉住一条连接，advisory lock 与 `ensureMigrationsTable`/`getAppliedMigrations`/`runMigration` 全跑在它上面。advisory lock 是会话级的，而迁移事务需要连接——若锁与迁移分占两条连接，连接池配小（甚至 1）时会自锁死锁；同连接设计从根上消除该风险。
3. **helper 签名改 `migrationDB` 接口**（`ExecContext`/`QueryContext`/`BeginTx`），`*DB` 与 `*sql.Conn` 都满足：`MigrateDB` 传专用连接，既有测试仍可直接用 `*DB` 调 `getAppliedMigrations`，零测试改动。
4. 解锁用独立 context（`context.Background()` + 超时），避免被调用方已取消的 ctx 卡住；即便解锁语句失败，会话结束也会释放锁。

## 验证（双仓一致，容器 `golang:1.25` + 一次性真实 PG）

- **并发回归测试** `TestMigrateDB_ConcurrentMigratorsSerialize`：一次性新库上 4 个 goroutine 同时 `MigrateDB`，连接池恰为 4（证明单连接设计不需第二条连接）；断言全部成功且 `schema_migrations` 恰含全量迁移（无重复无丢失）。无 `createdb`/`DROP ... FORCE` 权限时干净 skip。
- **负向验证**：临时禁用锁后该测试 5/5 必现竞态（`CREATE EXTENSION` 撞 `pg_extension_name_index` 唯一索引），证明测试真能抓到该 bug、非空过。
- **默认并行全树带真实 DB**：OSS 19 包、commercial 20 包全绿、零 FAIL（此前只有 `-p 1` 能过）。
- migrator.go / migrator_test.go 双仓字节一致，gofmt 干净。

## 边界

- 锁键是全局常量：同一 Postgres 实例上所有库的迁移共享一把锁。迁移是启动期低频操作，跨库串行无实际代价；如需按库隔离可改用 `pg_advisory_lock(数据库OID)`，当前不做。
- 生产多实例同时启动首次迁移同样被此锁保护（不只是测试）。
