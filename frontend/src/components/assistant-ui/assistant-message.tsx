/**
 * 消息渲染 — Assistant 消息（含 Agent Steps + 流式文章 + 反馈）
 *
 * 一条 assistant 消息包含多个 parts:
 *   tool-call parts → CompactStepTimeline (绿点时间线，默认折叠)
 *   reasoning parts → ThinkingPanel (可折叠思考过程)
 *   text parts      → MarkdownContent (流式文章)
 *   data parts      → OutlineTool / FeedbackBar / ReviewCard
 */
import { useEffect, useState } from "react";
import { PenLine, Pause, Copy, Check, RefreshCw, ChevronRight, Brain, Download, FileText, FilePlus, Maximize2 } from "lucide-react";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from "@/components/ui/collapsible";
import { Badge } from "@/components/ui/badge";
import type { ChatMessage, ToolCallPart, TextPart, DataPart, ReasoningPart } from "@/stores/agent-store";
import type { WriteMode } from "@/lib/types";
import { useAgentStore } from "@/stores/agent-store";
import { MarkdownContent } from "./markdown-content";
import { OutlineTool } from "@/components/tools/outline-tool";
import { FeedbackBar } from "@/components/feedback/feedback-bar";
import { STEP_LABELS } from "@/lib/constants";
import { toast } from "@/stores/toast-store";
import { cn } from "@/lib/utils";
import { TypingDots, StreamingCursor, PulseIndicator } from "@/components/animation";

interface AssistantMessageProps {
  message: ChatMessage;
  traceId: string | null;
  version?: number;
  totalVersions?: number;
}

export function AssistantMessage({ message, traceId, version = 1, totalVersions = 1 }: AssistantMessageProps) {
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
  const reasoningParts = message.parts.filter(
    (p): p is ReasoningPart => p.type === "reasoning"
  );
  const otherParts = message.parts.filter(
    (p) => p.type !== "tool-call" && p.type !== "reasoning"
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
    <div className="flex gap-3 px-4 py-3 anim-fade-up">
      <Avatar className="h-8 w-8 shrink-0">
        <AvatarFallback className={cn(
          "bg-muted text-foreground",
          isRunning && "ring-2 ring-primary/40 ring-offset-1"
        )}>
          <PenLine className="h-4 w-4" />
        </AvatarFallback>
      </Avatar>

      <div className="flex-1 min-w-0 space-y-2">
        {/* Agent 步骤流程（紧凑时间线，默认折叠） */}
        {toolCallParts.length > 0 && (
          <CompactStepTimeline parts={toolCallParts} isRunning={isRunning} />
        )}

        {/* 思考过程（可折叠） */}
        {reasoningParts.length > 0 && (
          <ThinkingPanel parts={reasoningParts} isRunning={isRunning} />
        )}

        {/* text 之前的 data parts（如 outline） */}
        {beforeTextParts
          .filter((p): p is DataPart => p.type === "data")
          .map((part, i) => (
            <DataPartRenderer key={`before-${i}`} part={part} traceId={traceId} />
          ))}

        {/* 流式文章输出 */}
        {textParts.length > 0 && (
          <div className="codex-card p-4">
            {/* 版本标签 + 文章标题 */}
            <div className="flex items-start justify-between gap-2 mb-3">
              {message.articleTitle && (
                <h2 className="text-xl font-bold text-foreground leading-tight flex-1">
                  {message.articleTitle}
                </h2>
              )}
              {totalVersions > 1 && (
                <Badge variant="outline" className="shrink-0 text-xs gap-1">
                  <FileText className="h-3 w-3" />
                  v{version}/{totalVersions}
                </Badge>
              )}
            </div>
            {textParts.map((part, i) => (
              <div key={i}>
                <MarkdownContent content={part.text} />
                {part.streaming && !isPaused && (
                  <StreamingCursor active className="ml-0.5" />
                )}
              </div>
            ))}
            {/* 暂停状态提示 */}
            {isPaused && isRunning && (
              <div className="mt-3 flex items-center gap-2 rounded-md bg-amber-50 dark:bg-amber-950/30 border border-amber-200 dark:border-amber-900 px-3 py-2 anim-fade-in">
                <Pause className="h-4 w-4 text-amber-600" />
                <span className="text-sm text-amber-700 dark:text-amber-400 font-medium">已暂停</span>
                <span className="text-xs text-amber-600 dark:text-amber-500">— 点击下方播放按钮继续</span>
              </div>
            )}
            {/* 流式字数进度条 */}
            {isRunning && textParts.some((p) => p.streaming) && (
              <WordCountProgress text={textParts.map((t) => t.text).join("")} />
            )}
            {/* 消息操作按钮 */}
            {!isRunning && textParts.some((p) => p.text.trim()) && (
              <MessageActions text={textParts.map((t) => t.text).join("")} title={message.articleTitle} />
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
          <div className="flex items-center gap-2 text-sm text-muted-foreground py-2 anim-fade-in">
            <TypingDots label="正在思考中" />
          </div>
        )}
      </div>
    </div>
  );
}

/**
 * 紧凑步骤时间线 — 绿点流程，默认折叠，运行中仅展示当前进行步骤
 */
function CompactStepTimeline({ parts, isRunning }: { parts: ToolCallPart[]; isRunning: boolean }) {
  const runningCount = parts.filter((p) => p.status === "running").length;
  const completedCount = parts.filter((p) => p.status === "complete").length;
  const currentStep = parts.find((p) => p.status === "running");
  const [open, setOpen] = useState(false);

  // 运行中时自动展开，完成后自动折叠
  useEffect(() => {
    if (isRunning && runningCount > 0) {
      setOpen(true);
    } else if (!isRunning) {
      setOpen(false);
    }
  }, [isRunning, runningCount]);

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

/**
 * 思考过程面板 — 可折叠的 reasoning 内容区块
 *
 * 运行中默认展开（实时展示思考流），完成后默认折叠。
 * 点击标题可手动切换展开/折叠状态。
 */
function ThinkingPanel({ parts, isRunning }: { parts: ReasoningPart[]; isRunning: boolean }) {
  const fullText = parts.map((p) => p.text).join("");
  const [open, setOpen] = useState(false);
  const [userToggled, setUserToggled] = useState(false);

  // 运行中自动展开，完成后自动折叠（除非用户手动操作过）
  useEffect(() => {
    if (isRunning && !userToggled) {
      setOpen(true);
    } else if (!isRunning && !userToggled) {
      setOpen(false);
    }
  }, [isRunning, userToggled]);

  const handleToggle = (next: boolean) => {
    setUserToggled(true);
    setOpen(next);
  };

  if (!fullText.trim()) return null;

  return (
    <Collapsible open={open} onOpenChange={handleToggle}>
      <CollapsibleTrigger asChild>
        <button className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-ui py-1 group">
          <Brain className={cn(
            "h-3.5 w-3.5 transition-colors",
            isRunning ? "text-primary animate-pulse" : "text-muted-foreground"
          )} />
          <span className="font-medium">
            {isRunning ? "思考中..." : "思考过程"}
          </span>
          {isRunning && (
            <Badge variant="outline" className="text-primary border-primary/30 animate-pulse">
              live
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
        <div className="mt-1.5 rounded-lg border border-primary/20 bg-primary/5 dark:bg-primary/10 p-3 max-h-64 overflow-y-auto">
          <MarkdownContent content={fullText} />
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
      const feedbackData = part.data as { article?: string; has_feedback?: boolean };
      const feedbackArticle = article || feedbackData?.article || "";
      if (traceId && feedbackArticle) {
        // key by traceId so the component remounts when switching sessions,
        // resetting submitted state from the server-side has_feedback flag.
        return <FeedbackBar key={traceId} traceId={traceId} article={feedbackArticle} hasFeedback={feedbackData?.has_feedback} />;
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
    <div className="codex-card p-4 space-y-3 anim-fade-scale">
      <div className="flex items-center justify-between">
        <h4 className="text-sm font-medium">质量评分</h4>
        {passed !== undefined && (
          <Badge variant={passed ? "success" : "destructive"}>
            {passed ? "通过" : "未通过"}
          </Badge>
        )}
      </div>

      <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
        {Object.entries(scores).map(([dim, score], i) => (
          <div key={dim} className="flex items-center gap-2 text-xs anim-fade-up" style={{ animationDelay: `${i * 40}ms` }}>
            <span className="text-muted-foreground shrink-0">
              {scoreLabels[dim] ?? dim}
            </span>
            <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden">
              <div
                className={cn(
                  "h-full rounded-full transition-ui",
                  score >= 0.8 ? "bg-emerald-500" : score >= 0.6 ? "bg-amber-500" : "bg-red-500"
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

/**
 * 消息操作按钮 — Copy / Regenerate
 */
function MessageActions({ text, title }: { text: string; title?: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    // 组合标题 + 正文，生成干净的纯文本
    const fullText = title ? `${title}\n\n${text}` : text;
    navigator.clipboard.writeText(fullText);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const handleDownload = () => {
    const fullText = title ? `# ${title}\n\n${text}` : text;
    const blob = new Blob([fullText], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    // 文件名：取标题前 20 字，替换非法字符
    const safeName = (title ?? "article").slice(0, 20).replace(/[<>:"/\\|?*\n]/g, "_");
    a.download = `${safeName}.md`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  const handleRegenerate = () => {
    // Trigger regeneration by re-sending the last user message
    const store = useAgentStore.getState();
    const session = store.sessions.find((s) => s.id === store.activeSessionId);
    if (!session) return;
    const lastUserMsg = [...session.messages].reverse().find((m) => m.role === "user");
    if (!lastUserMsg) return;
    const textParts = lastUserMsg.parts.filter((p) => p.type === "text");
    const userText = textParts.map((p: { text: string }) => p.text).join("");
    if (userText) {
      store.startWriting({ message: userText, style: session.style, mode: session.mode as WriteMode });
    }
  };

  const handleContinue = () => {
    const store = useAgentStore.getState();
    const session = store.sessions.find((s) => s.id === store.activeSessionId);
    if (!session) return;
    // 续写：基于当前文章内容继续延伸
    const continuePrompt = `基于以下已写内容的风格和逻辑，继续续写：\n\n${text.slice(-500)}`;
    store.startWriting({ message: continuePrompt, style: session.style, mode: session.mode as WriteMode });
    toast.info("续写已开始", "AI 将基于当前内容继续延伸");
  };

  const handleExpand = () => {
    const store = useAgentStore.getState();
    const session = store.sessions.find((s) => s.id === store.activeSessionId);
    if (!session) return;
    // 扩写：选取最后一段进行展开
    const paragraphs = text.split(/\n\n+/).filter(Boolean);
    const lastPara = paragraphs[paragraphs.length - 1] ?? text.slice(-300);
    const expandPrompt = `请将以下段落进行扩写，增加更丰富的论据和细节：\n\n${lastPara}`;
    store.startWriting({ message: expandPrompt, style: session.style, mode: session.mode as WriteMode });
    toast.info("扩写已开始", "AI 将展开最后一段的论述");
  };

  return (
    <div className="mt-3 flex items-center gap-1 border-t pt-2 anim-fade-in flex-wrap">
      <button
        onClick={handleCopy}
        className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground transition-ui"
      >
        {copied ? (
          <>
            <Check className="h-3.5 w-3.5 text-emerald-600" />
            <span className="text-emerald-600">已复制</span>
          </>
        ) : (
          <>
            <Copy className="h-3.5 w-3.5" />
            <span>复制</span>
          </>
        )}
      </button>
      <button
        onClick={handleRegenerate}
        className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground transition-ui"
      >
        <RefreshCw className="h-3.5 w-3.5" />
        <span>重写</span>
      </button>
      <button
        onClick={handleContinue}
        className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground transition-ui"
        title="继续写，延伸内容"
      >
        <FilePlus className="h-3.5 w-3.5" />
        <span>续写</span>
      </button>
      <button
        onClick={handleExpand}
        className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground transition-ui"
        title="选取末尾段落展开论述"
      >
        <Maximize2 className="h-3.5 w-3.5" />
        <span>扩写</span>
      </button>
      <button
        onClick={handleDownload}
        className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground transition-ui"
        title="下载为 Markdown 文件"
      >
        <Download className="h-3.5 w-3.5" />
        <span>下载</span>
      </button>
    </div>
  );
}

/**
 * WordCountProgress — 流式写作中的实时字数进度条
 * 从 session.style 获取目标字数范围，显示当前进度
 */
function WordCountProgress({ text }: { text: string }) {
  const sessionStyle = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.style ?? "yinyue";
  });

  // 风格对应的目标字数范围（fallback）
  const STYLE_WORD_RANGE: Record<string, [number, number]> = {
    yinyue: [1800, 2800],
    sheping: [1500, 2500],
    xinwen: [800, 1500],
    pinglun: [1200, 2000],
    sanwen: [800, 1500],
  };
  const range = STYLE_WORD_RANGE[sessionStyle] ?? [1000, 2000];
  const targetMin = range[0];
  const targetMax = range[1];

  // 计算中文字数（去除空白和标记符号）
  const cleanText = text.replace(/\s/g, "").replace(/[#*>\-_`~]/g, "");
  const currentCount = cleanText.length;
  const progressPercent = Math.min(100, (currentCount / targetMax) * 100);

  // 判断当前状态
  const isBelowTarget = currentCount < targetMin;
  const isInRange = currentCount >= targetMin && currentCount <= targetMax;
  const isAboveTarget = currentCount > targetMax;

  return (
    <div className="mt-3 flex items-center gap-3 rounded-md bg-muted/30 dark:bg-muted/10 px-3 py-2 anim-fade-in">
      <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden">
        <div
          className={cn(
            "h-full rounded-full transition-all duration-300",
            isBelowTarget ? "bg-blue-400" : isInRange ? "bg-emerald-500" : "bg-amber-500"
          )}
          style={{ width: `${progressPercent}%` }}
        />
      </div>
      <div className="flex items-center gap-1.5 text-xs tabular-nums shrink-0">
        <span className={cn(
          "font-medium",
          isInRange ? "text-emerald-600" : isAboveTarget ? "text-amber-600" : "text-muted-foreground"
        )}>
          {currentCount}
        </span>
        <span className="text-muted-foreground/60">/ {targetMin}-{targetMax} 字</span>
      </div>
    </div>
  );
}
