# Memory Forgetting（V2.9 M6，清单第 12 项收口）

**日期**：2026-09-03
**范围**：OSS + Commercial 双仓同步；V2.9 最后一项——ProjectMemory 的遗忘策略（roadmap §12）。设计依据 `docs/18` §18.8 风险条目与 roadmap §12 策略表，矫正记录回写 §18.15。V2.9 上下文线（M1–M5）+ 记忆线（M6）就此收口。

## 变更

1. **版本化遗忘策略（`internal/projectmemory/forgetting.go`）**：`ForgetPolicy` v1 是纯决策表——`CandidateHorizon`（默认 30 天）退役各 lane 的过期候选、`ClaimDecayHorizon`（60 天）衰减无佐证的 open/supported claim、`DecisionColdHorizon`（90 天）归档冷却后的 superseded 决策。零值 horizon = 该类永不遗忘（遗忘是 opt-in 卫生，不是默认行为）。`Hash()` 把配置绑定进台账，改任一 horizon 即换 hash。**canon（facts）没有 horizon 字段、没有决策分支**——"Canon 不能因为时间过去而被自动遗忘"由类型系统保证，测试钉死（含"激进策略也忘不掉 canon"）。
2. **迁移 103：`project_memory_forgetting_log`**：append-only 遗忘台账（089 触发器拒绝任何 UPDATE），每行记录被遗忘对象、真实 from_status→to_status、规则名、policy version + hash、执行 actor 与时间。"忘了什么、为什么忘、谁忘的"成为可重放的证据。fresh DB 自动应用验证通过。
3. **sweep 应用（`internal/writingstore/projectmemory_forgetting.go`）**：`PreviewForgettingPolicy`（只数不动，dry-run）与 `ApplyForgettingPolicy`（单事务：先 SELECT 捕获真实原状态 → 条件 UPDATE 复核源状态与 horizon → 仅对实际转换行落账）。幂等由构造保证（第二次运行零转换零落账）。**actor 门禁 = policy 或 user**：model/capability/validator 永远无权遗忘——记忆生命周期是治理领地，不是模型领地。sweep 表集合结构性不含 `project_facts`。
4. **CLI `cmd/memory-forget`**：`-policy-dump`（无库）、默认 preview、`-apply`（可带 `-policy` 覆盖与 `-operator` 身份）、`-log N` 读台账。preview 是 runbook 走查的默认形态。

## 策略表投影（roadmap §12 → ProjectMemory）

| roadmap 类型 | 落点 | v1 动作 |
|---|---|---|
| Project Canon | `project_facts` | **永不自动衰减**；区间失效 + user supersede 是唯一退出（已存在） |
| Session Detail（快衰减） | 各 lane 的 candidate/staged | 过期 → archived（fact-lane 为 rejected）；释放术语/实体唯一槽位 |
| Behavioral Pattern（证据强化/衰减） | `project_claims` | open/supported 超期无新佐证 → rejected；corroborate 刷新 updated_at 即续命 |
| Source Evidence（freshness 失效） | `project_claims` 佐证道 | 同上（advisory 层承载） |
| Open Question（resolved 后 archive） | `project_open_questions` | answered/dropped 已是终态且被编译排除，v1 无进一步动作（台账无规则可记） |
| User Preference（慢衰减） | UserMemory 四层（V2.6 前系统） | **不在本里程碑**：属旧记忆系统，V2.9 范围是 ProjectMemory |

## 设计矫正（回写 docs/18 §18.15）

1. **遗忘 = 状态转换 + 台账，不是删除**：全部转换落在各表既有的 CHECK 状态值内（零 schema 侵入），历史行保留可审计；"归档释放唯一槽位"（术语/实体）是候选池卫生的实际收益，测试钉住。
2. **canon 不变量做成结构性的**：策略类型没有 fact horizon、sweep 表集合没有 project_facts——不变量不依赖运行时检查或约定。
3. **sweep 与交互式归档分离**：既有 `ArchiveTerminology/Thread/Entity` 保持 user-only（HITL 交互路径）；policy sweep 是独立的治理路径（policy|user actor + 台账），两者不互相冒充。

## 验证（双仓一致，容器 `golang:1.25`）

- 7 个文件字节一致（forgetting.go / forgetting_test.go / 迁移 103 up+down / projectmemory_forgetting.go / 其测试 / cmd/memory-forget）；gofmt 干净。
- **真实 Postgres（一次性容器，不触碰开发库）**：迁移 103 fresh DB 自动应用；`TestForgettingSweepForgetsStaleAndPreservesCanon`（preview 计数、model actor 拒绝、4 类转换、canon 存活且仍在 active 集、台账逐行核对真实 from-status/rule/hash/actor、重跑幂等）与 `TestForgettingSweepFreesTerminologySlot`（过期候选占槽→sweep 释放→同名可再 stage）通过；`projectmemory`/`writingstore`/`database` 三包带 DB 全绿。
- 全树 `go test ./internal/... -p 1`（无 DB 官方口径）双仓全绿（20 包）。

## 附带修复：editorial DB 测试夹具（schema 漂移）

带真实 `TEST_DATABASE_URL` 跑全树时 `internal/editorial` 集成测试失败，根因是测试夹具未跟上迁移 087（`editorial_tasks` 合并进 `agent_traces` 并 DROP）与 users 主键约定，属既有缺陷、与 M6 无关（官方无 DB 口径下这些测试一直 skip，故历史未暴露）。应要求在本会话内独立小修，双仓同步：

1. `testUser` 返回 `uid::text` 改为 `id::text`——`agent_traces.user_id` 外键引用 `users(id)` 主键，非 `uid` 唯一列（FK 违规根因）。
2. `testStore` 的 TRUNCATE 列表与两处 `setupTaskAtStatus`/approval 的原始 SQL：`editorial_tasks` → `agent_traces`、`status` → `editorial_status`、`WHERE id` → `WHERE trace_id`（对齐 store 的 `Task.ID == trace_id` 映射）。

修复后双仓 editorial 包与全树在真实 PG 上全绿（OSS 19 包 / commercial 20 包，零 FAIL）。

## 已知问题（遗留）

~~M4b 记录的全树并行测试迁移竞态~~ —— 已于同日独立小修解决（`pg_advisory_lock` 串行化并发 migrator，见 `docs/releases/2026-09-03-migration-advisory-lock.md`）；双仓全树现可在默认并行 `-p` 下带真实 DB 全绿，不再依赖 `-p 1` 规避。

## 边界

- UserMemory 四层（偏好慢衰减、行为模式强化）不在 v1：属旧记忆系统，需要单独设计其衰减信号。
- `open_questions` 的 answered/dropped 终态已满足"resolved 后不再进上下文"，未引入额外 archived 层。
- 遗忘 sweep 未接调度器：按 runbook §9.9 显式运行（preview → 走查 → apply），与证据采集作业的纪律一致。
- Role Policy、V3.0 Capability Runtime 不在范围。
