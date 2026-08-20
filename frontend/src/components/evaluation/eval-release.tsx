import { GitPullRequestDraft, RotateCcw, ShieldCheck } from "lucide-react";
import type { WABenchReleaseItem } from "@/lib/admin-types";
import { EvalEmptyState, EvalSectionHeader, GateBadge } from "./eval-states";

function evidenceEntries(value: Record<string, unknown>) {
  return Object.entries(value).slice(0, 12);
}

function displayEvidence(value: unknown): string {
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") return String(value);
  if (value == null) return "—";
  try { return JSON.stringify(value); } catch { return "[unavailable]"; }
}

export function EvalRelease({ items }: { items: WABenchReleaseItem[] }) {
  return (
    <div className="space-y-7">
      <EvalSectionHeader eyebrow="Release evidence / 07" title="发布是一个有证据、有负责人、有回滚条件的决策" description="当前仍为 shadow 门禁。只有完成同批回归和正式切换后，WABench 才能成为生产发布的唯一依据。" />
      {items.length === 0 ? <EvalEmptyState icon={GitPullRequestDraft} title="还没有发布决策" description="空白状态不等于通过。完成运行、评审、Badcase 定位和红队门禁后才能生成决策。" /> : (
        <div className="space-y-8">{items.map((item) => <article key={item.decisionId} className="border-y border-[#161917]/20 dark:border-border"><header className="flex flex-col gap-4 bg-[#ebe9e1]/60 px-4 py-4 dark:bg-muted/20 sm:flex-row sm:items-center sm:justify-between"><div><div className="flex items-center gap-2"><ShieldCheck className="h-4 w-4" /><p className="font-semibold">{item.candidateId}</p></div><p className="mt-1 font-mono text-xs text-muted-foreground">{item.decisionId} · run {item.runId}</p></div><div className="flex items-center gap-3"><div className="text-right text-xs text-muted-foreground"><p>{new Date(item.decidedAt).toLocaleString("zh-CN")}</p><p className="mt-1">负责人 {item.ownerRef}</p></div><GateBadge decision={item.decision} /></div></header><div className="grid gap-6 px-4 py-5 xl:grid-cols-[1fr_0.7fr]"><div><h4 className="text-xs font-bold uppercase tracking-[0.14em] text-muted-foreground">门禁证据</h4><dl className="mt-3 grid grid-cols-[minmax(120px,0.45fr)_1fr] gap-x-4 gap-y-2 text-xs">{evidenceEntries(item.evidence).map(([key, value]) => <div key={key} className="contents"><dt className="truncate text-muted-foreground">{key}</dt><dd className="break-all font-mono">{displayEvidence(value)}</dd></div>)}</dl>{Object.keys(item.evidence).length === 0 && <p className="mt-3 text-sm text-muted-foreground">没有附加证据。</p>}</div><div className="space-y-5"><div><h4 className="text-xs font-bold uppercase tracking-[0.14em] text-muted-foreground">例外</h4><p className="mt-2 text-sm">{item.exceptions.length ? `${item.exceptions.length} 项例外，需要单独审批` : "无例外"}</p></div><div><h4 className="flex items-center gap-1.5 text-xs font-bold uppercase tracking-[0.14em] text-muted-foreground"><RotateCcw className="h-3.5 w-3.5" />回滚条件</h4>{item.rollbackConditions.length ? <ul className="mt-2 space-y-2 text-xs">{item.rollbackConditions.map((condition, index) => <li key={index} className="border-l-2 border-red-700/35 pl-3">{displayEvidence(condition)}</li>)}</ul> : <p className="mt-2 text-sm text-red-800 dark:text-red-300">未固化回滚条件</p>}</div></div></div></article>)}</div>
      )}
    </div>
  );
}
