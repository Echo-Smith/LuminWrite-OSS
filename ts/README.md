# LuminWrite TypeScript

这里是 LuminWrite 的 TypeScript 重写，只放在 `ts/`。仓库里原有的 Go 后端和 React 前端不动。

M0 打的是地基：块锚点修订、只追加的版本链。会话属于文档：一篇文档有一个主会话，也可以有分叉、接口和协同会话。派生文档是另一篇独立文档，不跟分叉会话混在一起。不需要模型密钥就能安装、构建、测试和跑演示。

本机默认一个所有者。首次 `lw init` 或 `lw serve` 会在数据目录写下 `owner.token`（权限 0600），库里只留令牌的 SHA-256。`/api/v1` 的调用者由这把令牌推出，请求体里的 `actor` 和 `ownerId` 会被忽略。默认只监听 `127.0.0.1`。

外部 Agent 用所有者签发的 API Key（`lw keys create` 或 `POST /api/v1/me/api-keys`）。明文只在创建时出现一次，库里只存 SHA-256。密钥带 scope 和策略；不能管理凭据、写作笔记、模型 slot、订阅源，也不能再签发密钥。按密钥的请求速率是进程内令牌桶，重启清零。人改过的文档上，外部 Agent 的回合默认挂成提案。

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

`pnpm demo` 会建一篇文档。主会话只改第 3 段；从旧版本分出的会话再改第 3 段会被拒绝，改第 5 段可以落下。然后从旧版本派生一篇新文档，派生文档自己的修改不会回到源文档。把段落写回源文档必须用源文档当前的块锚点。成功时输出 `DEMO_OK`。

真实模型不会在测试或演示里被调用。只有同时设置 `LW_LLM_ENABLE=1` 和 `LW_LLM_API_KEY`（或 `OPENAI_API_KEY`）时，`@luminwrite/models` 才会构造 OpenAI 兼容客户端。M0 的写作回合使用 Pi 的 faux 提供者，修订内容由确定性计划函数提出，服务端负责校验锚点和说人话。

## 包

| 包 | M0 状态 |
|---|---|
| `contracts` `doc-model` `plainspeak` | 浏览器和 Node 共用的文档模型与空话检测 |
| `db` | Drizzle schema。运行时默认是数据目录里的 PGlite，设置 `LW_DATABASE_URL` 才连 Postgres |
| `models` | 档位路由、可重试回退、用量账本接口 |
| `harness` | `HarnessPort` 后面的 pi-durable 1.3.0。一个会话一个 SQLite 对话，不按文档复用 |
| `style` | 默认文风 `plain-professional`，说人话地板不可关 |
| `eval-adapter` | 确定性说人话冒烟，负对照排最后 |
| `edition` | 开源扩展口：授权、提案策略、实时通道 |
| `server` `cli` | HTTP（`/api/v1`，所有者令牌或 API Key）、`lw init` / `lw owner rotate-token` / `lw keys` / `lw serve`，以及演示 |
| `jobs` `prefs` `kb` `sources` `research` `roles` `library` | 空桩，留给后续里程碑 |

M0 的验证记录见 `docs/m0-verification.md`。会话和派生文档见 `docs/m1-sessions.md`。PGlite 上的 pg-boss 和 pgvector 见 `docs/m1-pglite-findings.md`。

## 密钥扫描

仓库根目录的 `.gitleaks.toml` 在默认规则之外增加了裸 `sk-` 密钥规则。提交前钩子在 `.githooks/pre-commit`：

```bash
git config core.hooksPath .githooks
```

本机需要 gitleaks v8.30.1。钩子命令是 `gitleaks git --pre-commit --redact --staged`。CI 只扫工作区，不扫历史，避免已作废、但还留在历史里的密钥把检查打红。
