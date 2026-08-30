import type { AgentStartPayload } from "./types.ts";

export const TASK_MODES = ["auto", "writing", "guided", "polish"] as const;
export const ORCHESTRATION_MODES = ["auto", "fast", "outline_first", "sourced", "strict_research"] as const;
export const ASSURANCE_LEVELS = ["flexible", "standard", "sourced", "strict"] as const;
export const APPROVAL_MODES = ["conditional", "always", "auto"] as const;
export const QUALITY_STATES = ["candidate_draft", "accepted_draft", "verified_deliverable"] as const;

export type TaskMode = (typeof TASK_MODES)[number];
export type OrchestrationMode = (typeof ORCHESTRATION_MODES)[number];
export type AssuranceLevel = (typeof ASSURANCE_LEVELS)[number];
export type ApprovalMode = (typeof APPROVAL_MODES)[number];
export type QualityState = (typeof QUALITY_STATES)[number];
export type DocumentLifecycle = "provisional" | "committed";
export type UserChoice<T> = { value: T; source: "user" | "system_inference" | "platform_default" };

export interface ExecutionControls {
  taskMode: UserChoice<TaskMode>;
  orchestrationMode: UserChoice<OrchestrationMode>;
  assuranceLevel: UserChoice<AssuranceLevel>;
  approvalMode: UserChoice<ApprovalMode>;
}

export interface ExecutionRecommendation {
  taskMode?: TaskMode;
  orchestrationMode?: OrchestrationMode;
  assuranceLevel?: AssuranceLevel;
  approvalMode?: ApprovalMode;
}

export function resolveExecutionControls(
  user: Partial<ExecutionControls>,
  recommendation: ExecutionRecommendation,
): ExecutionControls {
  const choose = <T>(explicit: UserChoice<T> | undefined, suggested: T | undefined, fallback: T): UserChoice<T> => {
    if (explicit?.source === "user") return explicit;
    if (suggested !== undefined) return { value: suggested, source: "system_inference" };
    if (explicit !== undefined) return explicit;
    return { value: fallback, source: "platform_default" };
  };

  return {
    taskMode: choose(user.taskMode, recommendation.taskMode, "auto"),
    orchestrationMode: choose(user.orchestrationMode, recommendation.orchestrationMode, "auto"),
    assuranceLevel: choose(user.assuranceLevel, recommendation.assuranceLevel, "standard"),
    approvalMode: choose(user.approvalMode, recommendation.approvalMode, "conditional"),
  };
}

export function lifecycleForEvent(type: WritingEventType): DocumentLifecycle | null {
  if (type === "writing.document.delta") return "provisional";
  if (type === "writing.document.committed") return "committed";
  return null;
}

export interface WritingOrigin {
  kind: "user" | "model" | "system";
  ref: string;
}

export type DocumentNodeType =
  | "document" | "section" | "paragraph" | "text" | "strong" | "emphasis"
  | "link" | "citation" | "ordered_list" | "unordered_list" | "list_item"
  | "blockquote" | "code_block" | "table" | "table_row" | "table_cell";

export interface DocumentNode {
  block_id?: string;
  type: DocumentNodeType;
  attrs: Record<string, unknown>;
  children: DocumentNode[];
  text?: string;
  destination?: string;
  source_id?: string;
  origin: WritingOrigin;
  content_hash?: string;
}

export interface DocumentVersion {
  schema_version: "lcp/1.0";
  document_id: string;
  version_id: string;
  base_version_id: string | null;
  content_hash: string;
  version_hash: string;
  root: DocumentNode;
}

export interface DocumentRecord {
  document_id: string;
  owner_user_id: string;
  title: string;
  status: string;
  current_version: number;
  current_version_id?: string;
  metadata: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface StoredDocumentVersion {
  document: DocumentVersion;
  sequence: number;
  contract_id: string;
  contract_version: number;
  quality_state: QualityState;
  created_at: string;
}

export type RunStatus =
  | "draft" | "contract_confirmed" | "planning" | "planned" | "awaiting_approval"
  | "running" | "pausing" | "paused" | "replanning" | "failed" | "cancelling"
  | "cancelled" | "completed";

export interface PlanBudget {
  max_cost_usd: number;
  max_duration_ms: number;
  max_concurrency: number;
  max_nodes: number;
  max_items: number;
}

export interface RuntimeRun {
  run_id: string;
  document_id: string;
  contract_id: string;
  contract_hash: string;
  contract_version: number;
  base_version_id?: string;
  status: RunStatus;
  active_plan_id?: string;
  active_plan_version?: number;
  approval_mode: ApprovalMode;
  approval_status?: string;
  budget: PlanBudget;
  permissions: string[];
  last_event_sequence: number;
  last_snapshot_id?: string;
  last_snapshot_version?: number;
}

export type FindingSeverity = "BLOCKER" | "ERROR" | "WARNING";
export type FindingStatus = "open" | "resolved" | "waived";

export interface QualityFinding {
  finding_id: string;
  severity: FindingSeverity;
  category: string;
  code: string;
  message: string;
  validator_id: string;
  validator_status?: "passed" | "failed" | "unavailable" | "skipped";
  explanation?: string;
  fix_scope?: string;
  location?: { block_id?: string; claim_id?: string; source_ref?: string };
  status: FindingStatus;
  waiver_decision_id?: string | null;
}

export interface UserQualitySummary {
  quality_state: QualityState;
  requested_assurance: AssuranceLevel;
  achieved_assurance: AssuranceLevel;
  assurance_satisfied: boolean;
  key_findings: QualityFinding[];
}

export interface AuditQualityReport {
  report: Record<string, unknown> & {
    report_id: string;
    quality_state: QualityState;
    findings: QualityFinding[];
  };
}

export interface WritingArtifactEventPayload {
  artifact_id: string;
  artifact_type: string;
  content_hash: string;
  lifecycle: string;
}

export type WritingEventType =
  | "writing.run.status" | "writing.document.delta" | "writing.document.committed"
  | "writing.node.status" | "writing.artifact.created" | "writing.quality.updated"
  | "writing.ledger.event";

export interface WritingEvent<TPayload = Record<string, unknown>> {
  protocol: "lumin-writing.v2";
  type: WritingEventType;
  run_id: string;
  sequence: number;
  timestamp: string;
  status: string;
  payload: TPayload;
}

export interface Revision {
  operation: "insert" | "replace" | "delete" | "move";
  target_block_id?: string;
  target_hash?: string;
  parent_block_id?: string;
  index?: number;
  node?: DocumentNode;
  reason: string;
  semantic_impact: string;
}

export interface RevisionSet {
  base_version: string;
  revisions: Revision[];
}

export type LegacyAgentStartAdapter = AgentStartPayload;

export const QUALITY_STATE_COPY: Record<QualityState, { label: string; description: string }> = {
  candidate_draft: { label: "候选稿", description: "内容已生成，尚未通过完整验收。" },
  accepted_draft: { label: "已接受稿", description: "已满足当前编辑要求，可继续完善。" },
  verified_deliverable: { label: "已验证成稿", description: "已通过所需验证并形成完整快照。" },
};
