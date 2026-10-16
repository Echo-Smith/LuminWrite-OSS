# M1-04 待实现确认

2026-10-16，对着 pi-ai 1.3.0、pi-durable 1.3.0 和当前 PGlite schema 核对。

## pi-ai：把 stream 委托回内置 provider

可以。`Models.stream` 先用 byok provider 自己的 `auth.apiKey.resolve()` 解开密钥，再调用 `provider.stream`，此时 `options.apiKey` 已经是解开的值。`byokFromBase` 把 `model.provider` 改回内置 id 后交给原来的 `stream`。测试确认内置实现看到的是原来的 provider id 和凭据库里的 key，响应 JSON 里没有 key。兼容端点（`openai-compatible` / `anthropic-compatible`）不走这条委托，直接 `createProvider` 加 `openai-completions` 或 `anthropic-messages` 的 lazy API。

给 `createModels` 的 `AuthContext.env()` 固定返回 `undefined`，`fileExists()` 固定返回 `false`。机器上的 `OPENAI_API_KEY` 不会被 pi-ai 当成凭据。

## pi-durable 会不会把解开的 header 写进 SQLite

这一轮不会。用 `byok_<id>` 跑完一个 faux 回合后，逐字节扫 SQLite 文件、拼上捕获的 `console.error`、`run_events` 形状的 JSON 和 `usage_ledger` 形状的 JSON，都找不到测试 key。`AssistantMessage` 上也没有 apiKey 字段。密钥只在当次 `resolve()` 里 `reveal()` 一次。

## 文档没有 final 状态

M0 表有 `documents.final_version_id`（可空）和 `documents.status`（文本，默认 `draft`）。没有 `final` 枚举。本 PR 不迁移；等文档生命周期那一步再把列去掉。

## 丢掉未保存文档

`doc_versions.document_id` 是 `ON DELETE CASCADE`，同时 `BEFORE DELETE` 触发器会抛 `doc_versions is append-only`。因此 `DELETE FROM documents` 会失败，不能靠级联清掉版本。未保存文档以后应标记丢弃，而不是硬删。

## Pi 对话能不能删

pi-durable 1.3.0 的 `Harness` / `Conversation` 类型里没有删除整段对话的方法。偏好原文如果进过对话，删偏好条目清不掉那份副本。M1 还没有删除确认框；以后要单独提供“删除这篇文档的会话记录”，并写明聊天记录里可能还留着原文。
