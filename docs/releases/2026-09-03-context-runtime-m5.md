# Context Runtime（V2.9 M5）

**日期**：2026-09-03
**范围**：OSS + Commercial 双仓同步；V2.9 M5 Context Runtime——真实分词、预算监控与预压缩、溢出优先级保留、按失败类别命名的恢复路径、document_state 数据源与 draft/quality/finalize 激活。设计依据 `docs/18` §18.5.2/§18.5.6，矫正记录回写 §18.14。

## 变更

1. **真实分词（M5a，`contextcompiler`）**：M3 的逐行近似（1 行 = 1 token）替换为分段感知的确定性估算器 `tokenCount`——CJK/假名/谚文与全半角形式块（U+FF00–FFEF）按 ~1 token/rune，其他文字按词 ~rune/4，标点逐 rune 计；对 CJK 重型 provider 分词器（Qwen/DeepSeek）有意高估——预算是安全限，低估是不可恢复方向。`CompilerVersion` 1→2（裁剪存活内容变化即行为变化）。envelope 新增 `TotalTokens`（元数据，不进 hash）；`truncateToTokens` 改为行粒度 token 记账（渲染字节与记账一致）；驻留溢出改用哨兵错误 `ErrResidentOverflow`（保留 "fails closed" 措辞供既有证据工具 grep）。
2. **预算监控与预压缩（M5b）**：编译器报告压力——`Envelope.Pressure()` 与 0.70/0.85 阈值常数，越阈记入包外诊断（scope `pressure`，不进 payload、不动 hash）。运行时新增 `ContextRuntime` 组件：per-capability 冷却（默认 5 分钟）+ 进行中守卫，`CompressedBudget` 按 0.8×缩预算且不得低于驻留保护。orchestrator `compileNodeContext` 接线：压力 ≥0.85 时受控重编译一次，更小的 envelope 替换注入并落库（基础设施失败一律降级回原 envelope，绝不因此拒绝节点）。遥测新增 `MetricContextPressure`（`context_pressure`，有界基数）：warn / recompile / recompiled / recompile_failed / recompile_refused / cooling / in_flight。
3. **溢出优先级保留（M5c）**：M3 的"每块独立 share 独立裁剪"升级为保留遍历——按有效保留序（`RetentionPriority` 在前，其余按装配序，驻留块恒第一）访问各块，每块保留至其 share + 尚未认领的预算（驻留层闲置 + share 表 floor 空隙 + 高优先块未用额），余量按行边界裁剪至 0，裁剪/丢弃如实记入 `Trimmed`/`Missing`。不变式可证明并被测试钉住：Σ used ≤ pool = total − resident。契约面：`Input.RetentionPriority`（非法块名拒绝，空白/重复忽略）+ `ContextContract.RetentionPriority`（进 `ValidateContextContract` 校验）+ `ContextRetentionPriority()`；draft 契约声明 `[document_state]`（续写最需要的是演化中的文档）。
4. **恢复路径（M5d）**：`RecoveryPathFor(category, recompiles)` 决策表——`source_failed`→`reload_source`（重试后→`human_escalation`）、`compile_failed`→`recompile`（重试后→`human_escalation`）、驻留溢出→`recompile_compressed`（重试后→`human_escalation`）、预算耗尽→`recompile_compressed`、`persist_failed`→`recompile`（影子纪律：落库失败从不阻断执行）。source/compile/precompress 失败的 `MetricContextEnvelope` 遥测 `Reason` 携带路径名，证据消费方按名路由。
5. **document_state 数据源与激活（M5e）**：新渲染器 `renderDocumentState`——文档序遍历 AST 子树，节标题 + 每块一行（80 rune 截断），无版本的文档渲染真实空态"文档尚未提交任何版本：正文为空"。`StoreContextSource` 接线：文档记录缺失或 current-version 指针悬空→留空（真实数据缺口，fail-closed 显式记 missing）；记录存在即可满足 required。据此激活 draft/quality/finalize 的 `EnforceRequiredContext: true`——required `document_state` 自首次 run 起有稳定数据源，与 M4b 的 per-manifest 评审流程同构（outline/research 先例）。

## 设计矫正（回写 docs/18 §18.14）

1. **"真实分词"落地为确定性分段感知估算器，而非 provider BPE**：纯函数契约（无网络、无词表资产、逐字节可复现）与双仓纪律优先；provider-exact（cl100k/o200k）作为后续可选项，届时同样走版本升级。
2. **驻留层闲置预算回收进保留池**（M3 锁死语义的矫正）：M3 下 Σ share ≤ total − ResidentBudget，全局溢出永不会发生，"溢出优先级保留"无从谈起；驻留层的保护 = 从不裁剪 + 自身溢出 fail-closed，其闲置不再冻结。这是 CompilerVersion 升版的第二个理由。
3. **M3 测试重写**：`TestCompileTrimsTailFirstInAssemblyOrder` → `TestCompileRetentionWalkRespectsBudgetUnderOverflow`。旧断言（canon ≤ 静态 share）与新语义直接冲突——canon 合法回收驻留闲置与 floor 空隙后高于静态 share；新断言钉住预算不变式、裁剪记录与装配块一致、丢弃即显式 missing。

## 验证（双仓一致，容器 `golang:1.25`）

- 10 个文件字节一致（compiler.go / compiler_test.go / context.go / context_test.go / context_runtime.go / context_runtime_test.go / documentstate.go / orchestrator.go / telemetry.go / capability.go）。
- 全树 `go test ./internal/... -p 1` 全绿（`-p 1` 规避既有的 fresh-DB 迁移竞态，见 M4b 已知问题，本变更未触碰）。
- 新测试：分词感知分段契约、压力三档阈值（诊断不进 payload、hash 不变）、保留遍历预算不变式与 `RetentionPriority` 覆盖、`RecoveryPathFor` 决策表、`ContextRuntime` 五态分类 + 压缩预算下限、orchestrator 预压缩/预警接线、document_state 渲染器（章节/截断/空态/确定性）；M4b enforce 测试与 shadow wiring 测试适配（draft 供 document_state）。
- DB-gated 测试（无 `TEST_DATABASE_URL`）按惯例 skip；真实证据库未触碰。

## 边界

- `context_pressure` 遥测未接 exporter 侧看板。
- 未引入 provider-exact 分词；估算器系数是有意的保守选择，词表化/配置化留待证据驱动。
- `source_evidence` / `style_directives` 数据源仍缺位（各自契约里为 optional，不阻塞 enforce）。
- Role Policy 不在范围；percent/production 门禁语义未触碰。
