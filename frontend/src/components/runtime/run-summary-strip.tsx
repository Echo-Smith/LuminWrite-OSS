import { CirclePause, CirclePlay, LoaderCircle, OctagonX, Route, ShieldAlert } from "lucide-react";
import type { RuntimeRun, UserQualitySummary } from "@/lib/writing-runtime-types";
import { QualityStatus } from "@/components/quality/quality-status";
import { cn } from "@/lib/utils";

const STATUS_COPY: Record<string, string> = {
  draft: "正在整理任务合约",
  contract_confirmed: "任务合约已确认",
  planning: "正在安排写作步骤",
  planned: "写作计划已就绪",
  awaiting_approval: "等待你确认后执行",
  running: "正在形成正文",
  pausing: "正在安全暂停",
  paused: "已暂停，可继续",
  replanning: "正在调整写作步骤",
  failed: "运行未完成，请查看原因",
  cancelling: "正在中止运行",
  cancelled: "本次运行已中止",
  completed: "本次写作已完成",
};

interface RunSummaryStripProps {
  run: RuntimeRun | null;
  nodeStatuses: Record<string, string>;
  quality: UserQualitySummary | null;
  legacyStatus?: string;
  onControl?: (action: "pause" | "resume" | "cancel") => void;
}

export function RunSummaryStrip({ run, nodeStatuses, quality, legacyStatus, onControl }: RunSummaryStripProps) {
  const statuses = Object.values(nodeStatuses);
  const complete = statuses.filter((status) => status === "completed").length;
  const running = statuses.filter((status) => status === "running").length;
  const status = run?.status ?? legacyStatus ?? "draft";
  const isBusy = status === "running" || status === "planning" || status === "replanning";

  return (
    <section className="run-summary-strip" aria-label="写作运行摘要" data-run-status={status}>
      <div className="flex min-w-0 items-center gap-3">
        <span className={cn("run-summary-mark", isBusy && "run-summary-mark-live")}>
          {isBusy ? <LoaderCircle className="h-4 w-4 animate-spin" /> : status === "failed" ? <ShieldAlert className="h-4 w-4" /> : <Route className="h-4 w-4" />}
        </span>
        <div className="min-w-0">
          <p className="truncate text-xs font-medium">{STATUS_COPY[status] ?? "写作工作台已就绪"}</p>
          <p className="truncate text-[10px] text-muted-foreground">
            {statuses.length > 0 ? `${complete}/${statuses.length} 个写作步骤完成${running ? `，${running} 个进行中` : ""}` : "计划与验证记录可在详情中查看"}
          </p>
        </div>
      </div>
      <div className="ml-auto flex shrink-0 items-center gap-2">
        <QualityStatus summary={quality} compact />
        {run && onControl && (
          <div className="flex items-center gap-1 border-l pl-2">
            {status === "running" && <button className="run-control" onClick={() => onControl("pause")} aria-label="暂停写作"><CirclePause className="h-4 w-4" /></button>}
            {status === "paused" && <button className="run-control" onClick={() => onControl("resume")} aria-label="继续写作"><CirclePlay className="h-4 w-4" /></button>}
            {!["completed", "cancelled", "failed"].includes(status) && <button className="run-control run-control-danger" onClick={() => onControl("cancel")} aria-label="中止写作"><OctagonX className="h-4 w-4" /></button>}
          </div>
        )}
      </div>
    </section>
  );
}
