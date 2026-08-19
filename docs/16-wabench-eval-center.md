# Luminbuddy Eval Center

## 定位与边界

Eval Center 是 WritingAgentBench 在 Luminbuddy V2 的管理与决策界面。它把数据集、候选版本、运行、人工/模型评审、Badcase 和发布证据组织成同一条可审计链路，但当前仍处于 **Shadow** 阶段：页面展示和保存门禁结论，不会自动切换生产 Prompt、模型、Memory 或发布开关。

原有 `evaluation_*` 数据继续作为 legacy 诊断记录；WABench 分数只从 `wabench_*` 新模型读取，两者不混算。

## 七个工作区

| 工作区 | 决策问题 | 主要证据 |
|---|---|---|
| 总览 | 当前候选是否具备继续评审条件 | 平均分、硬失败、接受率、修改负担、延迟、来源边界、最新门禁 |
| 数据集 | 使用了哪一批冻结样本 | suite 分区、版本、样本数、隐私级别、冻结哈希 |
| 候选 | 实际比较的版本是什么 | Prompt/Memory/模型/代码/工具/feature flag 清单与哈希 |
| 运行 | 生成、Judge 或工具链是否失败 | 状态、进度、失败阶段、实际计分样本、Trace 数量 |
| 评审 | 谁在何时以何种方式给出结论 | 五项 Rubric、接受档位、修改负担、评审人/角色/方法/时间/标签来源、盲评与仲裁 |
| Badcase | 失败属于哪个可修复环节 | 症状、硬失败 ID、七类根因、Owner、修复版本、回归状态 |
| 发布 | 是否满足上线或回滚条件 | 基线/候选、门禁结论、例外、决策人、回滚开关 |

成本数据只有在运行记录提供可信成本字段时展示；缺失时明确标记为“不可用”，不会从调用次数反推伪精确金额。

## 权限

所有 `/api/v2/admin/evaluation/wabench/*` 接口复用现有 Admin 认证中间件：有效管理员 JWT 或静态 `ADMIN_TOKEN` 才能访问。页面同时做前端权限降级：非管理员不可创建候选、启动运行、建立红队套件或导入评审；API 仍是最终授权边界。

没有新增 1Panel 环境变量。部署只需要已有 PostgreSQL、Admin 认证以及 Task 8—9 所需的模型/工具配置。

## API

读模型：

```text
GET /api/v2/admin/evaluation/wabench/overview
GET /api/v2/admin/evaluation/wabench/suites?limit=200
GET /api/v2/admin/evaluation/wabench/candidates?limit=200
GET /api/v2/admin/evaluation/wabench/runs?limit=200
GET /api/v2/admin/evaluation/wabench/reviews?runId=<runId>&limit=500
GET /api/v2/admin/evaluation/wabench/badcases?limit=300
GET /api/v2/admin/evaluation/wabench/releases?limit=100
```

评审导入：

```text
GET  /api/v2/admin/evaluation/wabench/reviews/template.xlsx
POST /api/v2/admin/evaluation/wabench/reviews/import
Content-Type: multipart/form-data; name="file"
```

上传仅接受 `.xlsx`，单文件不超过 8 MB、单次不超过 500 条。服务会先校验完整工作簿，再在单个数据库事务中写入；任一行失败时不会产生部分导入。`reviewId` 是不可变审计 ID，重复 ID 不覆盖历史记录。

Task 9 的候选、运行和红队接口继续保留：

```text
PUT  /api/v2/admin/evaluation/wabench/candidates/{id}
POST /api/v2/admin/evaluation/wabench/runs
GET  /api/v2/admin/evaluation/wabench/runs/{id}
POST /api/v2/admin/evaluation/wabench/red-team/seed
```

列表接口的 `limit` 范围是 1—500。

## 中文 Excel 契约

模板包含“评审记录”和“填写说明”两个工作表。评审记录列顺序如下：

```text
评审ID、输出ID、评审人、评审角色、评审方式、标签来源、是否盲评、
任务符合度、来源忠实度、结构与推理、风格一致性、可直接使用程度、
用户接受档位、平均修改量、硬失败ID、主要根因、次要根因、
评审时间、是否仲裁、备注
```

- 五项 Rubric：1—5 整数；
- 用户接受档位：直接使用、少量修改、大量修改、拒绝、未知；
- 平均修改量：0—3，或无需修改、轻度、中度、重度；未知可留空；
- 根因：输入、检索、Prompt、Memory、工具、模型、交互；
- 布尔值：是/否；
- 时间：ISO 8601 或 `YYYY-MM-DD HH:mm:ss`；
- 多个硬失败或次要根因：用逗号、分号或竖线分隔。

导入失败返回 HTTP 422，并逐条给出工作表行号、中文列名和原因。

## 仲裁语义

- 少于两名独立人工评审，或两名评审结论一致：`not_required`；
- 两个不同评审人 ID 的人工评审在五项分数或接受档位上存在分歧，且没有仲裁记录：`pending`；
- 存在 `是否仲裁=是` 且评审人 ID 不属于原 A/B 评审人的独立记录：`resolved`。

仲裁是追加的新评审记录，不覆盖 A/B 原始意见，也不会简单平均掉分歧。

## 隐私契约

管理中心列表只返回 ID、哈希、受控引用、计数、指标和元数据，不返回私有样本输入、模型输出正文或来源全文。私有/脱敏 suite 会显示明确的隐私标识；正文只保留 `agent_trace:<traceId>` 等受控引用或 `hash_only` 状态。

任何新增字段如果可能包含用户正文，必须先通过角色授权和字段级隐私评审，不能直接加入 Center 列表 DTO。

## 验证与上线条件

Task 10 完成只代表管理闭环可用，不代表 WABench 已接管生产发布门禁。正式切换仍需：

1. 在同批公开与私有样本上完成 V1/V2 归一化回归；
2. 确认红队、来源边界和硬失败均满足门禁；
3. 完成 Shadow/双写观察和人工仲裁；
4. 验证发布开关与回滚路径；
5. 再由后续任务显式切换生产门禁。
