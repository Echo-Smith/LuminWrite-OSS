/**
 * 研究进度面板 — 消费 GET /runs/{id}/research 与 research.progress 事件的投影。
 *
 * T09：优先用服务端 counts.papers_* 展示真实已读篇数（全文 x / 摘要 y / 未读 z），
 * 阅读上限用运行合同投影 spec.max_papers（缺省回退启动表单值）。
 * 刻意不把 max_papers（阅读上限）显示成「已读 N 篇」：上限是预算配置值，
 * 未全部读完时必须显式标注 ≠ 已读篇数。
 */
import { AlertTriangle, BookOpen, BookOpenCheck, CircleCheck, Clock3, FileQuestion, Layers, PauseCircle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { describeResearchCounts, paperReadingCounts, resolveReadingCap, type ResearchProgressView } from "@/lib/research-api";
import type { ResearchSlice } from "@/stores/research-slice";

const PHASE_LABELS: Record<string, string> = {
  pending: "等待启动",
  discovering: "正在检索文献",
  ranking: "正在筛选候选",
  fetching: "正在获取全文",
  parsing: "正在解析文档",
  reading: "正在阅读来源",
  packing: "正在冻结证据包",
  gate_evidence: "等待证据确认",
  gate_outline: "等待提纲确认",
  writing: "正在撰写正文",
  quality_gate_paused: "质量门暂停（引用校验未通过）",
  failed: "运行失败",
};

export function researchPhaseLabel(phase: string): string {
  return PHASE_LABELS[phase] ?? phase;
}

interface ResearchProgressProps {
  runId: string;
  slice: ResearchSlice;
  /** 启动表单值回退：spec.max_papers 缺省（旧后端 / 纯事件投影）时使用。 */
  maxPapers?: number | null;
}

export function ResearchProgress({ runId, slice, maxPapers = null }: ResearchProgressProps) {
  const progress: ResearchProgressView | null = slice.progress;
  if (!progress) return null;

  const counts = progress.counts ?? {};
  // 上限解析：服务端运行合同投影优先，缺省回退启动表单值。
  const readingCap = resolveReadingCap(slice.spec?.max_papers, maxPapers);
  const summary = describeResearchCounts(counts, readingCap);
  const failed = counts.failed ?? 0;
  const deferred = counts.deferred ?? 0;
  const paperCounts = paperReadingCounts(counts);

  return (
    <section className="research-progress" aria-label="研究进度">
      <header className="research-progress-header">
        <span className="flex items-center gap-2 text-sm font-semibold"><Layers className="h-4 w-4" />研究进度</span>
        <Badge variant="outline" className="text-[11px]">{researchPhaseLabel(progress.phase)}</Badge>
      </header>

      <div className="research-progress-counts" data-testid="research-counts">
        <span className="font-medium tabular-nums">{summary.label}</span>
        {paperCounts && (
          <span className="research-progress-papers inline-flex items-center gap-2" data-testid="research-paper-counts">
            <span className="inline-flex items-center gap-1"><BookOpenCheck className="h-3.5 w-3.5" />全文 {paperCounts.fullText}</span>
            <span className="inline-flex items-center gap-1"><BookOpen className="h-3.5 w-3.5" />摘要 {paperCounts.abstract}</span>
            <span className="inline-flex items-center gap-1 text-muted-foreground"><FileQuestion className="h-3.5 w-3.5" />未读 {paperCounts.unread}</span>
          </span>
        )}
        {failed > 0 && <span className="inline-flex items-center gap-1 text-destructive"><AlertTriangle className="h-3.5 w-3.5" />失败 {failed}</span>}
        {deferred > 0 && <span className="inline-flex items-center gap-1 text-amber-600 dark:text-amber-400"><PauseCircle className="h-3.5 w-3.5" />待补充 {deferred}</span>}
        {counts.total > 0 && counts.completed === counts.total && <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400"><CircleCheck className="h-3.5 w-3.5" />全部任务完成</span>}
      </div>
      {readingCap !== null && (
        <p className="research-progress-cap" data-testid="research-cap">阅读上限 {readingCap} 篇（{slice.spec ? "运行合同投影" : "启动表单值"}；预算配置，非已读数）</p>
      )}
      {summary.capNote && (
        <p className="research-progress-capnote" data-testid="research-cap-note">{summary.capNote}</p>
      )}

      {progress.tasks.length > 0 && (
        <ul className="research-task-list" aria-label="研究任务摘要">
          {progress.tasks.map((task) => (
            <li key={task.task_key}>
              <span className="research-task-status" data-status={task.status}><Clock3 className="h-3 w-3" />{task.status}</span>
              <p className="min-w-0 flex-1 truncate">{task.task_key}</p>
              {task.error_code && <code className="research-task-error">{task.error_code}</code>}
            </li>
          ))}
        </ul>
      )}

      {progress.errors.length > 0 && (
        <div className="research-progress-errors" role="alert">
          <p className="text-xs font-semibold text-destructive">失败任务（可恢复进度已保留）</p>
          <ul>
            {progress.errors.map((error) => (
              <li key={error.task_key}>
                <span>{error.task_key}</span>
                {error.error_code && <code>{error.error_code}</code>}
                <small>第 {error.attempt} 次尝试</small>
              </li>
            ))}
          </ul>
        </div>
      )}

      <p className="research-progress-meta">运行 {runId} · 事件序号 {progress.last_event_sequence}（断线后以 GET 为准合并）</p>
    </section>
  );
}
