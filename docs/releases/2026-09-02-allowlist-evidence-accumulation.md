# Allowlist 证据积累作业（rollout evidence accumulation）

**日期**：2026-09-02
**范围**：OSS + Commercial 双仓同步；仅治理运行时与测试基建，不涉及部署、配置切换或真实流量。

## 背景与缺口

Task13 交付了 `AllowlistPromotionGate` + `governance-gate` CLI，但存在一个未走通的语义缝：晋升门禁按 **allowlist policy hash** 统计 `runtime.shadow_compared` 证据，而 shadow 对比只在 `RolloutShadow` 模式产生，证据因此只能挂在 **shadow policy hash** 下（policy hash 覆盖全部字段，含 `mode`）。结果：任何新 allowlist policy 的 `assess` 都必然 `insufficient_comparisons`，Task13 文档所述"在目标环境持续采集同一 policy hash 的 shadow evidence"在实现上不可能达成。门禁测试此前全部使用 stub store，未暴露该缝。

## 变更

1. **运行时语义（`rollout.go`）**：allowlist 未命中主体（`allowlist_miss`）在保持 baseline lane 的同时继续执行 shadow 对比（`RunShadow=true`）。证据由此在当前 allowlist policy hash 下持续累积，allowlist 主体候选路径与 HITL 语义不变。
2. **执行器语义（`rollout_executor.go`）**：候选权威执行器（激活后形态）遇到 `allowlist_miss` 时正常服务 baseline 并记录常规执行证据，不再记 `shadow_mode_unavailable` 权威违规——激活后的未命中流量是预期流量，不应污染 evidence health 的失败计数。shadow-only 执行器仍拒绝 shadow lane 以外的 shadow 流量；allowlist 主体在 shadow-only 执行器上仍被 `candidate_lane_blocked` 拒绝（fail-closed 不变）。
3. **纵向测试基建（`vertical_test.go`）**：`verticalRolloutBackend` 新增 `ids`（每次调用独立 run/document lineage）与 `nodePolicy`（把指定节点绑定到治理 allowlist policy）两个可选钩子；`verticalResult` 暴露 `policyHashes`。既有测试零行为变化。
4. **证据采集 harness（`evidence_accumulation_test.go`）**：`TestAllowlistEvidenceAccumulationHarness` 在真实模型 + PostgreSQL 上重跑三个治理纵向场景，未命中主体路由使对比证据累积在治理 allowlist policy hash 下；每场景后输出只读 `EvidenceAssessment`。幂等键按 run 隔离，重复执行累积而非覆盖。无凭据时安全跳过。
5. **运维入口**：`scripts/run-allowlist-evidence-accumulation.sh`、`make evidence-accumulate`（采集）、`make evidence-gate`（只读评估）。runbook 新增 §9.5，明确采集与验收数据库必须隔离（验收套件会 TRUNCATE 级联清空证据）。

## 验证

- `go build ./...`、`go vet ./internal/writingruntime`、`writingruntime` 全量单测与 `-race`、`writingstore`/`writingplan`/`writingkernel` 回归：全部通过（本地容器 `golang:1.25`，无真实凭据环境下 live 套件按设计跳过）。
- 新增测试：`TestAllowlistMissKeepsShadowEvidenceFresh`（shadow-only 执行器 + allowlist miss → 对比证据落在 allowlist policy hash 且 canonical 零污染）、`TestAuthoritativeExecutorServesBaselineOnAllowlistMiss`（激活后形态 miss 服务 baseline、无权威违规记录）、`TestAllowlistAndPercentageRouteBySubject` 扩展断言。

## 边界（未改变）

未审批、未激活、未部署、未推送、未切换真实流量；`percentage`/`enabled` 仍被门禁拒绝。本变更只让"证据可以在正确的 policy hash 下累积"成为可能，allowlist 发布仍需 §9.2/§9.3 的评估、审批与受控激活。

## 本地证据与审批记录（2026-09-01/02，仅限本地证据库）

在独立本地数据库（`writing_agent_evidence`，与主库和 Task13 验收库隔离）上走完了 §9.5 采集 → §9.2 只读评估 → §9.3 审批的完整前半程。真实模型为 `deepseek-v4-flash`（临时测试凭据，测试后由提供方删除）。

- **证据**：三个纵向场景各完成 3 次采集运行，每个治理 allowlist policy hash 下累计 3 条 `runtime.shadow_compared`，失败 0 条；重复运行按独立 lineage 累积（期间修复了契约/意图计划/计划/决策 ID 未随 run 派生导致的跨次运行冲突）。
- **只读评估**：三个场景 `assess` 均返回 `allowed=true`。
- **审批**（append-only，绑定 exact policy hash / policy version 2 / activation key `change-local-evidence-accumulation`；operator `zcode-stabilization-agent`；TTL 24h，created 2026-09-01T17:40Z，expires 2026-09-02T17:40Z；理由注明"激活仍是独立受控变更"）：

| 场景 | approval_id | policy_hash |
|---|---|---|
| long_form | `approval_9197fbec7de9ef607e2e0fb3e23666bc` | `sha256:73c970a8a0102f72b251b21adeab398bc4a781aac6e623c5bb4dc53926976997` |
| multi_material | `approval_eef468c32c49c94cedbb5be5315e39e9` | `sha256:81c4c698a393d59c50e91ce302473f3d3bbd1e77a56ee1ea4ed16a297bca1b84` |
| faithful_rewrite | `approval_1a27a337fb3d2780ea04efdf41e7fa68` | `sha256:9d844c2cc9aa25ef5145a2cea6ece5621354f0a109fe451472df408d20c63bfb` |

- **复核**：审批后立即重跑 `assess`，三场景仍 `allowed=true`。
- **采集已停止**：审批后继续采集会产生新证据并使审批 `approval_evidence_stale` 失效，因此按日 cron 已删除；如需续期，重新采集并按 §9.2/§9.3 重走评估与审批。
- **激活未执行**：以上记录只覆盖"证据 + exact-scope 审批"。是否对指定 subject 激活 allowlist policy 仍是独立的受控变更，需要显式授权后按 §9.3/§9.6 执行。
