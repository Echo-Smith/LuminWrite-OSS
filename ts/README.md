# LuminWrite TypeScript

这里是 LuminWrite 的 TypeScript 重写，只放在 `ts/`。仓库里原有的 Go 后端和 React 前端不动。

M0 打的是地基：块锚点修订、只追加的版本链、以及每篇文档一个 Pi 对话。不需要模型密钥就能安装、构建、测试和跑演示。

## 环境

- Node.js 24（见 `.nvmrc`）
- pnpm 12.11.2（`packageManager` 字段）

```bash
cd ts
pnpm install
pnpm -r build
pnpm -r test
pnpm demo
```

`pnpm demo` 会建一篇文档，写出大纲和初稿，再只改第 2 段。其余段落与初稿逐字相同，然后回滚到大纲。成功时输出 `DEMO_OK`。

真实模型不会在测试或演示里被调用。只有同时设置 `LW_LLM_ENABLE=1` 和 `LW_LLM_API_KEY`（或 `OPENAI_API_KEY`）时，`@luminwrite/models` 才会构造 OpenAI 兼容客户端。M0 的写作回合使用 Pi 的 faux 提供者，修订内容由确定性计划函数提出，服务端负责校验锚点和说人话。

## 包

| 包 | M0 状态 |
|---|---|
| `contracts` `doc-model` `plainspeak` | 浏览器和 Node 共用的文档模型与空话检测 |
| `db` | Drizzle schema。测试和演示用 PGlite，`openPostgres` 留给 `DATABASE_URL` |
| `models` | 档位路由、可重试回退、用量账本接口 |
| `harness` | `HarnessPort` 后面的 pi-durable 1.3.0，SQLite 存储 |
| `style` | 默认文风 `plain-professional`，说人话地板不可关 |
| `eval-adapter` | 确定性说人话冒烟，负对照排最后 |
| `edition` | 开源扩展口：授权、提案策略、实时通道 |
| `server` `cli` | 最小 HTTP 和演示 |
| `jobs` `prefs` `kb` `sources` `research` `roles` `library` | 空桩，留给后续里程碑 |

验证记录和与功能地图的偏差见 `docs/m0-verification.md`。

## 密钥扫描

仓库根目录的 `.gitleaks.toml` 在默认规则之外增加了裸 `sk-` 密钥规则。提交前钩子在 `.githooks/pre-commit`：

```bash
git config core.hooksPath .githooks
```

本机需要 gitleaks v8.30.1。钩子命令是 `gitleaks git --pre-commit --redact --staged`。CI 只扫工作区，不扫历史，避免已作废、但还留在历史里的密钥把检查打红。
