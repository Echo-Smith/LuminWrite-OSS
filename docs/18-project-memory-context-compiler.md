# 18. ProjectMemory 与 Context Compiler 设计（V2.9 目标态）

**状态**：设计文档（尚未实现）
**日期**：2026-09-02
**参考调研**：NarraCat（`yannikzz/narracat-novel-agent`，AGPL-3.0，仅借鉴设计不引入代码）、LucidWrite（`Shiny-Qiu/LucidWrite`）、DeepSeek Harness（`deepseek-ai/deepseek-harness`，MIT）。调研结论先经本仓库治理惯例消化：任何"写记忆"的路径都必须与 governed writing kernel 的 lineage/evidence/HITL 语义一致。

## 18.1 目标

LuminBuddy 已有四层 User Memory、文件层与实体网络（`internal/memory`）、WorldState（`internal/worldstate`）与治理写作运行时（`internal/writingkernel` / `writingplan` / `writingruntime` / `writingstore`）。本设计不推翻它们，补两块缺失的一等对象：

1. **ProjectMemory**——"这个项目当前有哪些确定事实"的项目级 canonical state，与 UserMemory、SourceEvidence、DocumentState 严格分离。
2. **Context Compiler**——把 Contract、Plan Node、ProjectMemory、SourceEvidence、Document AST、UserMemory **确定性编译**成 ContextEnvelope，作为每个 Capability 的唯一上下文入口。

一句话不变式：

```text
User Memory != Project Memory != Source Evidence != Document State
```

检索负责"找得到"，编译负责"这个 Node 该看到什么"。二者不得再混在 `retrieve_context` 一个工具里。

## 18.2 参考项目取舍

| 来源 | 采纳 | 不采纳 |
|---|---|---|
| NarraCat | 事实=带有效期区间的行（非可变真值）；受控谓词词表 + 工具侧别名归一；实体只存"出生证明"，演化真值只在 facts；全量 provenance（as_of/来源）；确定性纯代码 pack builder + 分块预算 + 硬截断；永不丢弃的 through-line 驻留层（独立预算）；显式缺省通道（缺什么就明说，禁止模型猜）；双通道诊断（连续性缺口进包内、系统诊断走包外） | 小说领域专用谓词/中文 schema 硬编码（LuminBuddy 词表必须可配置）；固定 12k 总预算不可配；MCP 每操作一工具的重量级面板 |
| DeepSeek Harness | "model-visible means logged"——模型可见的输入必须先落入 append-only 类型化事件日志，投影派生上下文；compaction/recovery 是消费生命周期的普通组件而非核心逻辑；fail-closed 审批；溢出内容 spill 到存储并交付定位符 | 整套 Cordis 框架（profiles/patches/70+ 服务）——单产品应用消化不了这种通用性；它没有结构化语义记忆，需与 NarraCat 式 canonical state 配对 |
| LucidWrite | 质量阈值阶梯（pass/polish/rewrite/escalate，分角色维度 + improvementHints 回灌改写提示）；预压缩常数（0.70 监控 / 0.85 触发 / 冷却 + 进行中守卫）；按失败类别命名的恢复路径 | 其文件式按日转储 memory（session 级、无 canon、无 provenance）；`**ARTIFACTS:**` 文本正则解析——结构化输出必须走工具调用/JSON 契约；生产硬化程度低，模式只作思路 |

## 18.3 四层分离与现有实现的映射

| 层 | 内容 | 现有载体 | 本设计动作 |
|---|---|---|---|
| UserMemory | 用户怎么写（偏好、风格、历史） | `internal/memory` 四层体系 | 保持不动；通过编译器以只读引用进包 |
| ProjectMemory | 项目当前确定事实 | 无 | 新建（§18.4） |
| SourceEvidence | 事实从哪来 | Artifact provenance/SourceRefs、RuntimeEvidence | 复用并强化为 fact 级引用（§18.4.4） |
| DocumentState | 文档已写成什么 | Artifact + Document AST + WorldState | 保持不动；编译器读取 |

禁止项：任何 Capability 不得绕过编译器直接拼 UserMemory + 检索结果；ProjectMemory 不存用户偏好，UserMemory 不存项目事实——出现混写即视为 schema 违规。

## 18.4 ProjectMemory 设计

### 18.4.1 事实是带有效期区间的行，不是可变真值

借 NarraCat 的核心决定：canonical fact 从不 UPDATE。一条事实是 `{subject, predicate, object, valid_from, valid_to(NULL=现行)}`；"修改"= 旧行盖 `valid_to` 戳 + 新行生效。好处与治理运行时的既有哲学一致——append-only、可审计、可回放任意时点状态（与 `writing_rollout_approvals`、RuntimeEvidence 的 append-only 纪律同构）。

### 18.4.2 受控词表

predicate/claim 类型收敛为受控词表（初始集合按写作领域设计，如 `identity / location / possession / goal / state / relationship / style_rule / constraint`），词表版本化；未收录概念必须走 `x-` 前缀扩展并被计入警告，而不是自由文本。归一（别名→canonical、关系对无序化）在**写入工具侧**完成，不允许模型侧自行规范化。

### 18.4.3 实体只存"出生证明"

`entities` 行只放不可变标识（entity_uid、canonical name、aliases、创建来源）；一切随时间演化的属性都活在 facts 里，实体卡按需由 facts 机械折叠。候选实体池（candidate → promoted）防止从草稿里直接污染 canon。

### 18.4.4 SourceEvidence 与 provenance

每条 fact 必须携带：来源 artifact/source_ref、提取 run_id（复用 governed run lineage）、`as_of` 时点、提取器标识。无 provenance 的 fact 只能进入 `claims`（待证池），不得进入 `canonical_facts`。claims → canonical 的晋升是显式动作（证据齐全自动晋升 or HITL 确认），失败则衰减归档。

### 18.4.5 结构化对象

- `terminology`：项目术语表（term、definition、aliases、禁止用法），编译时优先级高于通用风格规则。
- `decisions` / `open_questions`：已定决策（带依据与决策时点）与未决问题；编译时 open_questions 进驻留层（§18.5.3），防止长会话漂移。
- `constraints`：硬约束（世界观规则、格式要求），违例必须由质量门捕获。
- `through_line`：贯穿线（核心问题、不可逆事件、伏笔账本），字段级继承 NarraCat 的"驻留层永不丢弃"语义（§18.5.3）。
- `relationships`：实体关系行，同 facts 的有效期区间语义。

### 18.4.6 写入路径：Memory Keeper + 候选写入

借鉴 roadmap P1 Role Policy 与 NarraCat 的 lane 隔离：**任何模型都不持 canon 直接写权限**。写入分三段：

1. 提取方（写作者/研究者 Capability）产出 `memory_candidate`（工具调用 JSON 契约，不做正文正则解析）；
2. 机械校验（词表、归一、provenance 完整性、区间合法性）不通过即拒收，连续失败两次停 lane；
3. 校验通过的候选由 Memory Keeper 角色提交；涉及既定 canon 的**修正/撤回**（correct/retract 类操作）必须 HITL 确认——canon 变更是受控变更，与 rollout 审批同级别纪律。

## 18.5 Context Compiler 设计

### 18.5.1 输入与输出

```text
输入：WritingContract + ExecutablePlan.Node + ProjectMemory + SourceEvidence + Document AST + UserMemory
输出：ContextEnvelope（绑定 node attempt，进 lineage/evidence）
```

编译器是**纯确定性代码，零 LLM 调用**（NarraCat ADR-0011 语义）。同一输入 + 同一编译器版本必须产出逐字节相同的 envelope——envelope hash 与 governed run 的 artifact hash 纪律对齐，可审计、可重放。

### 18.5.2 分块预算与硬截断

Envelope 由命名块组成，每块独立预算，总量硬截断（预算表可配置，不硬编码 12k）。初始块清单（对齐 LuminBuddy 的 Node 形态）：

```text
contract_digest        合同摘要与本次 Node 目标
through_line_anchor    贯穿线驻留层（独立预算，见 18.5.3）
canon_facts            与本 Node 相关的现行 canonical facts
entities_cards         机械折叠的实体卡（按 Node 引用集）
open_decisions         未决问题与待定决策
terminology            术语与约束
document_state         当前文档 AST 相关子树
source_evidence        本 Node 引用的证据片段（带引用标识）
style_directives       UserMemory 风格指令（只读投影）
```

溢出顺序按块声明（尾块先裁），块内按优先级排序后截断；裁剪结果记入 envelope 元数据，供证据侧复核"模型实际看到了什么"。

### 18.5.3 永不丢弃的驻留层

`through_line_anchor` 拥有独立预算，不参与溢出裁剪：核心贯穿线、open_questions、未回收伏笔/约束。保证第 1 章埋的线在第 300 节仍在场（NarraCat 语义）；若驻留层自身超预算，编译**失败**（fail-closed）而不是静默截断——驻留层超限说明 canon 膨胀，是运营问题不是裁剪问题。

### 18.5.4 显式缺省与双通道诊断

- **缺省显式化**：Node 声明需要而编译器给不出的块，以 `missing: {block, reason}` 进 envelope（如 `reason: 尚无 canon 记录 / 超预算未随包给出`），禁止模型靠猜补位。
- **双通道**：连续性/证据缺口（内容问题）进包内，由执行方回答；预算/编译系统诊断（工程问题）只走包外工具返回，不进包内（NarraCat ADR-0016 语义：机器字段不进用户通道）。

### 18.5.5 CapabilityManifest 契约

每个 Capability 声明（roadmap §5 的 YAML，落实为 JSON schema）：

```json
{
  "required_context": ["contract_digest", "canon_facts"],
  "optional_context": ["source_evidence", "style_directives"],
  "forbidden_context": ["user_memory_raw"],
  "token_budget": 8000,
  "freshness": "as_of_run",
  "retention_priority": ["contract_digest", "through_line_anchor"]
}
```

- `required_context` 缺块 → 编译拒绝该 Node 执行（fail-closed），错误进 route/authority 证据；
- `forbidden_context` 是硬约束：编译器遇到 UserMemory 原始层等禁止块直接拒绝，不做脱敏后放行；
- `retention_priority` 覆盖缺省裁剪顺序。

### 18.5.6 Context Runtime（P1 衔接）

编译器之上再叠运行时职责（roadmap §6，借 LucidWrite 常数）：上下文压力 0.70 预警、0.85 触发预压缩（冷却 + 进行中守卫）、溢出后按优先级保留（Contract > Current Goal > Project Canon > Through-line > Open Decisions > Provenance > Document State）、按失败类别命名的恢复路径。compaction/recovery 作为消费 envelope 生命周期的组件实现，不进 kernel 不变量。

## 18.6 存储与包位草图

- 新包 `internal/projectmemory`（schema + 受控词表 + 校验 + 机械折叠）与 `internal/contextcompiler`（块装配 + 预算 + envelope hash）；存储落 `writingstore` 新表族（`project_facts`、`project_entities`、`project_claims`、`project_terminology`、`project_decisions`、`project_through_line`、`memory_candidates`），全部 append-only + 迁移编号顺延。
- 事件侧沿用 "model-visible means logged"：envelope 内容与其 missing/裁剪元数据作为 run 事件落库（entity_kind 归入现有 lineage 体系），投影查询由派生视图承担。
- 词表与预算表为版本化配置（进 `specs/` 或 config），hash 参与 policy/证据绑定，改动即新版本——与 rollout policy hash 覆盖全部字段的教训一致。

## 18.7 实施顺序（对齐 V2.9 清单）

1. **M1 Schema 与写入路径**：`projectmemory` 包 + 迁移 + 受控词表 + Memory Keeper 候选写入（含 HITL 修正门）。
2. **M2 证据与晋升**：claims → canonical 晋升、provenance 完整性校验、与 SourceEvidence/artifact 引用打通。
3. **M3 编译器 MVP**：确定性装配 + 分块预算 + 驻留层 + 显式缺省 + envelope 落库；先服务单一纵向场景（长文）。
4. **M4 Manifest 契约**：required/optional/forbidden + fail-closed 语义接入 Executor 权限模型（复用 P0 权限清单，与 Role Policy P1 合流）。
5. **M5 Context Runtime**：预算监控、预压缩、恢复；贯穿 blind A/B 验收（prompt/policy 变更走双臂盲评，对齐 NarraCat 的变更验收门）。

## 18.8 风险与开放问题

- **canon 膨胀**：facts 只增不减，需要 forgetting 策略（roadmap §12 的 Active→Decay→Consolidated→Archived）在 M2 就设计区间归档，否则驻留层预算会长期承压。
- **词表演进**：初始谓词集合的粒度决定后续所有折叠逻辑；宁可小词表 + 显式 `x-` 扩展 + 警告计数，不要一开始求全。
- **双写漂移**：WorldState 与 ProjectMemory 边界（章节内状态 vs 项目级 canon）需在 M1 用一条规则钉死：跨会话、跨文档、需要 provenance 的进 ProjectMemory；其余留在 WorldState/DocumentState。
- ** envelope 兼容性**：envelope hash 进入 lineage 后，编译器任何行为变更都是新版本；需要与 rollout 相同的"policy 固化 + dump 工具"走查模式，避免 hash 漂移导致证据挂空。
