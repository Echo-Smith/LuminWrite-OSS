# Governed Adapter Rollout Readiness — 2026-08-29

Task12（governed adapter shadow rollout）实现完成后的就绪评估。结论按发布阶梯分级，各级独立判定；上级达成不代表下级自动授权。

```text
off → shadow → allowlist → percentage → enabled
```

## 结论

| 级别 | 结论 | 说明 |
|---|---|---|
| local shadow | **ready** | 双版本已实现并全量验证，默认 shadow 模式 |
| allowlist | **not ready（有明确解锁路径）** | 待 durable evidence 接线、晋升门禁与放量前全量回归 |
| percentage | **not authorized** | 暂不开放；依赖 allowlist 阶段的运行证据 |
| production | **not authorized** | 未授权，不执行部署、推送或真实流量 |

## 交付状态

- OSS：`codex/governed-runtime-oss` @ `5369154 feat: add governed adapter shadow rollout`（29 文件，+2557/−99）
- Commercial：`codex/governed-runtime-commercial` @ `9d8804b feat: add governed adapter shadow rollout`（33 文件，+2837/−98，含 5 个 Task12 规格文档）
- 双版本一致性：governed 分支全部共同文件 blob 级一致（0 差异）；差异仅剩 OSS 独有 5 个编译 stub 与 Commercial 独有 21 个计费/搜索真实实现，符合发布面设计。
- 迁移链：两版本均到 `095_governed_rollout_evidence`；095 down/up 循环在 ParadeDB（paradedb/paradedb:v0.22.2-pg17）上验证通过。

## 验证矩阵（本日全部通过）

| 验证项 | OSS | Commercial |
|---|---|---|
| `go test ./... -count=1`（golang:1.25 容器） | ✅ | ✅ |
| `go test -race`（writingruntime / writingstore / agent） | ✅ | ✅（race 含 agent） |
| writingstore 集成测试（全新 ParadeDB 实例，含 095 up 链） | ✅ | ✅ |
| migration 095 down → up 循环 + 约束检查 | ✅（前次） | ✅（本次） |
| 前端 `npm test -- --run`（26 项） | ✅ | ✅ |
| 前端 `npm run lint` | 0 errors（78 原有 warnings） | 0 errors（78 原有 warnings） |
| 前端 `npm run build` | ✅ | ✅ |

代码审阅覆盖七项检查点，全部通过：shadow 结果不进 canonical Artifact；RunCore 无隐式 SessionStore / 终态事件 / 文档写入；CompleteNodeAttempt 覆盖 succeeded / failed / paused / cancelled；错误码全部稳定；RunLedger evidence 与 migration 095 约束一致；metric 标签全为低基数枚举（无 run/user/artifact/URL/正文）；route evidence 失败自动回 baseline、execution evidence 失败 fail-closed 丢弃 provisional result。

## 本次实现新增的关键防线

1. **ShadowContentGateway**（`shadow://<policy-hash>/<run>/<key>/<hash>`）：shadow 产出写入独立 namespace，与 canonical ContentGateway 物理分离；支持按 run 前缀清除与 TTL 清扫（默认 7 天）。canonical 提交路径由 Orchestrator 强制拒绝 `shadow://` 引用（`ARTIFACT_COMMIT_FAILED`），shadow 引用无法伪装成 canonical ref。
2. **RunCore 取消语义修复**：流式 LLM 客户端在中断时返回部分文本 + nil error；governed core 路径现在在取消/超时后稳定失败，不再把截断正文当成功结果交给 canonical 提交。
3. **HarnessCore 防御测试**：RunCore 不 LoadHistory / 不 StoreMessage / 不发 StreamDone / 不发 Completed（persistent=false 门控 + recording 断言，含 success 与 cancel 两条路径）；Core 输出缺 required artifact、重复/未声明/空 body、usage 未计量（`EXECUTOR_USAGE_UNMEASURED`）均 fail-closed。

## 解锁下一级的前置条件

### allowlist（解锁前必须完成）

- **durable evidence 接线**：生产组合根提供持久化 `RolloutEvidenceStore` 与 `ShadowContentSink`（当前 local shadow 使用内存实现，代码内已注明该边界）。
- **晋升门禁**：design 约定“evidence 存储失败的 policy 不得晋升为 candidate-authoritative”；当前为运维纪律，尚未代码化。晋升流程需把 policy 版本、durable 接线检查和放开时的全量回归固化为 checklist。
- **candidate-authoritative 接线规程**：candidate adapter 必须换接 canonical gateway（ShadowContentGateway 下的候选结果会被提交防线稳定拒绝），该规程需写入运行手册。
- 放量前重跑一次双版本全量回归。

### percentage（当前不开放）

在 allowlist 阶段积累 durable evidence 与候选质量对比数据之前不启用。确定性分流（policy hash + activation key + run 稳定哈希）已实现并有测试，但这不是开放 percentage 的充分条件。

### production（未授权）

不执行。任何生产部署、远端推送、真实流量放量都需要单独授权；本报告不构成授权。

## 残留备注

- `canonical_commit` metric 的 lane 标签恒为 `baseline`（canonical 提交无论来源 lane 都由 baseline 权威路径执行）；语义可接受，评估阶段如需区分来源请通过 evidence store 而非 metric。
- 本次会话中 Mimosa 提交前扫描未能产出完整结论（scanner 缓冲不足，兼容策略放行）；本报告不作出安全结论，建议在放量前补一次完整审计。
- Commercial 的 Task11/Task12 文档按既有惯例折叠进 feat 提交，不单独成 docs 提交。
