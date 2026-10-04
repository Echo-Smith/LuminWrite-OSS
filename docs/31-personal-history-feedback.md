# 31 — 个人写作记录与反馈历史（单用户化 Phase 2）

> 状态：已实现（2026-10-05）。
> 关联：`docs/30-byok-personal-models.md`（Phase 1）、`docs/03-api-specification.md`（端点）、admin `trace-history`（全站对位视图）。

## 1. 范围与定位

Phase 1 把「模型服务 / 用量统计」搬进了个人中心；Phase 2 处理剩下的两个用户价值项：

1. **写作记录个人视图**（个人中心 → 写作记录）：列表（状态 / 评分 / 反馈徽标 / 耗时 / 归档筛选 / 分页）+ 详情回放（步骤时间线、评审分维条、token 三格、文章正文）。复用既有用户态 `GET /api/v2/sessions` 与 `GET /api/v2/sessions/{id}`，不新增查询端点；admin `trace-history` 保持全站运维视图定位不变。
2. **我的反馈**（同 section 的第二个 tab）：只读历史。反馈本身是一次性的（`HasFeedback` 拒绝重复提交），因此无编辑/删除面。

## 2. 安全修复（本轮最重要的部分）

1. **`/api/v2/sessions` 组横向越权修复**：此前 `GET /sessions/{id}` 及 artifacts / events / versions / plan 共 7 个子 handler 只按 trace_id 查、不校验归属——任何登录用户可读任意用户的写作记录全文。现统一加 `assertTraceOwner`（`GetTraceUserID` 比 for owner，legacy NULL 归属行视为无主、一律 404，存在性不跨账号泄露）。title / delete / article-update 原本已带归属过滤。
2. **`GetTrace` NULL `current_step` 崩溃修复**：详情 SELECT 对 `current_step` 补 `COALESCE`（老数据该列可为 NULL，此前详情端点对这类行直接 500/404）。
3. **`POST /feedback` 鉴权与归属落库**：路由挂 `jwtOptionalMiddleware`（游客提交行为保留）；`SaveFeedback` 现在把提交者 user_id 写入 `feedback_segments.user_id`（该列自 migration 006 存在但写入路径从不填）。历史 NULL 行经 trace JOIN 覆盖，无需回填迁移。
4. **前端幽灵端点清理**：`POST /sessions/{id}/duplicate` 后端从未实现（一直 404 静默失败），且"复制一条 trace 记录"语义不成立——删除侧边栏「复制会话」动作与 store 函数。真需求出现时按「同输入重写」语义另做。

## 3. folders / batch 半成品补齐

migration 109 建好了 `session_folders` 表、`agent_traces.folder_id`、归档索引，但 handler 从未接线——侧边栏的文件夹/归档/批量管理一直是静默失败的空壳。本轮补齐：

- `GET/POST /api/v2/session-folders`、`PUT/DELETE /api/v2/session-folders/{id}`（CRUD，归属在 SQL）；
- `POST /api/v2/sessions/batch`（`delete` 软删 / `archive` / `unarchive` / `move`，逐行 `WHERE user_id` 校验，move 的目标 folder 同 statement 校验属主；返回 `{affected}`，契约与前端既有调用一致）；
- 全部挂 `jwtAuthMiddleware + rejectGuestMiddleware`。

## 4. 列表级聚合字段

`TraceRepo.ListTraces` 两个分支补 `review_result`（Go 侧平均成 `review_score`，语义同 admin 列表）与 `has_feedback`（EXISTS 子查询）——个人记录列表由此获得评分列与反馈徽标，消除与 admin 视图的信息差（详情级本就是超集）。

## 5. 端点汇总

```
GET    /api/v2/sessions                     # 既有；列表新增 review_score / has_feedback
GET    /api/v2/sessions/{id}                # 既有；新增归属校验（404）
GET    /api/v2/sessions/{id}/artifacts|events|versions[/{vid}]|plan   # 既有；新增归属校验
GET/POST    /api/v2/session-folders         # 新增
PUT/DELETE  /api/v2/session-folders/{id}    # 新增
POST   /api/v2/sessions/batch               # 新增
GET    /api/v2/feedback/mine                # 新增（本人反馈，JOIN 覆盖历史行）
POST   /api/v2/feedback                     # 既有；jwtOptional + user_id 落库
```

## 6. 明确不做

| 项 | 原因 |
|---|---|
| purpose 评审分流（BYOK 深化） | 已尽调（verification 只需 3 处装配 + purpose 进缓存维度，schema 不动），留待下轮 |
| KB 双轨合并 | 需先定合并方向（素材中心 vs admin KB 页），工程量独立成期 |
| feedback 聚合读端点鉴权加固 | 低敏感（风格级聚合分），避免影响 admin 页，记录在案 |
| duplicate 会话 | 语义不成立（见 §2.4） |
