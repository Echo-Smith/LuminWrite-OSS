# M1.2 — validator executor 设计与实现记录

> V3.0 M1 第二增量（2026-09-04 实现）。目标：让 sourced/strict 模板引用的 `core.validation.evidence` / `core.validation.fact` 真正可执行——此前这两个能力类在模板（templates.go）与编译器保障底线（`validatorsForAssurance`）中均被引用，但能力目录里**根本没有声明**，引擎也没有对应步骤，导致 sourced/strict 模板只能 fail-closed 为 T4 诊断计划。

## 22.1 范围发现（实现前排查）

1. **目录缺口**：`DefaultCapabilityRegistry` 只声明了 `core.validation.quality`；`validation.evidence` / `validation.fact` / `research.strict`（strict 模板的研究节点类）三个类均无 manifest。这是 sourced/strict 无法编译的根因。
2. **引擎缺口**：engine/steps 无现成 evidence/fact 校验步（只有 PostReview 的联网事实核查注入，形态是评审附加上下文，不是独立 artifact 产出）。
3. **数据面齐备**：`source_pack` 产出格式（`{query, count, results[]}`，`engine.SearchResult`）已稳定；quality 节点的 `OptionalInputTypes` 已声明接受 `evidence_report`/`fact_report`——只差生产者。

## 22.2 设计决策

- **D1 validator = 报告生产者，不是门**：质量门只在 quality 节点（`CommitCheckpoint` 的 accepted 门）。validator 的 findings 只进报告、随 artifact 流入 quality 消费，**绝不 fail 节点**。这是与 LucidWrite confidence 路由的原则性区别（docs/20 §20.4）。
- **D2 degrade-don't-fail**：沿 PostReview 的 graceful 语义——LLM 未接、调用失败、响应不可解析、输入缺失（无 draft / source_pack 不可解析）一律产出 `mode="degraded"` 的结构完整报告（`issues` 带 `review_skipped`），运行继续。降级被诚实记录而非静默吞掉。
- **D3 目录补声明**：三个 manifest（evidence/fact validator + strict_search）。strict_search 与 collect 共享 `engine.step.search` executor——executor 绑定是共享基础设施，manifest 才是能力身份；strict 差异留给 M1.3 per-request profile。
- **D4 上下文契约**：validator required 仅 `contract_digest`（EnforceRequiredContext=true）；source_pack 走输入 artifact 而非上下文块——它是评审对象不是上下文；`ContextSourceEvidence` 作为 optional（terminology 辅助对齐术语）。
- **D5 usage 记账**：LLM 路径记 prompt/completion tokens；degraded 路径 `Measured=true` + 零 token（结构化记录，cost 为 0）。

## 22.3 报告 schema（`validatorReport`）

```json
{
  "validator": "core.validation.evidence",
  "mode": "llm | degraded",
  "scores": {"evidence_coverage": 0.82},          // evidence_coverage | factuality
  "issues": [{"severity": "...", "type": "...", "message": "..."}],
  "passed": true,
  "checked_at": "2026-09-04T09:00:00Z"
}
```

issues 词汇表兼容 `engine.ReviewIssue`（quality 消费无需二次映射）；evidence 用 `unsupported_claim`，fact 用 `fact_conflict` / `unverified_claim`。artifact 类型：evidence → `evidence_report`，fact → `fact_report`（media `application/json`）。

## 22.4 实现切片

- `writingruntime/validator_runner.go`：`ValidatorRunner{LLM, Now}`（LegacyNodeRunner）。按节点 capability 分 evidence/fact 两个 prompt（评审维度、issue 类型不同），draft 8000 字节 / 20 信源×800 字节截断防上下文爆；LLM 走 `tools.LLMClient.Chat`（JSON mode、temperature 0）。
- `writingplan/capability.go`：目录补三个 manifest（§22.2 D3/D4）。
- 测试：`validator_runner_test.go`（6 个单测：无 LLM 降级 ×2、缺失输入降级、外类拒绝、httptest fake LLM 正常路径含 token 记账、不可解析降级、LLM 500 降级）+ `templates_test.go`（`TestSourcedAndStrictTemplatesCompileOnceValidatorsDeclared`：sourced/strict 契约分别编译为 T1 且含全部 required validators；`boundDefaultCapabilityRegistry` 修为按 executor id 去重绑定）+ `delivery_protocol_test.go`（harness 化为 `runDeliveryScenario`，支持 headClasses 插 validator 节点 + 初始 source_pack 注入）。

## 22.5 验收（真实 PG）

- `TestSourcedDeliveryWithValidatorThroughOrchestrator`：sourced 形状（draft → evidence → quality → finalize）真实 PG 全链 PASS；evidence_report 落 canonical（内容寻址体校验 `validator/mode=degraded/passed`）；整条交付谱系（candidate 版本 + promotion + revision_set）一致。Validator 以 nil LLM 运行，端到端钉死 degrade-don't-fail。
- 双仓 5 文件字节一致（capability.go / templates_test.go / validator_runner.go / validator_runner_test.go / delivery_protocol_test.go）；双仓 `-p 1` 全树带 DB 零失败。

## 22.6 遗留

- validator 的 LLM 正式接入与 per-capability 模型选择随 M1.3/M1.4（composition 工厂传真 LLM）。
- `fact` 模板的 research.strict 差异（更严的信源门槛/数量下限）在 M1.3 per-request profile 落地，届时 strict 模板才是语义完整的 strict。
- validator 报告的 quality 消费侧（issues 聚合进 quality report 的 evidence/fact 维度）待 quality runner 在 M1.4 挂载时接线。
