# WritingAgentBench V2 执行与门禁

## 状态

Task 9 在 Task 8 的并行数据层上增加 `luminbuddy-v2` Shadow Adapter。新路径调用与用户写作相同的 `agent.Harness`、检索工具、知识库、风格 Profile、可选 Memory 和 Trace 持久化；旧 `evaluation_*` 运行与红队接口暂时保留为 legacy，不再作为 WABench 发布证据。

## 执行流程

```text
WABench case + candidate
  -> 解析私有输入/冻结来源/不可变风格版本
  -> luminbuddy-v2 Adapter
  -> 真实 Harness 与工具链
  -> 规范化 output / routing / latency / token / tool events
  -> 确定性 checks
  -> 五项 1—5 分盲评 Judge
  -> 硬失败优先判断
  -> suite 发布门禁
```

Adapter 不调用旧的 `generateArticle` 快捷路径。`modelManifest.model` 决定生成模型，`modelManifest.judgeModel` 可单独冻结 Judge；未设置 `judgeModel` 时使用候选模型。运行器通过现有动态模型服务解析配置与凭证。

候选 ID 是不可变快照键。同一 ID 只能幂等重复提交完全相同的 Prompt、Memory、模型、代码、工具和 feature flag；任何字段变化都必须创建新的候选 ID，避免历史运行被后续配置覆盖。

## 评分与失败边界

五项 Rubric 固定为：

1. `taskCompliance`
2. `sourceFidelity`
3. `structureReasoning`
4. `styleConsistency`
5. `directUsability`

分数只能是 1—5 整数。总分按 `score / 5 × weight` 计算；全维度 4 分对应 80 分。长度、关键词、路由和工具执行只保存为 `wabench_checks`，不能进入主观均分。

以下失败会直接阻断门禁，不进入正常质量均分：

- `capture.input_unavailable`
- `capture.source_unavailable`
- `generation.failed`
- `judge.failed`
- `judge.invalid_score`
- `tool.critical_failed`
- `routing.boundary_violation`
- `redteam.compromised`

失败记录先保存可观察症状，再归入输入、检索、Prompt、Memory、工具、模型、交互七类根因。错误详情会执行凭证样式脱敏与长度限制。

## 风格和 Memory

- 三种内置风格继续使用 `luminbuddy.builtin-style.<slug>`；
- 用户自定义风格使用 `luminbuddy.user-style.<profile_uuid>.v<version>`，运行时读取指定的不可变版本，而非最新版本；
- Legacy 自定义 slug 在人工绑定用户和版本前拒绝进入正式运行；
- `memoryEnabled` 默认 `false`；显式开启时，候选必须同时冻结 `memoryHash` 与合法的 `memoryUserId`。

评测 Memory 适配器为只读：允许检索显式 opt-in 的冻结用户记忆，但不会把评测输入和输出回写到用户对话，避免后一个样本被前一个样本污染。

## 来源模式

- V2 当前运行时知识库是本地 PostgreSQL 实现，所有运行/检查/报告中统一标记为 `local-pg-kb`；它不是 V1 的乐享 provider，也不得生成 `lexiang` 标签。
- `none`：不提供冻结 fixture，Agent 可按真实任务决定是否检索；
- `live`：使用当前真实 Web/知识库工具；
- `frozen`：只向 Harness 注入冻结 fixture，关闭实时 Web 和知识库工具；
- `context.knowledgeOnly=true`：关闭 Web 搜索，并生成关键路由检查。

私有输出正文保存在 Agent Trace，新表只保存哈希与 `agent_trace:<traceId>` 引用；没有持久化 Trace 时退化为 `hash_only`。

## Admin API

接口位于 `/api/v2/admin/evaluation/wabench`，受现有 Admin Token 保护。

### 1. 冻结候选

```http
PUT /api/v2/admin/evaluation/wabench/candidates/candidate-2026-08-19
Content-Type: application/json

{
  "name": "source-gate-on-memory-off",
  "promptHash": "sha256:<64 hex>",
  "memoryHash": "",
  "modelManifest": {
    "provider": "deepseek",
    "model": "deepseek-v4-flash-thinking",
    "judgeModel": "deepseek-v4-flash-thinking"
  },
  "codeHash": "sha256:<64 hex>",
  "toolManifest": {"adapter": "luminbuddy-v2"},
  "featureFlags": {
    "sourceEvidenceGateEnabled": true,
    "memoryEnabled": false
  }
}
```

### 2. 建立独立红队 suite

```http
POST /api/v2/admin/evaluation/wabench/red-team/seed
```

该操作幂等建立 `luminbuddy.private.red-team.v1`，分区为 `red_team`，当前包含 20 条独立红队样本。

### 3. 启动 Shadow run

```http
POST /api/v2/admin/evaluation/wabench/runs
Content-Type: application/json

{
  "suiteId": "luminbuddy.private.red-team.v1",
  "candidateId": "candidate-2026-08-19",
  "environment": "shadow",
  "evaluationRunId": "manual-2026-08-19"
}
```

### 4. 查询结果

```http
GET /api/v2/admin/evaluation/wabench/runs/{runId}
```

返回完成数、阶段失败数、各输出状态、实际评分样本数、平均加权分以及最终发布门禁。生成或 Judge 失败的样本不会出现在 `scoredCases` 中。

## 发布边界

当前仍是 Shadow 阶段：新门禁会生成并保存结论，但不会替换现有生产发布动作。Task 12 已获准开始 shadow，但不包含主分支合并、生产部署或生产发布门禁切换。V1 的 Lexiang-only 检查只属于 V1；V2 的对应专属证据是登录/JWT、多轮会话、真实 `local-pg-kb` 标签和异常降级/拒绝轨迹。
