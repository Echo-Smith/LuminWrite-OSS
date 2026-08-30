# Governed Adapter Rollout Implementation Plan

> Task12：在 Task11 单一事实源与 B2 契约之上接通三类旧写作能力，默认 shadow，生产放量另行授权。

## 交付 1：规格与策略模型

**新增** `backend/internal/writingruntime/rollout.go`、`rollout_test.go`

实现 `RolloutMode`、`AdapterRolloutPolicy`、binding/hash 校验、deterministic subject bucket、kill switch 和 `RouteDecision`。测试无效配置、过期策略、绑定错误、allowlist、边界百分比、稳定决策与默认 shadow。

## 交付 2：Task11 telemetry 与审计证据

**新增** `backend/internal/writingruntime/telemetry.go`、`telemetry_test.go`

定义低基数 observation 与高基数 evidence。identity 只进入 evidence；metric dimensions 只允许 family/executor/capability/mode/lane/status/error_code。MaterialAdapter 和 Orchestrator 在既有边界上发出观察，Attempt/RunLedger 仍为 canonical audit。

## 交付 3：Shadow executor

**新增** `backend/internal/writingruntime/rollout_executor.go`、`rollout_executor_test.go`

以相同 request 运行 baseline/candidate；shadow 结果隔离且不可提交。比较 manifest、hash、source refs、usage、duration、error code。evidence 失败时 baseline 可继续，但 candidate-authoritative 路由 fail-closed。

## 交付 4：三类适配器

**修改** `backend/internal/writingruntime/executor_adapters.go`

Engine Step 和 Editorial Role 使用现有纯 runner；新增无 Store 的 Harness Core runner。三者由相同 factory 构造 enabled policy adapter，只有 RolloutExecutor 可以承接；原 Harness.Run 与 Editorial DAG factory 继续拒绝。

## 交付 5：纵向场景与质量门

**新增** `specs/lcp/v1/fixtures/scenarios/*.json`、`backend/internal/writingruntime/integration_test.go`

用真实 contract/plan/artifact/orchestrator/quality gate 组合，外部 LLM 用确定性 runner 替代。验证长文、多材料和忠实改写的 artifacts、lineage、BLOCKER 与质量晋升。

## 交付 6：故障矩阵与回归

覆盖取消、超时、重复、候选失败、审计失败、策略切换、材料哈希失败、validator fallback 和 canonical commit 失败。运行：

```bash
docker run --rm -e GOFLAGS=-mod=readonly \
  -v /tmp/luminbuddy-go-mod-cache:/go/pkg/mod \
  -v /tmp/luminbuddy-go-build-cache:/root/.cache/go-build \
  -v <repo>/backend:/src -w /src golang:1.25 go test ./...

npm test -- --run
npm run lint
npm run build
```

## 交付 7：双版本与就绪报告

同步共同文件到商业版，分别验证并提交。新增 `docs/releases/2026-08-29-governed-runtime-readiness.md`，分别给出 local shadow-ready、allowlist-ready、production-ready 结论和未满足条件。不 push、不部署、不放量。

## 提交边界

1. `docs: define governed adapter rollout`
2. `feat: add governed adapter shadow rollout`
3. `test: verify governed writing scenarios`
4. `docs: record task12 readiness`
