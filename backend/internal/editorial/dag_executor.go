package editorial

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// ─── DAG 执行器（Beta: 编辑部模式 Phase 2.3）────────────
//
// DAGExecutor 管理 SubAgent 集群的并行/串行执行。
// 按拓扑序执行就绪节点，节点完成后更新下游节点。
// 借鉴 LangGraph 的 StateGraph 执行模式和 Codex 的多 Agent 编排。

// taskRun 持有单个 task 的运行时状态，实现 taskID 级别隔离
type taskRun struct {
	mu        sync.Mutex
	status    WorkflowStatus
	results   map[string]*NodeResult // nodeID → result
	finalized atomic.Bool            // 防止 finalize 被多次调用
	cancel    context.CancelFunc     // 用于 cancel 操作
}

func newTaskRun() *taskRun {
	return &taskRun{
		status:  WorkflowStatusCreated,
		results: make(map[string]*NodeResult),
	}
}

// DAGExecutor DAG 执行器
type DAGExecutor struct {
	registry    *DynamicAgentRegistry
	store       *Store
	emitter     EventEmitter
	executors   map[string]AgentExecutorAdapter // agentID → executor
	tokenBudget *DAGTokenBudget

	// 按 taskID 隔离的运行时状态
	runsMu sync.Mutex
	runs   map[string]*taskRun // taskID → run state

	// plan 缓存: taskID → PlanResult
	planCache map[string]*PlanResult
}

// NewDAGExecutor 创建 DAG 执行器
func NewDAGExecutor(registry *DynamicAgentRegistry, store *Store, emitter EventEmitter) *DAGExecutor {
	return &DAGExecutor{
		registry:    registry,
		store:       store,
		emitter:     emitter,
		executors:   make(map[string]AgentExecutorAdapter),
		tokenBudget: NewDAGTokenBudget(0), // 默认无限制
		runs:        make(map[string]*taskRun),
		planCache:   make(map[string]*PlanResult),
	}
}

// getOrCreateRun 获取或创建 task 的运行时状态
func (e *DAGExecutor) getOrCreateRun(taskID string) *taskRun {
	e.runsMu.Lock()
	defer e.runsMu.Unlock()
	run, ok := e.runs[taskID]
	if !ok {
		run = newTaskRun()
		e.runs[taskID] = run
	}
	return run
}

// getRun 获取 task 的运行时状态（不存在返回 nil）
func (e *DAGExecutor) getRun(taskID string) *taskRun {
	e.runsMu.Lock()
	defer e.runsMu.Unlock()
	return e.runs[taskID]
}

// deleteRun 清理 task 的运行时状态
func (e *DAGExecutor) deleteRun(taskID string) {
	e.runsMu.Lock()
	defer e.runsMu.Unlock()
	delete(e.runs, taskID)
}

// RegisterExecutor 注册 Agent 执行器
func (e *DAGExecutor) RegisterExecutor(agentID string, exec AgentExecutorAdapter) {
	e.executors[agentID] = exec
	slog.Info("dag executor: executor registered", "agent_id", agentID)
}

// EmitterHolder 接口用于在 DAG 执行前注入事件发射器。
// Agent 执行器实现此接口后，DAGExecutor 会为每个节点创建 NodeEmitter 并注入。
//
// 设计：使用 emitterHolder mixin 提供 goroutine 安全的 emitter 传递，
// 避免共享执行器实例上的数据竞态。
type EmitterHolder interface {
	SetCurrentEmitter(em engine.EventEmitter)
	ClearCurrentEmitter()
}

// CachePlan 缓存 Planner 输出，并注册生成的 Agent 到 registry。
func (e *DAGExecutor) CachePlan(taskID string, plan *PlanResult) {
	e.runsMu.Lock()
	defer e.runsMu.Unlock()
	e.planCache[taskID] = plan
	// 注册生成的 Agent 到 registry
	configs := make([]*AgentConfig, len(plan.Agents))
	for i := range plan.Agents {
		configs[i] = &plan.Agents[i]
	}
	e.registry.ApplyGeneratedAgents(taskID, configs)
	slog.Info("dag executor: plan cached", "task_id", taskID, "agents", len(plan.Agents))
}

// GetPlan 获取缓存的 Planner 输出
func (e *DAGExecutor) GetPlan(taskID string) (*PlanResult, bool) {
	e.runsMu.Lock()
	defer e.runsMu.Unlock()
	plan, ok := e.planCache[taskID]
	return plan, ok
}

// GetRegistry 返回 registry 引用
func (e *DAGExecutor) GetRegistry() *DynamicAgentRegistry {
	return e.registry
}

// GetTokenBudget 返回 Token 预算管理器（外部读取总用量）
func (e *DAGExecutor) GetTokenBudget() *DAGTokenBudget {
	return e.tokenBudget
}

// Cancel 取消指定 task 的 DAG 执行
func (e *DAGExecutor) Cancel(taskID string) bool {
	e.runsMu.Lock()
	run, ok := e.runs[taskID]
	e.runsMu.Unlock()
	if !ok || run == nil {
		return false
	}
	run.mu.Lock()
	if run.cancel != nil {
		run.cancel()
	}
	run.status = WorkflowStatusFailed
	run.mu.Unlock()
	slog.Info("dag executor: task cancelled", "task_id", taskID)
	return true
}

// Execute 遍历 DAG，按拓扑序执行就绪节点
func (e *DAGExecutor) Execute(ctx context.Context, spec *WorkflowSpec, task *Task) error {
	// 校验 DAG
	if err := ValidateDAG(spec, e.registry); err != nil {
		return fmt.Errorf("dag validation: %w", err)
	}

	// 获取或创建此 task 的隔离运行状态
	run := e.getOrCreateRun(task.ID)

	run.mu.Lock()
	run.status = WorkflowStatusRunning
	run.finalized.Store(false) // 重置 finalize 标志
	run.mu.Unlock()

	// 创建可取消的 context，存储 cancel 函数
	execCtx, cancel := context.WithCancel(ctx)
	run.mu.Lock()
	run.cancel = cancel
	run.mu.Unlock()

	e.emit(OrchestratorEvent{
		Type:    "workflow.started",
		TaskID:  task.ID,
		Payload: map[string]interface{}{"node_count": len(spec.Nodes)},
	})

	// 构建 nodeID → NodeSpec 映射
	nodeMap := make(map[string]NodeSpec)
	for _, node := range spec.Nodes {
		nodeMap[node.ID] = node
	}

	// 跟踪每个节点的剩余依赖数
	remainingDeps := make(map[string]int)
	// 跟踪每个节点的下游节点
	downstream := make(map[string][]string)
	for _, node := range spec.Nodes {
		remainingDeps[node.ID] = len(node.Dependencies)
		for _, dep := range node.Dependencies {
			downstream[dep] = append(downstream[dep], node.ID)
		}
	}

	// 找到所有入口节点（依赖数为 0）
	readyQueue := make(chan string, len(spec.Nodes))
	for _, node := range spec.Nodes {
		if remainingDeps[node.ID] == 0 {
			readyQueue <- node.ID
		}
	}

	// 并行执行控制
	var wg sync.WaitGroup
	completedCount := atomic.Int32{}
	totalNodes := int32(len(spec.Nodes))
	var hasFailed atomic.Bool

	// done channel: 当所有节点完成时，通知主循环退出
	done := make(chan struct{})

	// 启动 worker goroutine 池
	const maxWorkers = 4
	sem := make(chan struct{}, maxWorkers)

	for {
		select {
		case <-execCtx.Done():
			wg.Wait()
			// 清理 cancel 函数
			run.mu.Lock()
			run.cancel = nil
			run.mu.Unlock()
			return execCtx.Err()

		case <-done:
			// 所有节点已完成，等待 goroutine 退出
			wg.Wait()
			// 清理 cancel 函数
			run.mu.Lock()
			run.cancel = nil
			run.mu.Unlock()
			if hasFailed.Load() {
				return fmt.Errorf("DAG execution failed: one or more nodes failed")
			}
			return nil

		case nodeID, ok := <-readyQueue:
			if !ok {
				// channel 关闭，所有节点已处理
				continue
			}
			if hasFailed.Load() {
				// 标记剩余节点为 skipped
				run.mu.Lock()
				if _, exists := run.results[nodeID]; !exists {
					run.results[nodeID] = &NodeResult{
						NodeID: nodeID,
						Status: NodeStatusSkipped,
						Error:  "upstream dependency failed",
					}
				}
				run.mu.Unlock()
				if completedCount.Add(1) == totalNodes {
					e.finalize(execCtx, task, spec, false, run)
					close(done)
				}
				continue
			}

			node := nodeMap[nodeID]

			// 检查是否有依赖失败
			run.mu.Lock()
			failed := e.hasFailedDependency(node, run.results)
			if failed {
				run.results[nodeID] = &NodeResult{
					NodeID: nodeID,
					Status: NodeStatusSkipped,
					Error:  "upstream dependency failed",
				}
			}
			run.mu.Unlock()

			if failed {
				if completedCount.Add(1) == totalNodes {
					e.finalize(execCtx, task, spec, false, run)
					close(done)
				}
				continue
			}

			wg.Add(1)
			sem <- struct{}{} // 获取信号量

			go func(n NodeSpec) {
				defer wg.Done()
				defer func() { <-sem }() // 释放信号量

				err := e.executeNode(execCtx, n, nodeMap, task, run)

				run.mu.Lock()
				result := &NodeResult{
					NodeID:    n.ID,
					StartedAt: ptrTime(time.Now()),
				}
				if err != nil {
					result.Status = NodeStatusFailed
					result.Error = err.Error()
					hasFailed.Store(true)
				} else {
					result.Status = NodeStatusCompleted
				}
				result.FinishedAt = ptrTime(time.Now())
				run.results[n.ID] = result
				run.mu.Unlock()

				if err == nil {
					// 更新下游节点的依赖计数
					run.mu.Lock()
					for _, ds := range downstream[n.ID] {
						remainingDeps[ds]--
						if remainingDeps[ds] == 0 {
							readyQueue <- ds
						}
					}
					run.mu.Unlock()
				}

				if completedCount.Add(1) == totalNodes {
					if !hasFailed.Load() {
						e.finalize(execCtx, task, spec, true, run)
					} else {
						e.finalize(execCtx, task, spec, false, run)
					}
					close(done)
				}
			}(node)
		}
	}
}

// executeNode 执行单个 DAG 节点
func (e *DAGExecutor) executeNode(ctx context.Context, node NodeSpec, nodeMap map[string]NodeSpec, task *Task, run *taskRun) error {
	// 获取 Agent 配置
	agentCfg, ok := e.registry.Get(node.AgentID)
	if !ok {
		return fmt.Errorf("agent config not found: %s", node.AgentID)
	}

	// 获取执行器
	exec, ok := e.executors[node.AgentID]
	if !ok {
		// 尝试按 BaseRole 查找
		baseRole := agentCfg.BaseRole
		if exec2, ok2 := e.executors[baseRole]; ok2 {
			exec = exec2
		} else {
			return fmt.Errorf("executor not registered for agent: %s (base: %s)", node.AgentID, baseRole)
		}
	}

	// ── 注入 NodeEmitter 到执行器，实现流式事件桥接 ──
	nodeEmitter := NewNodeEmitter(task.ID, node.ID, node.AgentID, e.emitter)
	if holder, ok := exec.(EmitterHolder); ok {
		holder.SetCurrentEmitter(nodeEmitter)
		defer holder.ClearCurrentEmitter()
	}

	// 加载上游 Artifact
	var upstreamArtifacts []Artifact
	for _, artType := range node.InputArtifacts {
		art, err := e.store.GetLatestApprovedArtifact(ctx, task.ID, artType)
		if err == nil && art != nil {
			upstreamArtifacts = append(upstreamArtifacts, *art)
		}
	}

	// 根据上下文传递模式构建 Agent 上下文
	agentRole := baseRoleToAgentRole(agentCfg.BaseRole)
	ac := ForkContext(
		node.ContextFork,
		node.ForkNTurns,
		upstreamArtifacts,
		nil, // DAG 模式下暂不传递完整对话历史
		agentRole,
		task.ID,
		task.OwnerID,
	)

	// ── 关键：注入 Planner 生成的 AgentConfig 到 AgentContext ──
	ac.AgentConfig = agentCfg

	// 发射 node.started 事件
	e.emit(OrchestratorEvent{
		Type:    "node.started",
		TaskID:  task.ID,
		Payload: map[string]interface{}{
			"node_id":   node.ID,
			"agent_id":  node.AgentID,
			"agent_name": agentCfg.Name,
			"label":    node.Label,
		},
	})

	slog.Info("dag: node started",
		"task_id", task.ID, "node_id", node.ID,
		"agent_name", agentCfg.Name, "output", node.OutputArtifact)

	// 执行
	start := time.Now()
	artifact, err := exec.Execute(ctx, ac, task)
	durationMs := time.Since(start).Milliseconds()

	if err != nil {
		slog.Error("dag: node failed",
			"task_id", task.ID, "node_id", node.ID, "error", err, "duration_ms", durationMs)

		e.emit(OrchestratorEvent{
			Type:    "node.failed",
			TaskID:  task.ID,
			Payload: map[string]interface{}{
				"node_id":  node.ID,
				"error":    err.Error(),
				"duration_ms": durationMs,
			},
		})
		return err
	}

	// 更新 Token 预算
	if e.tokenBudget != nil && artifact != nil {
		e.tokenBudget.UpdateUsage(node.AgentID, int64(artifact.TokenCost))
	}

	slog.Info("dag: node completed",
		"task_id", task.ID, "node_id", node.ID,
		"artifact_type", node.OutputArtifact, "duration_ms", durationMs)

	e.emit(OrchestratorEvent{
		Type:    "node.completed",
		TaskID:  task.ID,
		Payload: map[string]interface{}{
			"node_id":      node.ID,
			"artifact_id":  artifactID(artifact),
			"artifact_type": node.OutputArtifact,
			"duration_ms":  durationMs,
			"tokens_used":  tokenCost(artifact),
		},
	})

	return nil
}

// hasFailedDependency 检查节点的依赖是否有失败的
func (e *DAGExecutor) hasFailedDependency(node NodeSpec, results map[string]*NodeResult) bool {
	for _, dep := range node.Dependencies {
		if result, ok := results[dep]; ok {
			if result.Status == NodeStatusFailed || result.Status == NodeStatusSkipped {
				return true
			}
		}
	}
	return false
}

// finalize 完成 DAG 执行
func (e *DAGExecutor) finalize(ctx context.Context, task *Task, spec *WorkflowSpec, success bool, run *taskRun) {
	// 防止多个 goroutine 同时完成时重复调用 finalize
	if !run.finalized.CompareAndSwap(false, true) {
		return
	}

	run.mu.Lock()
	if success {
		run.status = WorkflowStatusCompleted
	} else {
		run.status = WorkflowStatusFailed
	}
	run.mu.Unlock()

	// 成功完成时更新 Task 状态为 pending_publish（等待人类发布）
	if success && e.store != nil {
		if err := e.store.UpdateTaskStatus(ctx, task.ID, StatusPendingPublish, AssigneeHuman); err != nil {
			slog.Error("dag: failed to update task status to pending_publish",
				"task_id", task.ID, "error", err)
		} else {
			slog.Info("dag: task status updated to pending_publish", "task_id", task.ID)
		}
	}

	eventType := "workflow.completed"
	if !success {
		eventType = "workflow.failed"
	}

	e.emit(OrchestratorEvent{
		Type:    eventType,
		TaskID:  task.ID,
		Payload: map[string]interface{}{
			"node_count":    len(spec.Nodes),
			"total_tokens":  e.tokenBudget.GetTotalUsed(),
		},
	})

	// 清理生成角色
	if e.registry != nil {
		e.registry.CleanupGeneratedAgents(task.ID)
	}

	// 清理 plan 缓存和运行状态，防止内存泄漏
	e.runsMu.Lock()
	delete(e.planCache, task.ID)
	delete(e.runs, task.ID)
	e.runsMu.Unlock()
}

// GetStatus 获取工作流状态
func (e *DAGExecutor) GetStatus(taskID string) WorkflowStatus {
	run := e.getRun(taskID)
	if run == nil {
		return WorkflowStatusCreated
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.status
}

// GetResults 获取所有节点结果
func (e *DAGExecutor) GetResults(taskID string) map[string]*NodeResult {
	run := e.getRun(taskID)
	if run == nil {
		return map[string]*NodeResult{}
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	result := make(map[string]*NodeResult, len(run.results))
	for k, v := range run.results {
		result[k] = v
	}
	return result
}

// emit 发射编排事件
func (e *DAGExecutor) emit(evt OrchestratorEvent) {
	if e.emitter != nil {
		evt.Timestamp = time.Now()
		e.emitter.Emit(evt)
	}
}

// ─── 辅助函数 ─────────────────────────────────────────────

func ptrTime(t time.Time) *time.Time {
	return &t
}

func artifactID(a *Artifact) string {
	if a == nil {
		return ""
	}
	return a.ID
}

func tokenCost(a *Artifact) int {
	if a == nil {
		return 0
	}
	return a.TokenCost
}

// GenerateNodeID 生成节点 ID
func GenerateNodeID() string {
	return "node-" + uuid.New().String()[:8]
}

// baseRoleToAgentRole 将 AgentConfig.BaseRole 映射到对应的 AgentRole
func baseRoleToAgentRole(baseRole string) AgentRole {
	switch baseRole {
	case "researcher":
		return RoleResearch
	case "writer":
		return RoleWriting
	case "reviewer":
		return RoleReview
	default:
		return RoleResearch // 安全 fallback
	}
}
