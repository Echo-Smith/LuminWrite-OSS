/**
 * Agent Step 卡片 — 单个步骤的渲染
 */
import { useState } from "react";
import {
  Brain,
  Search as SearchIcon,
  Globe,
  Filter,
  ListTree,
  PenLine,
  ShieldCheck,
  Wrench,
  Database,
  Sparkles,
  MessageCircle,
  BookOpen,
  FileText,
  Hash,
  Type,
  SearchCheck,
  Split,
  Check,
  X,
  Loader2,
  ChevronRight,
  AlertTriangle,
  type LucideIcon,
} from "lucide-react";
import type { ToolCallPart } from "@/stores/agent-store";
import { STEP_LABELS, STEP_DESCRIPTIONS, STEP_ICONS } from "@/lib/constants";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { PulseIndicator } from "@/components/animation";

const ICON_MAP: Record<string, LucideIcon> = {
  Brain,
  Search: SearchIcon,
  Globe,
  Filter,
  ListTree,
  PenLine,
  ShieldCheck,
  Wrench,
  Database,
  Sparkles,
  MessageCircle,
  BookOpen,
  FileText,
  Hash,
  Type,
  SearchCheck,
  Split,
};

const STEP_ICON_KEY: Record<string, string> = STEP_ICONS;

interface AgentStepCardProps {
  part: ToolCallPart;
  defaultOpen?: boolean;
}

export function AgentStepCard({ part, defaultOpen = false }: AgentStepCardProps) {
  const [expanded, setExpanded] = useState(defaultOpen);

  const Icon = ICON_MAP[STEP_ICON_KEY[part.toolName]] ?? Brain;
  const label = STEP_LABELS[part.toolName] ?? part.toolName;
  const description = STEP_DESCRIPTIONS[part.toolName] ?? "";

  const isRunning = part.status === "running";
  const isComplete = part.status === "complete";
  const isError = part.status === "error";
  const isDegraded = part.status === "degraded";

  return (
    <div
      className={cn(
        "rounded-lg border transition-ui ",
        isRunning && "border-primary/30 bg-primary/5",
        isComplete && "border-emerald-200/50 bg-emerald-50/30 dark:bg-emerald-950/10",
        isError && "border-red-200/50 bg-red-50/30 dark:bg-red-950/10",
        isDegraded && "border-amber-200/50 bg-amber-50/30 dark:bg-amber-950/10",
        !isRunning && !isComplete && !isError && !isDegraded && "border-border bg-card"
      )}
    >
      {/* 头部 */}
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex w-full items-center gap-3 px-3 py-2 text-left"
      >
        <div
          className={cn(
            "flex h-7 w-7 shrink-0 items-center justify-center rounded-md transition-ui",
            isRunning && "bg-muted text-foreground",
            isComplete && "bg-emerald-100 text-emerald-600 dark:bg-emerald-950/40 dark:text-emerald-400",
            isError && "bg-red-100 text-red-600 dark:bg-red-950/40 dark:text-red-400",
            isDegraded && "bg-amber-100 text-amber-600 dark:bg-amber-950/40 dark:text-amber-400",
            !isRunning && !isComplete && !isError && !isDegraded && "bg-muted text-muted-foreground"
          )}
        >
          {isRunning ? (
            <Loader2 className="h-4 w-4 anim-spin" />
          ) : isComplete ? (
            <Check className="h-4 w-4" />
          ) : isError ? (
            <X className="h-4 w-4" />
          ) : isDegraded ? (
            <AlertTriangle className="h-4 w-4" />
          ) : (
            <Icon className="h-4 w-4" />
          )}
        </div>

        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">{label}</span>
            {isDegraded && (
              <Badge variant="outline" className="bg-amber-100 text-amber-700 dark:bg-amber-950/40 dark:text-amber-400">已跳过</Badge>
            )}
            {part.durationMs && (
              <span className="text-xs text-muted-foreground font-mono-sm">
                {(part.durationMs / 1000).toFixed(1)}s
              </span>
            )}
          </div>
          {!expanded && (
            <p className="text-xs text-muted-foreground truncate">
              {isDegraded && part.error ? part.error : description}
            </p>
          )}
        </div>

        <ChevronRight
          className={cn(
            "h-4 w-4 shrink-0 text-muted-foreground transition-ui",
            expanded && "rotate-90"
          )}
        />
      </button>

      {/* 展开内容 */}
      {expanded && (
        <div className="border-t px-3 py-2 anim-fade-in">
          <StepResult part={part} />
        </div>
      )}
    </div>
  );
}

function StepResult({ part }: { part: ToolCallPart }) {
  if (part.status === "degraded" && part.error) {
    return (
      <div className="space-y-1 text-sm">
        <div className="flex items-center gap-2 text-amber-700 dark:text-amber-400">
          <AlertTriangle className="h-3.5 w-3.5" />
          <span>步骤已跳过（非关键步骤失败）</span>
        </div>
        <p className="text-xs text-amber-600 dark:text-amber-500 pl-5">{part.error}</p>
      </div>
    );
  }

  if (part.error) {
    return <p className="text-sm text-red-600">{part.error}</p>;
  }

  if (!part.result) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <PulseIndicator status="running" size="sm" ring={false} />
        <span>{STEP_DESCRIPTIONS[part.toolName] ?? "执行中..."}</span>
      </div>
    );
  }

  // 根据步骤类型渲染不同结果
  const result = part.result as Record<string, unknown>;

  switch (part.toolName) {
    case "intent":
      return (
        <div className="space-y-1 text-sm">
          <div className="flex gap-2">
            <span className="text-muted-foreground">任务类型：</span>
            <Badge variant="secondary">{String(result.taskMode ?? result.mode ?? "writing")}</Badge>
          </div>
          {result.normalizedInput != null && (
            <div className="flex gap-2">
              <span className="text-muted-foreground">归一化：</span>
              <span>{String(result.normalizedInput)}</span>
            </div>
          )}
        </div>
      );

    case "search":
      return <SearchResults result={result} />;

    case "relevance":
      return (
        <div className="space-y-1 text-sm">
          <div className="flex gap-2">
            <span className="text-muted-foreground">过滤后素材：</span>
            <Badge variant="success">{String(result.count ?? 0)} 条</Badge>
          </div>
          {result.deduped !== undefined && (
            <div className="flex gap-2">
              <span className="text-muted-foreground">去重移除：</span>
              <span>{String(result.deduped)} 条</span>
            </div>
          )}
        </div>
      );

    case "post_review":
      return <ReviewResult result={result} />;

    case "fact_check":
      return <FactCheckResult result={result} />;

    default:
      return (
        <pre className="text-xs text-muted-foreground whitespace-pre-wrap overflow-x-auto">
          {JSON.stringify(result, null, 2)}
        </pre>
      );
  }
}

function SearchResults({ result }: { result: Record<string, unknown> }) {
  const count = result.count as number | undefined;
  const results = (result.results as Array<Record<string, unknown>>) ?? [];

  return (
    <div className="space-y-1.5 text-sm">
      <div className="flex items-center gap-2">
        <span className="text-muted-foreground">检索结果：</span>
        <Badge variant="secondary">{count ?? results.length} 条</Badge>
      </div>
      {results.length > 0 && (
        <div className="space-y-1">
          {results.slice(0, 5).map((r, i) => (
            <div key={i} className="flex items-start gap-2 text-xs">
              <Badge variant="outline" className="shrink-0">{String(r.source ?? "web")}</Badge>
              <span className="truncate">{String(r.title ?? "")}</span>
            </div>
          ))}
          {results.length > 5 && (
            <p className="text-xs text-muted-foreground">还有 {results.length - 5} 条...</p>
          )}
        </div>
      )}
    </div>
  );
}

function ReviewResult({ result }: { result: Record<string, unknown> }) {
  const scores = (result.scores ?? {}) as Record<string, number>;
  const passed = result.passed as boolean | undefined;
  const issues = (result.issues ?? []) as Array<Record<string, unknown>>;

  return (
    <div className="space-y-2 text-sm">
      <div className="flex items-center gap-2">
        <span className="text-muted-foreground">质量检查：</span>
        {passed ? (
          <Badge variant="success">通过</Badge>
        ) : (
          <Badge variant="destructive">未通过</Badge>
        )}
      </div>
      <div className="flex flex-wrap gap-3">
        {Object.entries(scores).map(([dim, score]) => (
          <div key={dim} className="flex items-center gap-1 text-xs">
            <span className="text-muted-foreground">{dim}:</span>
            <span className="font-medium">{(score * 100).toFixed(0)}</span>
          </div>
        ))}
      </div>
      {issues.length > 0 && (
        <div className="space-y-1">
          {issues.map((issue, i) => (
            <div
              key={i}
              className={cn(
                "text-xs",
                issue.severity === "high" && "text-red-600",
                issue.severity === "medium" && "text-amber-600",
                issue.severity === "low" && "text-muted-foreground"
              )}
            >
              ⚠ {String(issue.message ?? "")}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// ─── 事实核查结果 ─────────────────────────────────────────

function FactCheckResult({ result }: { result: Record<string, unknown> }) {
  const claim = String(result.claim ?? "");
  const status = String(result.status ?? "unknown");
  const source = String(result.source ?? "");
  const content = String(result.content ?? "");
  const error = String(result.error ?? "");

  const isOk = status === "ok";
  const isSkipped = status === "skipped";
  const isError = status === "error";

  return (
    <div className="space-y-2 text-sm">
      <div className="flex items-center gap-2">
        <ShieldCheck className={`h-4 w-4 ${isOk ? "text-emerald-500" : isError ? "text-red-500" : "text-muted-foreground"}`} />
        <span className="font-medium">事实核查</span>
        <Badge
          variant="outline"
          className={
            isOk
              ? "bg-emerald-50 text-emerald-700 border-emerald-200"
              : isError
                ? "bg-red-50 text-red-700 border-red-200"
                : "bg-gray-50 text-gray-700 border-gray-200"
          }
        >
          {isOk ? "已查证" : isSkipped ? "跳过" : isError ? "失败" : status}
        </Badge>
        {source && (
          <span className="text-xs text-muted-foreground">来源：{source}</span>
        )}
      </div>

      {claim && (
        <div className="flex gap-2">
          <span className="text-muted-foreground shrink-0">声明：</span>
          <span className="text-xs">{claim}</span>
        </div>
      )}

      {content && (
        <div className="mt-2 p-2 bg-muted/30 rounded text-xs leading-relaxed whitespace-pre-wrap max-h-[200px] overflow-y-auto">
          {content}
        </div>
      )}

      {error && (
        <div className="flex items-center gap-1.5 text-xs text-red-600">
          <AlertTriangle className="h-3.5 w-3.5" />
          <span>{error}</span>
        </div>
      )}
    </div>
  );
}
