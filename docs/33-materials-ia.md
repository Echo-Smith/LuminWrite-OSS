# 33 — 素材信息架构（选题 / 订阅 / 知识库）

> 状态：已实现（2026-10-02，OSS `feat/materials-ia-and-packages`；商业线 `b27b8a0` 同步移植）。

## 1. 背景与决策

OSS 版的「选题」「素材」「RSS 订阅」此前分散在三处：选题中心独立页、素材中心
（`/materials`，后重定向）、RSS 订阅是素材页侧边栏里的一个弹窗。本次重构按四个
决策收口：

1. **三页签素材壳**：`/topics` FloatingShell 改「选题 / 订阅 / 知识库」三个页签，
   壳标题与侧边栏入口统一为「素材」；`?tab=rss` / `?tab=materials` 白名单式直达。
2. **RSS 订阅升格为页签**：弹窗（模态套模态）改为页签主体，自取文件夹列表、
   挂载即加载；侧边栏恢复纯文件夹语义。
3. **选题 / RSS 可存知识库**：选题新增 `POST /api/v2/topics/{id}/knowledge`；
   RSS 条目本就自动入库（见 §3）。
4. **知识库维护面板轻量合并**：统计、分块/实体查看、检索调试、图谱（懒加载）
   进入知识库页签的「总览」；破坏性维护（重分块 / 重导入）与多 KB 管理保留在
   实验室更名后的「知识库拓展管理」面板。

## 2. 页签结构

```
素材（/topics，FloatingShell）
├─ 选题    热搜 / 自定义 / 收藏 + AI 角度推荐 + 「保存至知识库」
├─ 订阅    RSS 源管理：CRUD、立即抓取、OPML 导入导出、抓取状态
└─ 知识库  文件夹 + 素材列表 + 检索调试 + 「知识库总览」（统计 / 图谱）
```

页签状态与 URL 同步（`?tab=`），切换用 `replace` 不污染历史。

## 3. 保存至知识库

**RSS**：条目抓取后直接落 `knowledge_base` 文档 + `knowledge_chunks` +
`user_materials(source_type='rss')`（`rss_ingest.go` 三写），无需额外动作；订阅
页展示每源「上次抓取 / 连续失败」状态与「立即抓取」（refresh 端点）。

**选题**：`POST /api/v2/topics/{id}/knowledge`（jwtAuth + rejectGuest）：
- `mode=text`（默认）：正文走 `KbManager.AddDocument` + 分块 + `SaveMaterial
  (source_type="topic")`；缺省回退选题描述，常见「收藏备用」零 body；
- `mode=url`：选题原文链接走共享 `URLImporter`（fetch → 抽取 → 分块 →
  embedding，与 RSS 全文回退同一管线）。

**写入路径统一**（本次的关键修复）：`AddDocument` 原先不写 `kb_id`，而
`AddDocumentToKB` 才写——导致素材家族（materials / RSS / 文件解析）写入的文档
在按 `kb_id` 过滤的 KB 视图里查不到。现在 `AddDocument` 插入即带
`kb_id='default'`（种子 KB，migration 046），`AddDocumentToKB` 的非默认覆盖
不受影响；migration 125 回填窗口期存量行，chunk 的 `kb_id` 从父文档继承
（`AddChunk` 子查询），无需单独回填。

## 4. 知识库总览与拓展面板

- 总览（知识库页签内，默认收起）：统计卡片走用户侧 `/api/v2/kb/stats`；
  实体图谱按需挂载（`KBGraphPanel` 仅在展开时请求，避免拖慢首屏）。
- 拓展管理（实验室开关 `labsKbMaintenance` 后）：多 KB CRUD、
  `generate-embeddings` / `rechunkAll` / `reimportAll` 三个全局破坏性操作，
  仍走 `/api/v2/admin/kb/*` admin 路由组。分块/实体查看此前已通过
  `kb-inspect.tsx` 下沉到用户侧，本次提升为总览内一等入口。

## 5. 端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v2/rss/subscriptions` | 订阅列表（rejectGuest） |
| POST/PUT/DELETE | `/api/v2/rss/subscriptions[/{id}]` | 订阅 CRUD |
| POST | `/api/v2/rss/subscriptions/{id}/refresh` | 立即抓取 |
| GET/POST | `/api/v2/rss/subscriptions/opml` · `/import` | OPML 导入导出 |
| POST | `/api/v2/topics/{id}/knowledge` | 选题存知识库 |
| GET | `/api/v2/kb/stats` · `/documents/{id}/chunks` · `/graph` | 总览数据（用户侧） |

## 6. 双线差异

- OSS：RSS 后端自本版起提供（migration 123/124）；商业线经 `b27b8a0` 同号移植
  （其 `material_folders` 为真实现，「订阅源 = 文件夹」语义更完整）。
- 商业线个人中心无 BYOK「模型服务」section（模型设置留在「风格和技能」窗），
  因此「保存至知识库」按钮在两条线均可用，但模型解析优先级仅 OSS 具备用户层。
