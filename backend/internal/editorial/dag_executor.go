package editorial

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// ─── DAG 执行器（Beta: 编辑部模式 Phase 2.3）────────────
//
// DAGExecutor 管理 SubAgent 集群的并行/串行执行。
// 按拓扑序执行就绪节点，节点完成后更新下游节点。
// 借鉴 LangGraph 的 StateGraph 执行模式和 Codex 的多 Agent 编排。

// DAGExecutor DAG 执行器
type DAGExecutor struct {
	registry    *DynamicAgentRegistry
	store       *Store
	emitter     EventEmitter
	executors   map[string]AgentExecutorAdapter // agentID → executor
	tokenBudget *DAGTokenBudget

	mu      sync.Mutex
	status  WorkflowStatus
	results map[string]*NodeResult // nodeID → result

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
		results:     make(map[string]*NodeResult),
		status:      WorkflowStatusCreated,
		tokenBudget: NewDAGTokenBudget(0), // 默认无限制
		planCache:   make(map[string]*PlanResult),
	}
}

// RegisterExecutor 注册 Agent 执行器
func (e *DAGExecutor) RegisterExecutor(agentID string, exec AgentExecutorAdapter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.executors[agentID] = exec
	slog.Info("dag executor: executor registered", "agent_id", agentID)
}

// CachePlan 缓存 Planner 输出，并注册生成的 Agent 到 registry。
func (e *DAGExecutor) CachePlan(taskID string, plan *PlanResult) {
	e.mu.Lock()
	defer e.mu.Unlock()
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
	e.mu.Lock()
	defer e.mu.Unlock()
	plan, ok := e.planCache[taskID]
	return plan, ok
}

// GetRegistry 返回 registry 引用
func (e *DAGExecutor) GetRegistry() *DynamicAgentRegistry {
	return e.registry
}

// Execute 遍历 DAG，按拓扑序执行就绪节点
func (e *DAGExecutor) Execute(ctx context.Context, spec *WorkflowSpec, task *Task) error {
	// 校验 DAG
	if err := ValidateDAG(spec, e.registry); err != nil {
		return fmt.Errorf("dag validation: %w", err)
	}

	e.mu.Lock()
	e.status = WorkflowStatusRunning
	e.mu.Unlock()

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

	// 启动 worker goroutine 池
	const maxWorkers = 4
	sem := make(chan struct{}, maxWorkers)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case nodeID, ok := <-readyQueue:
			if !ok {
				// channel 关闭，所有节点已处理
				break
			}
			if hasFailed.Load() {
				// 标记剩余节点为 skipped
				e.mu.Lock()
				if _, exists := e.results[nodeID]; !exists {
					e.results[nodeID] = &NodeResult{
						NodeID: nodeID,
						Status: NodeStatusSkipped,
						Error:  "upstream dependency failed",
					}
				}
				e.mu.Unlock()
				completedCount.Add(1)
				if int(completedCount.Load()) == int(totalNodes) {
					e.finalize(ctx, task, spec, false)
					return nil
				}
				continue
			}

			node := nodeMap[nodeID]

			// 检查是否有依赖失败（加锁防止与 worker goroutine 的写入竞态）
			e.mu.Lock()
			failed := e.hasFailedDependency(node, e.results)
			if failed {
				e.results[nodeID] = &NodeResult{
					NodeID: nodeID,
					Status: NodeStatusSkipped,
					Error:  "upstream dependency failed",
				}
			}
			e.mu.Unlock()

			if failed {
				completedCount.Add(1)
				if int(completedCount.Load()) == int(totalNodes) {
					e.finalize(ctx, task, spec, false)
					return nil
				}
				continue
			}

			wg.Add(1)
			sem <- struct{}{} // 获取信号量

			go func(n NodeSpec) {
				defer wg.Done()
				defer func() { <-sem }() // 释放信号量

			err := e.executeNode(ctx, n, nodeMap, task)

			e.mu.Lock()
			result := &NodeResult{
				NodeID:     n.ID,
				StartedAt:  ptrTime(time.Now()),
			}
			if err != nil {
				result.Status = NodeStatusFailed
				result.Error = err.Error()
				hasFailed.Store(true)
			} else {
				result.Status = NodeStatusCompleted
			}
			result.FinishedAt = ptrTime(time.Now())
			e.results[n.ID] = result
			e.mu.Unlock()

			if err == nil {
				// 更新下游节点的依赖计数（需要加锁防止并发 map 写入）
				e.mu.Lock()
				for _, ds := range downstream[n.ID] {
					remainingDeps[ds]--
					if remainingDeps[ds] == 0 {
						readyQueue <- ds
					}
				}
				e.mu.Unlock()
			}

				completedCount.Add(1)
				if int(completedCount.Load()) == int(totalNodes) {
					if !hasFailed.Load() {
						e.finalize(ctx, task, spec, true)
					} else {
						e.finalize(ctx, task, spec, false)
					}
				}
			}(node)
		}

		// 检查是否全部完成
		if int(completedCount.Load()) >= int(totalNodes) {
			break
		}
	}

	wg.Wait()

	if hasFailed.Load() {
		return fmt.Errorf("DAG execution failed: one or more nodes failed")
	}
	return nil
}

// executeNode 执行单个 DAG 节点
func (e *DAGExecutor) executeNode(ctx context.Context, node NodeSpec, nodeMap map[string]NodeSpec, task *Task) error {
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

	// 加载上游 Artifact
	var upstreamArtifacts []Artifact
	for _, artType := range node.InputArtifacts {
		art, err := e.store.GetLatestApprovedArtifact(ctx, task.ID, artType)
		if err == nil && art != nil {
			upstreamArtifacts = append(upstreamArtifacts, *art)
		}
	}

	// 根据上下文传递模式构建 Agent 上下文
	// 根据 AgentConfig.BaseRole 映射到对应的 AgentRole
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

	// 注入组织知识
	// ac.LocalMemory = orgKnowledge // 可选

	// 注入 AgentConfig 的 Persona 到 AgentContext
	// （通过 LocalMemory 或直接在 Executor 中处理）

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
func (e *DAGExecutor) finalize(ctx context.Context, task *Task, spec *WorkflowSpec, success bool) {
	e.mu.Lock()
	if success {
		e.status = WorkflowStatusCompleted
	} else {
		e.status = WorkflowStatusFailed
	}
	e.mu.Unlock()

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

	// 清理 plan 缓存，防止内存泄漏
	e.mu.Lock()
	delete(e.planCache, task.ID)
	// 清理 results，为下一次执行准备干净状态
	e.results = make(map[string]*NodeResult)
	e.mu.Unlock()
}

// GetStatus 获取工作流状态
func (e *DAGExecutor) GetStatus() WorkflowStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

// GetResults 获取所有节点结果
func (e *DAGExecutor) GetResults() map[string]*NodeResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make(map[string]*NodeResult, len(e.results))
	for k, v := range e.results {
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
