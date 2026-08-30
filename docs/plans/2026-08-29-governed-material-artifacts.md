# Governed Material Artifacts Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 把材料、来源和 Executor 结果纳入单一治理 Artifact 链路，同时预埋但不放量 B2 旧算子适配契约。

**Architecture:** MaterialAdapter 在 Run 边界生成不可变初始 Artifact；ExecutorAdapter 只接收版本化请求并返回 provisional ExecutionResult；Orchestrator 通过 writingstore 事务统一提交。Task11 默认注册表对 Harness、Editorial DAG 和 Engine 旧算子保持 fail-closed。

**Tech Stack:** Go 1.25、PostgreSQL/writingstore、React/TypeScript、Node test runner。

---

### Task 1: MaterialAdapter domain

**Files:**
- Create: `backend/internal/writingruntime/material_adapter.go`
- Create: `backend/internal/writingruntime/material_adapter_test.go`

1. 写租户隔离、快照哈希、篡改、来源和冲突失败测试。
2. 运行 `go test ./internal/writingruntime -run Material -count=1`，确认失败。
3. 实现最小 MaterialAdapter 和 typed payload。
4. 重跑测试，确认通过。

### Task 2: B2 adapter contract

**Files:**
- Modify: `backend/internal/writingruntime/executor.go`
- Modify: `backend/internal/writingruntime/executor_adapters.go`
- Modify: `backend/internal/writingruntime/executor_adapters_test.go`

1. 写身份、权限、错误码、权威写入和三类 adapter conformance 失败测试。
2. 实现 RuntimeError、ExecutorAdapter、AdapterPolicy 和统一验证。
3. 确认三个 fake legacy family 通过同一测试，生产仍不可注册。

### Task 3: Orchestrator authority

**Files:**
- Modify: `backend/internal/writingruntime/orchestrator.go`
- Modify: `backend/internal/writingruntime/orchestrator_test.go`
- Modify: `backend/internal/server/writing_api.go`
- Modify: `backend/internal/server/writing_routes_test.go`

1. 写 contract + materials 初始输入与稳定错误投影测试。
2. 实现组合 InitialArtifactProvider，并允许计划验证受持久化材料引用支持。
3. 验证失败节点不产生 Artifact，成功节点只经 CompleteNodeAttempt 提交。

### Task 4: Frontend governance projection

**Files:**
- Modify: `frontend/src/lib/material-api.ts`
- Modify: `frontend/src/components/topic/materials-tab.tsx`
- Create: `frontend/tests/material-governance-ui.test.ts`

1. 写治理状态和 material_refs 行为测试。
2. 增加可选 governance 字段与兼容显示。
3. 删除该入口复制材料预览作为权威输入的行为。

### Task 5: Verification and synchronization

1. 运行 `go test ./internal/writingruntime ./internal/writingplan ./internal/writingstore ./internal/server -count=1`。
2. 运行 `npm test && npm run lint && npm run build`。
3. 更新任务状态和索引（若存在）。
4. 提交 OSS，cherry-pick 到商业版，再运行相同验证并比较共同文件树。
