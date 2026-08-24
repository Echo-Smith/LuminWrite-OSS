/**
 * Workflow Store — Beta: 编辑部模式 DAG 工作流状态管理
 *
 * 管理 Planner 返回的 Agent 集群 + DAG 拓扑，
 * 以及 DAG 执行过程中的节点状态更新。
 * 通过 WebSocket 接收 workflow 和 node 事件。
 */
import { create } from "zustand";
import type { Node, Edge } from "@xyflow/react";

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

  // 获取 React Flow 节点和边
  getFlowNodes: () => Node[];
  getFlowEdges: () => Edge[];

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

  setWorkflowFailed: (error) => {
    set({ runStatus: "failed", errorMessage: error });
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
    });
  },
}));
