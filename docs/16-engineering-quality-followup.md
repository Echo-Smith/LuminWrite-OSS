# 工程质量缺口补齐记录

日期：2026-08-20
范围：WABench V2 发版分支的既有 Go vet、前端 lint、依赖安全与生产包体积问题。

## 结论

| 项目 | 修复前 | 修复后 |
| --- | --- | --- |
| 默认 Go 校验 | `go test ./...` 被 `internal/agent/tools.go` 重复条件的 vet 错误阻断 | 默认 `go test ./...` 全量通过 |
| 前端 lint | `npm run lint` 因缺少 ESLint 命令直接失败 | ESLint flat config 生效，0 error；44 个既有 warning 可持续治理 |
| npm 安全审计 | 2026-08-20 复现为 16 项：6 high、10 moderate | `npm audit` 为 0 |
| 依赖树 | `tw-shimmer` 要求 Tailwind 4，而项目使用 Tailwind 3，依赖树无效 | 移除未使用的 `tw-shimmer`，依赖树恢复一致 |
| 最大 JS chunk | 约 1,040 KB，超过 Vite 500 KB 默认阈值 | 约 469 KB；React、Radix、Markdown、图标与通用 vendor 独立分包 |
| 构建告警 | 5 个 store 同时动态/静态导入告警，另有大 chunk 告警 | `npm run build` 无告警 |

## 具体处理

1. 将关键词分隔符中的四个重复 ASCII 双引号条件修正为双引号、单引号和中文左右双引号，并增加回归测试。
2. 按 ESLint 当前 flat config 方式安装 `eslint`、`@eslint/js`、`typescript-eslint`、React Hooks/Refresh 插件和浏览器 globals。
3. 保留适合既有项目的渐进式门禁：规则错误阻断，历史未使用变量与 Hook 依赖问题先作为 warning 暴露，不通过关闭全部规则制造“假绿色”。
4. 将 `react-router-dom` 升至修复版本，将 `postcss` 升至安全版本；对 `@assistant-ui/react@0.10` 精确固定的旧版 `nanoid` 使用同主版本安全补丁 override。后续升级 assistant-ui 大版本时应删除并重新审计该 override。
5. 删除源码未使用且 peer dependency 不兼容的 `tw-shimmer`。
6. 使用 Vite 7 的 `build.rollupOptions.output.manualChunks` 做稳定 vendor 分包，并把实际无收益的动态 store import 统一为静态引用。

## 验证命令

```bash
cd backend && go test ./...
cd frontend && npm run lint
cd frontend && npm run test:wabench
cd frontend && npm run build
cd frontend && npm audit
```

安全审计是时间点结果。每次发版仍需重新运行 `npm audit`，不能把本记录视为永久安全证明。
