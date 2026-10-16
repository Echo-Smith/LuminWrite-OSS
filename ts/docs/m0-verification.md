# M0 验证记录

日期：2026-10-16。对照功能地图 v1.3 的 §8.3。只记录和 M0 有关的项。后面的里程碑标成未做。

## V1 SQLite 与 Node 24

在 Node v24.22.0 上直接 `import { DatabaseSync } from "node:sqlite"`，没有加 `--experimental-sqlite`，进程没有打印 ExperimentalWarning。内存库能执行 `PRAGMA journal_mode`。

`@earendil-works/pi-durable` 1.3.0 的 Node SQLite 适配器在打开连接时执行 `PRAGMA journal_mode = WAL` 和 `PRAGMA synchronous = NORMAL`（包内 `dist/storage/sqlite/node.js`）。`journal_mode` 会写进库文件；`synchronous` 是连接级设置，另开一个 `node:sqlite` 连接读到的默认值不能用来否定适配器。

崩溃续跑：`packages/harness` 的测试把子进程停在 replay-safe 工具里（先把计数器写成 1，再等释放文件），父进程 `SIGKILL`，再打开同一个 SQLite 文件，`resume()` 之后 `submission(id).wait()`。计数器变成 2，提交 id 不变。工具用 `api.memo` 记住“已经开始等待”，重跑时不再等待。

没有做断电模拟。Pi 文档自己的说法是：进程崩溃后已提交的检查点还在，断电可能丢掉最近一次提交。如果这条存储以后不稳定，退路仍是 JSONL，或按 D16 提前做 Postgres 存储。

## V2 Postgres 存储

M1 结束时才评估自写 Postgres 存储。M0 用官方 SQLite 适配器。未测。

## V3 接口变动

锁定 `@earendil-works/pi-ai`、`pi-durable`、`chord` 1.3.0。

功能地图里的 `HarnessPort` 草图和 1.3.0 的真实接口不一样：`submit` 在 `Conversation` 上，`resume()` 是同步且幂等的，`watch()` 返回带 `start/stop` 的 `WatchHandle`，不是异步迭代器。这些差异被包在 `@luminwrite/harness` 里，业务代码看不到 Pi 的类型。真实接口能完成检查点、`requestId` 幂等和杀进程续跑，所以没有退回 pi-agent-core。

## V4 多进程

官方存储没有跨进程锁，一个进程持有一个库文件。M0 不测多进程。

## V5 事件翻译

只用 `conversation.watch()`，不用实验性的 `watchEvents()`。`watch()` 的回调帧被收成 `HarnessPort.watch()` 的异步迭代。回合结束后，把这次快照里还没见过的条目写成 `run_events`，`type` 用条目自己的 `kind`。实测一轮大纲至少会出现 `pi.user`、`pi.system`、`pi.assistant`。产品事件另有 `turn.started` 和 `edits.applied`。SSE 目前是把已落库的事件一次性写成 `id:` / `data:`，不是长连接。

## V6–V9

微调档位、风格市场、协同和热榜都不在 M0。未测。

## V10 块 id 与 Better Auth

`doc-model` 的 `splitBlock` 把原 id 留在前半，后半用新 id。`mergeBlocks` 只合并相邻块，并保留前一块的 id。这是我们要对齐的 UniqueID 行为，有单元测试。

`@tiptap/extension-unique-id` 3.32.0 提供了 `generateUniqueIds`，不需要创建 Editor 就能给 JSON 补 id。M0 没有装 StarterKit，也没有在 Node 里跑 Editor 的拆段、合段和跨块粘贴，所以真实扩展的行为还没有实测。Better Auth 的 api-key 插件要到 M1，PGlite 下限流存储也还没碰。

## 测试

`pnpm -r build` 和 `pnpm -r test` 不需要 API 密钥。文档模型有哈希向量、冲突码、不同块互不影响，以及 fast-check 属性测试。数据库测试在 PGlite 上覆盖块级比较并交换、过期锚点拒绝、回滚追加版本、版本表的 append-only 触发器、回合幂等键和提案拒绝。Harness 测试覆盖大纲、初稿、只改一段、重复 `requestId`、关掉进程再打开、空话不落版本，以及上面的杀进程续跑。HTTP 测试和 `pnpm demo` 走同一条路径。

## 和功能地图不一致的地方

1. **提交会等到回合结束。** 草图里的 `submit` 马上返回 `queued` 或 `running`。M0 的 `submitTurn` 会等 Pi 的 `submission.wait()`，再返回终态。HTTP 因此是同步的。M1 的任务 API 再改成入队后立刻返回。
2. **测试数据库是 PGlite。** D3 把本地模式定为次要目标，但要求 SQL 能在 PGlite 上跑。CI 不另起 Postgres。`openPostgres(connectionString)` 仍然用 node-postgres，类型上转换成 PGlite 那份 Drizzle 客户端。
3. **`db` 依赖 `doc-model`。** 锚点校验、rebase 和块投影在同一个事务里完成。功能地图写的是 db 只依赖 contracts。拆开会让版本和投影不在一个事务里。
4. **用量账本的写入在 `db`，接口在 `edition`。** `models` 不依赖 `db`，避免路由层碰 SQL。
5. **M0 的写作工具只有 `read_blocks` 和 `propose_edits`。** `delegate` 和 `search_kb` 属于 M1 的角色和检索。Harness 因此也不依赖还是空桩的 `roles`、`research`、`prefs`。
6. **`run_events.seq` 用 integer。** 地图写 bigint。M0 的序号到不了 2^31，SSE 的 `id` 仍然是这个序号。
7. **块内 diff 不用 jieba。** `doc-model` 要在浏览器里跑，不能依赖 Node 专属分词。拉丁字母和数字成词，每个汉字单独成词。
8. **`insert_block` 不接受调用方指定的 id。** 新块 id 由 `doc-model` 分配。重放人工插入时，文本能对齐，新块 id 不能对齐。已有块的替换可以按 id 重放。
9. **同一批里两次改同一块算 `overlap`，整批拒绝。** Go 版 `ApplyRevisionSet` 会按顺序重封。M0 选择拒绝，避免一批操作里的后一条偷偷吃掉前一条的结果。
10. **说人话词表是起步版。** 这个仓库里没有 `lw-eval/lweval/aitone.py`，也没有 `aitone_zh.json`。`packages/plainspeak/src/aitone_zh.json` 按功能地图里的例子整理，并在文件里注明。评测冒烟只做确定性排序：具体文本在前，中性负对照的 `hollowScore` 最高。没有 6 道现场题，也没有 ¥3 预算，因为 CI 不能依赖密钥或外网模型。
11. **写作回合不调用 `models` 路由。** 回合内部是 faux。`models` 自己有路由、回退和兼容接口的测试。这样即使环境里碰巧有密钥，测试也不会出网。
12. **`replace_text` 写回时把块收成一个文本子节点。** M0 不保留 mark。
13. **gitleaks 的 CI 使用 `gitleaks detect --no-git`。** 默认规则曾经漏过裸 `sk-` 密钥，历史里那把已经作废的密钥不重写。工作区扫描加上自定义规则。已有测试夹具（幂等键、素材编号、示例域名）会误触发默认的 generic-api-key，在 allowlist 里按字符串排除，不排除整个目录。

## M1 接着做什么

知识库上传和混合检索、检索规划、研究员和审校等子 Agent、提案的逐块接受、非实时修订界面、文章库基础能力，以及任务 API 和 API Key。出口仍然是功能地图里的 H2、H3，以及一个外部 Agent 只靠公开 API 走完写、改、回滚。
