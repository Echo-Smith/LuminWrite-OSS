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
  | "chat"
  | "parallel_pre_write";

export type AgentStepStatus = "running" | "complete" | "error" | "await_input" | "degraded";

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
  | "agent.stream.reset"
  | "agent.reasoning"
  | "agent.stream.done"
  | "agent.article_title"
  | "agent.paused"
  | "agent.resumed"
  | "agent.await_input"
  | "agent.completed"
  | "agent.error"
  | "agent.cancelled"
  | "session.resumed"
  | "memory.used"
  | "editorial.event";

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
  agent_mode?: "pipeline" | "unified";
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
  /** 标题独立评审给出的建议标题（供自动修正使用） */
  title_suggestion?: string;
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
  /** 选题来源："user"=用户自定义；"tencent"/"weibo"/"baidu"/"bilibili"等=各平台热搜；"hotlist"=聚合热搜；"system"=系统 */
  source: string;
  platform: string;
  hot_rank: number;
  fetched_at: string;
  favorited_at?: string;
  recommendation_reason?: string;
}

export interface WritingAngle {
  angle: string;
  style: string;
  word_count: number;
  rationale: string;
}

export interface RelatedArticle {
  trace_id: string;
  user_id: string;
  style_slug: string;
  mode: string;
  status: string;
  article_title: string;
  article_preview: string;
  created_at: string;
  completed_at: string;
}

export interface TopicDetail {
  topic: Topic;
  writing_angles: WritingAngle[];
  related_articles: RelatedArticle[];
  favorited: boolean;
}

export interface PlatformStat {
  platform: string;
  count: number;
}

export interface TrendPoint {
  timestamp: string;
  hot_rank: number | null;
  platform: string;
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
