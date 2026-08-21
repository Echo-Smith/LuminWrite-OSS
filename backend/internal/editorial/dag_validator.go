package editorial

import "errors"

// ─── DAG 校验器（Beta: 编辑部模式 Phase 2.8）────────────
//
// 环检测 + 合法性校验，确保 Planner 生成的 DAG 无环、结构合法。

var (
	ErrDAGHasCycle        = errors.New("DAG has cycle")
	ErrDAGEmpty           = errors.New("DAG has no nodes")
	ErrDAGNoEntryPoint    = errors.New("DAG has no entry point (node with no dependencies)")
	ErrDAGNoExitPoint     = errors.New("DAG has no exit point (node with no dependents)")
	ErrDAGInvalidNodeID   = errors.New("DAG node ID references non-existent node")
	ErrDAGInvalidAgentID = errors.New("DAG agent_id references non-existent agent")
	ErrDAGDuplicateNode  = errors.New("DAG has duplicate node ID")
)

// ValidateDAG 校验 DAG 的合法性：
// 1. 非空
// 2. 无环
// 3. 有至少一个入口节点（无依赖）
// 4. 有至少一个出口节点（无下游）
// 5. 所有 dependencies 引用的 node ID 存在
// 6. 所有 agent_id 引用的 Agent 存在（如果提供 registry）
// 7. 无重复节点 ID
func ValidateDAG(spec *WorkflowSpec, registry *DynamicAgentRegistry) error {
	if spec == nil || len(spec.Nodes) == 0 {
		return ErrDAGEmpty
	}

	// 检查重复 ID + 构建 ID 集合
	nodeIDs := make(map[string]bool)
	for _, node := range spec.Nodes {
		if nodeIDs[node.ID] {
			return ErrDAGDuplicateNode
		}
		nodeIDs[node.ID] = true
	}

	// 检查 dependencies 引用合法性
	hasEntry := false
	for _, node := range spec.Nodes {
		if len(node.Dependencies) == 0 {
			hasEntry = true
		}
		for _, dep := range node.Dependencies {
			if !nodeIDs[dep] {
				return ErrDAGInvalidNodeID
			}
		}
	}

	// 环检测 — Kahn's algorithm (topological sort)
	// 放在入口/出口检查之前，因为有环的 DAG 可能同时没有入口点
	if err := detectCycle(spec.Nodes); err != nil {
		return err
	}

	if !hasEntry {
		return ErrDAGNoEntryPoint
	}

	// 检查出口节点
	hasExit := false
	dependentMap := make(map[string]bool) // 哪些节点被其他节点依赖
	for _, node := range spec.Nodes {
		for _, dep := range node.Dependencies {
			dependentMap[dep] = true
		}
	}
	for _, node := range spec.Nodes {
		if !dependentMap[node.ID] {
			hasExit = true
			break
		}
	}
	if !hasExit {
		return ErrDAGNoExitPoint
	}

	// 检查 agent_id 引用合法性（如果提供 registry）
	if registry != nil {
		for _, node := range spec.Nodes {
			if _, ok := registry.Get(node.AgentID); !ok {
				return ErrDAGInvalidAgentID
			}
		}
	}

	return nil
}

// detectCycle 使用 Kahn's algorithm 检测环
func detectCycle(nodes []NodeSpec) error {
	// 构建邻接表和入度表
	adj := make(map[string][]string)
	inDegree := make(map[string]int)

	for _, node := range nodes {
		inDegree[node.ID] = len(node.Dependencies)
		for _, dep := range node.Dependencies {
			adj[dep] = append(adj[dep], node.ID)
		}
	}

	// 找入度为 0 的节点
	queue := make([]string, 0)
	for _, node := range nodes {
		if inDegree[node.ID] == 0 {
			queue = append(queue, node.ID)
		}
	}

	processed := 0
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		processed++

		for _, downstream := range adj[nodeID] {
			inDegree[downstream]--
			if inDegree[downstream] == 0 {
				queue = append(queue, downstream)
			}
		}
	}

	if processed != len(nodes) {
		return ErrDAGHasCycle
	}
	return nil
}

// TopologicalSort 返回拓扑排序后的节点列表。
// 如果有环，返回错误。
func TopologicalSort(nodes []NodeSpec) ([]NodeSpec, error) {
	if err := detectCycle(nodes); err != nil {
		return nil, err
	}

	// Kahn's algorithm
	adj := make(map[string][]string)
	inDegree := make(map[string]int)
	nodeMap := make(map[string]NodeSpec)

	for _, node := range nodes {
		nodeMap[node.ID] = node
		inDegree[node.ID] = len(node.Dependencies)
		for _, dep := range node.Dependencies {
			adj[dep] = append(adj[dep], node.ID)
		}
	}

	queue := make([]string, 0)
	for _, node := range nodes {
		if inDegree[node.ID] == 0 {
			queue = append(queue, node.ID)
		}
	}

	result := make([]NodeSpec, 0, len(nodes))
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		result = append(result, nodeMap[nodeID])

		for _, downstream := range adj[nodeID] {
			inDegree[downstream]--
			if inDegree[downstream] == 0 {
				queue = append(queue, downstream)
			}
		}
	}

	return result, nil
}
