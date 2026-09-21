/**
 * Workflow REST API — Editorial Transport Migration.
 *
 * 取代已下线的 workflow.* WebSocket 消息（command facade）：
 *   POST   /api/v2/workflows            规划（Planner 生成 DAG + 持久化 Task）
 *   POST   /api/v2/workflows/{id}/execute  启动 DAG 执行
 *   POST   /api/v2/workflows/{id}/cancel   取消执行
 *   GET    /api/v2/workflows/{id}          当前任务状态
 *
 * 执行进度经 SSE（/api/v2/sse/topics 上的 workflow:* / node:* 事件）接收，
 * 见 hooks/use-workflow-sse.ts。刻意没有 pause/resume —— 后端尚无
 * checkpoint/resume 语义，API 面上也不应出现这类假端点。
 */
import type { AgentConfig, PlanResult, WorkflowSpec } from "@/stores/workflow-store";
import { useAuthStore } from "@/stores/auth-store";

const WORKFLOW_PREFIX = "/api/v2/workflows";

interface APIEnvelope<T> { success?: boolean; data?: T }
interface ErrorBody { error?: { code?: string; message?: string }; code?: string; message?: string }

export class WorkflowApiError extends Error {
  code: string;
  status: number;
  constructor(code: string, status: number, message?: string) {
    super(message ?? code);
    this.code = code;
    this.status = status;
  }
}

async function workflowFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = useAuthStore.getState().token;
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(init.headers as Record<string, string> ?? {}),
  };
  const response = await fetch(path, { ...init, headers });
  if (!response.ok) {
    let code = "WORKFLOW_UNAVAILABLE";
    let message: string | undefined;
    try {
      const body = (await response.json()) as ErrorBody;
      const reported = body.error?.code ?? body.code;
      if (reported) code = reported;
      if (body.error?.message) message = body.error.message;
    } catch { /* keep defaults */ }
    throw new WorkflowApiError(code, response.status, message);
  }
  const body = (await response.json()) as APIEnvelope<T> & T;
  return (body.data ?? body) as T;
}

// ─── Request / Response types ───────────────────────────

export interface WorkflowCreateRequest {
  user_input: string;
  title?: string;
  description?: string;
  style_slug?: string;
  tags?: string[];
  kb_enabled?: boolean;
}

/** 与旧 WS workflow.created payload 相同结构的视图。 */
export interface WorkflowCreatedView {
  task_id: string;
  agents: AgentConfig[];
  workflow: WorkflowSpec;
  rationale: string;
}

export interface WorkflowRunView {
  task_id: string;
  status: string;
  node_count?: number;
}

// ─── API calls ──────────────────────────────────────────

/** POST /api/v2/workflows — Planner 生成 DAG 并持久化 Task。 */
export function createWorkflow(req: WorkflowCreateRequest): Promise<WorkflowCreatedView> {
  return workflowFetch<WorkflowCreatedView>(WORKFLOW_PREFIX, {
    method: "POST",
    body: JSON.stringify(req),
  });
}

/** POST /api/v2/workflows/{id}/execute — 启动 DAG 执行（进度走 SSE）。 */
export function executeWorkflow(taskId: string): Promise<WorkflowRunView> {
  return workflowFetch<WorkflowRunView>(`${WORKFLOW_PREFIX}/${encodeURIComponent(taskId)}/execute`, {
    method: "POST",
  });
}

/** POST /api/v2/workflows/{id}/cancel — 取消执行中的 DAG。 */
export function cancelWorkflow(taskId: string): Promise<WorkflowRunView> {
  return workflowFetch<WorkflowRunView>(`${WORKFLOW_PREFIX}/${encodeURIComponent(taskId)}/cancel`, {
    method: "POST",
  });
}

/** GET /api/v2/workflows/{id} — 当前任务状态（editorial task 视图）。 */
export function getWorkflow(taskId: string): Promise<Record<string, unknown>> {
  return workflowFetch<Record<string, unknown>>(`${WORKFLOW_PREFIX}/${encodeURIComponent(taskId)}`);
}

/** 便捷封装：createWorkflow 返回视图 → workflow-store 的 PlanResult。 */
export function createdViewToPlan(view: WorkflowCreatedView): PlanResult {
  return {
    agents: view.agents ?? [],
    workflow: view.workflow ?? ({} as WorkflowSpec),
    rationale: view.rationale ?? "",
  };
}
