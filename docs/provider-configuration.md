# 模型提供方配置

后端 LLM 客户端走标准 OpenAI `/chat/completions` 协议（`DEEPSEEK_BASE_URL +
"/chat/completions"`），扩展字段（`thinking` / `reasoning_effort` /
`reasoning_content`）均为 `omitempty` 或纯响应侧——未启用时不进入请求体。
因此任何 OpenAI 兼容服务都可作为模型后端。

## 配置项

```bash
DEEPSEEK_BASE_URL=https://api.deepseek.com   # 不带 /v1，代码自动拼接 /chat/completions
DEEPSEEK_API_KEY=sk-…
DEEPSEEK_DEFAULT_MODEL=deepseek-v4-flash     # 必须换成目标服务支持的模型名
DEEPSEEK_TIMEOUT=120s
DEEPSEEK_MAX_TOKENS=131072                   # 按目标模型上限调整
DEEPSEEK_RESPONSES_API_RATIO=0               # Responses API 灰度，第三方服务保持 0
```

## 切换到其他提供方

```bash
# OpenAI 官方
DEEPSEEK_BASE_URL=https://api.openai.com/v1
DEEPSEEK_DEFAULT_MODEL=gpt-4o
DEEPSEEK_MAX_TOKENS=16384

# vLLM / Ollama / LM Studio（本地自托管）
DEEPSEEK_BASE_URL=http://host.docker.internal:11434/v1   # Ollama 示例
DEEPSEEK_DEFAULT_MODEL=qwen2.5:32b
```

同时把 Admin 后台「模型配置」里 DB 热配置的条目同步更新——DB 有模型配置时
动态客户端优先于 env（env 只是启动兜底）。

## 已验证边界（代码审查结论）

- 请求体为 OpenAI 超集，扩展字段默认不发送；标准 `/chat/completions` 全功能
  （含流式、工具调用）与协议方无耦合。
- **必须改模型名**：默认 `deepseek-v4-flash` 在其他服务不存在，启动健康检查
  与首条消息就会失败。
- `DEEPSEEK_RESPONSES_API_RATIO` 保持 0：Responses API 是 DeepSeek/OpenAI
  官方特性，第三方兼容服务普遍不支持。
- SenseNova 修正分支按 host 精确匹配（`token.sensenova.cn`），对其他服务无影响。
- Embedding 独立配置（`DASHSCOPE_*`），目前仅阿里 DashScope 一种实现；
  语义去重/记忆检索在未配置时自动降级关闭，不影响主流程。

## 已知限制

未做逐提供方回归（本仓库 CI 只跑 DeepSeek/SenseNova 组合）。切换后请先在
Admin「模型配置」对每个任务类型做一次连通性测试，再放量。系统性的多提供方
抽象见 V3 路线图 M6（ProviderAdapter）。
