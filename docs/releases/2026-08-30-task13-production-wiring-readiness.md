# Task13 生产接线验收记录 — 2026-08-30

## 已完成并验证（OSS `4509b97` / Commercial `75965ba`）

1. **canonical 内容存储与恢复边界**：`writing_canonical_content`（migration 097）按 key 幂等存储治理产物正文，Load 逐次校验声明哈希（篡改/漂移 → `MATERIAL_INTEGRITY_FAILED`），重启恢复读同一行。down 迁移在有工件引用时拒绝执行。
2. **Governed Runtime 组合根**：`server/governed_runtime.go` 是唯一组装点——writingstore 持久化、durable rollout evidence、durable shadow sink（migration 096 + TTL）、canonical gateway、持久化 checkpoint、真实材料源（user_materials）全部从生产依赖构造。模型或存储缺失时整组 fail-closed。
3. **三类真实 adapter 注册**：Engine 家族挂真实管线步骤（SearchStep/OutlineStep/WriteStepWithKB/PostReview）、Editorial 家族挂 RoleAgentRunner（finalize → revision_set）、Harness 家族的 AgentHarnessCoreBridge 作为 draft 能力的 shadow-isolated candidate——与 Engine baseline 组成 rollout pair，默认 rollout 为 off。
4. **普通任务自动调度**：CreateRun 事务提交后立即在服务端自有 context 上派发（HTTP 请求生命周期不约束 run）。
5. **批准后调度**：approval 提交并使 run 进入 planned 后派发。
6. **WRITING_RUNTIME_NOT_READY**：组合不完整时创建接口在**任何持久化之前**返回 503 + 稳定错误码（HTTP 边界与服务层双重守卫），僵尸 planned run 从结构上不可能；调度本身失败时记录 run.transition_rejected 审计事件。

单元与 race 测试全部通过；两栈已在本地 compose 部署并通过冒烟（health db/redis/llm、前端 200、096/097 表在位）。

## 部署验收现状

- 双栈运行 Task13 构建：OSS（:8080/:3002，全新库）与 Commercial（:8081/:3003，全新库）全部容器 healthy。
- readiness 分层（installed/configured/reachable/ready）与 provider preflight 已在 Task 2/3 交付，`/health` 不再因对象已构造而报告可用。

## 待真实凭证后执行的最终验收（item 7 剩余部分）

当前 `.env.docker` 中 `AI_API_KEY` 为占位符（`your-deepseek-api-key`），Commercial 付费检索源未配置真实凭证。以下验收在填入真实凭证后执行：

1. OSS：`AI_API_KEY=<真实 DeepSeek key>` → 重启 backend → 长文创作链路（research 跳过、outline → draft → quality → finalize）真实 LLM 端到端；
2. Commercial：`AI_API_KEY` + `TAVILY_API_KEY`（或对应付费源）→ 多材料综合与忠实改写链路，含真实外部检索与冲突处理；
3. 三条链路验收点：Candidate/Accepted/Verified 状态迁移、引用与材料快照、cost/token evidence、pause/cancel、shadow 隔离（显式安装 shadow policy 后候选不进 canonical）；
4. 重启 backend：验证 evidence/shadow/审计记录在重启后仍可读、可清理。

**凭证就绪后**，重跑本文件的部署验收节即可闭环 Task13；allowlist 放量仍需基于 durable evidence 的单独决策。
