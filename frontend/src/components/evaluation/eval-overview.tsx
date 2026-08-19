import { AlertTriangle, ArrowRight, CircleCheck, Gauge, ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { WABenchOverview, WABenchReleaseItem, WABenchRunItem } from "@/lib/admin-types";
import { formatWABenchLatency, formatWABenchPercent, formatWABenchScore, type WABenchWorkspace } from "@/lib/wabench-eval";
import { EvalEmptyState, EvalMetric, EvalSectionHeader, EvalStatus, GateBadge } from "./eval-states";

export function EvalOverview({ overview, runs, releases, onNavigate }: {
  overview: WABenchOverview;
  runs: WABenchRunItem[];
  releases: WABenchReleaseItem[];
  onNavigate: (workspace: WABenchWorkspace) => void;
}) {
  const latestRun = runs[0];
  const latestRelease = releases[0];
  const hasBlockingSignal = overview.latestGateDecision === "fail" || overview.latestGateDecision === "rollback" || overview.failedRunCount > 0;
  return (
    <div className="space-y-8">
      <EvalSectionHeader
        eyebrow="Decision desk / 01"
        title="先回答：这个版本能发吗？"
        description="只展示 WABench v1 可比较证据。历史 0—1 分和旧风格分组不进入这里的质量均分。"
      />

      <section className="grid gap-0 border-y border-[#161917]/20 dark:border-border xl:grid-cols-[1.4fr_0.6fr]">
        <div className="py-6 xl:border-r xl:border-[#161917]/15 xl:pr-8 dark:xl:border-border">
          <div className="flex items-start gap-4">
            <div className={`mt-1 flex h-10 w-10 shrink-0 items-center justify-center rounded-full ${hasBlockingSignal ? "bg-red-700/10 text-red-800 dark:text-red-300" : "bg-emerald-700/10 text-emerald-800 dark:text-emerald-300"}`}>
              {hasBlockingSignal ? <ShieldAlert className="h-5 w-5" aria-hidden="true" /> : <CircleCheck className="h-5 w-5" aria-hidden="true" />}
            </div>
            <div>
              <p className="text-xs font-bold uppercase tracking-[0.16em] text-muted-foreground">当前判断</p>
              <h4 className="mt-2 text-2xl font-semibold tracking-tight">
                {overview.latestGateDecision ? `最新门禁：${overview.latestGateDecision}` : "证据不足，尚不可发布"}
              </h4>
              <p className="mt-2 max-w-2xl text-sm leading-6 text-muted-foreground">
                {overview.latestGateDecision
                  ? "请继续核对硬失败、来源边界和人工仲裁；页面上的判断仍为 shadow 证据。"
                  : "先冻结数据集与候选，再运行完整 Agent 链路。没有 gate decision 时不使用“默认通过”。"}
              </p>
            </div>
          </div>
          <div className="mt-6 flex flex-wrap gap-3">
            <Button className="min-h-11 gap-2 bg-[#161917] text-[#f4f3ee] hover:bg-[#161917]/85 dark:bg-foreground dark:text-background" onClick={() => onNavigate("release")}>
              查看发布证据 <ArrowRight className="h-4 w-4" aria-hidden="true" />
            </Button>
            <Button variant="outline" className="min-h-11" onClick={() => onNavigate("badcases")}>Inspect Badcases</Button>
          </div>
        </div>
        <div className="grid grid-cols-2 gap-x-5 gap-y-5 py-6 xl:pl-8">
          <EvalMetric label="Suites" value={String(overview.suiteCount)} note="公开、私有、红队与探针" />
          <EvalMetric label="Candidates" value={String(overview.candidateCount)} note="不可变冻结版本" />
          <EvalMetric label="Runs" value={String(overview.runCount)} note={`${overview.runningCount} 正在运行`} />
          <EvalMetric label="Reviews" value={String(overview.reviewCount)} note="人评、机评与规则" />
        </div>
      </section>

      <section className="grid gap-x-6 gap-y-6 sm:grid-cols-2 xl:grid-cols-4">
        <EvalMetric label="平均质量分" value={formatWABenchScore(overview.averageScore)} note="五项 Rubric 换算为 100 分" tone={(overview.averageScore ?? 0) >= 80 ? "good" : "warn"} />
        <EvalMetric label="硬失败率" value={formatWABenchPercent(overview.hardFailureRate)} note="硬失败优先于质量均分" tone={(overview.hardFailureRate ?? 0) > 0 ? "danger" : "good"} />
        <EvalMetric label="人评接受代理" value={formatWABenchPercent(overview.acceptanceRate)} note="直接使用 + 少量修改" />
        <EvalMetric label="平均修改负担" value={formatWABenchScore(overview.modificationBurden)} note="0 无需修改 · 3 重度修改" />
        <EvalMetric label="P50 延迟" value={formatWABenchLatency(overview.p50LatencyMs)} note="完整 Agent 运行" />
        <EvalMetric label="P95 延迟" value={formatWABenchLatency(overview.p95LatencyMs)} note="关注长尾体验" />
        <EvalMetric label="来源边界失败" value={String(overview.sourceBoundaryFailureCount)} note="Lexiang-only 等路由检查" tone={overview.sourceBoundaryFailureCount > 0 ? "danger" : "good"} />
        <EvalMetric label="成本状态" value={overview.costStatus === "unavailable" ? "不可用" : overview.costStatus} note="不使用伪估算值" tone={overview.costStatus === "unavailable" ? "warn" : "default"} />
      </section>

      <section className="grid gap-8 xl:grid-cols-2">
        <div>
          <div className="flex items-center gap-2 border-b border-border pb-3">
            <Gauge className="h-4 w-4" aria-hidden="true" />
            <h4 className="text-sm font-semibold">最近运行</h4>
          </div>
          {latestRun ? (
            <div className="grid grid-cols-[1fr_auto] gap-4 border-b border-border/70 py-4">
              <div className="min-w-0">
                <p className="truncate font-mono text-xs">{latestRun.runId}</p>
                <p className="mt-1 truncate text-sm font-semibold">{latestRun.candidateName} → {latestRun.suiteName}</p>
                <p className="mt-1 text-xs text-muted-foreground">{latestRun.completedCases}/{latestRun.totalCases} 完成 · {latestRun.failedCases} 失败</p>
              </div>
              <div className="text-right"><EvalStatus value={latestRun.status} /><p className="mt-2 font-mono text-sm">{formatWABenchScore(latestRun.averageWeightedScore)}</p></div>
            </div>
          ) : <EvalEmptyState title="尚无 WABench 运行" description="进入“运行”工作区，选择冻结数据集和候选版本。" action={{ label: "创建首次运行", onClick: () => onNavigate("runs") }} />}
        </div>
        <div>
          <div className="flex items-center gap-2 border-b border-border pb-3">
            <AlertTriangle className="h-4 w-4" aria-hidden="true" />
            <h4 className="text-sm font-semibold">最新发布决策</h4>
          </div>
          {latestRelease ? (
            <div className="grid grid-cols-[1fr_auto] gap-4 border-b border-border/70 py-4">
              <div className="min-w-0">
                <p className="truncate text-sm font-semibold">{latestRelease.candidateId}</p>
                <p className="mt-1 truncate font-mono text-xs text-muted-foreground">{latestRelease.decisionId}</p>
                <p className="mt-2 text-xs text-muted-foreground">负责人 {latestRelease.ownerRef} · 回滚条件 {latestRelease.rollbackConditions.length}</p>
              </div>
              <GateBadge decision={latestRelease.decision} />
            </div>
          ) : <EvalEmptyState title="尚无发布决策" description="运行完成后才会生成门禁证据；空值不等于通过。" />}
        </div>
      </section>
    </div>
  );
}
