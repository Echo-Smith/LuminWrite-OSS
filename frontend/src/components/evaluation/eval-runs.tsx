import { useEffect, useState } from "react";
import { GitCompareArrows, Loader2, Play } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { WABenchCandidateItem, WABenchRunItem, WABenchSuiteItem } from "@/lib/admin-types";
import { formatWABenchScore } from "@/lib/wabench-eval";
import { EvalEmptyState, EvalSectionHeader, EvalStatus, GateBadge } from "./eval-states";

export function EvalRuns({ runs, suites, candidates, onCreateRun, canMutate = false }: {
  runs: WABenchRunItem[];
  suites: WABenchSuiteItem[];
  candidates: WABenchCandidateItem[];
  onCreateRun: (suiteId: string, candidateId: string, environment: string) => Promise<void>;
  canMutate?: boolean;
}) {
  const [suiteId, setSuiteId] = useState("");
  const [candidateId, setCandidateId] = useState("");
  const [environment, setEnvironment] = useState("staging");
  const [creating, setCreating] = useState(false);
  useEffect(() => { if (!suiteId && suites[0]) setSuiteId(suites[0].suiteId); }, [suiteId, suites]);
  useEffect(() => { if (!candidateId && candidates[0]) setCandidateId(candidates[0].candidateId); }, [candidateId, candidates]);

  const createRun = async () => {
    if (!suiteId || !candidateId) return;
    setCreating(true);
    try { await onCreateRun(suiteId, candidateId, environment); } finally { setCreating(false); }
  };

  return (
    <div className="space-y-7">
      <EvalSectionHeader eyebrow="Execution ledger / 04" title="每一次运行都是一笔可追溯的实验" description="使用真实 Agent Harness，显式保留生成、Judge、工具和路由失败。空输出不评分。" />

      <section className="grid gap-4 border-y border-[#161917]/20 bg-[#ebe9e1]/60 py-5 dark:border-border dark:bg-muted/20 lg:grid-cols-[1fr_1fr_0.65fr_auto] lg:items-end lg:px-5">
        <div className="space-y-2"><Label htmlFor="wabench-suite">冻结数据集</Label><Select value={suiteId} onValueChange={setSuiteId}><SelectTrigger id="wabench-suite" className="min-h-11 bg-background"><SelectValue placeholder="选择数据集" /></SelectTrigger><SelectContent>{suites.map((item) => <SelectItem key={item.suiteId} value={item.suiteId}>{item.name} · {item.version}</SelectItem>)}</SelectContent></Select></div>
        <div className="space-y-2"><Label htmlFor="wabench-candidate">冻结候选</Label><Select value={candidateId} onValueChange={setCandidateId}><SelectTrigger id="wabench-candidate" className="min-h-11 bg-background"><SelectValue placeholder="选择候选" /></SelectTrigger><SelectContent>{candidates.map((item) => <SelectItem key={item.candidateId} value={item.candidateId}>{item.name}</SelectItem>)}</SelectContent></Select></div>
        <div className="space-y-2"><Label htmlFor="wabench-environment">环境</Label><Select value={environment} onValueChange={setEnvironment}><SelectTrigger id="wabench-environment" className="min-h-11 bg-background"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="development">development</SelectItem><SelectItem value="staging">staging</SelectItem><SelectItem value="production-shadow">production-shadow</SelectItem></SelectContent></Select></div>
        <Button className="min-h-11 gap-2 bg-[#161917] text-[#f4f3ee] hover:bg-[#161917]/85 dark:bg-foreground dark:text-background" disabled={creating || !suiteId || !candidateId || !canMutate} onClick={createRun}>{creating ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}{canMutate ? "启动 Shadow Run" : "只读权限"}</Button>
      </section>

      {runs.length === 0 ? <EvalEmptyState icon={GitCompareArrows} title="尚无运行记录" description="冻结 suite 和 candidate 后启动首次 shadow run。任务运行在后台异步执行。" /> : (
        <div className="space-y-0 border-y border-[#161917]/20 dark:border-border">{runs.map((run) => {
          const progress = run.totalCases > 0 ? Math.min(100, (run.completedCases / run.totalCases) * 100) : 0;
          return <article key={run.runId} className="grid gap-5 border-b border-border/70 py-5 last:border-b-0 xl:grid-cols-[1.2fr_0.8fr_0.55fr]">
            <div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><EvalStatus value={run.status} /><span className="text-xs text-muted-foreground">{run.environment} · {run.trafficType}</span></div><p className="mt-3 truncate font-mono text-xs">{run.runId}</p><p className="mt-1 truncate text-sm font-semibold">{run.candidateName} → {run.suiteName}</p></div>
            <div><div className="flex items-center justify-between text-xs"><span>{run.completedCases}/{run.totalCases} 完成</span><span className={run.failedCases > 0 ? "text-red-800 dark:text-red-300" : "text-muted-foreground"}>{run.failedCases} 失败</span></div><Progress value={progress} className="mt-3 h-1.5" /><p className="mt-2 text-xs text-muted-foreground">{run.scoredCases} 条有效机评 · adapter {run.adapterId}</p></div>
            <div className="flex items-center justify-between gap-4 xl:justify-end"><div className="text-right"><p className="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">质量分</p><p className="mt-1 font-mono text-xl">{formatWABenchScore(run.averageWeightedScore)}</p></div><GateBadge decision={run.gateDecision} /></div>
          </article>;
        })}</div>
      )}
    </div>
  );
}
