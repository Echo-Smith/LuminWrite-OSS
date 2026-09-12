# CapabilityManifest 上下文契约与影子接线（V2.9 M4a）

**日期**：2026-09-02
**范围**：OSS + Commercial 双仓同步；V2.9 主线第五步，落地 `docs/18` §18.5.5 的 Manifest 契约并让编译器成为 Node 执行的数据入口（影子模式）。**required fail-closed 未激活**（M4b 独立受控变更），存量 run 行为零变化。

## 变更

1. **`writingplan` Manifest 契约**：
   - `ContextContract{RequiredContext, OptionalContext, ForbiddenContext, ContextTokenBudget}` 嵌入 `CapabilityManifest`（复用既有 deep-copy 与注册校验链）；`ContextBlockName` 九个块名常量 + `ValidContextBlock`；校验：块名必须合法、三集合两两不相交（块不能既 required 又 forbidden）、budget 非负；`ContextWanted()` 返回 required∪optional 稳定序；
   - 5 个内置 capability 契约草案：research 唯一声明 `ForbiddenContext=[style_directives]`（证据收集保持风格中立）；outline/draft/quality/finalize 按角色声明 required（draft/quality/finalize 均需 contract_digest + document_state）+ optional。
2. **`contextcompiler` 白名单语义**：`Wanted` 非空即允许清单——只装配声明块，未声明块即使有数据也被跳过（M3 的"有数据必渲染"改为契约可绑定）；导出 `Blocks()`/`ValidBlock()`；非法 wanted 块名编译报错。
3. **运行时影子接线**（`writingruntime/context.go`）：
   - `ContextSource` 接口 + `StoreContextSource` 默认实现（经 run→document→project 从 store 拉 facts/terminology/decisions/questions/entities/threads；无 project 的 run 返回空 input，全部 wanted 块进 missing，优雅降级）；
   - `ContextEnvelopeSink` 接口（复用 M3 落库）；
   - `Orchestrator.Context/Envelopes` nil-safe：编译→校验→落库→注入 `ExecutionRequest.Context`→`LegacyNodeInput.Context`，任一失败降级为 nil envelope，**永不改变执行结果**（telemetry `runtime.context_envelope` 记录 succeeded/source_failed/compile_failed/persist_failed）。
4. **`writingstore`**：新增 `DocumentProjectID`（run→project 关联查询，M1 的 `writing_documents.project_id` 首次被消费）。

## 验证（双仓一致，容器 `golang:1.25`）

- 新测试：compiler 白名单（契约外块不装配/非法块名拒绝）、writingplan 契约校验（冲突/未知块/负预算/注册拒绝 + 默认契约抽查）、runtime 三个接线测试（编译→落库→注入全链路含 missing 记录；source 失败降级不改结果；默认关闭）。
- 全树 `go test ./...` + fresh DB `writingstore` 套件全绿；真实证据库未触碰；双仓 11 个文件字节一致。

## 边界

required-missing 只记录不拒绝（fail-closed 属 M4b）；`StoreContextSource` 的 contract_digest 是占位摘要（真实契约摘要归引擎侧）；document_state/source_evidence/style_directives 三个块当前无数据源（声明 required 的 draft/quality/finalize 会持续在 missing 通道出现，属预期影子行为）；Role Policy 角色→能力映射不在本里程碑（P1/V3.0）。
