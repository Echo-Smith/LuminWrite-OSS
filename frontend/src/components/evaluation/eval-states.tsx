import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import { AlertTriangle, DatabaseZap, EyeOff, Loader2, LockKeyhole, RotateCcw, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { WABenchGateDecision } from "@/lib/admin-types";

const gateStyles: Record<WABenchGateDecision, string> = {
  pass: "border-emerald-700/25 bg-emerald-700/10 text-emerald-800 dark:text-emerald-300",
  fail: "border-red-700/25 bg-red-700/10 text-red-800 dark:text-red-300",
  conditional: "border-amber-700/25 bg-amber-700/10 text-amber-800 dark:text-amber-300",
  rollback: "border-red-900/25 bg-red-900/10 text-red-900 dark:text-red-200",
  "": "border-border bg-muted/45 text-muted-foreground",
};

const gateLabels: Record<WABenchGateDecision, string> = {
  pass: "通过",
  fail: "阻断",
  conditional: "有条件通过",
  rollback: "回滚",
  "": "未决策",
};

export function GateBadge({ decision, className }: { decision: WABenchGateDecision; className?: string }) {
  return (
    <span className={cn("inline-flex min-h-7 items-center rounded-full border px-2.5 text-xs font-semibold", gateStyles[decision], className)}>
      {gateLabels[decision]}
    </span>
  );
}

export function EvalStatus({ value }: { value: string }) {
  const style = value === "completed" || value === "active" || value === "pass"
    ? "bg-emerald-700/10 text-emerald-800 dark:text-emerald-300"
    : value === "failed" || value.includes("failed") || value === "fail"
      ? "bg-red-700/10 text-red-800 dark:text-red-300"
      : value === "running" || value === "pending"
        ? "bg-amber-700/10 text-amber-800 dark:text-amber-300"
        : "bg-muted text-muted-foreground";
  return <span className={cn("inline-flex min-h-6 items-center rounded px-2 text-xs font-medium", style)}>{value || "unknown"}</span>;
}

export function PrivacyBadge({ value }: { value: "public" | "redacted" | "private" }) {
  const Icon = value === "public" ? ShieldCheck : value === "redacted" ? EyeOff : LockKeyhole;
  const label = value === "public" ? "可公开" : value === "redacted" ? "已脱敏" : "私有·正文遮罩";
  return (
    <span className="inline-flex min-h-7 items-center gap-1.5 rounded-full border border-border bg-background px-2.5 text-xs text-muted-foreground">
      <Icon className="h-3.5 w-3.5" aria-hidden="true" />
      {label}
    </span>
  );
}

export function EvalMetric({ label, value, note, tone = "default" }: {
  label: string;
  value: string;
  note?: string;
  tone?: "default" | "good" | "warn" | "danger";
}) {
  const toneClass = {
    default: "text-foreground",
    good: "text-emerald-800 dark:text-emerald-300",
    warn: "text-amber-800 dark:text-amber-300",
    danger: "text-red-800 dark:text-red-300",
  }[tone];
  return (
    <div className="min-w-0 border-t border-border/70 pt-3">
      <p className="text-[11px] font-semibold uppercase tracking-[0.13em] text-muted-foreground">{label}</p>
      <p className={cn("mt-1 font-mono text-2xl font-semibold tabular-nums", toneClass)}>{value}</p>
      {note && <p className="mt-1 text-xs leading-5 text-muted-foreground">{note}</p>}
    </div>
  );
}

export function EvalSectionHeader({ eyebrow, title, description, action }: {
  eyebrow: string;
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-4 border-b border-border/70 pb-5 sm:flex-row sm:items-end sm:justify-between">
      <div className="max-w-3xl">
        <p className="text-[11px] font-bold uppercase tracking-[0.18em] text-muted-foreground">{eyebrow}</p>
        <h3 className="mt-2 text-2xl font-semibold tracking-[-0.03em]">{title}</h3>
        <p className="mt-2 max-w-2xl text-sm leading-6 text-muted-foreground">{description}</p>
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  );
}

export function EvalEmptyState({ icon: Icon = DatabaseZap, title, description, action }: {
  icon?: LucideIcon;
  title: string;
  description: string;
  action?: { label: string; onClick: () => void };
}) {
  return (
    <div className="border-y border-dashed border-border py-14 text-center">
      <Icon className="mx-auto h-8 w-8 text-muted-foreground/55" aria-hidden="true" />
      <h4 className="mt-4 text-sm font-semibold">{title}</h4>
      <p className="mx-auto mt-2 max-w-lg text-sm leading-6 text-muted-foreground">{description}</p>
      {action && <Button variant="outline" size="sm" className="mt-5 min-h-11" onClick={action.onClick}>{action.label}</Button>}
    </div>
  );
}

export function EvalLoadingState({ label = "正在读取 WABench 数据…" }: { label?: string }) {
  return (
    <div className="flex min-h-64 items-center justify-center gap-3 text-sm text-muted-foreground" role="status">
      <Loader2 className="h-5 w-5 animate-spin" aria-hidden="true" />
      {label}
    </div>
  );
}

export function EvalErrorState({ message, onRetry, permissionDenied = false }: {
  message: string;
  onRetry?: () => void;
  permissionDenied?: boolean;
}) {
  const Icon = permissionDenied ? LockKeyhole : AlertTriangle;
  return (
    <div className="border-y border-red-700/20 bg-red-700/[0.035] px-6 py-12 text-center">
      <Icon className="mx-auto h-8 w-8 text-red-800 dark:text-red-300" aria-hidden="true" />
      <h4 className="mt-4 text-sm font-semibold">{permissionDenied ? "当前角色无权查看评测中心" : "评测数据加载失败"}</h4>
      <p className="mx-auto mt-2 max-w-xl text-sm leading-6 text-muted-foreground">{message}</p>
      {onRetry && !permissionDenied && (
        <Button variant="outline" size="sm" className="mt-5 min-h-11 gap-2" onClick={onRetry}>
          <RotateCcw className="h-4 w-4" aria-hidden="true" />
          重新加载
        </Button>
      )}
    </div>
  );
}
