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
import { Copy, Check, RefreshCw, ChevronRight, Brain, Download, FileText, FilePlus, Maximize2, Layers, Pencil, ChevronDown, FileType, Save, Loader2 } from "lucide-react";
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from "@/components/ui/collapsible";
import { Badge } from "@/components/ui/badge";
import type { ChatMessage, ToolCallPart, TextPart, DataPart, ReasoningPart, CompactionPart } from "@/stores/agent-store";
import type { WriteMode } from "@/lib/types";
import { exportMarkdown, exportWord, exportPDF } from "@/lib/export-utils";
import { useAgentStore } from "@/stores/agent-store";
import { MarkdownContent } from "./markdown-content";
import { OutlineTool } from "@/components/tools/outline-tool";
import { FeedbackBar } from "@/components/feedback/feedback-bar";
import { toast } from "@/stores/toast-store";
import { cn } from "@/lib/utils";
import { TypingDots, StreamingCursor, AnimatedCheck, ConfettiBurst, StreamText } from "@/components/animation";
import { CompactStepTimeline } from "@/components/tools/compact-step-timeline";

interface AssistantMessageProps {
  message: ChatMessage;
  traceId: string | null;
  version?: number;
  totalVersions?: number;
}

export function AssistantMessage({ message, traceId, version = 1, totalVersions = 1 }: AssistantMessageProps) {

  // 分离 tool-call parts 和其他 parts
  const toolCallParts = message.parts.filter(
    (p): p is ToolCallPart => p.type === "tool-call"
  );
  const reasoningParts = message.parts.filter(
    (p): p is ReasoningPart => p.type === "reasoning"
  );
  const compactionParts = message.parts.filter(
    (p): p is CompactionPart => p.type === "compaction"
  );
  const otherParts = message.parts.filter(
    (p) => p.type !== "tool-call" && p.type !== "reasoning" && p.type !== "compaction"
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
    <div className="px-4 py-3 anim-fade-up">
      <div className="space-y-2">
        {/* 对话历史压缩状态条 */}
        {compactionParts.length > 0 && (
          <CompactionBanner part={compactionParts[compactionParts.length - 1]} />
        )}

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

        {/* 流式文章输出 — 无对话框包裹，直接输出内容 */}
        {textParts.length > 0 && (
          <div>
            {/* 版本标签 + 文章标题 */}
            {((message.articleTitle || totalVersions > 1) && (
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
            ))}
            {textParts.map((part, i) => (
              <div key={i}>
                <StreamText streaming={part.streaming}>
                  <MarkdownContent content={part.text} />
                </StreamText>
                {part.streaming && (
                  <StreamingCursor active className="ml-0.5" />
                )}
              </div>
            ))}
            {/* 流式字数进度条 */}
            {isRunning && textParts.some((p) => p.streaming) && (
              <WordCountProgress text={textParts.map((t) => t.text).join("")} />
            )}
            {/* 消息操作按钮 */}
            {!isRunning && textParts.some((p) => p.text.trim()) && (
              <MessageActions text={textParts.map((t) => t.text).join("")} title={message.articleTitle} traceId={traceId} pointsUsed={message.pointsUsed} />
            )}
          </div>
        )}

        {/* text 之后的 data parts（仅 feedback，review 已移至右侧详情面板） */}
        {afterTextParts
          .filter((p): p is DataPart => p.type === "data" && p.dataType !== "review")
          .map((part, i) => (
            <DataPartRenderer key={`after-${i}`} part={part} traceId={traceId} article={textParts.map(t => t.text).join("")} />
          ))}

        {/* 等待提示 */}
        {!hasContent && isRunning && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground py-2 anim-fade-in">
            <TypingDots label="正在思考中" shimmer />
          </div>
        )}
      </div>
    </div>
  );
}

/**
 * 思考过程面板 — 仅显示当前步骤的思考内容
 *
 * 每步思考跟着步骤走（不再合并为一个文本块），
 * 每步完成后自动隐藏，仅保留最新步骤的思考内容。
 * 思考结束后输出文章。
 */
function ThinkingPanel({ parts, isRunning }: { parts: ReasoningPart[]; isRunning: boolean }) {
  // Only show the last (current, non-completed) reasoning part
  const lastPart = parts.length > 0 ? parts[parts.length - 1] : null;
  // If the last part is completed, show nothing (it's from a previous step)
  const currentText = lastPart && !lastPart.completed ? lastPart.text : "";
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

  if (!currentText.trim()) return null;

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
          <MarkdownContent content={currentText} />
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
      // Review content is displayed in the right-side detail panel (post_review step)
      return null;

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
          <div className="relative flex items-center">
            {passed && <ConfettiBurst count={10} />}
            <Badge variant={passed ? "success" : "destructive"}>
              {passed ? "通过" : "未通过"}
            </Badge>
          </div>
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
function MessageActions({ text, title, traceId, pointsUsed }: { text: string; title?: string; traceId: string | null; pointsUsed?: number }) {
  const [copied, setCopied] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editedText, setEditedText] = useState(text);
const [showExport, setShowExport] = useState(false);
const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  const handleCopy = () => {
    const fullText = title ? `${title}\n\n${text}` : text;
    navigator.clipboard.writeText(fullText);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
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
            <AnimatedCheck className="h-3.5 w-3.5 text-emerald-600" />
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
        onClick={() => { setEditedText(text); setEditing(!editing); }}
        className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground transition-ui"
        title="在线编辑文章"
      >
        <Pencil className="h-3.5 w-3.5" />
        <span>{editing ? "完成编辑" : "编辑"}</span>
      </button>
      <div className="relative">
        <button
          onClick={() => setShowExport(!showExport)}
          className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground transition-ui"
          title="导出文章"
        >
          <Download className="h-3.5 w-3.5" />
          <span>导出</span>
          <ChevronDown className="h-3 w-3" />
        </button>
        {showExport && (
          <>
            <div className="fixed inset-0 z-10" onClick={() => setShowExport(false)} />
            <div className="absolute right-0 top-full mt-1 z-20 rounded-md border border-border bg-background shadow-lg overflow-hidden anim-fade-in">
              <button
                onClick={() => { exportMarkdown(editing ? editedText : text, title); setShowExport(false); }}
                className="flex items-center gap-2 w-full px-3 py-1.5 text-xs text-foreground hover:bg-accent transition-ui"
              >
                <FileText className="h-3.5 w-3.5 text-muted-foreground" />
                <span>Markdown (.md)</span>
              </button>
              <button
                onClick={() => { exportWord(editing ? editedText : text, title); setShowExport(false); }}
                className="flex items-center gap-2 w-full px-3 py-1.5 text-xs text-foreground hover:bg-accent transition-ui"
              >
                <FileType className="h-3.5 w-3.5 text-blue-600" />
                <span>Word (.docx)</span>
              </button>
              <button
                onClick={() => { exportPDF(editing ? editedText : text, title); setShowExport(false); }}
                className="flex items-center gap-2 w-full px-3 py-1.5 text-xs text-foreground hover:bg-accent transition-ui"
              >
                <FileType className="h-3.5 w-3.5 text-red-600" />
                <span>PDF（打印）</span>
              </button>
            </div>
          </>
        )}
      </div>
      {pointsUsed != null && pointsUsed > 0 && (
        <div className="group relative ml-auto">
          <span className="flex items-center gap-1 rounded-md px-2 py-1 text-[10px] text-muted-foreground/60 cursor-default">
            <span>消耗 ~{pointsUsed.toFixed(1)} 积分</span>
          </span>
          {/* hover 显示详细说明 — 向下弹出避免截断 */}
          <div className="pointer-events-none absolute top-full right-0 mt-1 whitespace-nowrap rounded-md border border-border bg-popover px-2.5 py-1.5 text-[10px] text-muted-foreground opacity-0 shadow-lg z-50 transition-opacity group-hover:opacity-100">
            本次写作消耗约 {pointsUsed.toFixed(1)} 积分
          </div>
        </div>
      )}
      {editing && (
        <div className="w-full mt-2">
          <textarea
            value={editedText}
            onChange={(e) => setEditedText(e.target.value)}
            className="w-full min-h-[200px] rounded-md border border-border bg-background p-3 text-sm font-mono leading-relaxed resize-y focus:outline-none focus:ring-2 focus:ring-primary/40 transition-ui"
            placeholder="编辑文章内容（Markdown 格式）..."
          />
          <div className="flex items-center gap-3 mt-1">
            <button
              onClick={() => { setEditing(false); setEditedText(text); }}
              className="text-xs text-muted-foreground hover:text-foreground transition-ui"
            >
              取消
            </button>
            <button
              onClick={() => {
                navigator.clipboard.writeText(editedText);
                toast.success("已复制", "编辑后的内容已复制到剪贴板");
              }}
              className="text-xs text-muted-foreground hover:text-foreground transition-ui"
            >
              复制
            </button>
            {traceId && (
              <button
                onClick={async () => {
                  setSaving(true);
                  const ok = await useAgentStore.getState().saveArticleEdit(traceId, editedText, title);
                  setSaving(false);
                  if (ok) {
                    setSaved(true);
                    setEditing(false);
                    toast.success("已保存", "文章已更新，旧版本已自动归档");
                    setTimeout(() => setSaved(false), 2000);
                  } else {
                    toast.error("保存失败", "无法保存文章，请稍后重试");
                  }
                }}
                disabled={saving || editedText === text}
                className="flex items-center gap-1 text-xs text-emerald-600 hover:text-emerald-700 transition-ui disabled:opacity-50"
              >
                {saving ? <Loader2 className="h-3 w-3 animate-spin" /> : <Save className="h-3 w-3" />}
                保存
              </button>
            )}
            {saved && (
              <span className="text-xs text-emerald-600 flex items-center gap-1">
                <AnimatedCheck className="h-3 w-3" /> 已保存
              </span>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

/**
 * WordCountProgress — 流式写作中的实时字数进度条
 * 极简版：无外边框，只有进度条 + 当前字数
 * 从 API 动态获取所选风格的 word_range
 */

// 模块级缓存：slug → [min, max]
const styleWordRangeCache = new Map<string, [number, number]>();

function useStyleWordRange(slug: string): [number, number] {
  const [range, setRange] = useState<[number, number]>(
    () => styleWordRangeCache.get(slug) ?? [1000, 2000]
  );

  useEffect(() => {
    if (styleWordRangeCache.has(slug)) {
      setRange(styleWordRangeCache.get(slug)!);
      return;
    }
    const controller = new AbortController();
    fetch(`/api/v2/styles/${encodeURIComponent(slug)}`, { signal: controller.signal })
      .then((res) => res.json())
      .then((data) => {
        const p = data?.data ?? data;
        const wr = p?.word_range;
        if (wr) {
          const min = Array.isArray(wr) ? wr[0] : wr.min;
          const max = Array.isArray(wr) ? wr[1] : wr.max;
          const r: [number, number] = [min ?? 1000, max ?? 2000];
          styleWordRangeCache.set(slug, r);
          setRange(r);
        }
      })
      .catch(() => { /* 静默失败，用 fallback */ });
    return () => controller.abort();
  }, [slug]);

  return range;
}

function WordCountProgress({ text }: { text: string }) {
  const sessionStyle = useAgentStore((s) => {
    const session = s.sessions.find((sess) => sess.id === s.activeSessionId);
    return session?.style ?? "yinyue";
  });

  const [targetMin, targetMax] = useStyleWordRange(sessionStyle);

  // 计算中文字数（去除空白和标记符号）
  const cleanText = text.replace(/\s/g, "").replace(/[#*>\-_`~]/g, "");
  const currentCount = cleanText.length;
  const progressPercent = Math.min(100, (currentCount / targetMax) * 100);

  // 判断当前状态
  const isBelowTarget = currentCount < targetMin;
  const isInRange = currentCount >= targetMin && currentCount <= targetMax;

  // 以每 100 字为粒度触发 pop-in 动画，避免每帧闪烁
  const popInKey = Math.floor(currentCount / 100);

  return (
    <div className="mt-2 flex items-center gap-2.5 anim-fade-in">
      <div className="flex-1 h-1 rounded-full bg-muted/40 overflow-hidden">
        <div
          className={cn(
            "h-full rounded-full transition-all duration-300",
            isBelowTarget ? "bg-primary" : "bg-emerald-500"
          )}
          style={{ width: `${progressPercent}%` }}
        />
      </div>
      <span
        key={popInKey}
        className={cn(
          "text-xs tabular-nums font-medium anim-pop-in shrink-0",
          isInRange ? "text-emerald-600" : "text-muted-foreground"
        )}
      >
        {currentCount} / {targetMin}-{targetMax}
      </span>
    </div>
  );
}

/**
 * CompactionBanner — 对话历史压缩状态条
 * 当 Harness 对话历史执行 compaction 时显示，
 * 告知用户历史已被压缩为摘要，节省了多少 token。
 *
 * v3.0 增强：显示触发原因（消息数阈值 / Token 预算不足）
 * 和历史版本号，让用户了解压缩的上下文。
 */
function CompactionBanner({ part }: { part: CompactionPart }) {
  const [expanded, setExpanded] = useState(false);

  const triggerLabel = part.triggerReason === "token_budget"
    ? "Token 预算不足，自动压缩"
    : "对话过长，自动压缩";

  return (
    <div className="rounded-lg border border-blue-200/60 dark:border-blue-800/40 bg-blue-50/50 dark:bg-blue-950/20 px-3 py-2 anim-fade-in">
      <div className="flex items-center gap-2">
        <Layers className="h-3.5 w-3.5 text-blue-500 dark:text-blue-400 shrink-0" />
        <span className="text-xs font-medium text-blue-700 dark:text-blue-300">
          对话历史已压缩
        </span>
        <span className="text-xs text-muted-foreground">
          {part.originalMessages} 条消息 → {part.compactedMessages} 条摘要
        </span>
        <Badge variant="outline" className="text-xs gap-0.5 text-emerald-600 border-emerald-300/50 dark:text-emerald-400 dark:border-emerald-700/40">
          省 ~{part.savedTokens} tokens
        </Badge>
        {part.triggerReason && (
          <Badge variant="outline" className="text-xs gap-0.5 text-amber-600 border-amber-300/50 dark:text-amber-400 dark:border-amber-700/40">
            {triggerLabel}
          </Badge>
        )}
        {part.summaryPreview && (
          <button
            onClick={() => setExpanded(!expanded)}
            className="ml-auto text-xs text-blue-500 dark:text-blue-400 hover:underline transition-ui"
          >
            {expanded ? "收起" : "查看摘要"}
          </button>
        )}
      </div>
      {expanded && part.summaryPreview && (
        <div className="mt-2 pt-2 border-t border-blue-200/40 dark:border-blue-800/30">
          <p className="text-xs text-muted-foreground leading-relaxed">
            {part.summaryPreview}
          </p>
        </div>
      )}
    </div>
  );
}
