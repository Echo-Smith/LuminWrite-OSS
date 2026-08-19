import type {
  WABenchArbitrationStatus,
  WABenchGateDecision,
  WABenchReviewItem,
  WABenchRootCause,
} from "./admin-types";

export const WABENCH_WORKSPACES = [
  { key: "overview", label: "总览", description: "发布判断与核心指标" },
  { key: "datasets", label: "数据集", description: "分区、覆盖与隐私" },
  { key: "candidates", label: "候选", description: "冻结配置与哈希" },
  { key: "runs", label: "运行", description: "进度、失败与 Trace" },
  { key: "reviews", label: "评审", description: "溯源、分歧与仲裁" },
  { key: "badcases", label: "Badcase", description: "根因、Owner 与回归" },
  { key: "release", label: "发布", description: "门禁、例外与回滚" },
] as const;

export type WABenchWorkspace = (typeof WABENCH_WORKSPACES)[number]["key"];

export const ROOT_CAUSE_LABELS: Record<WABenchRootCause, string> = {
  input: "输入",
  retrieval: "检索",
  prompt: "Prompt",
  memory: "Memory",
  tool: "工具",
  model: "模型",
  interaction: "交互",
};

export const ACCEPTANCE_LABELS: Record<WABenchReviewItem["acceptanceLabel"], string> = {
  direct_use: "直接使用",
  light_edit: "少量修改",
  heavy_edit: "大量修改",
  reject: "拒绝",
  unknown: "未知",
};

export const ARBITRATION_LABELS: Record<WABenchArbitrationStatus, string> = {
  not_required: "无需仲裁",
  pending: "待仲裁",
  resolved: "已仲裁",
};

export function formatWABenchScore(value: number | null | undefined): string {
  return value == null || !Number.isFinite(value) ? "—" : value.toFixed(1);
}

export function formatWABenchPercent(value: number | null | undefined): string {
  return value == null || !Number.isFinite(value) ? "—" : `${(value * 100).toFixed(1)}%`;
}

export function formatWABenchLatency(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "—";
  return value >= 1000 ? `${(value / 1000).toFixed(1)} s` : `${Math.round(value)} ms`;
}

export function shortWABenchHash(value: string | undefined): string {
  if (!value) return "未冻结";
  const withoutPrefix = value.replace(/^sha256:/, "");
  return withoutPrefix.length > 14 ? `${withoutPrefix.slice(0, 7)}…${withoutPrefix.slice(-7)}` : withoutPrefix;
}

export function gateDecisionPriority(value: WABenchGateDecision): number {
  return { rollback: 4, fail: 3, conditional: 2, pass: 1, "": 0 }[value];
}

export function privacyLabel(value: "public" | "redacted" | "private"): string {
  return { public: "可公开", redacted: "已脱敏", private: "私有·正文遮罩" }[value];
}

export function reviewsDisagree(reviews: WABenchReviewItem[]): boolean {
  const human = reviews.filter((review) => review.reviewerType === "human" && !review.isArbitration);
  if (human.length < 2) return false;
  const first = human[0];
  return human.slice(1).some((review) =>
    review.taskCompliance !== first.taskCompliance
    || review.sourceFidelity !== first.sourceFidelity
    || review.structureReasoning !== first.structureReasoning
    || review.styleConsistency !== first.styleConsistency
    || review.directUsability !== first.directUsability
    || review.acceptanceLabel !== first.acceptanceLabel
  );
}

export interface NormalizedWABenchError {
  code: string;
  message: string;
  details: Array<{ row: number; column?: string; message: string }>;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function normalizeWABenchError(value: unknown): NormalizedWABenchError {
  if (!isRecord(value)) {
    return { code: "unknown_error", message: "操作失败，请重试", details: [] };
  }
  const error = isRecord(value.error) ? value.error : {};
  const data = isRecord(value.data) ? value.data : {};
  const rawDetails = Array.isArray(data.errors) ? data.errors : [];
  const details = rawDetails.flatMap((item) => {
    if (!isRecord(item) || typeof item.row !== "number" || typeof item.message !== "string") return [];
    return [{
      row: item.row,
      column: typeof item.column === "string" ? item.column : undefined,
      message: item.message,
    }];
  });
  return {
    code: typeof error.code === "string" ? error.code : "unknown_error",
    message: typeof error.message === "string" ? error.message : "操作失败，请重试",
    details,
  };
}
