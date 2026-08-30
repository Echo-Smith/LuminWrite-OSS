/**
 * Agent Step 卡片 — 单个步骤的渲染
 */
import { useState, useMemo, type ReactNode } from "react";
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
  ChevronDown,
  Code2,
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
    case "search_web":
    case "search_knowledge":
      return <SearchResults result={result} />;

    case "query_plan":
      return <QueryPlanResult result={result} />;

    case "memory_gate":
      return <MemoryGateResult result={result} />;

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
      return <JsonResultView result={result} />;
  }
}

function SearchResults({ result }: { result: Record<string, unknown> }) {
  // 兼容 Pipeline 模式（results 是数组）和 Harness 模式（items 是数组，results 是数量）
  const count = result.count as number | undefined;
  const results = (result.results as Array<Record<string, unknown>>) ?? (result.items as Array<Record<string, unknown>>) ?? [];
  const displayCount = count ?? results.length;

  return (
    <div className="space-y-1.5 text-sm">
      <div className="flex items-center gap-2">
        <span className="text-muted-foreground">检索结果：</span>
        <Badge variant="secondary">{displayCount} 条</Badge>
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
      {/* 如果结果有额外字段，展示 JSON */}
      {Object.keys(result).length > 2 && (
        <JsonResultView result={result} label="原始数据" defaultCollapsed={true} />
      )}
    </div>
  );
}

// ─── 查询计划结果 ─────────────────────────────────────────
function QueryPlanResult({ result }: { result: Record<string, unknown> }) {
  const queries = (result.queries as string[]) ?? [];
  const keywords = (result.keywords as string[]) ?? [];

  return (
    <div className="space-y-2 text-sm">
      {queries.length > 0 && (
        <div className="space-y-1">
          <span className="text-muted-foreground text-xs">查询计划：</span>
          {queries.map((q, i) => (
            <div key={i} className="rounded-md border border-border/40 bg-card/50 px-2.5 py-1.5 text-xs">
              <span className="text-muted-foreground/60 mr-1">Q{i + 1}:</span>
              {q}
            </div>
          ))}
        </div>
      )}
      {keywords.length > 0 && (
        <div>
          <span className="text-muted-foreground text-xs">关键词：</span>
          <div className="flex flex-wrap gap-1 mt-1">
            {keywords.map((kw, i) => (
              <Badge key={i} variant="outline" className="text-[10px]">{kw}</Badge>
            ))}
          </div>
        </div>
      )}
      {(queries.length === 0 && keywords.length === 0) && (
        <JsonResultView result={result} />
      )}
    </div>
  );
}

// ─── 记忆门控结果 ─────────────────────────────────────────
function MemoryGateResult({ result }: { result: Record<string, unknown> }) {
  const hit = result.hit as boolean | undefined;
  const reason = result.reason as string | undefined;
  const memories = (result.memories as Array<Record<string, unknown>>) ?? [];

  return (
    <div className="space-y-2 text-sm">
      <div className="flex items-center gap-2">
        <span className="text-muted-foreground">记忆检索：</span>
        {hit ? (
          <Badge variant="success">命中 {memories.length} 条</Badge>
        ) : (
          <Badge variant="outline">未命中</Badge>
        )}
      </div>
      {reason && (
        <p className="text-xs text-muted-foreground">{reason}</p>
      )}
      {memories.length > 0 && (
        <div className="space-y-1">
          {memories.slice(0, 3).map((m, i) => (
            <div key={i} className="rounded-md border border-border/40 bg-card/50 px-2.5 py-1.5 text-xs">
              <span className="text-muted-foreground/60">{String(m.content ?? m.text ?? "")}</span>
            </div>
          ))}
        </div>
      )}
      {Object.keys(result).length > 3 && (
        <JsonResultView result={result} label="原始数据" defaultCollapsed={true} />
      )}
    </div>
  );
}

// ─── JSON 格式化展示（可折叠 + 语法高亮） ────────────────
function highlightJson(jsonStr: string): ReactNode {
  const tokens: ReactNode[] = [];
  const regex = /("(?:[^"\\]|\\.)*"\s*:)|("(?:[^"\\]|\\.)*")|\b(true|false|null)\b|(-?\d+\.?\d*(?:[eE][+-]?\d+)?)|([{}[\],])/g;
  let lastIndex = 0;
  let key = 0;

  let match: RegExpExecArray | null;
  while ((match = regex.exec(jsonStr)) !== null) {
    if (match.index > lastIndex) {
      tokens.push(jsonStr.slice(lastIndex, match.index));
    }
    const [full, isKey, isString, isBool, isNum, isPunct] = match;
    if (isKey) {
      tokens.push(<span key={key++} className="text-purple-500 dark:text-purple-400">{full}</span>);
    } else if (isString) {
      tokens.push(<span key={key++} className="text-emerald-600 dark:text-emerald-400">{full}</span>);
    } else if (isBool) {
      tokens.push(<span key={key++} className="text-orange-500 dark:text-orange-400">{full}</span>);
    } else if (isNum) {
      tokens.push(<span key={key++} className="text-blue-500 dark:text-blue-400">{full}</span>);
    } else if (isPunct) {
      tokens.push(<span key={key++} className="text-muted-foreground">{full}</span>);
    } else {
      tokens.push(full);
    }
    lastIndex = regex.lastIndex;
  }
  if (lastIndex < jsonStr.length) {
    tokens.push(jsonStr.slice(lastIndex));
  }
  return tokens;
}

function JsonResultView({ result, label = "JSON 数据", defaultCollapsed = true }: { result: Record<string, unknown>; label?: string; defaultCollapsed?: boolean }) {
  const [collapsed, setCollapsed] = useState(defaultCollapsed);
  const prettyJson = useMemo(() => {
    try {
      return JSON.stringify(result, null, 2);
    } catch {
      return String(result);
    }
  }, [result]);

  // 截断过长的 JSON
  const maxLen = 2000;
  const truncated = prettyJson.length > maxLen;
  const displayJson = truncated ? prettyJson.slice(0, maxLen) + "\n... (已截断，共 " + prettyJson.length + " 字符)" : prettyJson;

  return (
    <div className="space-y-1">
      <button
        onClick={() => setCollapsed(!collapsed)}
        className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-ui"
        type="button"
      >
        <Code2 className="h-3.5 w-3.5" />
        <span>{label}</span>
        <ChevronDown className={"h-3 w-3 transition-transform " + (collapsed ? "" : "rotate-180")} />
      </button>
      {!collapsed && (
        <pre className="text-xs text-muted-foreground whitespace-pre-wrap overflow-x-auto rounded-md bg-muted/50 dark:bg-zinc-900/50 p-2.5 max-h-64 overflow-y-auto">
          <code className="font-mono text-xs">
            {highlightJson(displayJson)}
          </code>
        </pre>
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
  // Format 1: executeFactCheck tool result — { claims: N, verified: bool }
  const claimsCount = result.claims as number | undefined;
  const verified = result.verified as boolean | undefined;

  // Format 2: Jiaozhen single-claim result — { claim, status, source, content, error }
  const claim = String(result.claim ?? "");
  const status = String(result.status ?? "unknown");
  const source = String(result.source ?? "");
  const content = String(result.content ?? "");
  const error = String(result.error ?? "");

  // ── Format 1: Batch fact-check summary ──
  if (claimsCount !== undefined && claim === "") {
    return (
      <div className="space-y-2 text-sm">
        <div className="flex items-center gap-2">
          <ShieldCheck className={`h-4 w-4 ${verified ? "text-emerald-500" : "text-amber-500"}`} />
          <span className="font-medium">事实核查</span>
          <Badge variant="outline" className={verified ? "bg-emerald-50 text-emerald-700 border-emerald-200" : "bg-amber-50 text-amber-700 border-amber-200"}>
            {verified ? "已验证" : "仅提取"}
          </Badge>
          <Badge variant="secondary">{claimsCount} 条声明</Badge>
        </div>
        <p className="text-xs text-muted-foreground">
          {verified
            ? "已通过搜索引擎验证关键事实声明，请人工复核搜索结果。"
            : "搜索服务不可用，仅提取了事实性声明，请人工核实。"}
        </p>
      </div>
    );
  }

  // ── Format 2: Jiaozhen single-claim result ──
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
