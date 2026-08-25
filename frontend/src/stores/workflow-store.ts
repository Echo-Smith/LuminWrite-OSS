/**
 * Workflow Store — Beta: 工作台模式 DAG 工作流状态管理
 *
 * 管理 Planner 返回的 Agent 集群 + DAG 拓扑，
 * 以及 DAG 执行过程中的节点状态更新。
 * 通过 WebSocket 接收 workflow 和 node 事件。
 */
import { create } from "zustand";
import type { Node, Edge, Connection } from "@xyflow/react";

// ─── 类型定义 ─────────────────────────────────────────────

export interface AgentConfig {
  id: string;
  name: string;
  role: string;
  persona?: string;
  allowed_tools?: string[];
  priority?: number;
  can_produce?: string[];
  can_consume?: string[];
  is_generated?: boolean;
  bound_task_id?: string;
  base_role?: string;
  model?: string;
  created_at: string;
}

export interface NodeSpec {
  id: string;
  agent_id: string;
  label: string;
  dependencies: string[];
  input_artifacts: string[];
  output_artifact: string;
  context_fork?: number;
  fork_n_turns?: number;
  position?: { x: number; y: number };
}

export interface WorkflowEdge {
  from: string;
  to: string;
  label?: string;
}

export interface WorkflowSpec {
  task_id: string;
  nodes: NodeSpec[];
  edges: WorkflowEdge[];
  created_by: string;
  source: string;
  created_at: string;
}

export interface PlanResult {
  agents: AgentConfig[];
  workflow: WorkflowSpec;
  rationale: string;
}

export type NodeRunStatus = "pending" | "running" | "completed" | "failed" | "skipped";

export interface NodeRunState {
  node_id: string;
  status: NodeRunStatus;
  artifact_id?: string;
  artifact_type?: string;
  error?: string;
  tokens_used?: number;
  duration_ms?: number;
  stream_text?: string; // 流式输出文本
}

export type WorkflowRunStatus = "idle" | "planning" | "created" | "running" | "completed" | "failed" | "paused";

// ─── Store 定义 ───────────────────────────────────────────

interface WorkflowState {
  // 工作流定义
  plan: PlanResult | null;
  agents: AgentConfig[];
  workflowSpec: WorkflowSpec | null;

  // 执行状态
  runStatus: WorkflowRunStatus;
  nodeStates: Map<string, NodeRunState>;
  totalTokensUsed: number;
  errorMessage: string | null; // 工作流失败时的错误信息

  // 输入
  userInput: string;
  taskId: string;

  // 最终文章（DAG 完成后加载）
  finalArticle: { title: string; content: string; word_count: number } | null;
  finalArticleLoading: boolean;

  // 编辑模式
  isEditMode: boolean;
  selectedNodeId: string | null;

  // Actions
  setPlan: (plan: PlanResult) => void;
  setRunStatus: (status: WorkflowRunStatus) => void;
  setUserInput: (input: string) => void;
  setTaskId: (id: string) => void;

  // 节点状态更新
  setNodeStatus: (nodeId: string, status: NodeRunStatus) => void;
  setNodeStarted: (nodeId: string, agentName: string) => void;
  setNodeCompleted: (nodeId: string, artifactId: string, artifactType: string, tokensUsed: number, durationMs: number) => void;
  setNodeFailed: (nodeId: string, error: string, durationMs: number) => void;
  appendNodeStream: (nodeId: string, delta: string) => void;
  resetNodeStream: (nodeId: string) => void;

  // 工作流完成
  setWorkflowCompleted: (totalTokens: number) => void;
  setWorkflowFailed: (error: string) => void;
  loadFinalArticle: (taskId: string) => Promise<void>;
  loadPlan: (traceId: string) => Promise<boolean>;
  updatePlan: (traceId: string, plan: PlanResult) => Promise<boolean>;
  deletePlan: (traceId: string) => Promise<boolean>;

  // 获取 React Flow 节点和边
  getFlowNodes: () => Node[];
  getFlowEdges: () => Edge[];

  // ── 编辑模式 Actions ──
  setEditMode: (enabled: boolean) => void;
  selectNode: (nodeId: string | null) => void;
  addNode: (role: string, label: string, position?: { x: number; y: number }) => string;
  removeNode: (nodeId: string) => void;
  updateNode: (nodeId: string, patch: Partial<NodeSpec>) => void;
  updateAgent: (agentId: string, patch: Partial<AgentConfig>) => void;
  addEdge: (connection: Connection) => void;
  removeEdge: (edgeId: string) => void;
  syncNodePosition: (nodeId: string, position: { x: number; y: number }) => void;
  buildPlanFromState: () => PlanResult | null;
  savePlan: (traceId: string) => Promise<boolean>;

  // 重置
  reset: () => void;
}

// ─── 辅助函数 ─────────────────────────────────────────────

// 根据 AgentConfig 的 role 返回颜色
function getRoleColor(role: string): string {
  switch (role) {
    case "researcher":
      return "#3b82f6"; // blue
    case "writer":
      return "#10b981"; // green
    case "reviewer":
      return "#f59e0b"; // amber
    default:
      return "#8b5cf6"; // purple
  }
}

// 根据 NodeRunStatus 返回状态颜色
function getStatusColor(status: NodeRunStatus): string {
  switch (status) {
    case "pending":
      return "#94a3b8"; // slate
    case "running":
      return "#3b82f6"; // blue
    case "completed":
      return "#10b981"; // green
    case "failed":
      return "#ef4444"; // red
    case "skipped":
      return "#a3a3a3"; // gray
    default:
      return "#94a3b8";
  }
}

// 自动布局：将节点按拓扑层排列
function autoLayout(nodes: NodeSpec[]): Record<string, { x: number; y: number }> {
  const positions: Record<string, { x: number; y: number }> = {};
  const inDegree: Record<string, number> = {};
  const dependents: Record<string, string[]> = {};

  for (const node of nodes) {
    inDegree[node.id] = node.dependencies.length;
    for (const dep of node.dependencies) {
      if (!dependents[dep]) dependents[dep] = [];
      dependents[dep].push(node.id);
    }
  }

  // 按层排列
  const layers: string[][] = [];
  let current: string[] = nodes.filter((n) => inDegree[n.id] === 0).map((n) => n.id);

  while (current.length > 0) {
    layers.push(current);
    const next: string[] = [];
    for (const id of current) {
      for (const dep of dependents[id] || []) {
        inDegree[dep]--;
        if (inDegree[dep] === 0) next.push(dep);
      }
    }
    current = next;
  }

  const layerWidth = 280;
  const nodeHeight = 120;
  const startX = 50;
  const startY = 50;

  layers.forEach((layer, layerIdx) => {
    layer.forEach((nodeId, nodeIdx) => {
      positions[nodeId] = {
        x: startX + layerIdx * layerWidth,
        y: startY + nodeIdx * nodeHeight + (layer.length > 1 ? (200 - layer.length * nodeHeight) / 2 : 0),
      };
    });
  });

  return positions;
}

// ─── Store 实现 ───────────────────────────────────────────

export const useWorkflowStore = create<WorkflowState>((set, get) => ({
  plan: null,
  agents: [],
  workflowSpec: null,
  runStatus: "idle",
  nodeStates: new Map(),
  totalTokensUsed: 0,
  errorMessage: null,
  userInput: "",
  taskId: "",

  // 最终文章
  finalArticle: null,
  finalArticleLoading: false,

  // 编辑模式
  isEditMode: false,
  selectedNodeId: null,

  setPlan: (plan) => {
    const positions = plan.workflow.nodes.length > 0
      ? autoLayout(plan.workflow.nodes)
      : {};

    // 更新 workflowSpec 中的 position
    const updatedSpec: WorkflowSpec = {
      ...plan.workflow,
      nodes: plan.workflow.nodes.map((n) => ({
        ...n,
        position: n.position || positions[n.id] || { x: 0, y: 0 },
      })),
    };

    set({
      plan,
      agents: plan.agents,
      workflowSpec: updatedSpec,
      runStatus: "created",
      nodeStates: new Map(),
      totalTokensUsed: 0,
      errorMessage: null,
    });
  },

  setRunStatus: (status) => set({ runStatus: status }),
  setUserInput: (input) => set({ userInput: input }),
  setTaskId: (id) => set({ taskId: id }),

  setNodeStatus: (nodeId, status) => {
    const states = new Map(get().nodeStates);
    const existing = states.get(nodeId) || { node_id: nodeId, status: "pending" };
    states.set(nodeId, { ...existing, status });
    set({ nodeStates: states });
  },

  setNodeStarted: (nodeId, _agentName) => {
    const states = new Map(get().nodeStates);
    const existing = states.get(nodeId) || { node_id: nodeId, status: "pending" };
    states.set(nodeId, { ...existing, status: "running" });
    set({ nodeStates: states });
  },

  setNodeCompleted: (nodeId, artifactId, artifactType, tokensUsed, durationMs) => {
    const states = new Map(get().nodeStates);
    const existing = states.get(nodeId) || { node_id: nodeId, status: "running" };
    states.set(nodeId, {
      ...existing,
      status: "completed",
      artifact_id: artifactId,
      artifact_type: artifactType,
      tokens_used: tokensUsed,
      duration_ms: durationMs,
    });
    set({ nodeStates: states, totalTokensUsed: get().totalTokensUsed + tokensUsed });
  },

  setNodeFailed: (nodeId, error, durationMs) => {
    const states = new Map(get().nodeStates);
    const existing = states.get(nodeId) || { node_id: nodeId, status: "running" };
    states.set(nodeId, {
      ...existing,
      status: "failed",
      error,
      duration_ms: durationMs,
    });
    set({ nodeStates: states });
  },

  appendNodeStream: (nodeId, delta) => {
    const states = new Map(get().nodeStates);
    const existing = states.get(nodeId) || { node_id: nodeId, status: "running" };
    states.set(nodeId, {
      ...existing,
      stream_text: (existing.stream_text || "") + delta,
    });
    set({ nodeStates: states });
  },

  resetNodeStream: (nodeId) => {
    const states = new Map(get().nodeStates);
    const existing = states.get(nodeId) || { node_id: nodeId, status: "running" };
    states.set(nodeId, {
      ...existing,
      stream_text: "",
    });
    set({ nodeStates: states });
  },

  setWorkflowCompleted: (totalTokens) => {
    const taskId = get().taskId;
    set({ runStatus: "completed", totalTokensUsed: totalTokens });
    // 自动加载最终文章
    if (taskId) {
      get().loadFinalArticle(taskId);
    }
  },

  loadFinalArticle: async (taskId) => {
    set({ finalArticleLoading: true });
    try {
      const token = localStorage.getItem("auth_token") || "";
      const res = await fetch(`/api/v2/editorial/tasks/${taskId}/artifacts`, {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (!res.ok) throw new Error("Failed to fetch artifacts");
      const json = await res.json();
      const artifacts = json.data?.artifacts ?? json.artifacts ?? [];
      // 优先加载 revised_draft，其次 draft
      const draft = artifacts.find(
        (a: { type: string; status: string }) =>
          a.type === "revised_draft" && a.status === "approved",
      ) || artifacts.find(
        (a: { type: string; status: string }) =>
          a.type === "draft" && a.status === "approved",
      ) || artifacts.find((a: { type: string }) => a.type === "revised_draft") || artifacts.find((a: { type: string }) => a.type === "draft");
      if (draft) {
        try {
          const data = JSON.parse(draft.content);
          set({
            finalArticle: {
              title: data.title || "",
              content: data.content || data.body || "",
              word_count: data.word_count || 0,
            },
            finalArticleLoading: false,
          });
        } catch {
          set({ finalArticle: { title: "", content: draft.content, word_count: 0 }, finalArticleLoading: false });
        }
      } else {
        set({ finalArticleLoading: false });
      }
    } catch {
      set({ finalArticleLoading: false });
    }
  },

  // ── 从后端加载持久化的 plan（恢复历史会话的 DAG 视图）──
  loadPlan: async (traceId) => {
    try {
      const token = localStorage.getItem("auth_token") || "";
      const res = await fetch(`/api/v2/sessions/${traceId}/plan`, {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (!res.ok) return false;
      const json = await res.json();
      if (!json.success || !json.data?.plan) return false;

      const plan = json.data.plan as PlanResult;
      // 使用 setPlan 恢复 plan + agents + workflowSpec + runStatus
      get().setPlan(plan);
      // setPlan 会把 runStatus 设为 "created"，但历史会话应该保持 "completed"
      set({ runStatus: "completed" });
      return true;
    } catch {
      return false;
    }
  },

  // ── 更新 plan（用户编辑 DAG 后保存）──
  updatePlan: async (traceId, plan) => {
    try {
      const token = localStorage.getItem("auth_token") || "";
      const res = await fetch(`/api/v2/sessions/${traceId}/plan`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify(plan),
      });
      if (!res.ok) return false;
      const json = await res.json();
      if (!json.success) return false;
      // 同步更新本地状态
      get().setPlan(plan);
      return true;
    } catch {
      return false;
    }
  },

  // ── 删除 plan ──
  deletePlan: async (traceId) => {
    try {
      const token = localStorage.getItem("auth_token") || "";
      const res = await fetch(`/api/v2/sessions/${traceId}/plan`, {
        method: "DELETE",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (!res.ok) return false;
      // 清除本地 plan 状态
      set({ plan: null, agents: [], workflowSpec: null });
      return true;
    } catch {
      return false;
    }
  },

  setWorkflowFailed: (error) => {
    set({ runStatus: "failed", errorMessage: error });
  },

  // ── 编辑模式实现 ───────────────────────────────────────

  setEditMode: (enabled) => set({ isEditMode: enabled, selectedNodeId: null }),

  selectNode: (nodeId) => set({ selectedNodeId: nodeId }),

  addNode: (role, label, position) => {
    const state = get();
    if (!state.workflowSpec) return "";

    // 生成唯一 ID
    const nodeId = `n${Date.now().toString(36)}`;
    const agentId = `a${Date.now().toString(36)}`;

    // 根据角色确定 output_artifact 和 allowed_tools
    const roleConfig = BUILTIN_ROLE_CONFIGS[role] || BUILTIN_ROLE_CONFIGS["writer"];

    const newAgent: AgentConfig = {
      id: agentId,
      name: label,
      role,
      persona: roleConfig.persona,
      allowed_tools: roleConfig.allowed_tools,
      can_produce: roleConfig.can_produce,
      can_consume: roleConfig.can_consume,
      is_generated: true,
      base_role: role,
      created_at: new Date().toISOString(),
    };

    const newNode: NodeSpec = {
      id: nodeId,
      agent_id: agentId,
      label,
      dependencies: [],
      input_artifacts: [],
      output_artifact: roleConfig.default_output,
      context_fork: 0,
      position: position || { x: 300, y: 200 },
    };

    set({
      agents: [...state.agents, newAgent],
      workflowSpec: {
        ...state.workflowSpec,
        nodes: [...state.workflowSpec.nodes, newNode],
        // edges 不变（新节点无连接）
      },
      // 同步 plan
      plan: state.plan ? {
        ...state.plan,
        agents: [...state.agents, newAgent],
        workflow: {
          ...state.workflowSpec,
          nodes: [...state.workflowSpec.nodes, newNode],
        },
      } : null,
    });

    return nodeId;
  },

  removeNode: (nodeId) => {
    const state = get();
    if (!state.workflowSpec) return;

    const node = state.workflowSpec.nodes.find((n) => n.id === nodeId);
    if (!node) return;

    // 移除节点 + 对应 agent
    const remainingNodes = state.workflowSpec.nodes.filter((n) => n.id !== nodeId);
    const remainingAgents = state.agents.filter((a) => a.id !== node.agent_id);

    // 移除关联的 edges
    const remainingEdges = state.workflowSpec.edges.filter(
      (e) => e.from !== nodeId && e.to !== nodeId,
    );

    // 从其他节点的 dependencies 中移除对被删节点的引用
    const updatedNodes = remainingNodes.map((n) => ({
      ...n,
      dependencies: n.dependencies.filter((d) => d !== nodeId),
    }));

    set({
      agents: remainingAgents,
      workflowSpec: {
        ...state.workflowSpec,
        nodes: updatedNodes,
        edges: remainingEdges,
      },
      selectedNodeId: state.selectedNodeId === nodeId ? null : state.selectedNodeId,
      // 同步 plan
      plan: state.plan ? {
        ...state.plan,
        agents: remainingAgents,
        workflow: {
          ...state.workflowSpec,
          nodes: updatedNodes,
          edges: remainingEdges,
        },
      } : null,
    });
  },

  updateNode: (nodeId, patch) => {
    const state = get();
    if (!state.workflowSpec) return;

    const updatedNodes = state.workflowSpec.nodes.map((n) =>
      n.id === nodeId ? { ...n, ...patch } : n,
    );

    set({
      workflowSpec: { ...state.workflowSpec, nodes: updatedNodes },
      plan: state.plan ? {
        ...state.plan,
        workflow: { ...state.workflowSpec, nodes: updatedNodes },
      } : null,
    });
  },

  updateAgent: (agentId, patch) => {
    const state = get();
    const updatedAgents = state.agents.map((a) =>
      a.id === agentId ? { ...a, ...patch } : a,
    );

    set({
      agents: updatedAgents,
      plan: state.plan ? { ...state.plan, agents: updatedAgents } : null,
    });
  },

  addEdge: (connection) => {
    const state = get();
    if (!state.workflowSpec) return;
    if (!connection.source || !connection.target) return;

    // 检查是否已存在
    const exists = state.workflowSpec.edges.some(
      (e) => e.from === connection.source && e.to === connection.target,
    );
    if (exists) return;

    const newEdge: WorkflowEdge = {
      from: connection.source,
      to: connection.target,
      label: undefined,
    };

    // 同步 dependencies：target 的 dependencies 加入 source
    const updatedNodes = state.workflowSpec.nodes.map((n) => {
      if (n.id === connection.target) {
        if (!n.dependencies.includes(connection.source!)) {
          return { ...n, dependencies: [...n.dependencies, connection.source!] };
        }
      }
      return n;
    });

    set({
      workflowSpec: {
        ...state.workflowSpec,
        nodes: updatedNodes,
        edges: [...state.workflowSpec.edges, newEdge],
      },
      plan: state.plan ? {
        ...state.plan,
        workflow: {
          ...state.workflowSpec,
          nodes: updatedNodes,
          edges: [...state.workflowSpec.edges, newEdge],
        },
      } : null,
    });
  },

  removeEdge: (edgeId) => {
    const state = get();
    if (!state.workflowSpec) return;

    // 从 edgeId 解析 from/to（格式: edge-{idx}-{from}-{to}）
    const edge = state.workflowSpec.edges.find((e, idx) =>
      `edge-${idx}-${e.from}-${e.to}` === edgeId,
    );
    if (!edge) {
      // 也可能是 ReactFlow 自动生成的 id
      // 尝试从当前 flow edges 中查找
      return;
    }

    const remainingEdges = state.workflowSpec.edges.filter((e) => e !== edge);

    // 同步 dependencies：从 target 的 dependencies 中移除 source
    const updatedNodes = state.workflowSpec.nodes.map((n) => {
      if (n.id === edge.to) {
        return { ...n, dependencies: n.dependencies.filter((d) => d !== edge.from) };
      }
      return n;
    });

    set({
      workflowSpec: {
        ...state.workflowSpec,
        nodes: updatedNodes,
        edges: remainingEdges,
      },
      plan: state.plan ? {
        ...state.plan,
        workflow: {
          ...state.workflowSpec,
          nodes: updatedNodes,
          edges: remainingEdges,
        },
      } : null,
    });
  },

  syncNodePosition: (nodeId, position) => {
    const state = get();
    if (!state.workflowSpec) return;

    const updatedNodes = state.workflowSpec.nodes.map((n) =>
      n.id === nodeId ? { ...n, position } : n,
    );

    set({
      workflowSpec: { ...state.workflowSpec, nodes: updatedNodes },
    });
  },

  buildPlanFromState: () => {
    const state = get();
    if (!state.workflowSpec || !state.plan) return null;

    return {
      agents: state.agents,
      workflow: state.workflowSpec,
      rationale: state.plan.rationale,
    };
  },

  savePlan: async (traceId) => {
    const state = get();
    if (!state.workflowSpec || !state.plan) return false;

    // 前端 DAG 校验
    const validation = validateDAG(state.workflowSpec.nodes, state.agents);
    if (!validation.valid) {
      set({ errorMessage: validation.error! });
      return false;
    }

    const plan: PlanResult = {
      agents: state.agents,
      workflow: state.workflowSpec,
      rationale: state.plan.rationale,
    };

    return state.updatePlan(traceId, plan);
  },

  getFlowNodes: () => {
    const { workflowSpec, agents, nodeStates } = get();
    if (!workflowSpec) return [];

    return workflowSpec.nodes.map((node) => {
      const agent = agents.find((a) => a.id === node.agent_id);
      const nodeState = nodeStates.get(node.id);
      const status = nodeState?.status || "pending";
      const roleColor = getRoleColor(agent?.role || "default");
      const statusColor = getStatusColor(status);

      return {
        id: node.id,
        type: "agentNode",
        position: node.position || { x: 0, y: 0 },
        data: {
          label: node.label,
          agentName: agent?.name || "Unknown",
          agentRole: agent?.role || "default",
          outputArtifact: node.output_artifact,
          status,
          streamText: nodeState?.stream_text,
          tokensUsed: nodeState?.tokens_used,
          error: nodeState?.error,
          roleColor,
          statusColor,
        },
      };
    });
  },

  getFlowEdges: () => {
    const { workflowSpec } = get();
    if (!workflowSpec) return [];

    return workflowSpec.edges.map((edge, idx) => ({
      id: `edge-${idx}-${edge.from}-${edge.to}`,
      source: edge.from,
      target: edge.to,
      label: edge.label,
      animated: true,
      style: { stroke: "#94a3b8" },
    }));
  },

  reset: () => {
    set({
      plan: null,
      agents: [],
      workflowSpec: null,
      runStatus: "idle",
      nodeStates: new Map(),
      totalTokensUsed: 0,
      errorMessage: null,
      userInput: "",
      taskId: "",
      finalArticle: null,
      finalArticleLoading: false,
      isEditMode: false,
      selectedNodeId: null,
    });
  },
}));

// ─── 前端 DAG 校验（复刻后端 ValidateDAG 逻辑）──────────

export function validateDAG(
  nodes: NodeSpec[],
  agents: AgentConfig[],
): { valid: boolean; error?: string } {
  if (!nodes || nodes.length === 0) {
    return { valid: false, error: "DAG 不能为空" };
  }

  // 检查重复 ID
  const nodeIDs = new Set<string>();
  for (const node of nodes) {
    if (nodeIDs.has(node.id)) {
      return { valid: false, error: `节点 ID 重复: ${node.id}` };
    }
    nodeIDs.add(node.id);
  }

  // 检查 dependencies 引用合法性 + 入口节点
  let hasEntry = false;
  for (const node of nodes) {
    if (!node.dependencies || node.dependencies.length === 0) {
      hasEntry = true;
    }
    for (const dep of node.dependencies || []) {
      if (!nodeIDs.has(dep)) {
        return { valid: false, error: `节点 ${node.id} 引用了不存在的依赖: ${dep}` };
      }
    }
  }

  if (!hasEntry) {
    return { valid: false, error: "DAG 没有入口节点（无依赖的节点）" };
  }

  // 环检测 — Kahn's algorithm
  const inDegree: Record<string, number> = {};
  const adj: Record<string, string[]> = {};
  for (const node of nodes) {
    inDegree[node.id] = (node.dependencies || []).length;
    for (const dep of node.dependencies || []) {
      if (!adj[dep]) adj[dep] = [];
      adj[dep].push(node.id);
    }
  }

  const queue: string[] = nodes.filter((n) => inDegree[n.id] === 0).map((n) => n.id);
  let processed = 0;
  while (queue.length > 0) {
    const id = queue.shift()!;
    processed++;
    for (const downstream of adj[id] || []) {
      inDegree[downstream]--;
      if (inDegree[downstream] === 0) queue.push(downstream);
    }
  }
  if (processed !== nodes.length) {
    return { valid: false, error: "DAG 存在环（循环依赖）" };
  }

  // 检查出口节点
  const dependentMap = new Set<string>();
  for (const node of nodes) {
    for (const dep of node.dependencies || []) {
      dependentMap.add(dep);
    }
  }
  const hasExit = nodes.some((n) => !dependentMap.has(n.id));
  if (!hasExit) {
    return { valid: false, error: "DAG 没有出口节点（无下游的节点）" };
  }

  // 检查 agent_id 引用合法性
  const agentIDs = new Set(agents.map((a) => a.id));
  for (const node of nodes) {
    if (!agentIDs.has(node.agent_id)) {
      return { valid: false, error: `节点 ${node.id} 引用了不存在的 Agent: ${node.agent_id}` };
    }
  }

  return { valid: true };
}

// ─── 内置角色配置（复刻后端 BuiltinRoles）──────────────────

export const BUILTIN_ROLE_CONFIGS: Record<string, {
  allowed_tools: string[];
  can_produce: string[];
  can_consume: string[];
  default_output: string;
  persona: string;
}> = {
  researcher: {
    allowed_tools: ["search", "factcheck"],
    can_produce: ["research_brief", "source_pack", "fact_claims"],
    can_consume: ["topic_card"],
    default_output: "research_brief",
    persona: "你是一位严谨的研究员，擅长多源检索、信源分级、事实声明与证据绑定。",
  },
  writer: {
    allowed_tools: ["write"],
    can_produce: ["outline", "draft", "revised_draft"],
    can_consume: ["research_brief", "fact_claims", "review_report", "topic_card"],
    default_output: "draft",
    persona: "你是一位资深撰稿人，基于已批准研究包写作，按风格 Profile 生成提纲和初稿。",
  },
  reviewer: {
    allowed_tools: ["factcheck", "style_review"],
    can_produce: ["review_report"],
    can_consume: ["source_pack", "fact_claims", "draft", "revised_draft", "research_brief"],
    default_output: "review_report",
    persona: "你是一位严格的编辑，使用独立上下文审查事实、风格、风险。",
  },
};

// ─── 可用 Artifact 类型清单 ────────────────────────────────

export const ARTIFACT_TYPES = [
  "topic_card",
  "research_brief",
  "source_pack",
  "fact_claims",
  "outline",
  "draft",
  "revised_draft",
  "review_report",
] as const;
