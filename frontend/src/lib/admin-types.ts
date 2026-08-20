/**
 * Admin 统一类型定义
 *
 * 所有 Admin 页面共用的接口响应类型集中定义在此
 * 后端统一响应格式: { success: boolean; data?: T; error?: { code: string; message: string } }
 */

// ─── 通用响应类型 ──────────────────────────────────────────

export interface AdminApiResponse<T> {
  success: boolean;
  data?: T;
  error?: { code: string; message: string };
}

// ─── API Key 相关 ──────────────────────────────────────────

export interface APIKey {
  id: string;
  name: string;
  provider: string;
  key_value?: string; // 只在创建/编辑时使用，列表接口返回 mask
  key_mask?: string;
  base_url?: string;
  is_active: boolean;
  category: string;
  created_at?: string;
  updated_at?: string;
}

export interface APIKeyTestResult {
  status: "ok" | "fail";
  error?: string;
  message?: string;
}

// ─── MCP Server 相关 ───────────────────────────────────────

export interface MCPServer {
  id: string;
  name: string;
  transport: "stdio" | "sse";
  command?: string;
  args?: string[];
  env?: string[];
  url?: string;
  is_active: boolean;
  description?: string;
  status?: "connected" | "disconnected" | "error";
  tools_count?: number;
  created_at?: string;
  updated_at?: string;
}

export interface MCPStatus {
  enabled: boolean;
  connected: boolean;
  servers_count?: number;
  tools_count?: number;
}

export interface MCPTool {
  name: string;
  description?: string;
  server?: string;
}

// ─── Model Config 相关 ────────────────────────────────────

export interface ModelConfig {
  id: string;
  provider: string;
  model_name: string;
  display_name: string;
  api_key?: string;
  api_key_mask?: string;
  base_url: string;
  is_active: boolean;
  is_default: boolean;
  max_tokens?: number;
  temperature?: number;
  top_p?: number;
  created_at?: string;
  updated_at?: string;
}

export interface DiscoveredModel {
  id: string;
  name?: string;
}

// ─── Style Management 相关 ────────────────────────────────

export interface StyleVersion {
  version: number;
  config: Record<string, unknown>;
  published: boolean;
  published_at?: string;
  created_at?: string;
}

export interface StyleProfile {
  slug: string;
  name: string;
  description: string;
  current_version: number;
  published: boolean;
  archived: boolean;
  tags?: string[];
  created_at?: string;
  updated_at?: string;
}

// ─── Cron Job 相关 ─────────────────────────────────────────

export interface CronJob {
  id: string;
  name: string;
  schedule: string;
  command: string;
  is_active: boolean;
  last_run?: string;
  next_run?: string;
  last_status?: "success" | "failed" | "running";
  last_error?: string;
  created_at?: string;
}

// ─── Token Usage 相关 ─────────────────────────────────────

export interface TokenUsageStats {
  total_tokens: number;
  total_cost: number;
  by_model: { model: string; tokens: number; cost: number }[];
  by_day: { date: string; tokens: number; cost: number }[];
}

// ─── Trace History 相关 ───────────────────────────────────

export interface TraceRecord {
  id: string;
  user_id: string;
  username?: string;
  style_slug: string;
  intent: string;
  status: string;
  duration_ms: number;
  token_count: number;
  created_at: string;
  summary?: string;
}

// ─── Sensitive Word 相关 ──────────────────────────────────

export interface SensitiveWord {
  id: string;
  word: string;
  category: string;
  level: "low" | "medium" | "high";
  created_at?: string;
}

export interface SensitiveWordConfig {
  enabled: boolean;
  default_level: "low" | "medium" | "high";
  action: "warn" | "block" | "replace";
  replacement?: string;
}

// ─── Pending Style 相关 ───────────────────────────────────

export interface PendingReview {
  id: string;
  user_id: string;
  username?: string;
  style_name: string;
  style_slug: string;
  config: string;
  status: "pending" | "approved" | "rejected";
  note?: string;
  created_at: string;
  reviewed_at?: string;
}

// ─── Evaluation 相关 ───────────────────────────────────────

export interface EvaluationSet {
  id: string;
  name: string;
  description?: string;
  case_count: number;
  created_at?: string;
}

export interface EvaluationRun {
  id: string;
  set_id: string;
  status: "pending" | "running" | "completed" | "failed";
  score?: number;
  created_at?: string;
  completed_at?: string;
}

// ─── WritingAgentBench Eval Center ──────────────────────────────

export type WABenchPartition = "development" | "public_holdout" | "private_holdout" | "red_team" | "live_probe";
export type WABenchGateDecision = "pass" | "fail" | "conditional" | "rollback" | "";
export type WABenchArbitrationStatus = "not_required" | "pending" | "resolved";

export interface WABenchOverview {
  suiteCount: number;
  candidateCount: number;
  runCount: number;
  reviewCount: number;
  runningCount: number;
  failedRunCount: number;
  averageScore: number | null;
  hardFailureRate: number | null;
  acceptanceRate: number | null;
  modificationBurden: number | null;
  p50LatencyMs: number | null;
  p95LatencyMs: number | null;
  sourceBoundaryFailureCount: number;
  latestGateDecision: WABenchGateDecision;
  costStatus: "observed" | "estimated" | "unavailable";
}

export interface WABenchSuiteItem {
  suiteId: string;
  name: string;
  version: string;
  description: string;
  partition: WABenchPartition;
  visibility: "public" | "private";
  status: string;
  caseCount: number;
  coverage: Record<string, unknown>;
  privacy: Record<string, unknown>;
  contentHash?: string;
  taskCounts: Record<string, number>;
  privacyLabel: "public" | "redacted" | "private";
  createdAt: string;
}

export interface WABenchCandidateItem {
  candidateId: string;
  name: string;
  promptHash: string;
  memoryHash?: string;
  modelManifest: Record<string, unknown>;
  codeHash: string;
  toolManifest: Record<string, unknown>;
  featureFlags: Record<string, unknown>;
  createdAt: string;
}

export interface WABenchRunItem {
  runId: string;
  suiteId: string;
  suiteName: string;
  candidateId: string;
  candidateName: string;
  adapterId: string;
  runnerVersion: string;
  environment: string;
  trafficType: "user" | "smoke" | "replay";
  status: string;
  totalCases: number;
  completedCases: number;
  failedCases: number;
  scoredCases: number;
  averageWeightedScore: number | null;
  gateDecision: WABenchGateDecision;
  startedAt?: string;
  completedAt?: string;
  createdAt: string;
}

export interface WABenchReviewItem {
  reviewId: string;
  runId: string;
  outputId: string;
  caseId: string;
  outputStatus: string;
  textStorage: "inline_public" | "private_ref" | "hash_only";
  privacyLevel: "synthetic" | "anonymized" | "private";
  contentAvailable: boolean;
  reviewerId: string;
  reviewerRole: string;
  reviewerType: "human" | "model" | "rule";
  reviewMethod: string;
  labelSource: string;
  isBlind: boolean;
  taskCompliance: number;
  sourceFidelity: number;
  structureReasoning: number;
  styleConsistency: number;
  directUsability: number;
  acceptanceLabel: "direct_use" | "light_edit" | "heavy_edit" | "reject" | "unknown";
  modificationBurden?: number;
  hardFailureIds: string[];
  primaryRootCause?: WABenchRootCause;
  secondaryRootCauses: WABenchRootCause[];
  arbitrationStatus: WABenchArbitrationStatus;
  isArbitration: boolean;
  reviewedAt: string;
}

export type WABenchRootCause = "input" | "retrieval" | "prompt" | "memory" | "tool" | "model" | "interaction";

export interface WABenchBadcaseItem {
  runId: string;
  outputId: string;
  caseId: string;
  outputStatus: string;
  symptoms: Array<Record<string, unknown>>;
  primaryRootCause?: WABenchRootCause;
  hardFailureIds: string[];
  owner?: string;
  fixVersion?: string;
  regressionStatus: string;
  privacyLevel: "synthetic" | "anonymized" | "private";
  traceRef?: string;
  createdAt: string;
}

export interface WABenchReleaseItem {
  decisionId: string;
  runId: string;
  candidateId: string;
  suiteId: string;
  decision: Exclude<WABenchGateDecision, "">;
  evidence: Record<string, unknown>;
  exceptions: Array<Record<string, unknown>>;
  rollbackConditions: Array<Record<string, unknown>>;
  ownerRef: string;
  decidedAt: string;
}

export interface WABenchListResponse<T> {
  items: T[];
  count: number;
}

export interface WABenchWorkbookValidationError {
  row: number;
  column?: string;
  message: string;
}

export interface WABenchReviewImportResult {
  fileName?: string;
  sheetName: string;
  importedRows?: number;
  totalRows?: number;
  validRows?: number;
  errors?: WABenchWorkbookValidationError[];
}

// ─── Feedback 相关 ────────────────────────────────────────

export interface FeedbackAggregation {
  style_slug: string;
  profile_version: number;
  total: number;
  positive: number;
  negative: number;
  suggestions?: string[];
}

// ─── Overview Stats 相关 ──────────────────────────────────

export interface OverviewStats {
  total_users: number;
  total_sessions: number;
  total_traces: number;
  total_tokens: number;
  active_styles: number;
  today_sessions: number;
  today_tokens: number;
}

// Audit Log
export interface AuditLogEntry {
  id: number;
  actor_id: string;
  actor_role: string;
  action: string;
  resource: string;
  resource_id: string;
  detail: string;
  changes: Record<string, unknown>;
  ip_address: string;
  user_agent: string;
  created_at: string;
}
