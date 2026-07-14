/**
 * 消息渲染 — Assistant 消息（含 Agent Steps + 流式文章 + 反馈）
 *
 * 一条 assistant 消息包含多个 parts:
 *   tool-call parts → AgentStepCard (ChainOfThought 手风琴)
 *   text parts      → MarkdownContent (流式文章)
 *   data parts      → OutlineTool / FeedbackBar / ReviewCard
 */
import { useRef, useEffect } from "react";
import { PenLine, Pause } from "lucide-react";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from "@/components/ui/collapsible";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import type { ChatMessage, ToolCallPart, TextPart, DataPart } from "@/stores/agent-store";
import { useAgentStore } from "@/stores/agent-store";
import { MarkdownContent } from "./markdown-content";
import { AgentStepCard } from "@/components/tools/agent-step-card";
import { OutlineTool } from "@/components/tools/outline-tool";
import { FeedbackBar } from "@/components/feedback/feedback-bar";
import { STEP_LABELS } from "@/lib/constants";
import { cn } from "@/lib/utils";

interface AssistantMessageProps {
  message: ChatMessage;
  traceId: string | null;
}

export function AssistantMessage({ message, traceId }: AssistantMessageProps) {
  // 获取当前会话状态（用于判断是否暂停）
  const sessionStatus = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.status ?? "idle";
  });
  const isPaused = sessionStatus === "paused";

  // 分离 tool-call parts 和其他 parts
  const toolCallParts = message.parts.filter(
    (p): p is ToolCallPart => p.type === "tool-call"
  );
  const otherParts = message.parts.filter(
    (p) => p.type !== "tool-call"
  );

  // 找到第一个 text part 的位置（用于在 text 之前显示 tool-calls）
  const firstTextIdx = otherParts.findIndex((p) => p.type === "text");

  // 在 text part 之前的 other parts（通常是 data parts）
  const beforeTextParts = firstTextIdx >= 0 ? otherParts.slice(0, firstTextIdx) : otherParts;
  // text parts
  const textParts = otherParts.filter((p): p is TextPart => p.type === "text");
  // text 之后的 data parts（review 等）
  const afterTextParts = firstTextIdx >= 0 ? otherParts.slice(firstTextIdx + 1).filter((p) => p.type !== "text") : [];

  const isRunning = message.status === "running";
  const hasContent = message.parts.length > 0;

  return (
    <div className="flex gap-3 px-4 py-3">
      <Avatar className="h-8 w-8 shrink-0">
        <AvatarFallback className={cn(
          "bg-blue-100 text-blue-600",
          isRunning && "ring-2 ring-blue-400 ring-offset-1"
        )}>
          <PenLine className="h-4 w-4" />
        </AvatarFallback>
      </Avatar>

      <div className="flex-1 min-w-0 space-y-2">
        {/* Agent Steps 手风琴组 */}
        {toolCallParts.length > 0 && (
          <AgentStepsAccordion parts={toolCallParts} isRunning={isRunning} />
        )}

        {/* text 之前的 data parts（如 outline） */}
        {beforeTextParts
          .filter((p): p is DataPart => p.type === "data")
          .map((part, i) => (
            <DataPartRenderer key={`before-${i}`} part={part} traceId={traceId} />
          ))}

        {/* 流式文章输出 */}
        {textParts.length > 0 && (
          <div className="rounded-lg bg-muted/30 p-4">
            {textParts.map((part, i) => (
              <div key={i}>
                <MarkdownContent content={part.text} />
                {part.streaming && !isPaused && (
                  <span className="inline-block h-4 w-0.5 animate-pulse bg-blue-500 ml-0.5 align-text-bottom" />
                )}
              </div>
            ))}
            {/* 暂停状态提示 */}
            {isPaused && isRunning && (
              <div className="mt-3 flex items-center gap-2 rounded-md bg-amber-50 border border-amber-200 px-3 py-2">
                <Pause className="h-4 w-4 text-amber-600" />
                <span className="text-sm text-amber-700 font-medium">已暂停</span>
                <span className="text-xs text-amber-600">— 点击下方播放按钮继续</span>
              </div>
            )}
          </div>
        )}

        {/* text 之后的 data parts（如 review、feedback） */}
        {afterTextParts
          .filter((p): p is DataPart => p.type === "data")
          .map((part, i) => (
            <DataPartRenderer key={`after-${i}`} part={part} traceId={traceId} article={textParts.map(t => t.text).join("")} />
          ))}

        {/* 等待提示 */}
        {!hasContent && isRunning && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground py-2">
            <span className="flex gap-1">
              <span className="h-2 w-2 animate-bounce rounded-full bg-muted-foreground/40" style={{ animationDelay: "0ms" }} />
              <span className="h-2 w-2 animate-bounce rounded-full bg-muted-foreground/40" style={{ animationDelay: "150ms" }} />
              <span className="h-2 w-2 animate-bounce rounded-full bg-muted-foreground/40" style={{ animationDelay: "300ms" }} />
            </span>
            <span>正在思考中...</span>
          </div>
        )}
      </div>
    </div>
  );
}

/**
 * Agent Steps 手风琴组 — 将连续的 tool-call parts 分组到可折叠容器中
 */
function AgentStepsAccordion({ parts, isRunning }: { parts: ToolCallPart[]; isRunning: boolean }) {
  const runningCount = parts.filter((p) => p.status === "running").length;
  const completedCount = parts.filter((p) => p.status === "complete").length;
  const currentStep = parts.find((p) => p.status === "running");

  const scrollRef = useRef<HTMLDivElement>(null);

  // 自动滚动到正在运行的步骤
  useEffect(() => {
    if (scrollRef.current && isRunning) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [parts, isRunning]);

  return (
    <Collapsible defaultOpen={isRunning}>
      <CollapsibleTrigger asChild>
        <button className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-colors py-1">
          <div className="flex items-center gap-1.5">
            {isRunning && runningCount > 0 ? (
              <span className="flex h-5 w-5 items-center justify-center">
                <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-blue-400 border-t-transparent" />
              </span>
            ) : (
              <span className="flex h-5 w-5 items-center justify-center text-green-600">✓</span>
            )}
            <span className="font-medium">
              {isRunning && currentStep
                ? `正在执行：${STEP_LABELS[currentStep.toolName] ?? currentStep.toolName}`
                : `Agent 步骤 (${completedCount}/${parts.length})`}
            </span>
          </div>
          {isRunning && (
            <Badge variant="outline" className="text-blue-600 border-blue-300">
              {runningCount} 运行中
            </Badge>
          )}
        </button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div
          ref={scrollRef}
          className="space-y-1.5 pt-1 max-h-[400px] overflow-y-auto scrollbar-thin"
        >
          {parts.map((part, i) => (
            <AgentStepCard key={i} part={part} defaultOpen={part.status === "running"} />
          ))}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

/**
 * Data Part 渲染器
 */
function DataPartRenderer({
  part,
  traceId,
  article,
}: {
  part: DataPart;
  traceId: string | null;
  article?: string;
}) {
  switch (part.dataType) {
    case "outline":
      return <OutlineTool data={part.data as Parameters<typeof OutlineTool>[0]["data"]} attempt={part.attempt} maxAttempts={part.maxAttempts} />;

    case "review":
      return <ReviewCard data={part.data as Record<string, unknown>} />;

    case "feedback": {
      // Use text parts article, or fall back to data.article
      const feedbackArticle = article || (part.data as { article?: string })?.article || "";
      if (traceId && feedbackArticle) {
        return <FeedbackBar traceId={traceId} article={feedbackArticle} />;
      }
      return null;
    }

    default:
      return null;
  }
}

/**
 * 质量评分卡片
 */
function ReviewCard({ data }: { data: Record<string, unknown> }) {
  const scores = (data.scores ?? {}) as Record<string, number>;
  const passed = data.passed as boolean | undefined;
  const issues = (data.issues ?? []) as Array<Record<string, unknown>>;

  const scoreLabels: Record<string, string> = {
    factuality: "事实准确性",
    structure: "结构合规",
    style: "风格符合",
    rhetoric: "修辞运用",
    length: "篇幅控制",
    title_quality: "标题质量",
    safety: "内容安全",
  };

  return (
    <div className="rounded-lg border p-4 space-y-3">
      <div className="flex items-center justify-between">
        <h4 className="text-sm font-medium">质量评分</h4>
        {passed !== undefined && (
          <Badge variant={passed ? "success" : "destructive"}>
            {passed ? "通过" : "未通过"}
          </Badge>
        )}
      </div>

      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
        {Object.entries(scores).map(([dim, score]) => (
          <div key={dim} className="flex items-center gap-2 text-xs">
            <span className="text-muted-foreground shrink-0">
              {scoreLabels[dim] ?? dim}
            </span>
            <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden">
              <div
                className={cn(
                  "h-full rounded-full transition-all",
                  score >= 0.8 ? "bg-green-500" : score >= 0.6 ? "bg-amber-500" : "bg-red-500"
                )}
                style={{ width: `${score * 100}%` }}
              />
            </div>
            <span className="font-medium tabular-nums">{(score * 100).toFixed(0)}</span>
          </div>
        ))}
      </div>

      {issues.length > 0 && (
        <div className="space-y-1 border-t pt-2">
          {issues.map((issue, i) => (
            <div
              key={i}
              className={cn(
                "text-xs flex items-start gap-1",
                issue.severity === "high" && "text-red-600",
                issue.severity === "medium" && "text-amber-600",
                (!issue.severity || issue.severity === "low") && "text-muted-foreground"
              )}
            >
              <span>⚠</span>
              <span>{String(issue.message ?? "")}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
