# Percentage 阶梯工程与审批门禁（percentage promotion engineering）

**日期**：2026-09-02
**范围**：OSS + Commercial 双仓同步；仅治理运行时、晋升门禁与测试基建，不涉及部署、配置切换或真实流量。basis points 保持 0/不部署即无任何流量影响。

## 背景与缺口

allowlist 阶段已完成证据积累与审批（见 2026-09-02 allowlist release doc）。percentage 是阶梯的下一级，但实现存在三处缺口：

1. **证据语义缝（与 allowlist 同款）**：`percentage_miss` 主体走 baseline 但不运行 shadow 对比，证据无法在 percentage policy hash 下累积——任何 percentage policy 的 `assess` 必然 `insufficient_comparisons`。
2. **桶稳定性**：`stableBucket` 的哈希输入包含 policy hash，调大 basis points 会把全部主体重新洗牌（人群不单调扩容，违反灰度语义）。
3. **门禁与阶梯**：`PercentagePromotionGate` 不存在；`GatedRolloutPolicyProvider` 对 percentage 无 fail-closed 门禁；`writing_rollout_approvals.target_mode` 的 CHECK 只允许 allowlist，percentage 审批无处落库；CLI 拒绝 percentage policy。

## 变更

1. **运行时语义（`rollout.go`）**：percentage 未命中主体（`percentage_miss`）在保持 baseline lane 的同时继续执行 shadow 对比（`RunShadow=true`），与 allowlist miss 语义对称；命中主体（`percentage_match`）走候选 lane、不运行 shadow。`stableBucket` 的哈希输入只保留 `activation_key + subject`（去掉 policy hash），basis points 变化只单调扩容命中人群；换 activation key 才会重排（测试钉死：`TestPercentageBucketStableAcrossRamp`）。
2. **执行器语义（`rollout_executor.go`）**：候选权威执行器对 `percentage_miss` 按预期流量服务 baseline，不记 `shadow_mode_unavailable` 权威违规，失败计数不被污染；shadow-only 执行器语义不变。
3. **晋升门禁（`promotion_gate.go`）**：新增 `PercentagePromotionGate`，证据标准与 allowlist 相同（7 天 ≥3 条对比、0 失败、24h 新鲜度）+ exact percentage policy hash 审批绑定 + **机械阶梯检查**：同一 activation key 必须已持有 allowlist 阶段审批，否则 `allowlist_stage_missing` 拒绝。共享 `assessPromotionEvidence`/`bindPromotionApproval` 辅助，两个门禁行为一致。`GatedRolloutPolicyProvider` 未配置 `PercentageGate` 时对 percentage policy 一律 fail-closed（`percentage_gate_not_configured`），即使有审批也拒绝。
4. **审批存储（`writingstore/rollout.go` + 迁移 `097_percentage_promotion`）**：`RecordRolloutApproval` 校验放开到 allowlist|percentage；新增 `LatestRolloutApprovalByActivationKey`（阶梯查询，按 activation key 取最新审批，可限定 target mode）。迁移 097 放开 `target_mode` CHECK，down 迁移删除 percentage 审批后恢复 allowlist-only；append-only 触发器保持。
5. **CLI（`cmd/governance-gate`）**：接受 allowlist + percentage policy；percentage 自动走 `PercentagePromotionGate`；`approve` 在写库前机械校验同 activation key 的 allowlist 阶段审批（缺失即报 `allowlist stage approval missing`）；输出新增 `target_mode`。
6. **测试**：routing/bucket/证据/权威执行器四个 percentage 测试；`PercentagePromotionGate` 门禁测试（新鲜健康、exact 审批、阶梯缺失、无 gate fail-closed）；store 集成测试覆盖 percentage 审批落库与三种阶梯查询。

## 验证（双仓一致，容器 `golang:1.25`）

- OSS 与 Commercial 分别执行：`go build ./...`、`go vet`（writingruntime/writingstore/governance-gate）、`writingruntime` 全量、`-race -run 'Percentage|Allowlist|Promotion'` 全绿。
- `writingstore` 全量套件在**独立临时数据库**（fresh DB，迁移自动应用至 097）通过；验收后临时库已删除。真实证据库 `writing_agent_evidence` 未被触碰，复核确认 3 条 allowlist 审批原样在库。
- 双仓 10 个变更文件 `diff` 字节一致。

## 边界（未改变）

未审批任何 percentage policy、未部署、未推送、未切换真实流量、basis points 未在任何运行环境调整。**阶梯当前状态**：allowlist 阶段有 3 条本地审批（已过期，见 allowlist release doc），percentage 阶段无审批、无激活。percentage 发布仍需：percentage policy 证据积累 → §9.7 评估 → §9.3/§9.7 审批（阶梯校验）→ 独立受控激活变更。runbook 新增 §9.7 记录完整走查路径。
