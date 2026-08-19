import type { ReactNode } from "react";
import {
  Activity, Archive, Beaker, Bug, ClipboardCheck,
  GitCompareArrows, RefreshCw, Rocket, ShieldCheck,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import type { WABenchCandidateItem, WABenchGateDecision, WABenchRunItem } from "@/lib/admin-types";
import { WABENCH_WORKSPACES, type WABenchWorkspace, shortWABenchHash } from "@/lib/wabench-eval";
import { cn } from "@/lib/utils";
import { GateBadge } from "./eval-states";

const workspaceIcons = {
  overview: Activity,
  datasets: Archive,
  candidates: Beaker,
  runs: GitCompareArrows,
  reviews: ClipboardCheck,
  badcases: Bug,
  release: Rocket,
};

export function EvalCenterShell({
  workspace,
  onWorkspaceChange,
  onRefresh,
  refreshing,
  candidate,
  latestRun,
  gateDecision,
  children,
}: {
  workspace: WABenchWorkspace;
  onWorkspaceChange: (workspace: WABenchWorkspace) => void;
  onRefresh: () => void;
  refreshing: boolean;
  candidate?: WABenchCandidateItem;
  latestRun?: WABenchRunItem;
  gateDecision: WABenchGateDecision;
  children: ReactNode;
}) {
  return (
    <div className="eval-center min-h-full bg-[#f4f3ee] text-[#161917] dark:bg-background dark:text-foreground">
      <div className="border-b border-[#161917]/15 bg-[#f4f3ee] px-4 py-5 dark:border-border dark:bg-background sm:px-6">
        <div className="flex flex-col gap-5 xl:flex-row xl:items-end xl:justify-between">
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <span className="inline-flex min-h-7 items-center gap-1.5 rounded-full border border-[#161917]/20 px-2.5 text-[11px] font-bold uppercase tracking-[0.14em]">
                <ShieldCheck className="h-3.5 w-3.5" aria-hidden="true" />
                WABench v1
              </span>
              <span className="inline-flex min-h-7 items-center rounded-full bg-[#161917] px-2.5 text-[11px] font-bold uppercase tracking-[0.14em] text-[#f4f3ee] dark:bg-foreground dark:text-background">
                Shadow
              </span>
            </div>
            <h2 className="mt-3 text-3xl font-semibold tracking-[-0.045em] sm:text-4xl">Luminbuddy Eval Center</h2>
            <p className="mt-2 max-w-2xl text-sm leading-6 text-[#161917]/65 dark:text-muted-foreground">
              从样本、候选到发布证据的单一决策链。当前页面不会替代生产发布门禁。
            </p>
          </div>
          <Button
            variant="outline"
            onClick={onRefresh}
            disabled={refreshing}
            className="min-h-11 gap-2 border-[#161917]/20 bg-transparent hover:bg-[#161917]/5 dark:border-border"
          >
            <RefreshCw className={cn("h-4 w-4", refreshing && "animate-spin")} aria-hidden="true" />
            刷新证据
          </Button>
        </div>

        <div className="mt-6 grid overflow-hidden border-y border-[#161917]/20 text-sm dark:border-border md:grid-cols-[1.25fr_1fr_0.8fr]">
          <div className="border-b border-[#161917]/15 py-3 md:border-b-0 md:border-r md:px-4 dark:border-border">
            <p className="text-[10px] font-bold uppercase tracking-[0.16em] text-[#161917]/50 dark:text-muted-foreground">冻结候选</p>
            <p className="mt-1 truncate font-semibold">{candidate?.name ?? "尚未创建候选"}</p>
            <p className="mt-0.5 font-mono text-xs text-[#161917]/55 dark:text-muted-foreground">code {shortWABenchHash(candidate?.codeHash)}</p>
          </div>
          <div className="border-b border-[#161917]/15 py-3 md:border-b-0 md:border-r md:px-4 dark:border-border">
            <p className="text-[10px] font-bold uppercase tracking-[0.16em] text-[#161917]/50 dark:text-muted-foreground">最近运行</p>
            <p className="mt-1 truncate font-semibold">{latestRun?.runId ?? "尚无 WABench 运行"}</p>
            <p className="mt-0.5 text-xs text-[#161917]/55 dark:text-muted-foreground">{latestRun ? `${latestRun.environment} · ${latestRun.status}` : "先冻结数据集与候选"}</p>
          </div>
          <div className="flex items-center justify-between gap-3 py-3 md:px-4">
            <div>
              <p className="text-[10px] font-bold uppercase tracking-[0.16em] text-[#161917]/50 dark:text-muted-foreground">发布门禁</p>
              <p className="mt-1 text-xs text-[#161917]/55 dark:text-muted-foreground">最新证据决策</p>
            </div>
            <GateBadge decision={gateDecision} />
          </div>
        </div>
      </div>

      <div className="grid min-h-[680px] lg:grid-cols-[218px_minmax(0,1fr)]">
        <aside className="border-b border-[#161917]/15 bg-[#ebe9e1] dark:border-border dark:bg-muted/20 lg:border-b-0 lg:border-r">
          <nav className="flex gap-1 overflow-x-auto p-2 lg:sticky lg:top-0 lg:flex-col lg:overflow-visible lg:p-3" aria-label="Eval Center 工作区">
            {WABENCH_WORKSPACES.map((item, index) => {
              const Icon = workspaceIcons[item.key];
              const active = workspace === item.key;
              return (
                <button
                  key={item.key}
                  type="button"
                  onClick={() => onWorkspaceChange(item.key)}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "group flex min-h-12 min-w-[128px] items-center gap-3 rounded-md px-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#1e6b52] focus-visible:ring-offset-2 lg:min-w-0",
                    active ? "bg-[#161917] text-[#f4f3ee] dark:bg-foreground dark:text-background" : "text-[#161917]/70 hover:bg-[#161917]/7 dark:text-muted-foreground dark:hover:bg-muted",
                  )}
                >
                  <span className={cn("font-mono text-[10px] tabular-nums", active ? "text-[#f4f3ee]/55" : "text-[#161917]/40 dark:text-muted-foreground")}>{String(index + 1).padStart(2, "0")}</span>
                  <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
                  <span>
                    <span className="block text-sm font-semibold">{item.label}</span>
                    <span className={cn("hidden text-[11px] leading-4 lg:block", active ? "text-[#f4f3ee]/60" : "text-[#161917]/45 dark:text-muted-foreground")}>{item.description}</span>
                  </span>
                </button>
              );
            })}
          </nav>
        </aside>
        <main className="min-w-0 bg-[#f8f7f2] p-4 dark:bg-background sm:p-6 xl:p-8">
          <div className="mx-auto max-w-[1480px]">{children}</div>
        </main>
      </div>
    </div>
  );
}
