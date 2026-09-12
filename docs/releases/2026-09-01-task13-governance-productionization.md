# Task13 治理运行时工程化就绪报告 — 2026-09-01

## 结论

| 阶段 | 状态 | 边界 |
|---|---|---|
| local shadow | **ready** | evidence 与 shadow body 已持久化，三条真实模型纵向链路已通过 |
| allowlist | **not activated / not release-qualified** | 具备 fail-closed 评估与审批机制，但未创建真实发布审批、未激活任何 subject、未积累生产 shadow 证据 |
| percentage | **not authorized** | 生产 gate 明确拒绝 percentage / enabled 晋升 |
| production | **not authorized** | 未部署、未推送、未切流，本报告不构成发布授权 |

Task11/Task12 的三个主要工程缺口已经补齐：持久化 evidence/sink、晋升运维门禁、真实 LLM 三场景验收。它们是 allowlist 的必要前置，不是 allowlist 或生产授权本身。

## 已实现

1. migration 096 新增独立的 `writing_shadow_contents` 和 append-only `writing_rollout_approvals`。shadow body 没有 canonical Artifact/Document 写路径；降级在仍有 approval 或 shadow 数据时 fail closed。
2. rollout evidence 继续复用 append-only `writing_run_events`，新增按 policy hash 的健康聚合；shadow sink 支持幂等写、重启读取、run 前缀清理和基于存入时间的 TTL 清扫。
3. `AllowlistPromotionGate` 要求 allowlist policy、非 kill-switch、有效期、最新健康证据和 exact scope 审批；缺失、过期、失配、失败或新证据越过审批水位时拒绝。percentage/enabled 不可通过该门禁。
4. `governance-gate` CLI 只执行 `assess` 或追加审批，不改 policy、不重启、不切流。审批允许在新证据产生后追加新记录，旧记录不可修改或删除。
5. Server composition root 在 PostgreSQL 可用时构造 durable evidence、shadow sink 和 promotion gate 依赖，但没有注册或激活真实流量 policy。
6. 环境门控的 live suite 固定使用 `deepseek-v4-flash`，经完整 governed Orchestrator、真实 B2 adapter、quality registry、shadow gateway 和 PostgreSQL 重读执行长文、多材料综合、忠实改写。evidence 仅保存哈希、状态、用量清单和 validator summary，不保存凭据、prompt 或正文。

## 验证结果

| 门禁 | OSS | Commercial |
|---|---|---|
| `go test ./... -count=1` | 通过 | 通过 |
| `go test -race`（writingruntime / writingstore / server） | 通过 | 通过 |
| fresh ParadeDB 迁移链至 096 | 通过 | 相同迁移与集成套件通过 |
| PostgreSQL durable shadow / evidence / approval | 通过 | 通过 |
| migration 096 数据存在时降级阻断 | 通过 | 共享迁移字节一致 |
| live long-form / multi-material / faithful-rewrite | 三场通过 | 三场通过 |
| 前端测试 | 41/41 | 41/41 |
| 前端 lint | 0 errors，75 warnings | 0 errors，75 warnings |
| 前端 build | 通过 | 通过 |

真实模型验收曾发现两处测试约束问题并已修正：短但非空的有效响应不再被任意 80 字阈值误拒；live shadow evidence 的异步等待窗改为跟随节点超时。两处都未放宽治理质量门、持久化或隔离断言。

## 下一授权边界

allowlist 仍需单独的发布决策：在目标环境持续采集同一 policy hash 的 shadow evidence，运行 `governance-gate -action assess`，由明确 operator 针对 exact policy/version/activation key 追加限时审批，再由另一个受控变更激活 policy。任何 percentage、enabled、生产部署、远端推送和真实用户流量都不在本任务授权范围内。
