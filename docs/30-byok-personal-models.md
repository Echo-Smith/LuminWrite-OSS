# 30 — BYOK 个人模型服务（单用户化 Phase 1）

> 状态：已实现（2026-09-30）。
> 关联：`docs/08-admin-dashboard.md`（admin 回退语义）、`docs/03-api-specification.md` §6.9-§6.11（端点）、`docs/02-database-schema.md`（`user_model_keys`）。

## 1. 背景与决策

OSS 版此前的模型配置是**全局单份**（`model_configs`，无 user_id）：部署者=管理员，所有注册用户共用一份密钥。这与「自托管个人工具」的目标用户（创作者自带 DeepSeek/OpenAI key）错配。

本阶段的定案（2026-09-29 会话决策）：

1. **全局配置保留为回退**：admin 的模型配置页更名「全局默认模型（回退）」，用户未自配时透明回退，现有部署零破坏；团队自托管（管理员配默认、成员自带覆盖）语义成立。
2. **BYOK per-user**：新表 `user_model_keys`，解析顺序「用户配置优先、全局回退、env 兜底」。
3. **个人中心承载用户价值功能**：新增「模型服务」「用量统计」两个 section；admin 不再是用户唯一入口。
4. **`DISABLE_REGISTRATION`**：单用户/自部署可关闭自助建号（register/guest → 403 `REGISTRATION_DISABLED`），前端隐藏注册入口。

## 2. 解析语义（核心）

`services.LLMService.GetClientForUser(ctx, userID, modelName)` 的四级顺序：

```
1. 用户精确匹配   user_model_keys WHERE user_id=$1 AND model_name=$2 AND is_active
2. 全局精确匹配   model_configs  WHERE model_name=$1 AND is_active
3. 用户默认       user_model_keys WHERE user_id=$1 AND is_default AND is_active
4. 全局默认       model_configs  WHERE is_default AND is_active
5. env 兜底       静态 LLMClient（DB 不可用或全部未命中）
```

要点：

- **显式选模型的请求解析到定义该模型的层**：用户在模型选择器里选了全局模型 → 继续用平台密钥（该模型只有平台能供）；选了自己的模型 → 自己的密钥。默认值只在「未指定模型」时兜底。
- **缓存按用户命名空间**：`LLMService` 进程级缓存 key 为 `userID|model`（原实现只有 `model`，多用户下会串号——本次修复）。`InvalidateUserCache(userID)` 只清该用户；`InvalidateCache()` 全清（admin 写路径语义不变）。TTL 仍为 30s。
- **用户身份来源分三路**：
  - HTTP 请求链路（编辑部 planner/executors、topic 生成、tool graph）：`GetClient` 从 ctx 读 `auth.Principal`，零改动生效；
  - **治理写作主管线**：`governed_worker` 用 `context.Background()` 启动 run，请求 ctx 到不了节点派发。因此 `governedRunnerFactory.llm` 改为 `func(userID string)`，StepFactory 闭包从 `env.Request.UserID`（源自 `writing_runs.owner_user_id`）取身份；`ValidatorRunner`/`LLMResearchDraftGenerator` 增加可选 `LLMForUser` 解析函数（静态 `LLM` 字段保留，测试/shadow 形态兼容）；
  - 系统链路（WABench judge、cron、评测、研究 fact reviewer）：显式空 userID，语义上就该用全局层。

## 3. 存储与安全

- 表 `user_model_keys`（migration `122`）：`user_id UUID FK users(id) ON DELETE CASCADE`、`(user_id, model_name)` 唯一、`(user_id) WHERE is_default` 部分唯一。`purpose` 列与 `model_configs.purpose` 同词表，**本期仅入库不路由**（见 §6）。
- 密钥沿用内联加密约定（AES-256-GCM，`API_KEY_ENCRYPTION_KEY` 派生）。**红线：未配置加密密钥的部署，BYOK 写端点一律 503 `byok_unavailable`**（repo 层 `encryptStoredKey` 与 handler 层 `userModelKeyGate` 双重拒绝）——全局配置历史上有「未配 env 则明文落库」的口子，BYOK 不接受。
- 响应永不携带明文/密文：只回 `has_api_key` + `api_key_masked`（前 3 + `***` + 后 4）。`APIKeyEncrypted` 字段 `json:"-"`，进程内保留供解密。
- 归属控制全部在 SQL（`WHERE user_id = $1::uuid`），按仓库惯例**不挂 requirePermission**（memories 同款）；路由挂 `jwtAuthMiddleware + rejectGuestMiddleware`（guest 无 BYOK）。
- repo 层预校验 id 的 UUID 格式，非法 id 返回 404 而非 500（cast 错误）。
- `is_active` 无 UI 开关：Create 强制 true，Update 不触碰该列（JSON 缺省的 Go 零值 false 不得停用条目）。

## 4. 端点与前端

- `GET/POST /api/v2/model-keys`、`PUT/DELETE /api/v2/model-keys/{id}`、`PUT /api/v2/model-keys/{id}/default`、`POST /api/v2/model-keys/{id}/test`（GET /models 探活 + preflight 风格错误分类）、`POST /api/v2/model-keys/discover`（明文 key 不落库的模型发现，对齐 admin discover 流程）。
- `GET /api/v2/models`（composer 模型选择器）改为合并列表：`source: "user" | "global"`，用户条目带「自定义」徽标。
- `GET /api/v2/usage?days=N`：个人用量聚合（agent_traces 按 user_id；无按模型维度——该表无 model 列，列入 §6）。
- 个人中心新 section：`pages/personal/model-service-section.tsx`（CRUD/测试/设默认/删除确认、guest 锁定态）、`pages/personal/usage-section.tsx`（7/30/90 天窗口 + CSS 条形图）。

## 5. 注册开关

`DISABLE_REGISTRATION=true`（`config.Server.DisableRegistration`）：

- 后端：`/auth/register`、`/auth/guest` → 403 `REGISTRATION_DISABLED`（已有账号不受影响）。
- 前端：`auth-store.init()` 收到 guest 403 时置模块级标记 `isRegistrationDisabled()`（不重试），auth-modal 隐藏注册 tab、guest_token 升级入口回落登录 tab。

## 6. 明确不做（下轮候选）

| 项 | 原因 |
|---|---|
| purpose 分 key 路由（generation/verification/embedding 各自的 key） | 现有 purpose 路由本身未落地（本次已补 SELECT），路由语义需单独定稿；列已入库预留 |
| embedding client per-user | KB 向量化是部署级资源，per-user 需与 KB 双轨合并一起设计 |
| KB 双轨合并（admin KB 页 vs /materials）、写作记录个人视图、feedback 个人历史 | 属「admin 功能拆分」第二阶段，需先定合并方向 |
| wabench/eval judge、memory 提取、研究 fact reviewer per-user | 系统级/验证级语义，保持全局层 |
| 商业线同步 | 本次仅 OSS 线 |

## 7. 验证

- 后端：`go test ./...`（TEST_DATABASE_URL 隔离库）全量零失败；专项覆盖：BYOK CRUD 与跨用户归属（404）、密钥不泄漏（masked）、无加密配置 503 红线、`DISABLE_REGISTRATION` 403、解析顺序五分支、**双用户同模型名的缓存不串号**、用户级缓存失效不误伤他人。
- 前端：`tsc -b` + `npm run lint`（0 error）+ `npm run build` + node:test 121/121。
