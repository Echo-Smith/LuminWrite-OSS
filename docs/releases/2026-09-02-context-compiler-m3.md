# Context Compiler MVP（V2.9 M3：确定性上下文编译）

**日期**：2026-09-02
**范围**：OSS + Commercial 双仓同步；V2.9 主线第四步，落地 `docs/18` §18.5 的 Context Compiler MVP。纯编译库 + 落库存储，**编译器未接入执行路径**（M4 范围），不涉及运行时行为、部署或真实流量。

## 变更

1. **`internal/contextcompiler`（新，纯函数库）**：
   - `Compile(Input) → Envelope`：确定性装配九个块（contract_digest、through_line_anchor 驻留层、canon_facts、terminology、open_decisions、entities_cards、source_evidence、document_state、style_directives）；无时钟、无 map 迭代序、无外部状态——同输入同版本必出逐字节相同 envelope；
   - **分块预算**：总预算可配置（默认 6000，刻意不搬 NarraCat 的 12k），按权重分摊到块（canon_facts 22% 最重）；`CompilerVersion` 钉死块集/预算表/装配序，改动即新版本（同 policy hash 纪律）；
   - **驻留层 fail-closed**：`through_line_anchor` 拥有独立预算（1200）且永不裁剪；驻留内容超预算 = 编译失败（canon 膨胀是运营问题，不是静默截断的理由）；总预算 ≤ 驻留预算直接拒绝；
   - **溢出裁剪**：行粒度、装配序尾块先裁、整行保留或丢弃；`Trimmed`（原始/保留行数）如实记录；
   - **显式缺省**：只有 Node 在 `Wanted` 里声明的块缺数据才进 `missing`（三种 reason），未声明的空块沉默；
   - **双通道**：`Diagnostics`（工程观察）走包外方法，hash 只覆盖模型可见的 `Blocks`——元数据变化不影响 hash，hash 回答"模型看到了什么"；
   - token 记账 MVP 用行数近似（每行 1 token），确定性优先，真实分词器归 M5。
2. **迁移 `102_context_envelopes`**：`project_context_envelopes` 专表（FK writing_runs，绑定 run/node/attempt，`(run, node, attempt, hash)` 唯一）——同一编译重放幂等，同 attempt 不同编译（版本升级/输入变化）作为独立行保留，形成完整审计链。设计原稿的"run 事件落库"矫正为专表（docs/18 §18.11），run 事件体系继续承载路由/权威证据。
3. **`writingstore/context_envelope.go`（新）**：`SaveContextEnvelope`（幂等 upsert；nil JSON 数组经 `jsonbArrayArg` 归一为 `[]` 满足列约束）、`ListContextEnvelopes`（按 attempt，最新在前）、`GetContextEnvelope`。
4. **测试**：包级 5 个单测（确定性重放与 hash 仅覆盖 Blocks、驻留层超限 fail-closed、多行输入下尾块裁剪顺序与份额遵守、缺省通道按 wanted 声明、驻留线索排序归一化使输入顺序不影响 hash）；store 集成测试 `TestContextEnvelopePersistenceIsReplayIdempotent`（重放保存幂等、payload 重哈希等于记录 hash、同 attempt 不同编译并存、absent 查询）。

## 验证（双仓一致，容器 `golang:1.25`）

- fresh DB 全量 `writingstore` 套件（迁移自动应用至 102）+ `contextcompiler` 单测 + `writingruntime`/`writingplan`/`writingkernel` 回归：全部通过。
- 真实证据库 `writing_agent_evidence` 未触碰；临时验证库已删除。
- 双仓 6 个变更文件 `diff` 字节一致（另加 docs 三个共享文件）。

## 边界

编译器尚未成为 Node 执行的数据入口：`Input` 由调用方预装载（相关性过滤是调用方职责），required_context fail-closed 拒绝执行属 M4；真实 token 计数、预算监控与预压缩属 M5；UserMemory 风格指令与 DocumentAST 以输入注入，编译器不与 `internal/memory` 服务耦合。
