# Required Context Fail-Closed 激活（V2.9 M4b）

**日期**：2026-09-02
**范围**：OSS + Commercial 双仓同步；V2.9 M4a 的受控续篇——required 上下文缺块的拒绝语义按 per-manifest 评审激活。本里程碑激活 outline 与 research 两个 capability；draft/quality/finalize 保持影子。

## 变更

1. **激活开关（`writingplan`）**：`ContextContract.EnforceRequiredContext`（默认 false）。每个 capability 拿着 M4a 影子数据独立评审后显式打开——不是全局策略，杜绝"一次合并打死所有存量 run"。
2. **拒绝语义（`writingruntime`）**：
   - 错误码 `CONTEXT_REQUIRED_MISSING`（RetryNever——数据缺口不会因重跑愈合），消息列出缺失块名；
   - `compileNodeContext` 拆为双通道：基础设施降级（source 失败/编译失败/落库失败）永远返回 nil envelope 不拒绝节点（基础设施缺口不是上下文缺口的证据）；仅"编译成功 + enforce 开启 + required 缺失"走 failNode；
   - 拒绝点同步写 attempt completion（status=failed + 稳定错误码 + 缺失块列表）——attempt 已 Start，不能悬空；测试驱动的修正；
   - 遥测 `MetricContextEnvelope`（`context_envelope`，有界基数）五状态：succeeded / required_missing / source_failed / compile_failed / persist_failed。
3. **激活范围**：outline 与 research（唯一 required 块 `contract_digest` 在 `StoreContextSource` 下始终有数据源）。draft/quality/finalize 的 required 包含 `document_state`——该块尚无数据源，保持影子，待其数据源落地后按同流程评审激活。

## 验证（双仓一致，容器 `golang:1.25`）

- 三个新测试：enforce 命中时节点失败带稳定错误码 + 缺失块名 + `required_missing` 遥测 + attempt completion 落账 + envelope 不落库；enforce 下 required 齐全照常注入执行；source 错误时 enforce 也降级不拒绝。
- 全树 19 个包 `go test ./internal/... -p 1` 全绿（`-p 1` 规避既有的 fresh-DB 迁移竞态，与本变更无关，见已知问题）；真实证据库未触碰；双仓 6 个文件字节一致。

## 已知问题（非本变更引入）

全树并行测试（默认 `-p` 并行包）时，`database` 与 `writingstore` 两个包会对同一全新 TEST_DATABASE_URL 并发跑迁移，`schema_migrations` 主键冲突。历史上未触发是因为此前验证都以单包或小集合运行。建议后续在测试迁移器中加 advisory lock，独立小变更处理。

## 边界

激活面 = {outline, research}；`context_envelope` 遥测未接 exporter 侧看板；`document_state`/`source_evidence`/`style_directives` 数据源与 draft/quality/finalize 的激活属后续里程碑；Role Policy 仍不在范围。
