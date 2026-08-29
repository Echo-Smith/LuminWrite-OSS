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

## 2026-08-29 加固轮次（外部审阅后）

针对“隔离依赖调用方正确接线”这一根因，完成以下系统级强制（OSS `6db29e0` / Commercial `059f5ad`）：

1. **隔离由构造器证明，不再信任接线**：shadow rollout 执行器只接受实现 `ShadowIsolatedCandidate` 的候选（构造期拒绝 canonical gateway 候选），并在执行期封锁 candidate lane（authority violation 埋点 + evidence）；candidate-authoritative 执行器反向拒绝隔离候选。模式晋升（shadow → allowlist+）从“改 policy”升级为“显式重建执行器”，属审计可见动作。
2. **材料快照进入单一事实源**：首次 dispatch 将初始材料清单以 run 级 `snapshot.created` 事件写入 RunLedger（first-writer-wins，FOR UPDATE 锁防并发交错）；恢复时只读该记录并重新校验，源材料变更无法影响暂停中的运行。无需新迁移。
3. **shadow 计费防御**：governed core 路径强制剥离扣费回调（`settleFuncFor`），shadow lane 工具执行不可能消耗用户积分。
4. **engine emitter 边界**：engine step 只接受 observer-only 的 `GovernedStepEmitter`，任意 legacy emitter 以 `LEGACY_WRITE_VIOLATION` 拒绝；nil 自动兜底。
5. **真实适配器纵向链路**：三场景各新增 adapter-level 集成测试（Orchestrator → shadow rollout → 真实 Engine/Editorial/Harness adapter → shadow gateway → canonical 提交），原快速场景测试保留。
6. **确定性与其他**：适配器 payload 遍历排序化（prompt 与 shadow hash 稳定）；rollout 路由支持显式 Subject（用户/租户）并回退 run id；`ExecutionResult.Validate` 拒绝同类型重复输出；095 down 迁移先清除存量 runtime.* 证据再恢复旧约束。

验证：双仓全量 `go test ./...`、race（writingruntime / writingstore / agent）、writingstore 集成测试（全新 ParadeDB 实例）与 095 up/down/up 循环全部通过；前端未改动，沿用前次结论。

## 2026-08-29 第二轮加固（外部复审后）

复审指出第一轮仍存在“依赖调用方正确接线”与“shadow 可影响 baseline”两类问题，本轮全部收敛为系统级强制（OSS `4d393ff` / Commercial `9cad05b`）。**更正**：第一轮 readiness 中“三场景各新增 adapter-level 集成测试”的表述不准确——当时的测试仅覆盖单节点三家族冒烟路径；真实的纵向验收以本轮为准。

1. **模式交叉封死**：candidate-authoritative 执行器现在拒绝 shadow mode（shadow 流量只能来自 shadow 执行器）；shadow 执行器拒绝 candidate lane。shadow 与权威模式只能通过重建不同执行器切换，且执行期校验 gateway 绑定的 policy hash 与当前 policy 一致，policy 轮换后旧 namespace 无法继续使用（`stale_shadow_namespace` 拒绝）。
2. **shadow supervisor**：shadow lane 在独立 goroutine 中运行，带独立截止时间（2× 节点超时）、panic recovery（候选崩溃不再波及进程）与三次失败熔断（熔断后停止派发 shadow lane，只跑 baseline）。baseline 结果同步返回，shadow 执行、比较与 validator 汇总全部异步 finalize——**baseline 调用方永远不等待 shadow lane**；忽略 context 的 legacy runner 只会泄漏一个 goroutine 并产生 `shadow_timeout` 比较证据。
3. **validator summary 进入 shadow evidence**：比较证据现在携带候选质量报告中真实校验器（writingquality registry）的 ID/版本/状态，符合规格对 evidence 内容的要求。
4. **lineage 完整性**：ExecutionResult 校验升级为“parents 集合必须等于全部执行输入”+“InputHashes 去重集合必须等于 parent 内容哈希集合”；不同 parent 共享内容时 lineage 去重记录。
5. **095 回滚保护**：down 迁移在存在治理证据时显式拒绝（RAISE 异常，约束不动）。实测另发现 `writing_run_events` 表本身有 append-only 触发器——DELETE 在数据库层即被禁止，“回滚不回写审计证据”由 schema 强制；降级在证据存在时事实上不可逆，必须先走受认可的导出流程。
6. **真实纵向场景**：长文创作（outline → draft → quality）、多材料综合（真实 MaterialAdapter 快照 → source pack → synthesis → quality → 真实冲突检测）、忠实改写（materials → rewrite → 真实 semantic preservation 校验器 + 确定性 checker）全部经真实 MaterialArtifactProvider、真实 B2 适配器、shadow gateway 与真实 writingquality 校验器注册表/FinalizeReport 门禁跑通；校验器结果进入 shadow evidence。内容为确定性生成，无 LLM。

验证：双仓全量 `go test ./...`、race、真实库上的材料快照并发/幂等/回滚测试、095 down 阻断实测全部通过。

## 当前就绪结论（取代早前表述）

- **local shadow：ready（开发验证用途）**——双份实现字节级一致，全部防御与真实纵向链路在本地验证通过。
- **Task12 不宣布“完整完成”**：剩余验收项见下。
- **allowlist：not ready**。剩余前置：durable evidence/sink 生产接线（仍为内存实现）；evidence 健康度晋升门禁的运维流程固化；真实 LLM 内容的纵向验收（当前内容为确定性生成，仅验证治理链路而非模型质量）。
- **percentage / production：not authorized**，不执行。

## 解锁下一级的前置条件

### allowlist（解锁前必须完成）

- **durable evidence 接线**：生产组合根提供持久化 `RolloutEvidenceStore` 与 `ShadowContentSink`（当前 local shadow 使用内存实现，代码内已注明该边界）。**仍待完成。**
- **晋升门禁**：模式交叉与 namespace 新鲜度已由代码强制；evidence 健康度与晋升审批仍是运维纪律，需固化为 checklist。**部分完成。**
- **candidate-authoritative 接线规程**：已由构造器强制（`NewRolloutExecutor` 只接受 canonical 候选、拒绝隔离候选；shadow 执行器无法产生任何 candidate/shadow 交叉流量），接线规程需写入运行手册。**结构上已强制，文档待补。**
- **真实 LLM 纵向验收**：以真实模型内容重跑三条纵向场景，确认 shadow 对比与质量门在非确定性内容下的行为。**待完成。**
- 放量前重跑一次双版本全量回归。

### percentage（当前不开放）

在 allowlist 阶段积累 durable evidence 与候选质量对比数据之前不启用。确定性分流（policy hash + activation key + run 稳定哈希）已实现并有测试，但这不是开放 percentage 的充分条件。

### production（未授权）

不执行。任何生产部署、远端推送、真实流量放量都需要单独授权；本报告不构成授权。

## 残留备注

- `canonical_commit` metric 的 lane 标签恒为 `baseline`（canonical 提交无论来源 lane 都由 baseline 权威路径执行）；语义可接受，评估阶段如需区分来源请通过 evidence store 而非 metric。
- 本次会话中 Mimosa 提交前扫描未能产出完整结论（scanner 缓冲不足，兼容策略放行）；本报告不作出安全结论，建议在放量前补一次完整审计。
- Commercial 的 Task11/Task12 文档按既有惯例折叠进 feat 提交，不单独成 docs 提交。
