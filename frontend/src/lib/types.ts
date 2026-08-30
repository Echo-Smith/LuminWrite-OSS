/**
 * 共享类型定义 — 贯穿前后端的数据结构
 */

// ─── Agent 消息协议 ──────────────────────────────────────

export type AgentStepName =
  // ── Harness 模式（架构 C）新工具名 ──
  | "search_web"
  | "search_knowledge"
  | "read_source"
  | "write_article"
  | "review_article"
  | "revise_section"
  | "word_count_check"
  | "rewrite_title"
  | "fact_check"
  | "retrieve_context"
  | "generate_outline"
  // ── 旧 pipeline/unified 工具名（兼容历史会话回放）──
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
  | "session.resume"
  // Beta: DAG 工作流消息
  | "workflow.start"
  | "workflow.edit"
  | "workflow.pause"
  | "workflow.resume"
  | "workflow.cancel";

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
  | "memory.dismiss"
  | "agent.compaction"
  | "task_name.updated"
  | "editorial.event"
  // Beta: DAG 工作流消息
  | "workflow.created"
  | "workflow.started"
  | "workflow.completed"
  | "workflow.failed"
  | "workflow.paused"
  | "workflow.resumed"
  | "node.started"
  | "node.stream.delta"
  | "node.stream.reset"
  | "node.reasoning.delta"
  | "node.step_start"
  | "node.step_complete"
  | "node.completed"
  | "node.failed"
  | "node.error"
  | "ping"; // server heartbeat — keeps connection alive through proxies

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
  agent_mode?: "harness" | "pipeline" | "editorial";
  session_id?: string;
  user_materials?: string[];
  /** Task11: server-resolved material identities; the browser never treats previews as authoritative content. */
  material_refs?: Array<{ material_id: string; source_ref: string; title?: string }>;
  word_limit?: number;
  /** 热搜选题原始链接（用于后端抓取事件背景增强写作叙事） */
  topic_url?: string;
  /** 是否启用素材库自动检索（默认 true）。关闭后 LLM 不会获得 search_knowledge 工具 */
  kb_enabled?: boolean;
  /** Task10 治理控制：旧入口仅作兼容适配，正式运行以 WritingContract 为准。 */
  orchestration_mode?: "auto" | "fast" | "outline_first" | "sourced" | "strict_research";
  assurance_level?: "flexible" | "standard" | "sourced" | "strict";
  approval_mode?: "conditional" | "always" | "auto";
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
  points_used?: number;
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
  /** 热搜原始链接（用于写作时抓取事件背景） */
  url?: string;
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
  images?: Array<{ url: string; description?: string }>;
  answer?: string;
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

// ─── 写作流程交付物 (Editorial Artifacts) ────────────────

export type ArtifactType =
  | "topic_card"
  | "research_brief"
  | "source_pack"
  | "fact_claims"
  | "outline"
  | "draft"
  | "review_report"
  | "revised_draft"
  | "memory_context";

export type ArtifactStatus = "draft" | "submitted" | "approved" | "rejected" | "superseded";

export interface WritingArtifact {
  id: string;
  task_id: string;
  type: ArtifactType;
  version: number;
  content: string; // JSON string, parse on demand
  status: ArtifactStatus;
  produced_by: string;
  reviewed_by?: string;
  review_note?: string;
  parent_id?: string;
  token_cost: number;
  created_at: string;
  updated_at: string;
}

// ─── Artifact Content Schemas (for parsing) ──────────────

export interface SourcePackContent {
  count: number;
  results: SearchResult[];
  query: string;
}

export interface ResearchBriefContent {
  topic?: string;
  search_queries?: string[];
  primary_search_query?: string;
  word_limit?: number;
  compressed_context?: string;
  search_plan?: Array<{ query: string; source: string }>;
  intent?: { taskMode: string; confidence: number; source: string };
  user_materials?: string[];
}

export interface DraftContent {
  title: string;
  content: string;
  word_count: number;
  fix_attempts?: number;
  note?: string;
}
