/**
 * 共享类型定义 — 贯穿前后端的数据结构
 */

// ─── Agent 消息协议 ──────────────────────────────────────

export type AgentStepName =
  | "intent"
  | "query_plan"
  | "search"
  | "relevance"
  | "outline"
  | "write"
  | "post_review"
  | "auto_fix"
  | "memory_gate"
  | "memory_extract"
  | "chat";

export type AgentStepStatus = "running" | "complete" | "error" | "await_input";

export interface StepRecord {
  step: AgentStepName;
  status: AgentStepStatus;
  startedAt?: string;
  completedAt?: string;
  durationMs?: number;
  result?: unknown;
  error?: string;
}

// ─── WebSocket 消息协议 ──────────────────────────────────

export type WSClientMessageType =
  | "agent.start"
  | "agent.pause"
  | "agent.resume"
  | "agent.cancel"
  | "agent.confirm"
  | "agent.edit"
  | "feedback.submit"
  | "session.resume";

export type WSServerMessageType =
  | "agent.created"
  | "agent.step.start"
  | "agent.step.complete"
  | "agent.stream"
  | "agent.stream.done"
  | "agent.paused"
  | "agent.resumed"
  | "agent.await_input"
  | "agent.completed"
  | "agent.error"
  | "agent.cancelled"
  | "session.resumed"
  | "memory.used";

export interface WSClientMessage {
  type: WSClientMessageType;
  payload: Record<string, unknown>;
}

export interface WSServerMessage {
  type: WSServerMessageType;
  payload: Record<string, unknown>;
}

// ─── 写作请求 ────────────────────────────────────────────

export type WriteMode = "auto" | "writing" | "guided" | "polish";

export interface AgentStartPayload {
  message: string;
  style?: string;
  mode?: WriteMode;
  model?: string;
  session_id?: string;
  user_materials?: string[];
  word_limit?: number;
}

// ─── 写作结果 ────────────────────────────────────────────

export interface ReviewIssue {
  severity: "high" | "medium" | "low";
  type: string;
  message: string;
}

export interface ReviewResult {
  scores: Record<string, number>;
  issues: ReviewIssue[];
  passed: boolean;
}

export interface AgentResult {
  article: string;
  review: ReviewResult;
  token_usage: { total_tokens: number };
}

// ─── Style Profile ───────────────────────────────────────

export interface StyleOption {
  slug: string;
  name: string;
  description: string;
  version: number;
  word_range: [number, number];
  tags: string[];
}

// ─── Topic ───────────────────────────────────────────────

export interface Topic {
  id: string;
  title: string;
  description: string;
  source: "system" | "user" | "hotlist";
  platform: string;
  hot_rank: number;
  fetched_at: string;
}

// ─── 提纲数据 ────────────────────────────────────────────

export interface OutlineItem {
  point: string;
  type: "opening" | "argument" | "conclusion";
}

export interface OutlineData {
  title: string;
  outline: OutlineItem[];
}

// ─── 检索素材 ────────────────────────────────────────────

export interface SearchResult {
  title: string;
  snippet: string;
  url?: string;
  source: string;
  relevance?: "strong" | "medium" | "weak" | "conflict";
}

// ─── 反馈 ────────────────────────────────────────────────

export type FeedbackSegmentType = "title" | "paragraph" | "sentence" | "overall";
export type FeedbackType = "good" | "bad" | "suggestion";

export interface FeedbackSegment {
  segment_type: FeedbackSegmentType;
  segment_index: number;
  segment_text: string;
  rating: number;
  feedback_type: FeedbackType;
  comment: string;
}
