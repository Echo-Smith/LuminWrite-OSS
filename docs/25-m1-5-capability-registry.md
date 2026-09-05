# M1.5 — Capability Registry 统一：设计与实现记录

> V3.0 M1 终增量（L，分片交付）。目标：把五套各自为政的注册/分发面收敛为一个 `UnifiedManifest` + `CapabilityResolver` 视图，旧 registry 经 adapter 兼容、无双权威。本文记录总设计与切片 1（已交付）。

## 25.1 五套面盘点（2026-09-04 复核）

| 面 | 注册权威 | 本进程形态 | M1.5 后角色 |
|---|---|---|---|
| governed | `writingplan.CapabilityRegistry` | 编译+dispatch 同一 registry（M1.4 Activate seam） | 目标模型，canonical `core.*` id |
| tool | `engine.ToolRegistry` | 每 session buildToolRegistry 装配；MCP 工具经 `RegisterTools` 以 `mcp__` 前缀入同一 registry（无独立 id 空间） | 视图 `tool.*` |
| editorial | `editorial.EditorialToolRegistry` + `DynamicAgentRegistry` | DAG 专用实例 | 视图 `editorial.tool.*` / `editorial.agent.*` |
| validator | `writingquality.ValidatorRegistry` | 包级 Default | 视图 `validator.*` |
| mcp | `mcp.Registry`（连接管理） | 工具已并入 toolRegistry | 随 tool 面可见（`tool.mcp__…`） |

## 25.2 设计决策

- **D1 视图非二写**：`internal/capability` 是只读收敛层——`UnifiedManifest`（id/class/source/version/description/IO/permissions/available）+ `Catalog` 接口 + `Resolver`（ByID/ByClass/Sources）。注册权威 100% 留在各 owning registry。
- **D2 前缀命名空间结构化防冲突**：governed 独占裸 id；其余面强制 `tool.`/`editorial.`/`validator.` 前缀——跨面 claim 在结构上不可表达（守卫测试钉死）。
- **D3 单权威守卫常驻**：`Resolver.CheckSingleAuthority` 校验命名空间形状 + 全局 id 唯一，是"收敛不变成第二个权威"的长期防线。
- **D4 surface 缺失即缺席**：某面未配置（如 editorial 未初始化）时 catalog 为空视图，resolver 照常组装——部署形态如实可见。
- **D5 permissions 语义**：governed 传真实治理权限名；legacy 面用标记（`legacy.tool`/`legacy.editorial`/`validation.run`）不发明等价物——权限统一是后续切片的事。

## 25.3 切片 1（已交付，2026-09-04）

- `internal/capability/capability.go`：包核心（D1–D3）+ 3 单测（组装查询 / 重复 source 拒绝 / 守卫语义）。
- `server/capability_catalogs.go`：四 surface adapter（governed/tool/editorial/validator）+ `Server.newCapabilityResolver()` 组装 + 只读 accessor。
- 支撑性只读扩展：`ToolRegistry.Descriptors()`、`EditorialToolRegistry.All()`（新增快照读取，注册语义零变化）；Server 持有 editorial 双 registry 的引用（DAG 实例不改）。
- 验收：`TestUnifiedCapabilityResolverCoversSurfaces`（真实 booted Server 四面齐、守卫通过、governed 类可查）+ `TestUnifiedCapabilityGuardKeepsNamespacesDisjoint`（漂移条目落 `editorial.agent.*` 命名空间、governed id 仍唯一归 governed）。

## 25.4 切片 2–4（已交付，2026-09-04）

- **切片 2 消费方迁移**：admin 端点 `GET /api/v2/admin/capabilities`（`handlers_capability.go`）——Resolver 的第一个生产消费方；每次读取运行 `CheckSingleAuthority`（守卫违例以 500 `CAPABILITY_AUTHORITY_CONFLICT` 暴露而非静默清单）；包扩展 `Resolver.BySource`。
- **切片 3 Role Policy 接入**：`capability.RolePolicy` 接口 + `PrefixRolePolicy`（前缀授权、缺省拒绝、`*` 全授）+ `Resolver.VisibleToRole`（nil policy=AllowAll 契约）+ server 侧 `editorialRolePolicy()`（researcher/writer/reviewer/admin 按 §20.7 角色隔离设计授权；admin 全通）；端点 `?role=` 返回角色视图。**职责边界**：本切片是 resolver 侧管道——调度强制点随 V3.0 M5（Role Capability Policy）落地，今天不改变 editorial DAG / 治理运行时的实际执行。
- **切片 4 权限语义统一**：`capability.GovernedPermissions(marker)` 映射表（`legacy.tool`/`legacy.editorial` → `model.invoke, materials.read, external.research, validation.run`；治理权限名与未知标记原样透传）——文档即代码；tool/editorial adapter 的 Permissions 字段改为展开后的治理权限名（视图不再暴露裸 legacy 标记）；**旧面网关仍自身权威**（映射是语义陈述，不是给旧面加装治理强制）。
- 验收：capability 包 5 单测（组装/重复 source/守卫/角色过滤/权限映射）+ `TestAdminCapabilitiesEndpoint`（真实路由 admin JWT：清单四面齐 + researcher 视图含 `core.retrieval.search` 不含 `core.draft.generate` + legacy 条目带治理权限名）；双仓 10 文件字节一致，双仓默认并行 `-count=1` 全树带 DB 零失败。
