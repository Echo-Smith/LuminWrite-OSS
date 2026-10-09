# 32 — RSS 订阅（素材）

> 状态：已实现（2026-10-09）。用户级 RSS/Atom 订阅：新条目周期性抓取后
> 以素材（`source_type='rss'`）落入指定文件夹，进入写作管线的混合检索。

## 1. 产品语义

**一个订阅源 = 一个自动更新的素材文件夹。** 用户订阅「少数派」，它的新
文章自动出现在「少数派」文件夹里，可被搜索、可被写作引用。无新概念
（复用现有文件夹与素材治理），无未读状态机——写作场景只需要「被检索
到」，不需要「被读过」。

为什么不进选题流：topics 是全局广播流（热搜），个人订阅混入会污染公共
流，语义错位。

## 2. 接入方式（复用清单）

| 环节 | 实现 |
|---|---|
| 订阅存储 | 新表 `rss_subscriptions`（migration 123）：user_id、feed_url（UNIQUE per user）、target_folder_id → material_folders、max_items_per_tick、etag/last_modified、fail_count |
| feed 解析 | `internal/services/rss`：`encoding/xml` 标准库，支持 RSS 2.0（content:encoded）、RDF/RSS 1.0、Atom 1.0；**零新增依赖** |
| 抓取 | SSRF 加固 fetcher：scheme 白名单、host 字面量 + DNS 解析双层私网/环回/链路本地阻断、redirect 复检、8MiB/20s 上限、条件 GET（If-None-Match/If-Modified-Since） |
| 正文入库 | **优先用 feed 自带正文**（`StripHTML` 去标签/实体解码），绕开站点专用的正则 HTML 抽取器；相对链接按 feed URL 解析为绝对地址 |
| 素材管线 | 复用 `AddDocument → ChunkText → AddChunk → SaveMaterial`（同 handlers_user_materials），治理字段（source_ref）自动生效，embedding 同步、入库即可搜 |
| 去重 | 双层：`knowledge_base.metadata->>'source_url'` 精确查重（防 feed 无稳定 guid）+ 内容撞键（uk_kb_content_hash）识别为跳过 |
| 调度 | 全局 cron 任务 `rss_fetch`（默认 `*/15 * * * *`，启动时幂等播种，admin 可改）：按 `last_fetched_at NULLS FIRST` 选到期源逐源抓取 |

## 3. 限量设计（防 cron 超时）

`AddChunk` 逐 chunk 同步调 embedding，RSS 的量级比既有 kb_auto_import 大一
个量级。三道闸：

- **每源每 tick**：`max_items_per_tick`（schema 默认 3，用户可调 1-20）；
- **全局每 tick**：20 条（`rssGlobalTickBudget`），超出顺延下个 tick；
- 抓取失败 `fail_count + 1` 记 `last_error`，成功即清零（admin 面板可见）。

## 4. API

```
GET    /api/v2/rss/subscriptions           # 我的订阅列表
POST   /api/v2/rss/subscriptions           # 订阅（创建时立即抓取一次校验+首批入库）
PUT    /api/v2/rss/subscriptions/{id}      # 编辑（标题/文件夹/限量/启停）
DELETE /api/v2/rss/subscriptions/{id}      # 删除订阅（已导入素材保留）
POST   /api/v2/rss/subscriptions/{id}/refresh  # 立即更新（与 cron 同一限量编排）
```

全部挂 `jwtAuthMiddleware + rejectGuestMiddleware`，归属控制在 SQL。

## 5. UI

素材窗口左侧文件夹栏新增「RSS 订阅」入口 → 管理面板（源列表：标题/
失败状态/上次抓取/目标文件夹，添加/编辑/立即更新/删除）。素材行
`source_type=rss` 显示 RSS 徽标。

## 6. P3 已落地（2026-10-09）

- **摘要源全文抓取**（migration 124 `fetch_full_text`，订阅级开关默认关）：
  feed 正文 < 400 runes 判定为摘要 → 经 `URLImporter` 抓原文（fetch →
  抽取 → 分块 → embedding）→ 以 RSS 素材入库；抓取失败降级用摘要，
  摘要仍不足 50 runes 才跳过。每次抓取计入 FullText 计数（refresh 响应可见）。
- **OPML 导入导出**：`GET /api/v2/rss/subscriptions/opml` 导出；
  `POST /api/v2/rss/subscriptions/import` 导入（原始 XML，≤1MiB、
  ≤200 feeds；逐源校验 + 抓取预检 + 重复跳过，单源失败不中断批次）。
  `rss.ParseOPML` 展平嵌套 outline，`BuildOPML` 往返一致。

## 7. 已知限制

1. **`knowledge_base.content_hash` 是全局唯一约束**：两个用户导入字节
   相同的内容时，后到者按「重复内容」跳过（存量限制，非 RSS 引入；
   修复需按 user 作用域化 content_hash 的迁移）。
2. OPML 导入暂不映射分类文件夹到 `material_folders`（展平导入）。
4. 检索质量取决于部署的 embedding 配置与 BM25 中文分词（存量特性，非 RSS
   特有）：未配置 embedding 时降级纯 BM25，中文连续词可能切分不开；配置
   `DASHSCOPE_API_KEY` 后 dense 检索自动参与 RRF 融合。
