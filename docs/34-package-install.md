# 34 — 下载安装体系（风格 / 技能 / 服务）

> 状态：已实现（2026-10-02，OSS `feat/materials-ia-and-packages`；商业线 `b27b8a0` 同步移植）。

## 1. 模型

风格库中的风格、技能与服务从「导入」（复制一份配置）改为「**下载安装**」：
包体落到固定目录，注册信息落 `installed_packages`，卸载以注册行为锚点三处
一起清。同一个安装管线两个来源：

- **builtin**：包体内嵌在二进制里（`internal/services/packages/builtin/`：
  `catalog.json` + 每包目录），安装时拷贝出；
- **url**：包体是 zip，经 SSRF 防护的下载器拉取后解压。

两者都产出「暂存目录 + manifest」，再走同一套按 kind 分发的注册。

## 2. 固定位置与三种落地形态

安装根：`data/packages/<kind>/<slug>/<version>/`（`PACKAGES_DIR` 可配置，
桌面化时指向 Application Support）。

| kind | 包体形态 | 生效态 | 卸载清理 |
|---|---|---|---|
| style | config JSON + 资产 | `user_style_profiles` 行 + 首个不可变版本（经既有 Loader DB 路径可见） | profile 行 + 目录 + 注册行 |
| skill | `SKILL.md` + scripts/references（目录即运行时形态） | 目录本身；注册行是列举锚点 | 目录 + 注册行 |
| service | MCP server manifest（command/args/env/url） | `mcp_servers` 行（+ 可选 `api_keys`） | server 行 + 目录 + 注册行 |

**凭据不进包**：服务包只注册服务器，密钥由用户在「风格和技能 → MCP 服务」
单独配置——与 BYOK 同款纪律。

**manifest（package.json）**：`{kind, slug, version, title, description, entry}`。
校验从严：kind 白名单；slug/version 白名单字符（同时是路径段）；entry 必须是
安全相对路径。

## 3. 安全红线（测试固化）

1. **SSRF**：install URL 仅 http/https；主机名解析后拒绝环回 / 私网 / 链路本地
   / 保留地址（覆盖 localhost、127/8、10/8、172.16/12、192.168/16、
   169.254/16 含云元数据、100.64/10 CGNAT、TEST-NET、240/4 等）；每个重定向
   跳转重新校验；大小（32MB）与超时（30s）双上限。
2. **Zip slip**：解压拒绝绝对路径 / `..` / 逃出目标目录的条目；文件数与单文件
   大小上限；只写普通文件（跳过符号链接等）；拷贝按声明大小 +1 截断。
3. **归属**：全部端点 jwtAuth + rejectGuest；安装 / 卸载的归属判断在 SQL
   （`WHERE user_id = $1::uuid`），与 BYOK 同款。
4. **幂等**：同一 (user, kind, slug) 重复安装返回 409 `already_installed`，
   需先卸载；安装位置已存在同样拒绝。

## 4. 端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v2/packages/catalog` | 内置目录（公开） |
| GET | `/api/v2/packages?kind=` | 我的已安装（rejectGuest） |
| POST | `/api/v2/packages/install` | `{source:"builtin",slug}` 或 `{source:"url",url}` |
| DELETE | `/api/v2/packages/{id}` | 卸载（三处一起清） |

失败分类：`already_installed` → 409；上游类（URL 非法 / 下载失败 / SSRF 拒绝）
→ 502 `upstream_error`；其余 → 400。

## 5. 前端

共享安装弹窗 `components/styles/package-install-dialog.tsx`（内置目录卡片 +
包地址输入 + 已安装列表与卸载），三处接入：

- **风格库**：全局风格卡片「导入 → 安装」（配置拷贝仍是全局风格的安装路径），
  另加「安装风格包」入口；
- **技能**：页签重构为「已安装技能包」（列举 / 卸载 / 版本 / 安装路径）+ 安装
  入口；原运行时工具插件（tool-plugins）降级为折叠的「高级」区，避免两种
  「技能」混叙；
- **MCP 服务**：服务密钥区加「安装服务包」入口（kind=service）。

## 6. 内置目录（v1）

| kind | slug | 版本 | 说明 |
|---|---|---|---|
| style | writer-starter | 1.0.0 | 三段式通用写作骨架，安装后进「我的风格」 |
| skill | knowledge-base | 1.0.0 | 知识库技能包示例（SKILL.md + 约定） |

服务包 v1 无内置项（经 URL 安装）；目录随发布包内置，不依赖市场后端。
自动升级 / 市场索引明确不做（见 docs/30 §6 同款边界）。

## 7. 双线同步

安装目录可被 `.commercial-files` 的 glob 表达（`skills/tencent-news/**` 等
商业技能包已是先例）；`data/packages/**` 建议纳入 glob，避免商业资产包漏入
开源仓。商业线经 `b27b8a0` 完成同号迁移（127）与前端移植。
