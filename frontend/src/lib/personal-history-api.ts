/**
 * Personal History API — 写作记录 / 我的反馈（个人中心）
 *
 * 写作记录复用用户态 sessions API（列表 + 详情），反馈走 feedback/mine。
 * 数据归属由后端按 JWT 收敛，前端不传 user_id。
 */
import { useAuthStore } from "@/stores/auth-store";

function authHeaders(): Record<string, string> {
  const token = useAuthStore.getState().token;
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...authHeaders(),
      ...(init?.headers ?? {}),
    },
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok || body?.success === false) {
    const code = body?.error?.code ?? `http_${res.status}`;
    const message = body?.error?.message ?? "请求失败";
    const err = new Error(message) as Error & { code: string };
    err.code = code;
    throw err;
  }
  return body?.data as T;
}

// ─── 写作记录 ──────────────────────────────────────────────

export interface HistorySession {
  trace_id: string;
  status: string;
  current_step?: string;
  user_input?: string;
  mode?: string;
  style_slug?: string;
  article_title?: string;
  custom_title?: string;
  task_name?: string;
  folder_id?: string;
  review_score?: number;
  has_feedback?: boolean;
  duration_ms?: number;
  created_at: string;
  completed_at?: string;
  updated_at?: string;
}

export interface HistoryStepRecord {
  step: string;
  status: string;
  startedAt?: string;
  completedAt?: string;
  durationMs?: number;
  error?: string;
}

export interface HistoryDetail extends HistorySession {
  article?: string | null;
  step_history?: HistoryStepRecord[];
  review?: {
    scores?: Record<string, number>;
    issues?: Array<{ severity: string; type: string; message: string }>;
    passed?: boolean;
  };
  token_usage?: {
    total_tokens?: number;
    prompt_tokens?: number;
    completion_tokens?: number;
  };
  error?: string;
}

export async function listMySessions(page = 1, pageSize = 20, archived?: boolean): Promise<{ sessions: HistorySession[]; total: number }> {
  const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  if (archived !== undefined) params.set("archived", String(archived));
  return request<{ sessions: HistorySession[]; total: number }>(`/api/v2/sessions?${params.toString()}`);
}

export async function getMySessionDetail(traceId: string): Promise<HistoryDetail> {
  return request<HistoryDetail>(`/api/v2/sessions/${encodeURIComponent(traceId)}`);
}

// ─── 我的反馈 ──────────────────────────────────────────────

export interface MyFeedbackRow {
  trace_id: string;
  trace_title: string;
  segment_type: string;
  segment_index?: number | null;
  segment_text: string;
  rating: number;
  feedback_type: string;
  comment: string;
  created_at: string;
}

export async function listMyFeedback(limit = 100): Promise<{ feedback: MyFeedbackRow[]; total: number }> {
  return request<{ feedback: MyFeedbackRow[]; total: number }>(`/api/v2/feedback/mine?limit=${limit}`);
}

// ─── 展示辅助 ──────────────────────────────────────────────

export const HISTORY_STATUS_LABELS: Record<string, string> = {
  completed: "已完成",
  running: "进行中",
  failed: "失败",
  paused: "已暂停",
  cancelled: "已取消",
  stopped: "已停止",
};

export const HISTORY_STEP_LABELS: Record<string, string> = {
  intent: "意图识别",
  query_plan: "检索规划",
  search: "多源检索",
  relevance: "素材过滤",
  outline: "结构规划",
  write: "文章生成",
  post_review: "写后自检",
  auto_fix: "自动修正",
  memory_gate: "记忆检索",
  memory_extract: "记忆提取",
  chat: "对话回复",
  parallel_pre_write: "并行预处理",
};

export const HISTORY_SCORE_LABELS: Record<string, string> = {
  factuality: "事实准确性",
  structure: "结构合规",
  style: "风格符合",
  relevance: "内容相关",
  risk: "内容安全",
  rhetoric: "修辞运用",
  length: "篇幅控制",
  title: "标题质量",
};

export const FEEDBACK_TYPE_LABELS: Record<string, string> = {
  good: "好评",
  bad: "差评",
  suggestion: "建议",
};

export const SEGMENT_TYPE_LABELS: Record<string, string> = {
  title: "标题",
  paragraph: "段落",
  sentence: "句子",
  overall: "整体",
};

export function displayTitle(s: HistorySession): string {
  return s.custom_title || s.article_title || s.task_name || s.user_input?.slice(0, 40) || s.trace_id;
}

export function formatDuration(ms?: number): string {
  if (!ms || ms <= 0) return "—";
  if (ms < 60_000) return `${Math.round(ms / 1000)}s`;
  return `${Math.floor(ms / 60_000)}m${Math.round((ms % 60_000) / 1000)}s`;
}
