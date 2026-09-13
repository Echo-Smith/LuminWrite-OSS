# 记忆注入路径优化计划：Memory Port 契约层 + Harness/Pipeline 补全

> **执行状态（2026-09-12）**：P0、P1、P2 核心已实现并双仓（commercial + oss）同步落地，
> 双仓 `go build ./...` 与 `go test ./...` 全部通过。
> - P0 ✅ `internal/memoryport`（三动词契约 + Bundle 分槽 + 渲染搬迁）+ adapter；
>   MemoryGateStep/MemoryExtractStep/ChatStep/AddMemory/PostReview/Harness 全部走 Port；
>   黄金集（adapter_test）+ prompt 快照（memory_snapshot_test）+ import 边界 CI 测试（boundary_test）。
> - P1 ✅ MemorySection（worldstate diff 推送）；retrieve_context 走 QueryOnDemand 真检索
>   （带 ExcludeIDs 去重）；retrieve_context+remember 全意图注册（含旧 ChatToolDefs 无搜索返回 nil 的修复）；
>   remember 工具 + SDK.Create PII 补丁（审计 L8）；会话收尾 SubmitOutcome（Transcript+BeforeRevision 浓信号）；
>   StoreMessage 异步 embedding；compaction 摘要落 working_summaries。
> - P2 ✅ 衰减闭式（types.go）；同步 embedding（Create + Extract）；
>   Gate union 多信号召回（语义 Top-20 ∪ bigram 关键词命中 ∪ 最近缺 embedding，gate.go unionRecall）；
>   提取浓信号（ExtractSession.BeforeRevision + RevisionExtractor diff 通道，deepseek_extractor）。
> - 未做（按计划顺延）：P2-2b 近重复折叠（需 SELECT 返回 embedding，等 P3 遥测证据）；
>   离线 consolidation job；P2-5 实体 boosting（证据门控未触发）；P3 遥测/黄金集扩容/shadow；P4 DAG/governed 接入。
> - 同步方式：双仓同 module path；15+2 文件以 git patch 落地（双仓改动前逐字节一致），9 个新文件直接新增；
>   server.go 双仓分叉（592 行），按语义手工合并 7 处装配点；types.go 保留 OSS 既有注释差异。

- 日期：2026-09-12
- 范围：`backend/internal/agent`（Harness）、`backend/internal/engine`（Pipeline）、`backend/pkg/memory` / `backend/internal/memory`（记忆系统）
- 不含：DAG/编辑部模式与 governed 内核接线（另立计划，复用本计划的契约层）

## 1. 背景与问题

前序分析（DAG 记忆消费、mem0 对比）确认了三个事实：

1. **同一套记忆，三种运行时接入深度割裂**：Pipeline ≫ Harness ≈ DAG。Pipeline 读写闭环基本成立；Harness 每次请求跑完整 Gate 检索，但检索结果**没有任何渲染进 prompt 的路径**（`buildSystemPrompt` 注册的 WorldState section 无 MemorySection，`internal/agent/harness.go:594-686`），只能靠 LLM 主动调 `retrieve_context(source="memory")`，而该工具查的是**本轮内存副本做关键词匹配**（`internal/agent/tools.go:1188-1237`），且只在 writing 意图注册（polish/shorten/chat 连拉取入口都没有）。
2. **写入信号薄、合并不语义**：Pipeline 提取输入只有终稿+确定性字段，不含对话与改前改后 diff（`internal/engine/steps/memory_steps.go:203-210`）；冲突合并按 `(category, key)` 精确匹配、新值无条件赢（`pkg/memory/conflict.go:126-156`），同义条目会随时间堆积。Harness 完全没有自动提取。
3. **消费方与记忆实现直接耦合**：Harness/Pipeline 直接 import `pkg/memory` 的 `RetrieveRequest`/`MemoryContext`/`FormatMemoryForPrompt` 等类型与格式化函数，`AgentContext.LocalMemory` 字段被 OrgKnowledge/对话历史等多用途复用。记忆系统要"自进化"（换提取策略、加衰减模型、接 LLM 合并、将来换存储），每次演进都会横穿三个执行引擎的渲染代码——**没有稳定的消费契约，就无法独立迭代**。

## 2. 设计目标

- **G1 注入路径唯一化**：所有执行引擎经同一个窄接口消费记忆，读路径按"消费者槽位"而非"记忆内部结构"组织。
- **G2 记忆系统可独立演进**：执行引擎只依赖由**消费方拥有的 DTO 契约**（anti-corruption layer）；`pkg/memory` 的门控/衰减/提取/存储策略可在契约后面自由替换，契约本身只做加性演进 + 版本协商。
- **G3 Harness 补齐到与 Pipeline 同水位**：记忆进 prompt、工具全意图可达、会话结束有提取。
- **G4 Pipeline 写回增强**：提取输入换浓（对话+diff+反馈）、入库前加语义判重、embedding 盲区收窄。
- **G5 自进化有反馈闭环**：注入命中/砍除/关闭等遥测 + 门控黄金集回归，让记忆策略的每次变更可评测、可灰度、可回滚。

非目标：不改记忆 schema（表结构）；不动 DAG/governed 执行路径；不引入外部记忆框架（mem0 等）——借鉴其 update-phase 思想但在 Port 适配器内部自研。

## 3. 现状工程完整度评估（打分依据详见 §7 验收）

| 维度 | Pipeline | Harness | 说明 |
|---|---|---|---|
| 存储/检索基建（PG+pgvector+实体+短期+工作摘要） | 8/10 | 8/10 | 共享同一 Gate 与存储；扣分项：embedding 保存后异步补，新记忆有语义盲区 |
| 读路径门控（tier 分流/衰减/证据边界/两击晋升/拒答） | 8/10 | 8/10 | Gate 设计是超出 mem0 的强项；扣分项：衰减为逐日近似循环、灰度关闭即全盲 |
| 注入消费（检索结果真正到达模型） | 7/10 | **2/10** | Pipeline：AddMemory 进 WriteStep/ChatStep、Tier3 进 PostReview，但预算最先被砍；Harness：检索了但不进任何 prompt，拉取工具查内存副本且仅 writing 可用 |
| 写回闭环（自动提取+反馈学习） | 6/10 | **1/10** | Pipeline：提取信号薄、key 精确合并；Harness：无提取、无 remember 工具、"记住X"不落地 |
| 短期/会话记忆 | 7/10 | 5/10 | Pipeline 有 compaction 落库+working summary；Harness compaction 只改内存不落库、写历史不带 embedding |
| 遥测与评测（注入是否有效、是否被采纳） | 3/10 | 1/10 | 仅 `memory.used` WS 事件与 `EmitMemoryExtracted` trace；无注入质量指标、无黄金集回归、无 A/B |
| 接口隔离/可演进性 | 3/10 | 3/10 | 消费方直依 SDK 类型与格式化函数；`LocalMemory` 字段类型复用冲突（types.go 注释自述）；无契约版本 |
| **综合** | **≈62%** | **≈25%** | |

结论：记忆"内核"（Gate/分层/晋升）完成度高，"边界"（消费契约、写回信号、遥测）完成度低。优化重心应放在边界而不是继续雕琢门控。

## 4. 架构：Memory Port 契约层（即我们的"注入模块"）

> 定位说明（2026-09-12）：本层就是"参考 mem0 2026 读端思路、自研的注入模块"的 Go 实现。模块边界刻意收窄为**只管注入**（三动词契约 + Bundle 分槽 + 消费端预算渲染）；提取/合并/衰减等策略全部留在 `pkg/memory` SDK 后面，不进模块。与 mem0-2026 的逐项对照：ADD-only accumulate=我们的 candidate/两击/supersede（已有且更保守）、单遍 LLM 提取（已有）、读端多信号融合与时间排序=P2-2（本计划）、实体链接 boosting=P2-5（本计划）。**禁止**发展出第二个并行的"记忆模块"（§8-L6：governed envelope 以 Bundle 为内容供给源，DAG 接同一 Port）。

```
┌──────────────┐  ┌──────────────┐        ┌─────────────────┐
│ Pipeline     │  │ Harness      │  (未来) │ DAG / governed  │
│ steps        │  │ tools+prompt │        │ executors       │
└──────┬───────┘  └──────┬───────┘        └───────┬─────────┘
       │        消费方拥有的 DTO，仅 3 个动词        │
       ▼                ▼                         ▼
  ╔═══════════════════════════════════════════════════╗
  ║  internal/memoryport   （稳定契约，接口冻结）      ║
  ║  PrepareInjection / QueryOnDemand / SubmitOutcome ║
  ╚═══════════════════════════╤═══════════════════════╝
                              ▼ adapter（唯一允许 import pkg/memory 的边界）
        ┌───────────────────────────────────────┐
        │ pkg/memory SDK（Gate/提取/衰减/合并…）  │ ← 自进化发生在这里
        │ + 策略配置 / rollout / 遥测埋点         │
        └───────────────────────────────────────┘
```

### 4.1 契约定义（新包 `backend/internal/memoryport/`）

```go
// ContractVersion 由适配器上报；消费方按已知版本渲染，未知字段忽略（加性演进）。
const ContractVersion = 1

// InjectionBundle 是"门控后可注入"的唯一载体。按消费者分槽，
// 记忆内部概念（tier/candidate/decay）不出现在契约里。
type InjectionBundle struct {
    Version        int
    WriteDirectives []Directive  // → 写作 prompt（现 Tier1+2）
    ReviewGuard    []Directive   // → 审校 prompt（现 Tier3）
    EntityProfile  string        // → 实体网络渲染文本（可空）
    WorkingSummary string        // → 会话工作摘要（可空）
    RefusalReason  string        // 非空=主动拒注入，消费方可提示
    Trace          TraceInfo     // 记忆ID列表等，供遥测回填
}
type Directive struct {
    ID       string // 供 dismiss/telemetry 引用；消费方不得解析其余语义
    Text     string // 渲染就绪的短语（格式化归 Port 侧，不再归引擎侧）
    Kind     DirectiveKind // preference | feedback | entity | summary
    Strength float64       // 仅用于消费端预算截断排序，0..1
}

// Port 是三动词契约。执行引擎只 import 本包。
type Port interface {
    // PrepareInjection：会话/请求开始时的批量预取+门控（现 Gate 的对外形态）
    PrepareInjection(ctx context.Context, req InjectionRequest) (*InjectionBundle, error)
    // QueryOnDemand：retrieve_context 类工具的后端，必须重新过门控/检索，
    // 而不是查内存副本
    QueryOnDemand(ctx context.Context, req QueryRequest) ([]Directive, error)
    // SubmitOutcome：请求/会话结束后的写回入口（提取输入由消费方凑齐，见 4.2）
    SubmitOutcome(ctx context.Context, out Outcome) error // 实现保证异步、不阻塞
    // Capabilities：能力协商，供消费方决定渲染哪些槽位
    Capabilities() Capabilities
}
```

配套 DTO：`InjectionRequest{UserID, ConversationID, Query, Intent, Explicit map[string]any}`；`Outcome{UserID, TraceID, Article, BeforeRevision, Transcript []Message, Feedbacks, Signals, Mode, StyleSlug}`——**注意 BeforeRevision/Transcript 字段现在不存在**，这是为 G4 铺路，适配器暂可忽略新字段（加性演进示例）。

### 4.2 演进纪律（保证"相对独立"的关键约定）

1. **import 规则**：除 `memoryport/adapter` 外，任何执行引擎代码禁止 import `pkg/memory`（CI lint 强制，见 P0-4）。引擎只见 DTO。
2. **渲染归引擎、格式化归 Port**：Prompt 预算、砍段、section 拼装是消费方职责（它知道剩余 token）；单条记忆怎么措辞（`--- 用户写作偏好 ---` 等）移到 Port 侧，引擎不再持有 `FormatMemoryForPrompt`。
3. **加性版本**：契约只加字段/槽位不删改；`Capabilities()` 让引擎按版本渲染。破坏性变更走 `ContractVersion` bump + 双适配器并行，不允许全库同步改。
4. **策略开关全在 Port 侧**：门控阈值、衰减、LLM 合并、灰度（现有 `RolloutConfig` 模式保留）都是 SDK 内部配置，引擎无感。
5. **`AgentContext.LocalMemory` 退役**：编辑部/引擎的字段复用改名为显式类型；DAG 计划接同一 Port。

## 5. 实施阶段

### P0 — 契约层落位（纯重构，行为零变化）≈ 2-3 天
1. 新建 `internal/memoryport/`（契约+DTO）与 `internal/memoryport/adapter/`（包装现 `Service.Retrieve/Extract`，含 tier→槽位映射与格式化函数搬迁）。
2. `MemoryGateStep`、`harness.retrieveMemory`、`ChatStep` 改为依赖 `Port`；`server.go` 装配点注入适配器。Harness 的 `MemoryRetriever`（agent/session.go:35-37）并入 Port。
3. `PromptBuilder.AddMemory`/`FormatReviewGuardForPrompt` 只消费 `InjectionBundle`。
4. CI 加 import-boundary 检查（`scripts/` 下 grep 型 lint 或 depguard 规则）。
5. 黄金集回归测试先行固化现行为（见 P3，但最小版在 P0 建）：≥30 条 (query, 记忆库快照) → 期望注入集 的 table test，重构前后 diff 为空。**另加渲染快照**：WriteStep/ChatStep/PostReview 的最终 prompt 字符串 golden file——门控黄金集只锁"选哪些记忆"，锁不住 P0-1 中格式化函数搬迁造成的措辞/空白漂移，两者都要 diff=0 才配得上"行为零变化"。
- **验收**：所有现有 memory 单测+新黄金集全绿；import 边界检查仅约束消费侧 `internal/agent` 与 `internal/engine`（grep 零 `pkg/memory` 引用，只剩 memoryport）；`internal/server` 的**管理面**（handlers_memory 等 REST CRUD 直连 Service）明确豁免——契约是消费契约，不得为通过 lint 而把 CRUD 动词塞进 Port 撑肥契约；harness_adapter 的桥接代码迁入 `memoryport/adapter`。黄金集 diff = 0。

### P1 — Harness 消费补全 ≈ 3-4 天
1. `internal/worldstate/sections.go` 新增 `MemorySection`（实现 Snapshot/RenderDiff；记忆在一次请求内是静态的，diff 机制保证只推送一次，与其余 section 相同的 token 节省逻辑——不额外承诺 prefix prompt cache 收益）。注册进 `harness.buildSystemPrompt`，渲染 `WriteDirectives`；`ReviewGuard` 在 review_article 工具执行时拼进其 prompt。
2. `retrieve_context(source="memory")` 改走 `Port.QueryOnDemand`（真检索+门控，带 query embedding），删除内存副本关键词匹配路径；注册进 `ReviseToolDefs` 与 `ChatToolDefs`。**一致性规则**：QueryOnDemand 结果与本轮 `MemorySection` 已注入内容可能部分重叠（query 不同、门控输入相同），适配器需按 bundle 内 ID 去重后只返回增量，避免 prompt 里出现两个版本的"用户偏好"互相打架。
3. 新工具 `remember(text)`：走 `Port.SubmitOutcome` 的显式分支落 Tier1（复用现 `Service.Create` 的 supersede 逻辑）；**已核实的雷：`SDK.Create`（sdk.go:168-200）不走 `checkAndCleanMemoryValue`，PII 过滤只在 Extract 链内（sdk.go:79）——显式路径是裸写入，而它恰恰是唯一置信度 1.0、candidate 门不拦、每轮必注入的 Tier1 通道，必须先补 PII 检查再开 remember 工具**；category/key 由轻量分类落定（五维之外统一归 `explicit` category，避免分类不确定性拖累显式指令的即时生效）；system prompt 指引加"用户明确要求记住时使用"。
4. 会话收尾挂 `Port.SubmitOutcome`（异步）：Outcome 带 Transcript；首期 Harness 提取可只开确定性通道。**实现前置**：Harness 的 `session.CurrentArticle` 每轮修订即被覆盖，无"改前稿"留存——需在 revise_section/write_article 工具执行前快照一份作为 `Outcome.BeforeRevision`，否则 diff 通道（P2-1 的浓信号）在 Harness 侧是空头字段。
5. Harness `StoreMessage` 补 embedding（与 Pipeline 对齐，消除历史行质量不对等）；compaction 摘要落 `working_summaries`。**成本控制**：只 embed 非 article 类型、长度 ≤2000 字的消息（article 类型入库时已是占位符场景），异步队列批量跑——逐条同步 embed 会把每轮写库变成 embedding 调用风暴。
- **验收**：交互式会话中，含偏好记忆的用户在 polish/chat 轮能验证模型引用了记忆（trace 里 `memory.used` 事件覆盖 harness 模式）；"记住：标题不要超过12字"后同用户新会话 Tier1 生效；retrieve_context 返回内容与直接 Gate 检索一致而非内存子串。

### P2 — Pipeline 写回增强（Port 内部演进，引擎不动）≈ 4-5 天
1. **提取输入换浓**：`memory_steps.go` 组装 Outcome 时带上 `NormalizedInput`（改前稿）、对话 transcript、PostReview issues 摘要；`deepseek_extractor.go` 提取 prompt 相应扩展（diff 对比通道："用户改了什么"比"终稿长什么样"信号强得多）。
2. **检索端融合替代写端语义合并**（2026-09-12 审计后重做，原"LLM 判重 ADD/UPDATE/DELETE"方案撤销——依据见 §6 mem0 2026-04 转向）：写路径维持现 key 精确合并 + 两击晋升不动；重复问题在**读路径**解决——(a) `pg_store.Search` 增加关键词/BM25 通道与向量结果融合（现纯语义 Top-20 对"偏好类短文本"召回差是堆积主因之一）；(b) Gate 对近重复注入条目（同 category、向量相似度 > 0.9）按时间取最新一条（temporal ranking，superseded 链已可追溯）。(c) 若仍需去重，做成**离线批处理 consolidation job**（低峰跑 LLM 聚类合并，输出仍是 candidate→人工/两击晋升），**绝不在写路径同步调 LLM**。前提：P2-3（同步 embedding）必须先落，否则新记忆无 embedding、近邻召回与去重看到的是旧世界。
3. **embedding 盲区收窄**（P2 内第一项，2-3 的前置）：`SDK.Create/ResolveAndSave` 改同步 embed。注意：现 List fallback **救不了这个盲区**——fallback 仅在语义结果为空时触发（gate.go:42-49），新记忆不可见时旧记忆早已占满 Top-20，fallback 根本不触发。双保险：(a) 同步 embed，超时降级异步但设熔断监控（实测 embedding p95 后再定超时值，`POST /memories` 与 remember 工具的 UX 延迟以此为预算）；(b) Gate 检索输入改为"向量 Top-20 ∪ 最近 N 条缺 embedding 的 active/candidate（按 created_at）"，union 后再走过滤链。
4. Gate 内部小修：衰减循环改闭式 `conf * 0.5^(days/halfLife)`。
5. **（可选，证据门控再启动）实体链接 boosting**——mem0-2026 五组件中我们唯一缺失的一块：现实体网络只是独立注入段（`--- 用户画像网络 ---`），不参与记忆召回。做法：新关联表把 `user_memories` 链到 `memory_entities`（写入时用已有 entity_extractor 同源链接）；Gate 检索输入加第三条召回路"query 命中实体 → 链上记忆加权并入"，把实体从"注入内容"升级为"召回因子"。启动前置条件（不满足则不做，防为对齐 mem0 而堆功能）：P3 遥测上线后，能证实"实体画像命中但对应偏好未被注入"的漏召回占比显著（>10% 请求）。
- **验收**：合成评测集（构造 100 条含同义改写的写入序列）上，融合检索+读端去重后**实际注入集**的重复率降 ≥50%（度量对象是注入 bundle 而非库内行数，避免与两击晋升语义打架）；写路径无新增同步 LLM 调用（延迟不劣化）；新记忆写入后下一次检索即可被语义召回。

### P3 — 自进化反馈闭环 ≈ 3-4 天（可与 P2 并行）
1. **注入遥测**：每次注入记录 bundle 摘要（IDs/tiers/是否被预算砍段/最终是否出现在 prompt），每次 dismiss、每次反馈评分按 traceID 关联——落 `memory_telemetry` 表（新 migration，仅追加）。
2. **指标**：注入命中率（被引用/被 dismiss）、预算砍除率、两击晋升率、反馈相关性（注入量 × 好评率）。接入现 monitoring/。
3. **黄金集扩展为 CI 门**：门控用例 ≥100 条，覆盖证据边界/衰减/拒答分支；记忆策略任何 PR 必须黄金集 diff 审批通过（复用 governed 内核已验证的 shadow/审批思路）。
4. **影子对比**：新策略先以 shadow 模式跑（计算 bundle 但不注入，只记 diff），与在任策略对比一周后再放量——复用 `RolloutConfig` 灰度骨架。
- **验收**：dashboard 可回答"上周注入的记忆被用户关闭率是多少"；一个故意改坏门控阈值的测试 PR 被黄金集 CI 拦下。

### P4 — 收口与外溢（下一期）
- DAG 执行器接 Port（组织知识注入槽位已存在于 `role_agent_runner.go:212-242`，只缺加载器——届时是适配器里加一个 `OrgKnowledgeProvider`）。
- **与 governed 内核的关系要定调，防止出现两套上下文装配**：`writingruntime/context.go` 的 ContextEnvelope 继续作为 governed 模式的**传输/审计载体**，`InjectionBundle` 是其**内容供给源之一**（envelope 编译时经 Port 取记忆 facts，而非另开一条记忆通路）。P4 开工前先写这条 ADR。

## 6. 关键取舍

- **为什么契约放 `internal/` 而不是 `pkg/`**：`pkg/memory` 保持纯 SDK 可外售；契约是应用边界，属 internal。适配器同时 import 两者，是唯一合法跨界点。
- **为什么 Harness 选"进 prompt + 拉取双轨"而不是纯拉取**：纯拉取省 token，但现状证明不可靠（工具不全意图注册、查内存副本）；预推小 bundle（预算最低优先级、可被砍，沿用 Pipeline 策略）+ 拉取兜底，是两种模式行为一致性的最小代价解。MemorySection 的 diff 推送缓解 token 成本。
- **为什么不直接接 mem0（2026-09-12 复核其现状后的结论）**：① **语言/运行时**：mem0 官方只有 Python/TS SDK（repo 语言构成 4.2M/3.1M 行，无 Go client），接入 = 多一个 Python sidecar（虽然 review-sidecar 有先例，但记忆在写请求的关键路径上，多一跳 RPC + 一套部署）；② **基准数字不可迁移**：其 2026-04 新算法的 92.5/LoCoMo 明确注明来自 managed platform 的专有优化，OSS 只有"方向性收益"；③ **设计错位**：其新算法转向 **single-pass ADD-only**（写入不合并）+ 读端多信号融合（语义+BM25+实体）+ 时间推理，而这恰是本项目 Gate 已有雏形（两击晋升=accumulate 哲学，实体网络已有）而它缺失的方向——本项目缺的**消费者分流、证据边界、置信衰减**mem0 至今没有；④ 讽刺的是，本计划初稿 P2-2 想抄的"LLM 写端语义合并"正是 mem0 自己 2026 实测后**放弃**的设计（v2 论文期的 ADD/UPDATE/DELETE tool-call 阶段被推翻）。综合：mem0 不作为组件引入，作为**演进路标**——其 2026 转向（读端融合+时间排序+accumulate）已吸收进 P2 修订版。契约层就位后，"换内核"选项仍平价保留。

## 7. 风险

| 风险 | 缓解 |
|---|---|
| P0 重构引入行为漂移 | 黄金集 diff=0 作为硬门；分 3 个提交（契约→Gate 消费点→格式化搬迁）各自回归 |
| Harness 预推记忆挤占 prompt 预算 | 沿用最低优先级+截断策略；`MaxInjectedPerIntent` 给 harness 配小值（如 5）灰度 |
| ~~语义合并 LLM 误删好记忆~~（审计后 P2-2 已撤销写端合并，风险消解） | 若走离线 consolidation job：输出仍是 candidate 走晋升门，不物理删（只标 superseded），可回滚 |
| 读端近重复去重（P2-2b）阈值误伤：把"标题偏好"和"小标题偏好"两条真不同记忆折叠成一条 | 去重仅在**同 category** 内 + 相似度 >0.9 触发；bundle trace 记录被折叠的 ID 对，telemetry 观察折叠率异常升高即回退阈值 |
| 提取输入含对话引入 PII 面扩大 | 现 PII 过滤器前移到 Port.SubmitOutcome 入口统一把关 |
| 契约过早冻结阻碍演进 | 冻结的是"三动词+按消费者分槽"骨架，DTO 字段加性演进；P4 用 DAG 接入验证扩展性后再考虑冻结评审 |

工作量合计约 12-16 人日；P0→P1 顺序硬性，P2/P3 可并行（P2 内 2-3 前置于 2-2），P4 另立项。

## 8. 审计修订记录（2026-09-12，结合 mem0 现状复核后）

对初稿的自审，按代码与 mem0 2026-04-新算法事实逐条修订：

| # | 严重度 | 初稿的雷 | 修订 |
|---|---|---|---|
| L1 | 高 | P0 import 边界验收含 `internal/server`，会误伤 handlers_memory 等合法管理面 CRUD（实测 server 层 6 处直依 SDK 类型），倒逼契约膨胀成 admin API | P0 验收改为只约束 `internal/agent`/`internal/engine` 消费侧，server 管理面豁免 |
| L2 | 高 | P2-2 拟抄的"LLM 写端语义合并（ADD/UPDATE/DELETE）"是 mem0 v2 论文期设计，其 2026-04 新算法已实测后放弃，转向 ADD-only+读端融合+时间推理；且写端合并与两击晋升互斥（同值判定在合并前/后不成立，候选永远无法晋升） | P2-2 重做为读端融合+同 category 近重复去重+可选离线 consolidation，写路径零 LLM 调用；验收对象改为注入 bundle 重复率 |
| L3 | 中 | P2-3 假设"List fallback 兜底 embedding 盲区"——错，fallback 仅在语义结果为空时触发（gate.go:42-49），旧记忆占满 Top-20 时新记忆照样不可见 | 双保险：同步 embed+熔断监控；Gate 输入改为向量 Top-20 ∪ 最近缺 embedding 条目 |
| L4 | 中 | P1 假设 Harness 有"改前稿"可做 diff 信号，实际 `session.CurrentArticle` 每轮修订即覆盖 | P1-4 加实现前置：修订前快照 BeforeRevision |
| L5 | 中 | P1-5 "StoreMessage 补 embedding"未限范围，逐条同步 embed = 每轮调用风暴 | 只 embed 非 article、≤2000 字消息，异步批量 |
| L6 | 中 | P4 让 envelope "同样消费 InjectionBundle"，会形成两套上下文装配并存 | 定调：envelope 是传输/审计载体，Bundle 是其内容供给源，P4 前出 ADR |
| L7 | 低 | P0"行为零变化"只靠门控黄金集，锁不住格式化搬迁造成的渲染漂移 | 加 prompt 字符串快照测试 |
| L8 | 中（已核实） | `remember`/`POST /memories` 显式路径无 PII 过滤（Create 不调 checkAndCleanMemoryValue），且 Tier1 置信 1.0 无门直注 | P1-3 改为先补 PII 再开工具 |
| L9 | 低 | P1-1 的 prompt cache 收益说法过度（diff 推送只省 token，不保证前缀缓存命中） | 措辞降级为 token 节省 |
| L10 | 提示 | 初稿引用的 mem0 对比基于 2025 论文期认知 | §6 以 2026-04 现状重写；其新方向反而验证了本项目两击晋升/accumulate 路线，读端融合列为 Port 内演进路标 |
