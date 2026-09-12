# Production 晋升策略（percentage → enabled 晋升门禁）

**日期**：2026-09-02
**范围**：OSS + Commercial 双仓同步；仅治理运行时、晋升门禁与测试基建，不涉及部署、配置切换或真实流量。V2.8 第 8 项（最后一项）交付后，V2.8 清单全部完成。

## 背景与缺口

allowlist → percentage 的门禁与阶梯已经落地（见同日 percentage release doc），但阶梯最后一级 percentage → enabled（生产全量）没有任何门禁：

1. **无生产门**：`GatedRolloutPolicyProvider` 对 enabled policy 走 allowlist gate 的 default 分支，mode 校验直接 `target_mode_not_allowlist` 拒绝——虽是 fail-closed，但没有任何合法路径评估或审批生产晋升。
2. **证据语义**：enabled 模式下所有主体走候选 lane，运行时不产生 `runtime.shadow_compared`，evidence health 在 enabled policy hash 下永远为空——照搬 allowlist/percentage 门禁（按自身 hash 查证据）在生产级必然 `insufficient_comparisons`，同样属于"语义上不可能达成"的门。
3. **审批无处落库**：`target_mode` CHECK 只允许 allowlist|percentage。

## 变更

1. **`ProductionPromotionGate`（`promotion_gate.go`）**：percentage → enabled 的晋升门。证据不在 enabled policy 自身 hash 下评估，而是解析同一 activation key 的 **percentage 阶段审批**，在**该审批记录的 percentage policy hash** 下要求同一证据标准（7 天 ≥3 对比、0 失败、24h 新鲜度）——即"灰度这一级现在仍然健康"。机械阶梯：percentage 阶段审批缺失即 `percentage_stage_missing`，过期即 `percentage_stage_expired`；生产审批本身 exact-scope 绑定 enabled policy hash/version/key。共享辅助重构为 `checkPromotionPolicyState` / `applyPromotionCriteria`，三个门禁行为一致。
2. **fail-closed（`GatedRolloutPolicyProvider`）**：新增 `ProductionGate *ProductionPromotionGate`；未配置时 enabled policy 一律拒绝（`production_gate_not_configured`），即使有生产审批。
3. **审批存储（`writingstore/rollout.go` + 迁移 `098_production_promotion`）**：`validApprovalTargetMode` 放开到 allowlist|percentage|enabled；**enabled 审批的 `evidence_health` 刻意携带 percentage policy hash**（enabled 无 shadow 对比，这是 store 层唯一允许 health hash ≠ record hash 的目标模式，其余模式仍强制相等），并新增 health hash 格式校验。迁移 098 放开 CHECK；down 迁移删除 enabled 审批后恢复 097 约束；append-only 触发器保持。
4. **CLI（`cmd/governance-gate`）**：接受 enabled policy（自动路由 `ProductionPromotionGate`）；approve 前机械校验同 activation key 的 percentage 阶段审批（缺失即报 `percentage stage approval missing`）。
5. **测试**：`TestProductionPromotionGateRequiresPercentageStageFreshEvidenceAndExactApproval`（happy path + 阶梯缺失 + 阶段过期 + 证据陈旧/失败 + 审批 mode/hash 绑定 + mode 不匹配七组断言；并钉死证据按 percentage hash 查询）、`TestGatedProviderFailsClosedForEnabledWithoutGate`；store 集成测试覆盖 enabled 审批落库（跨 hash health）、enabled 阶梯查询与非 enabled 模式的 hash 错配拒绝。

## 验证（双仓一致，容器 `golang:1.25`）

- OSS 与 Commercial 分别执行：`go build ./...`、`go vet`（writingruntime/writingstore/governance-gate）、`writingruntime` 全量、`-race -run 'Promotion|Gated'`、`writingstore` 全量（独立 fresh DB，迁移自动应用至 098，约束确认含 enabled）。全部通过。
- 真实证据库 `writing_agent_evidence` 未被触碰（复核 3 条 allowlist 审批原样在库）；临时验证库已删除。
- 双仓 7 个变更文件 `diff` 字节一致。

## 边界（未改变）

未审批任何 enabled policy、未部署、未推送、未切换真实流量。**阶梯当前状态**：allowlist 阶段 3 条本地审批（已过期）；percentage/enabled 阶段均无审批、无激活。生产发布需要：percentage 证据刷新 → §9.8 评估 → 生产审批（阶梯校验）→ 独立受控激活变更。runbook 新增 §9.8；§9.6/§9.7 重新编号并修正 release doc 与台账的章节引用。

## V2.8 清单收口

1. Durable Evidence Store ✅（Task13）
2. Shadow Artifact Sink ✅（Task13）
3. Baseline / Candidate Comparison ✅（Task13）
4. Real LLM 三场景纵向验收 ✅（Task13，2026-09-01）
5. Full Regression ✅（持续）
6. Allowlist Gate ✅（Task13 + 证据闭环 2026-09-02）
7. Percentage Rollout ✅（2026-09-02）
8. Production Promotion Policy ✅（本变更）

后续主线转入 V2.9（ProjectMemory / Context Compiler，设计已定稿 `docs/18-project-memory-context-compiler.md`）。
