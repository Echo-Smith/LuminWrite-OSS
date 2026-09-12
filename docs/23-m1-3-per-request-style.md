# M1.3 — per-request style/kb 解析进 runner：设计与实现记录

> V3.0 M1 第三增量（2026-09-04 实现）。目标：让 governed run 的 engine step 用上"这次请求要的风格"——治理路径此前 composition 即全局配置（进程生命周期固定 profile），无法响应 per-request 的 style 选择；per-request profile 进 runner 后，M1.4 的 runner 工厂才能用 `profile.KbID` 构造 `NewWriteStepWithKB`。

## 23.1 排查结论（实现前）

1. **legacy 语义**：server 按 `execCtx.StyleSlug` 解析 profile（`my_` 前缀 → userStyleStore，否则 `Loader.Get`，未知名落到内置 fallback）；`WriteStep` 用 `profile.SystemPrompt`/词表/`KbID`（KB 绑定随 profile 走，不独立解析）。
2. **治理路径缺口**：`EngineStepRunner.StepFactory` 是 `func() engine.Step`——step 在 composition 时构造，profile 只能是进程级；run 上没有 style 字段；`ExecutionRequest` 没有 style 通道。
3. **run 是请求单元**：`writing_runs` 是治理路径每次请求的真实落点，style 应随 run 走，而非随 composition 走。

## 23.2 设计决策

- **D1 style 挂 run 不挂 composition**：迁移 105 `writing_runs.style_slug`（VARCHAR(64)，NULL=默认语义），沿 legacy 词表（004 `agent_traces.style_slug`）。RunRecord/RuntimeRun/LoadRuntimeRun/CreateRun 全链接线。
- **D2 StepFactory 升级为 `func(StepEnv) (engine.Step, error)`**：`StepEnv{Request, Profile}` 每 attempt 构造 step——request（含 StyleSlug）与解析出的 profile 注入 factory；factory 错误照常 fail 节点（step 构造失败是真执行错误，不是风格缺口）。
- **D3 解析是 runner 的可插拔依赖**：`StyleResolver` 接口 + `LoaderStyleResolver`（包 `profile.Loader`）。resolver 挂在 `EngineStepRunner.Styles` 上，composition 保持 style-agnostic。
- **D4 降级语义（风格缺口≠执行契约缺口）**：空 slug / resolver 为 nil / resolver 错误 / 解析 miss → nil profile（引擎步骤内置 prompt），**永不 fail 节点**。与 M1.2 validator 降级、M4b 基础设施降级同一纪律。注意：`LoaderStyleResolver` 对未知 slug 返回 Loader 自身的 fallback profile（与 legacy server 行为对齐），不返回 nil——降级到"内置默认风格"也是一次有效解析。
- **D5 `my_` 用户自定义风格**：`LoaderStyleResolver` 显式不解析（返回 nil），因为 userStyleStore 需要 requesting user；`ExecutionRequest.UserID` 字段已备，composition 在 M1.4 接线 HTTP 层时补齐（届时换成组合 resolver：builtin 走 Loader，`my_` 走 userStyleStore）。
- **D6 orchestrator 只戳不改**：主循环把 `run.StyleSlug` 戳进每个 `ExecutionRequest`（一行），不做解析调用——解析频率是每 attempt 一次（runner 内），由 resolver 自身的缓存承担性能。

## 23.3 数据流

```text
CreateRun(StyleSlug) → writing_runs.style_slug (105)
    ↓ LoadRuntimeRun
Orchestrator 主循环: request.StyleSlug = run.StyleSlug
    ↓ executor.Execute
EngineStepRunner.Run:
    StepEnv{Request, Profile: resolveStepProfile(Styles, request)}
    → StepFactory(env) → step.Execute
```

## 23.4 实现切片

- 迁移 105（up/down）+ `RunRecord.StyleSlug` + `CreateRun` INSERT（16 列）+ `RuntimeRun.StyleSlug` + `LoadRuntimeRun` SELECT。
- `writingruntime/style_resolver.go`：`StepEnv` / `StyleResolver` / `LoaderStyleResolver` / `resolveStepProfile`（唯一降级路径）。
- `ExecutionRequest.StyleSlug/UserID`；orchestrator 戳入；`EngineStepRunner` factory 签名升级 + `Styles` 字段。
- 测试：`style_resolver_test.go`（5 单测：注入/空 slug/三种降级/factory 错误传播/Loader resolver 行为钉死）+ 4 个既有测试文件的 factory 签名适配 + `TestStyledRunResolvesProfileThroughOrchestrator`（DB：slug 经 105 列 round-trip → orchestrator 戳入 → factory 观察到 profile 实例与 slug）。

## 23.5 验收

- DB 验收 `TestStyledRunResolvesProfileThroughOrchestrator`（真实 PG）：带 `delivery-style` slug 的 run 全链交付成功；`LoadRuntimeRun.StyleSlug` 回读一致；draft runner 的 factory 收到的每个 StepEnv 都带正确 slug 与解析出的 profile；resolver 被以 run 的 slug 调用。
- 双仓 15 文件字节一致（迁移 2 + store 2 + runtime 6 + 测试 5）；双仓 `-p 1 -count=1` 全树带 DB 零失败。

## 23.6 遗留（M1.4）

- composition 工厂把 `LoaderStyleResolver`（+ userStyleStore 组合）与真实 LLM/KB 一起注入 runner：`Profile.KbID` → `NewWriteStepWithKB` 的 KB 选择。
- `my_` 用户风格解析需要 `ExecutionRequest.UserID` 由 HTTP 层（requesting user）戳入。
- server.go 的 `CreateRun` 路由尚未接收 StyleSlug 字段（M1.4 挂载时随 command 一起补）。
