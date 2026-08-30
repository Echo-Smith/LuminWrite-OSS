# Article Output Contract Design

## 背景

当前写作输出同时存在三套约定：WorldState 和 StyleProfile 要求 `{"title":"..."}` 加 `---ARTICLE---`，部分系统提示要求 Markdown 标题，Harness 与旧 Pipeline 又各自维护一套流式解析逻辑。模型在长上下文或多轮工具调用后可能偏离早期格式指令，因此不能把 Prompt 服从当作正确性保证。

## 决策

新输出统一使用 Markdown：第一条文章标题必须是 `## 标题`，空一行后输出正文。标题继续通过 `article.title` 事件独立发送，文章正文中不保留标题行。

输入侧采用宽容解析。解析器优先识别标准 `##`，兼容 `#`、标题前客套话、旧 JSON 加分隔符、首行短标题和完全无标题。已确认的提纲标题优先于模型临时生成的回退标题。无论协议是否识别成功，正文都必须完整且只能输出一次。

## 长上下文策略

格式提醒分成三层：全局任务说明、当前用户消息末尾的短提醒，以及 `write_article`/`revise_section` 工具结果中的近端提醒。近端提醒只说明“本轮若输出完整文章”的格式，不改变引导模式先确认提纲的流程。旧 Pipeline 的输出格式 section 保持 critical 优先级，不能被 Token 预算裁剪。

## 流式状态机

共享 `ArticleStreamParser` 负责 buffering、协议识别、标题事件、正文 delta、reset 和 finalize。Harness 与旧 Pipeline 共同调用它，不再复制解析分支。解析器在标题未决时暂存有限前缀；找到标题后只发送标题后的正文。超过上限仍未匹配时进入透传模式，使用已确认标题或最终回退，但绝不丢弃缓冲区。

## 可观测性

每次最终解析记录 `markdown`、`legacy_json`、`short_line_fallback` 或 `missing_title`。结果写入 ExecutionContext，并作为 `article_output` step result 进入现有 trace 的 `step_history`，同时输出带 `trace_id` 的结构化日志。该信息仅供管理端/trace 使用，不新增普通用户提示。

## 验收标准

- 标题跨任意 delta 边界时解析结果一致。
- 标题与正文同一 delta 时正文不重复。
- 模型忽略格式时正文仍完整。
- 旧 JSON 输出继续可用，但所有新 Prompt 不再要求 JSON。
- 长历史和工具调用之后仍存在近端格式提醒。
- Harness 与 Pipeline 使用同一解析器并产生同一协议标记。
- OSS 与商业版共享实现逐字一致，完整质量门禁通过。
