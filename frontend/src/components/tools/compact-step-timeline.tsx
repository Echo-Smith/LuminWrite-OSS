/**
 * CompactStepTimeline — 可折叠的步骤时间线
 *
 * 绿点流程，默认折叠，运行中仅展示当前进行步骤。
 * 用于对话框消息和右侧详情面板的流程 Tab。
 */
import { useEffect, useState } from "react";
import { ChevronRight } from "lucide-react";
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from "@/components/ui/collapsible";
import { Badge } from "@/components/ui/badge";
import type { ToolCallPart } from "@/stores/agent-store";
import { STEP_LABELS } from "@/lib/constants";
import { cn } from "@/lib/utils";
import { PulseIndicator } from "@/components/animation";

interface CompactStepTimelineProps {
  parts: ToolCallPart[];
  isRunning: boolean;
  /** 是否默认展开（详情面板可设为 true） */
  defaultOpen?: boolean;
}

export function CompactStepTimeline({ parts, isRunning, defaultOpen = false }: CompactStepTimelineProps) {
  const runningCount = parts.filter((p) => p.status === "running").length;
  const completedCount = parts.filter((p) => p.status === "complete").length;
  const currentStep = parts.find((p) => p.status === "running");
  const [open, setOpen] = useState(defaultOpen);

  // 运行中时自动展开，完成后自动折叠
  useEffect(() => {
    if (isRunning && runningCount > 0) {
      setOpen(true);
    } else if (!isRunning) {
      setOpen(false);
    }
  }, [isRunning, runningCount]);

  if (parts.length === 0) return null;

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger asChild>
        <button className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-ui py-1 group">
          <div className="flex items-center gap-1.5">
            {isRunning && runningCount > 0 ? (
              <PulseIndicator status="running" size="sm" />
            ) : (
              <PulseIndicator status="complete" size="sm" />
            )}
            <span className="font-medium">
              {isRunning && currentStep
                ? `正在执行：${STEP_LABELS[currentStep.toolName] ?? currentStep.toolName}`
                : `Agent 流程 (${completedCount}/${parts.length})`}
            </span>
          </div>
          {isRunning && (
            <Badge variant="outline" className="text-primary border-primary/30">
              {runningCount} 运行中
            </Badge>
          )}
          <ChevronRight
            className={cn(
              "h-3.5 w-3.5 text-muted-foreground transition-ui",
              open && "rotate-90"
            )}
          />
        </button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="relative space-y-1.5 pt-2 pl-1">
          {parts.map((part, i) => {
            const isCurrent = part.status === "running";
            const isDone = part.status === "complete";
            const isErr = part.status === "error";
            const isDegraded = part.status === "degraded";
            return (
              <div key={i} className="relative flex items-center gap-2.5">
                {/* 时间线竖线 */}
                {i < parts.length - 1 && (
                  <div className={cn(
                    "absolute left-[5px] top-4 bottom-[-6px] w-px",
                    isDone ? "bg-emerald-300/50 dark:bg-emerald-700/40" : "bg-border"
                  )} />
                )}
                {/* 状态圆点 */}
                <PulseIndicator
                  status={isDone ? "complete" : isCurrent ? "running" : isErr ? "error" : isDegraded ? "error" : "idle"}
                  size="sm"
                  ring={isCurrent}
                />
                {/* 标签 */}
                <span className={cn(
                  "text-xs",
                  isCurrent ? "text-primary font-medium" : isDone ? "text-muted-foreground" : isDegraded ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground/60"
                )}>
                  {STEP_LABELS[part.toolName] ?? part.toolName}
                  {isDegraded && <span className="ml-1 text-amber-500">（已跳过）</span>}
                </span>
                {/* 耗时 */}
                {part.durationMs != null && (
                  <span className="text-xs text-muted-foreground/60 font-mono-sm ml-auto">
                    {(part.durationMs / 1000).toFixed(1)}s
                  </span>
                )}
                {/* 错误/降级提示 */}
                {part.error && (
                  <span className={cn("text-xs truncate", isDegraded ? "text-amber-600 dark:text-amber-400" : "text-red-600")}>{part.error}</span>
                )}
              </div>
            );
          })}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
