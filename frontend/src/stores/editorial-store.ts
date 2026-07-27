import { create } from "zustand";
import { useAuthStore } from "@/stores/auth-store";

// ─── 类型定义 ─────────────────────────────────────────────

export type TaskStatus =
  | "draft"
  | "pending_approval"
  | "research"
  | "writing"
  | "review"
  | "pending_publish"
  | "published"
  | "archived";

export type ArtifactType =
  | "topic_card"
  | "research_brief"
  | "source_pack"
  | "fact_claims"
  | "outline"
  | "draft"
  | "review_report"
  | "revised_draft";

export type ArtifactStatus = "draft" | "submitted" | "approved" | "rejected" | "superseded";

export interface EditorialTask {
  id: string;
  title: string;
  description: string;
  owner_id: string;
  assignee_type: string;
  deadline: string | null;
  status: TaskStatus;
  accept_criteria: string;
  allowed_tools: string[];
  token_budget: number;
  token_used: number;
  priority: number;
  tags: string[];
  style_slug: string;
  conversation_id: string;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface Artifact {
  id: string;
  task_id: string;
  type: ArtifactType;
  version: number;
  content: string;
  status: ArtifactStatus;
  produced_by: string;
  reviewed_by: string;
  review_note: string;
  parent_id: string;
  token_cost: number;
  created_at: string;
  updated_at: string;
}

export interface Decision {
  id: string;
  task_id: string;
  type: string;
  decided_by: string;
  decided_by_type: string;
  status: string;
  rationale: string;
  evidence: string;
  artifact_id: string;
  created_at: string;
  decided_at: string | null;
}

export interface EditorialStats {
  total_tasks: number;
  by_status: Record<string, number>;
  total_artifacts: number;
  approval_rate: number;
  avg_rework_rounds: number;
  total_token_used: number;
  total_token_budget: number;
}

interface EditorialEvent {
  type: string;
  task_id: string;
  payload: Record<string, unknown>;
  timestamp: string;
}

// ─── 状态 ─────────────────────────────────────────────────

interface EditorialState {
  tasks: EditorialTask[];
  currentTask: EditorialTask | null;
  artifacts: Artifact[];
  decisions: Decision[];
  stats: EditorialStats | null;
  loading: boolean;
  error: string | null;
  events: EditorialEvent[];

  // Actions
  fetchTasks: (status?: string) => Promise<void>;
  fetchTask: (id: string) => Promise<void>;
  createTask: (input: {
    title: string;
    description: string;
    accept_criteria?: string;
    priority?: number;
    tags?: string[];
    style_slug?: string;
    token_budget?: number;
  }) => Promise<EditorialTask | null>;
  advanceTask: (id: string, targetStatus: TaskStatus, assigneeType?: string) => Promise<boolean>;
  fetchArtifacts: (taskId: string) => Promise<void>;
  submitArtifact: (taskId: string, input: {
    type: ArtifactType;
    content: string;
    produced_by: string;
    token_cost?: number;
  }) => Promise<Artifact | null>;
  reviewArtifact: (artifactId: string, status: ArtifactStatus, reviewNote: string) => Promise<boolean>;
  fetchDecisions: (taskId: string) => Promise<void>;
  createDecision: (taskId: string, input: {
    type: string;
    status: string;
    rationale: string;
    evidence?: string;
    artifact_id?: string;
  }) => Promise<boolean>;
  fetchStats: () => Promise<void>;
  pushEvent: (evt: EditorialEvent) => void;
  clearError: () => void;
}

const API_BASE = "/api/v2/editorial";

function getAuthHeaders(): Record<string, string> {
  const token = useAuthStore.getState().token;
  return {
    "Content-Type": "application/json",
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

export const useEditorialStore = create<EditorialState>((set, get) => ({
  tasks: [],
  currentTask: null,
  artifacts: [],
  decisions: [],
  stats: null,
  loading: false,
  error: null,
  events: [],

  fetchTasks: async (status?: string) => {
    set({ loading: true, error: null });
    try {
      const params = status ? `?status=${status}` : "";
      const res = await fetch(`${API_BASE}/tasks${params}`, { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`Failed to fetch tasks: ${res.statusText}`);
      const tasks = await res.json();
      set({ tasks, loading: false });
    } catch (e) {
      set({ error: (e as Error).message, loading: false });
    }
  },

  fetchTask: async (id: string) => {
    set({ loading: true, error: null });
    try {
      const res = await fetch(`${API_BASE}/tasks/${id}`, { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`Failed to fetch task: ${res.statusText}`);
      const task = await res.json();
      set({ currentTask: task, loading: false });
    } catch (e) {
      set({ error: (e as Error).message, loading: false });
    }
  },

  createTask: async (input) => {
    set({ error: null });
    try {
      const res = await fetch(`${API_BASE}/tasks`, {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify(input),
      });
      if (!res.ok) {
        const err = await res.json();
        throw new Error(err.error || "Failed to create task");
      }
      const task = await res.json();
      set((s) => ({ tasks: [task, ...s.tasks] }));
      return task;
    } catch (e) {
      set({ error: (e as Error).message });
      return null;
    }
  },

  advanceTask: async (id, targetStatus, assigneeType) => {
    set({ error: null });
    try {
      const res = await fetch(`${API_BASE}/tasks/${id}/advance`, {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify({ target_status: targetStatus, assignee_type: assigneeType }),
      });
      if (!res.ok) {
        const err = await res.json();
        throw new Error(err.error || "Failed to advance task");
      }
      // 更新本地状态
      set((s) => ({
        tasks: s.tasks.map((t) =>
          t.id === id ? { ...t, status: targetStatus } : t
        ),
        currentTask: s.currentTask?.id === id
          ? { ...s.currentTask, status: targetStatus }
          : s.currentTask,
      }));
      return true;
    } catch (e) {
      set({ error: (e as Error).message });
      return false;
    }
  },

  fetchArtifacts: async (taskId: string) => {
    try {
      const res = await fetch(`${API_BASE}/tasks/${taskId}/artifacts`, {
        headers: getAuthHeaders(),
      });
      if (!res.ok) throw new Error(`Failed to fetch artifacts: ${res.statusText}`);
      const artifacts = await res.json();
      set({ artifacts });
    } catch (e) {
      set({ error: (e as Error).message });
    }
  },

  submitArtifact: async (taskId, input) => {
    set({ error: null });
    try {
      const res = await fetch(`${API_BASE}/tasks/${taskId}/artifacts`, {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify(input),
      });
      if (!res.ok) throw new Error("Failed to submit artifact");
      const artifact = await res.json();
      set((s) => ({ artifacts: [...s.artifacts, artifact] }));
      return artifact;
    } catch (e) {
      set({ error: (e as Error).message });
      return null;
    }
  },

  reviewArtifact: async (artifactId, status, reviewNote) => {
    set({ error: null });
    try {
      const res = await fetch(`${API_BASE}/artifacts/${artifactId}/review`, {
        method: "PATCH",
        headers: getAuthHeaders(),
        body: JSON.stringify({ status, review_note: reviewNote }),
      });
      if (!res.ok) throw new Error("Failed to review artifact");
      const updated = await res.json();
      set((s) => ({
        artifacts: s.artifacts.map((a) => (a.id === artifactId ? updated : a)),
      }));
      return true;
    } catch (e) {
      set({ error: (e as Error).message });
      return false;
    }
  },

  fetchDecisions: async (taskId: string) => {
    try {
      const res = await fetch(`${API_BASE}/tasks/${taskId}/decisions`, {
        headers: getAuthHeaders(),
      });
      if (!res.ok) throw new Error(`Failed to fetch decisions: ${res.statusText}`);
      const decisions = await res.json();
      set({ decisions });
    } catch (e) {
      set({ error: (e as Error).message });
    }
  },

  createDecision: async (taskId, input) => {
    set({ error: null });
    try {
      const res = await fetch(`${API_BASE}/tasks/${taskId}/decisions`, {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify(input),
      });
      if (!res.ok) throw new Error("Failed to create decision");
      const decision = await res.json();
      set((s) => ({ decisions: [...s.decisions, decision] }));
      return true;
    } catch (e) {
      set({ error: (e as Error).message });
      return false;
    }
  },

  fetchStats: async () => {
    try {
      const res = await fetch(`${API_BASE}/stats`, { headers: getAuthHeaders() });
      if (!res.ok) throw new Error("Failed to fetch stats");
      const stats = await res.json();
      set({ stats });
    } catch (e) {
      set({ error: (e as Error).message });
    }
  },

  pushEvent: (evt) => {
    set((s) => ({ events: [...s.events.slice(-49), evt] }));
  },

  clearError: () => set({ error: null }),
}));
