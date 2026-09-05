# V3.0 — Capability Runtime 实施计划

> 承接 `docs/19`（治理内核）与 `docs/18`（V2.9 ProjectMemory/Context）。本文把改进路线 §7–§13、§16 的 V3.0 目标拆成可门控、可回滚、双仓同步的里程碑序列。核心不变量沿用 §8：**Everything as Capability，但 Stable Governance Kernel 不插件化**。

## 20.0 现状基线（2026-09-04 盘点）

- **治理运行时已建、已测、但未挂服务路径**：`writingruntime.Orchestrator` 与 `RolloutExecutor` 在非测试代码中从未构造；`newPersistentWritingAPI(store)` 的 `controller` 为 nil，`ControlRun` 直接返回 `errWritingRuntimeUnavailable`；没有任何地方调用 `Orchestrator.Execute`。V2.9 的 Context Compiler / ProjectMemory / Forgetting 因此不在真实 HTTP 流里被执行。
- **executor adapter 机器件已具备**：`executor_adapters.go` 有 `NewEngineStepExecutorAdapter`、`LegacyExecutor`、shadow 隔离（`NewShadowIsolatedExecutorAdapter`）、`EngineStepRunner`；`rollout_executor.go` 有 baseline/candidate/shadow 路由与 fail-closed 拒绝。W0 主要是 composition + 触发，不是从零造 adapter。
- **五套各自为政的注册/分发面**（V3.0 要收敛的对象）：
  1. `writingplan.CapabilityRegistry`（治理内核，5 个写作能力 + ContextContract）——目标模型。
  2. `engine/tool_registry.go` + `tool_plugin.go`（旧工具注册）。
  3. `mcp/registry.go` + `local_tool.go` + `server/mcp_server.go`（MCP client/server）。
  4. `editorial/editorial_tool_registry.go` + `agent_executors.go` + `role_agent_runner.go`（编辑部 agent/tool）。
  5. `writingquality/registry.go`（validator）。
- **编排现状**：线上写流程走 `workflow_handler.go` 的 `dagExecutor.Execute`（旧编辑部 DAG）；`AGENT_MODE=harness`。

## 20.1 里程碑序列（W0–W7 → V3.0 M0–M7）

每个里程碑 = 设计矫正回写本文 + 独立发布文档 + 台账 E-0xx + 门控日志 + 双仓字节一致 + 全树 `-p`（带真实 DB）回归 + 变更走 blind A/B（prompt/policy/runtime 语义变化时）。

| ID | 里程碑 | 依赖 | 相对量 | 验收要点 |
|---|---|---|---|---|
| **M0** | 服务路径接线（W0）：治理 Orchestrator + shadow RolloutExecutor + StoreContextSource 挂到写请求路径，env 门控、默认 shadow/off | 无 | M | 见 §20.2 |
| **M1** | Capability Registry 统一（W1）：五套面收敛为一个 `CapabilityManifest` + `CapabilityResolver`，迁移调用方 | M0 | **L** | 旧 registry 经 adapter 兼容、不双写权威；resolver 按 manifest 类型/输入/权限解析 |
| **M2 ✅** | Thin Orchestrator（2026-09-04 交付，docs/26）：主循环六动词显式化（`dispatch.go`：authorizeNode/prepareDispatch/dispatchAttempt/observeResult 逐字搬移，行为字节不变）+ delivery 路由表下沉（`DeliveryProtocol.Drive`/`deliveryDrivers()`，orchestrator 不再硬编码 draft/quality 分支，新交付类=协议内注册一行）+ **kernel 不变量反射守卫**（capability 合同面禁止 func/chan/store 句柄，`TestCapabilityContractHasNoWriteSurface` 钉死漂移） | M1 | M | orchestrator.go 796→749；双仓并行全树零失败；守卫测试 ×2 |
| **M3 ✅** | Runtime Lifecycle + Hook Bus（2026-09-04 交付，docs/27）：9 个命名 lifecycle 时刻（dispatched/before/submitted/checkpoint.before/after/completed/paused/failed/cancelled）+ `hookBus`（同步 fan-out、panic 遏制、nil 安全）+ **kernel 守卫**（LifecycleSnapshot 反射禁 func/chan/store 句柄——hook 只观察）+ telemetry 作第一个 hook（`MetricLifecycle` 同管线投影）+ `terminalLifecycleEvent` 集中分类；真实 orchestrator 序列测试逐位匹配 | M2 | M | `TestOrchestratorEmitsLifecycleSequence` + 守卫/bus 测试 ×4；双仓并行全树零失败 |
| **M4** | Quality Router（W4）：QualityEvidence→Accept/Polish/TargetedRewrite/RetryCapability/Replan/HITL | M2 | M | 不复制模型自报 confidence；Gate 答"能否进下一状态"，Router 答"不能则下一步" |
| **M5** | Role Capability Policy（W5）：role→capability 白名单，与 CapabilityManifest 联合决定可调用集；与现有 editorial 角色合流 | M1 | M | Researcher/Writer/Reviewer/MemoryKeeper 白名单；越权 fail-closed 进 authority 证据 |
| **M6** | ProviderAdapter（W6）：DeepSeek/OpenAI-compatible/未来 Pi 抽象到 executor adapter 之下 | M0 | S–M | Pi 若引入位于 ProviderAdapter 以下；LuminBuddy 继续持有 Contract/Plan/Permission/Budget/Artifact/Lineage/Quality/Rollout |
| **M7** | Capability Reliability Metrics（W7）：每能力可靠性/质量喂给路由与 rollout（复用 V2.8 evidence） | M1,M4 | M | 指标有界基数；驱动 M4 路由与 M0 的 allowlist/percentage 证据 |

关键路径：M0 → M1 → M2（脊柱）。M3–M7 可各自独立门控、按需排期，不必一次承诺全部。

## 20.2 M0（W0）设计

**目标**：让治理运行时在真实 HTTP 流里被执行，但**默认不改变用户可见行为**。

- **env 开关** `WRITING_RUNTIME_MODE ∈ {off, shadow, allowlist}`，默认 `off`（= 现状，controller 为 nil，零行为变化）。
  - `off`：完全不构造 orchestrator，与今天逐字节一致。
  - `shadow`：批准后的 governed run 由 `Orchestrator` 经 `RolloutExecutor`（shadow 模式）执行——baseline lane 权威、candidate lane 隔离进 shadow namespace；产出 `context_envelope`、shadow 对比、canonical artifact 走 baseline。用户可见流程仍是旧 editorial DAG，governed 执行只产生证据。
  - `allowlist`：命中 subject 走 candidate lane（受 promotion gate 约束），其余仍 shadow 采证。激活仍走 §9 独立授权，M0 只提供机制不提供授权。
- **composition**（`internal/server/governed_runtime.go`）：从 store/telemetry/engine-runner 构造 Orchestrator（Capabilities=DefaultCapabilityRegistry、Executors=按 5 capability 注册 `NewEngineStepExecutorAdapter`、StateMachine、Checkpoints、Initial、Materials、Context=`StoreContextSource`、Envelopes=store、ContextRuntime）+ shadow `RolloutExecutor` + `MutableRolloutPolicyProvider`（默认 `DefaultShadowPolicy`）。
- **执行触发**：`ApproveRun` 成功后，若 mode≠off，投递一个执行任务调用 `Orchestrator.Execute`（幂等、单 run 串行、错误进 run 事件不外抛）；`ControlRun` 的 Pause/Resume/Cancel 接到 orchestrator controller。
- **不做**：不改 editorial DAG 的用户可见路径；不在 M0 打开 allowlist/percentage 授权；不动 kernel 不变量。

**M0 验收**：mode=off 全树回归与现状逐字节一致；mode=shadow 下批准一个 governed run → 落 `context_envelope` + shadow 对比证据 + baseline canonical artifact（非 shadow namespace），且 UI 可见流程不变；composition 单测 + shadow 纵向测试 + 双仓一致。

**M0 实施拆分（2026-09-04）**：
- **M0a（已落）**：`writingruntime.RuntimeMode` + `NormalizeRuntimeMode`（未知/空→off）+ config `WRITING_RUNTIME_MODE`（默认 off）。零行为变化。
- **M0b-1（已落）**：`internal/server/governed_runtime.go` composition 脊柱——`newGovernedWritingRuntime(store, mode, deps, specs)` 按 capability 组装 baseline + shadow-isolated candidate + `NewShadowRolloutExecutor` + Orchestrator（含 `StoreContextSource`/`Envelopes`/`ContextRuntime`），`governedRunController` 适配 `writingRunController`（Resume→error）。mode=off→nil、allowlist→显式未接线报错、shadow 缺依赖→拒绝。工厂 capability-agnostic，per-capability runner 作入参。
- **M0b-2（待做）**：① 五 capability→engine-step 的 `EngineStepRunner` 映射（注意 `core.retrieval.search` 对应多步分支，需 composite step）；② **`writingstore.Store` 未实现 `TransitionStore`（缺 `RecordTransition`）**——生产 composition 需给 store 补 run-ledger transition 落账或加适配器；③ 批准后幂等执行触发器（单 run 串行、错误内化进 run 事件）；④ server.go 按 mode 挂载 controller + 触发；⑤ shadow 纵向测试（批准 governed run → 见 context_envelope + shadow 对比 + baseline canonical）。
  - **M0b-2a（已落 2026-09-04）**：破 ②——`Store.AppendRunEvent`（Store 级，复用 tx 版投影/幂等）+ `writingruntime.WritingStoreTransitionRecorder` 实现 `TransitionStore`（适配器置于 runtime 侧破循环依赖）；DB 门禁测试真实 PG 通过。
  - **M0b-2d（已落 2026-09-04，live 验证）**：V2.9 context 三件套接进纵向 harness（`fakeRuntimeStore.SaveContextEnvelope` + runner 的 `Context/Envelopes/ContextRuntime` + 每节点 `context_envelope` 断言）；离线纵向全绿，**真实模型（sensenova OpenAI-compatible，deepseek-v4-flash）跑 long_form 场景 PASS**，证明治理 orchestrator + 上下文编译 + shadow rollout + durable evidence 在真实模型下全链路执行。
  - **M0b-2b 续 / 2c（待做）**：生产 per-capability `EngineStepRunner` 工厂（真实 LLM/search/embedding/KB + Usage 提取）；server.go 按 `WRITING_RUNTIME_MODE` 挂载 controller + 批准后幂等执行触发器（首次把执行挂上生产写路径）。

### 20.2.1 M0b-2c 范围发现（2026-09-04，起服务验证前排查）

摸启动/执行路径时发现：**默认模板编译出的 plan 引用的能力，治理内核当前没有可运行的 executor**——
- `core.document.finalize` 的 executor 是 `kernel.document.finalize`（kernel 提交步，无对应 engine step）；
- 默认模板还引用 `core.validation.evidence`、`core.validation.fact` 两个 validator 能力，不在已注册的 5 能力内、也无 executor。

因此"挂载 runner + 起服务走真实路由"会在这些节点因缺 executor 失败——**这是能力未实现，不是接线可补**。M0b-2c 的正确边界据此收敛为：

- **M0 能证到的**：composition 脊柱（M0b-1）+ 内容层（M0b-2c 存储）+ 对**有 executor 的能力**（research/outline/draft/quality）的链路 live 执行（M0b-2d 已用真实模型 PASS）。
- **移交 M1 的**：补齐 validator（evidence/fact）与 finalize 的 executor、per-request style/kb 解析、以及"默认模板 plan 端到端服务"。M1（Capability Registry 统一）本就要把这些能力纳入统一 manifest + resolver，正好承接。

即：M0 交付"运行时可挂载、链路可跑、内容可存、上下文可编译"的地基与证据；"全量服务真实 plan"随 M1 能力补全自然达成，而非在 M0 硬接线。

## 20.5 M1 分解（Capability Registry 统一 + 让真实 plan 端到端服务）

M1 关键路径已排查清楚（2026-09-04）：最简真实 plan `tpl_fast_v1` = draft→quality→finalize；**draft（`NewWriteStepWithKB`）、quality（`NewPostReviewStep*`）的 engine step 已存在**，唯一缺的是 **finalize executor**（产 `revision_set` + 提交文档版本，kernel 提交、非 LLM step），且每个模板都以它收尾。kernel 原语齐备：`RevisionSet`/`ApplyRevisionSet`（revision.go）、`CommitDocumentVersion`、`lcp` 解析器、quality_state 门（candidate→accepted→verified）。

| ID | 增量 | 依赖 | 相对量 | 验收 |
|---|---|---|---|---|
| **M1.1 ✅** | finalize executor（2026-09-04 交付，按 docs/21 §21.8 简化版）：`FinalizeRunner` 读已 promoted 版本 → 产 revision_set artifact 落内容寻址存储（提交/晋升由 M1.0 协议 A/B 完成，原设想"finalize 调 `CommitDocumentVersion`"被 §21.8 约束 2 矫正） | M0b | M | `TestDeliveryProtocolThroughOrchestrator` 真实 PG 三节点全链：revision_set 一致性断言 PASS；质量门 fail-closed 由 M1.0 协议测试覆盖 |
| **M1.2 ✅** | validator executor（2026-09-04 交付，docs/22）：`ValidatorRunner`（evidence/fact 双 kind，degrade-don't-fail）+ 目录补齐三 manifest（evidence/fact validator、research.strict）——sourced/strict 模板自此可编译为 T1；DB 验收 sourced 形状全链（evidence_report 落 canonical + 交付谱系一致） | M1.1 | M | sourced/strict 编译钉死 + 真实 PG 全链 + 6 单测（fake LLM httptest） |
| **M1.3 ✅** | per-request style/kb 解析进 runner（2026-09-04 交付，docs/23）：`writing_runs.style_slug`（迁移 105）+ `StepFactory(func(StepEnv))` 每 attempt 构造 step + `StyleResolver`/`LoaderStyleResolver` 降级解析；KB 绑定随 profile（`KbID`），my\_ 用户风格留 M1.4 组合 resolver | M1.2 | S–M | DB 验收：slug round-trip → orchestrator 戳入 → factory 观察 profile；5 单测 |
| **M1.4 ✅** | 2c 挂载 + 执行触发 + **起服务端到端**（2026-09-04 交付，docs/24）：`mountGovernedRuntime`（mode 门控）+ per-capability runner 工厂（真实 LLM/KB/敏感词/jiaozhen）+ CreateRun/ApproveRun 双点幂等触发 + registry Activate seam 统一编译与 dispatch 目录；live 验收：真实 HTTP + deepseek-v4-flash 四节点全链 completed（revision_set=1、accepted_draft） | M1.1–1.3 | M | `TestGovernedRunEndToEndThroughHTTP` 双模（离线=治理 pause，live=completed）真实 PG PASS；双仓并行全树零失败 |
| **M1.5 ✅** | Capability Registry 统一（五套面收敛，docs/25 分片交付）：**切片 1**（UnifiedManifest+Resolver+CheckSingleAuthority + 四 surface adapter）、**切片 2**（admin `/capabilities` 端点，守卫常驻）、**切片 3**（RolePolicy/PrefixRolePolicy + editorial 角色策略 + `?role=` 视图，调度强制点留 M5）、**切片 4**（GovernedPermissions 映射，视图不再暴露裸 legacy 标记）——2026-09-04 全部交付 | M1.4 | **L** | 双仓并行全树零失败；旧 registry 经 adapter 兼容、无双权威（结构化前缀 + 常驻守卫） |

M1.1–M1.4 是"让真实 plan 端到端跑通"的脊柱（约 3–5 周）；M1.5 是更大的统一工程（约 6–10 周，分片交付）。**M1.1–M1.4 全部交付——治理运行时已挂上生产写路径并经真实模型端到端验证**；**M1.5 四切片全部交付（统一视图/守卫/admin 端点/角色策略管道/权限映射）——V3.0 M1 里程碑整体收官**；下一步 V3.0 M2（Thin Orchestrator）。

## 20.3 风险与纪律

- **服务路径变更是本项目最高风险面**：M0 一律默认 off、shadow 不改可见行为、执行触发幂等且错误内化；灰度到 allowlist 是 §9 的独立授权变更，不由 M0 代码触发。
- **M1 统一是最大不确定性**：迁移期旧 registry 必须经 adapter 兼容、不得出现双权威；先只统一写作能力还是含 MCP/Tool/Editorial，需在 M1 设计门单独定（影响近一半工作量）。
- **每步固定开销**：双仓同步 + 门控 + 迁移检查 + 真实模型纵向验收 + blind A/B（语义变化时）——这是"慢但可审计"的定价。
- **kernel 不插件化**：Contract/ExecutablePlan/Artifact/Document/Lineage/Permission/Quality/Snapshot 永远是内核，Hook/Capability 不得绕过（§8）。

## 20.4 与 V2.9 的衔接

V2.9 的 Context Compiler / ProjectMemory / Forgetting 在 M0 前是"建好未挂载"的死代码；M0 把它们接进真实流，M1–M7 在其上组合能力、路由与治理。V2.9 遗留项（UserMemory 四层衰减、`context_pressure` 看板、provider-exact 分词、source_evidence/style_directives 数据源）不阻塞 V3.0，按需在对应里程碑补齐。
