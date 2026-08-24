/**
 * 右侧面板 — Agent 协同流程 / 检索素材 / 风格信息
 *
 * 删除了冗余的"预览"Tab（文章已在主区域展示）
 * 流程Tab优化为多Agent协同视图，按阶段分组展示
 */
import { useState, useEffect, useCallback } from "react";
import { ChevronRight, Clock, Globe, Palette, Bot, Brain, Search, PenLine, ShieldCheck, Sparkles, Database, FileText, History, Loader2, BookOpen, type LucideIcon } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { useAgentStore } from "@/stores/agent-store";
import { useAuthStore } from "@/stores/auth-store";
import type { ToolCallPart } from "@/stores/agent-store";
import { toast } from "@/stores/toast-store";
import { cn } from "@/lib/utils";
import { StaggerItem } from "@/components/animation";
import { AgentStepCard } from "@/components/tools/agent-step-card";
import { CompactStepTimeline } from "@/components/tools/compact-step-timeline";

interface DetailPanelProps {
  onClose?: () => void;
}

// ─── Agent 角色映射 ──────────────────────────────────────

interface AgentRole {
  name: string;
  icon: LucideIcon;
  color: string;
  bgColor: string;
  steps: string[];
}

const AGENT_ROLES: AgentRole[] = [
  {
    name: "意图理解",
    icon: Brain,
    color: "text-blue-600 dark:text-blue-400",
    bgColor: "bg-blue-50 dark:bg-blue-950/30",
    steps: ["intent"],
  },
  {
    name: "记忆检索",
    icon: Database,
    color: "text-cyan-600 dark:text-cyan-400",
    bgColor: "bg-cyan-50 dark:bg-cyan-950/30",
    steps: ["memory_gate", "retrieve_context"],
  },
  {
    name: "素材研究",
    icon: Search,
    color: "text-amber-600 dark:text-amber-400",
    bgColor: "bg-amber-50 dark:bg-amber-950/30",
    steps: ["query_plan", "search", "search_web", "search_knowledge", "read_source", "relevance", "compress"],
  },
  {
    name: "结构规划",
    icon: Bot,
    color: "text-purple-600 dark:text-purple-400",
    bgColor: "bg-purple-50 dark:bg-purple-950/30",
    steps: ["outline", "generate_outline"],
  },
  {
    name: "写作生成",
    icon: PenLine,
    color: "text-indigo-600 dark:text-indigo-400",
    bgColor: "bg-indigo-50 dark:bg-indigo-950/30",
    steps: ["write", "write_article", "chat"],
  },
  {
    name: "质量保障",
    icon: ShieldCheck,
    color: "text-green-600 dark:text-green-400",
    bgColor: "bg-green-50 dark:bg-green-950/30",
    steps: ["post_review", "auto_fix", "review_article", "revise_section", "word_count_check", "rewrite_title", "fact_check"],
  },
  {
    name: "学习沉淀",
    icon: Sparkles,
    color: "text-pink-600 dark:text-pink-400",
    bgColor: "bg-pink-50 dark:bg-pink-950/30",
    steps: ["memory_extract"],
  },
];

export function DetailPanel({ onClose }: DetailPanelProps) {
  const [activeTab, setActiveTab] = useState("trace");

  const session = useAgentStore((s) => s.sessions.find((sess) => sess.id === s.activeSessionId));
  const messages = session?.messages ?? [];

  // 提取最后一条 assistant 消息中的 tool-call parts
  const lastAssistant = [...messages].reverse().find((m) => m.role === "assistant");
  const toolCallParts = (lastAssistant?.parts.filter(
    (p): p is ToolCallPart => p.type === "tool-call"
  ) ?? []) as ToolCallPart[];

  // 从 search step 的 result 中提取素材
  // 兼容 Pipeline 模式（toolName="search"）和 Harness 模式（toolName="search_web"/"search_knowledge"）
  const searchStep = toolCallParts.find((p) => p.toolName === "search" || p.toolName === "search_web");
  const searchResults = searchStep?.result as { results?: Array<Record<string, unknown>> | number; items?: Array<Record<string, unknown>> } | undefined;
  // Harness 模式的 result 中 items 是搜索结果数组，results 是数量（数字）
  // Pipeline 模式的 result 中 results 是搜索结果数组
  const webResults = Array.isArray(searchResults?.items) ? searchResults!.items! : (Array.isArray(searchResults?.results) ? searchResults!.results as Array<Record<string, unknown>> : []);

  // 从 search_knowledge step 提取素材库检索结果
  const kbSearchStep = toolCallParts.find((p) => p.toolName === "search_knowledge");
  const kbSearchResults = kbSearchStep?.result as { results?: Array<Record<string, unknown>> | number; items?: Array<Record<string, unknown>> } | undefined;
  const kbResults = Array.isArray(kbSearchResults?.items) ? kbSearchResults!.items! : (Array.isArray(kbSearchResults?.results) ? kbSearchResults!.results as Array<Record<string, unknown>> : []);

  // 从 memory_gate step 提取记忆检索结果
  // 兼容 Pipeline 模式（toolName="memory_gate"）和 Harness 模式（无对应步骤）
  const memoryStep = toolCallParts.find((p) => p.toolName === "memory_gate");
  const memoryResult = memoryStep?.result as { memories?: Array<Record<string, unknown>>; hit?: boolean; reason?: string } | undefined;
  const memories = memoryResult?.memories ?? [];
  const memoryHit = memoryResult?.hit ?? false;

  // 从 query_plan step 提取查询计划
  const queryPlanStep = toolCallParts.find((p) => p.toolName === "query_plan");
  const queryPlanResult = queryPlanStep?.result as { queries?: string[]; keywords?: string[] } | undefined;
  const queries = queryPlanResult?.queries ?? [];
  const keywords = queryPlanResult?.keywords ?? [];

  // 选题素材和用户素材
  const injectedMaterials = session?.injectedMaterials ?? [];

  return (
    <div className="flex h-full w-full sm:w-80 flex-col sm:border-l bg-surface anim-slide-left">
      {/* 头部 — 高度与主 header 一致，无分割线 */}
      <div className="flex h-12 shrink-0 items-center justify-between px-4">
        <h3 className="text-sm font-medium">详情面板</h3>
        {onClose && (
          <button
            onClick={onClose}
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-ui"
          >
            <ChevronRight className="h-4 w-4" />
            收起
          </button>
        )}
      </div>

      {/* Tabs */}
      <Tabs value={activeTab} onValueChange={setActiveTab} className="flex-1 flex flex-col overflow-hidden">
        <div className="px-2 pt-2">
          <TabsList className="w-full">
            <TabsTrigger value="trace" className="flex-1">
              <Clock className="h-3.5 w-3.5 mr-1" />
              流程
            </TabsTrigger>
            <TabsTrigger value="sources" className="flex-1">
              <Globe className="h-3.5 w-3.5 mr-1" />
              素材
            </TabsTrigger>
            <TabsTrigger value="style" className="flex-1">
              <Palette className="h-3.5 w-3.5 mr-1" />
              风格
            </TabsTrigger>
            <TabsTrigger value="versions" className="flex-1">
              <History className="h-3.5 w-3.5 mr-1" />
              版本
            </TabsTrigger>
          </TabsList>
        </div>

        <ScrollArea className="flex-1">
          <TabsContent value="trace" className="p-3 m-0">
            {/* 紧凑步骤时间线 — 与对话框同步 */}
            {toolCallParts.length > 0 && (
              <div className="mb-3">
                <CompactStepTimeline parts={toolCallParts} isRunning={lastAssistant?.status === "running"} />
              </div>
            )}
            <AgentCollaborationFlow parts={toolCallParts} />
          </TabsContent>
          <TabsContent value="sources" className="p-3 m-0">
            <SourcesList webResults={webResults} kbResults={kbResults} memories={memories} memoryHit={memoryHit} queries={queries} keywords={keywords} injectedMaterials={injectedMaterials} />
          </TabsContent>
          <TabsContent value="style" className="p-3 m-0">
            <StyleInfo slug={session?.style ?? "yinyue"} />
          </TabsContent>
          <TabsContent value="versions" className="p-3 m-0">
            <VersionHistory traceId={session?.traceId ?? null} />
          </TabsContent>
        </ScrollArea>
      </Tabs>
    </div>
  );
}

// ─── 多 Agent 协同流程 ──────────────────────────────────

function AgentCollaborationFlow({ parts }: { parts: ToolCallPart[] }) {
  if (parts.length === 0) {
    return (
      <div className="text-center text-xs text-muted-foreground py-8">
        开始写作后这里将显示 Agent 协同流程
      </div>
    );
  }

  const totalDuration = parts.reduce((sum, p) => sum + (p.durationMs ?? 0), 0);
  const runningCount = parts.filter((p) => p.status === "running").length;
  const completedCount = parts.filter((p) => p.status === "complete").length;
  const degradedCount = parts.filter((p) => p.status === "degraded").length;

  // 按Agent角色分组
  const roleGroups: { role: AgentRole; parts: ToolCallPart[] }[] = [];
  for (const role of AGENT_ROLES) {
    const roleParts = parts.filter((p) => role.steps.includes(p.toolName));
    if (roleParts.length > 0) {
      roleGroups.push({ role, parts: roleParts });
    }
  }

  return (
    <div className="space-y-3">
      {/* 总览统计 */}
      <div className="flex items-center justify-between text-xs">
        <span className="text-muted-foreground">
          {runningCount > 0 ? `${completedCount}/${parts.length} 步骤完成` : `${parts.length} 步骤`}
          {degradedCount > 0 && <span className="text-amber-600 dark:text-amber-400"> · {degradedCount} 跳过</span>}
        </span>
        <span className="font-medium tabular-nums">
          {(totalDuration / 1000).toFixed(1)}s
        </span>
      </div>

      <Separator />

      {/* Agent 协同流程图 */}
      <div className="space-y-1">
        {roleGroups.map((group, gi) => {
          const Icon = group.role.icon;
          const isAnyRunning = group.parts.some((p) => p.status === "running");
          const isAllComplete = group.parts.every((p) => p.status === "complete" || p.status === "degraded");
          const groupDuration = group.parts.reduce((sum, p) => sum + (p.durationMs ?? 0), 0);

          return (
            <div key={gi}>
              {/* 连接线 */}
              {gi > 0 && (
                <div className="flex justify-center py-0.5">
                  <div className={cn(
                    "h-4 w-px",
                    isAllComplete ? "bg-emerald-300 dark:bg-emerald-700" : "bg-border"
                  )} />
                </div>
              )}

              {/* Agent 角色头部 */}
              <div className={cn(
                "flex items-center gap-2 rounded-lg px-2.5 py-1.5 mb-1.5 transition-all",
                group.role.bgColor,
                isAnyRunning && "ring-1 ring-primary/30"
              )}>
                <div className={cn(
                  "flex h-6 w-6 items-center justify-center rounded-md shrink-0",
                  isAnyRunning ? "bg-background" : "bg-background/50"
                )}>
                  {isAnyRunning ? (
                    <div className="h-2.5 w-2.5 rounded-full bg-primary anim-pulse" />
                  ) : isAllComplete ? (
                    <Icon className={cn("h-3.5 w-3.5", group.role.color)} />
                  ) : (
                    <Icon className={cn("h-3.5 w-3.5 text-muted-foreground")} />
                  )}
                </div>
                <span className={cn(
                  "text-xs font-medium flex-1",
                  isAllComplete ? group.role.color : "text-muted-foreground"
                )}>
                  {group.role.name}
                </span>
                {groupDuration > 0 && (
                  <span className="text-[10px] text-muted-foreground tabular-nums">
                    {(groupDuration / 1000).toFixed(1)}s
                  </span>
                )}
                {group.parts.length > 1 && (
                  <Badge variant="outline" className="text-[10px] py-0 px-1.5">
                    {group.parts.length}
                  </Badge>
                )}
              </div>

              {/* 该角色下的步骤卡片 */}
              <div className="space-y-1.5 pl-2 border-l-2 border-dashed border-border/50 ml-[13px]">
                {group.parts.map((part, pi) => (
                  <StaggerItem key={`${gi}-${pi}`} index={pi} interval={40} animation="fade-up">
                    <AgentStepCard part={part} defaultOpen={part.status === "running"} />
                  </StaggerItem>
                ))}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

/**
 * 检索素材列表 — 按来源分类展示：选题素材 / 检索计划 / 记忆系统 / 网络搜索 / 素材库检索
 */
function SourcesList({
  webResults,
  kbResults,
  memories,
  memoryHit,
  queries,
  keywords,
  injectedMaterials = [],
}: {
  webResults: Array<Record<string, unknown>>;
  kbResults: Array<Record<string, unknown>>;
  memories: Array<Record<string, unknown>>;
  memoryHit: boolean;
  queries: string[];
  keywords: string[];
  injectedMaterials?: string[];
}) {
  const hasAnything = webResults.length > 0 || kbResults.length > 0 || memories.length > 0 || queries.length > 0 || keywords.length > 0 || injectedMaterials.length > 0;

  if (!hasAnything) {
    return (
      <div className="text-center text-xs text-muted-foreground py-8">
        写作开始后将显示检索到的素材
      </div>
    );
  }

  const relevanceColor: Record<string, string> = {
    strong: "success",
    medium: "secondary",
    weak: "outline",
    conflict: "destructive",
  };

  return (
    <div className="space-y-4">
      {/* 选题素材 */}
      {injectedMaterials.length > 0 && (
        <div className="space-y-2">
          <div className="flex items-center gap-1.5 text-xs font-medium text-emerald-600 dark:text-emerald-400">
            <FileText className="h-3.5 w-3.5" />
            选题素材
            <span className="font-mono-sm">{injectedMaterials.length} 条</span>
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            {injectedMaterials.map((mat, i) => (
              <StaggerItem key={i} index={i} interval={40} animation="fade-up">
                <span
                  className="flex items-center gap-1 rounded-md bg-emerald-50 dark:bg-emerald-950/20 border border-emerald-200/40 dark:border-emerald-900/40 px-2 py-0.5 text-xs text-emerald-700 dark:text-emerald-400 max-w-[200px] truncate"
                  title={mat}
                >
                  {mat.startsWith("📎 ") ? mat.slice(3) : mat}
                </span>
              </StaggerItem>
            ))}
          </div>
        </div>
      )}

      {/* 检索计划 */}
      {(queries.length > 0 || keywords.length > 0) && (
        <div className="space-y-2">
          <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
            <Search className="h-3.5 w-3.5" />
            检索计划
          </div>
          {queries.length > 0 && (
            <div className="space-y-1">
              {queries.map((q, i) => (
                <div key={i} className="rounded-md border border-border/40 bg-card/50 px-2.5 py-1.5 text-xs">
                  <span className="text-muted-foreground/60 mr-1">Q{i + 1}:</span>
                  {q}
                </div>
              ))}
            </div>
          )}
          {keywords.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {keywords.map((kw, i) => (
                <Badge key={i} variant="outline" className="text-[10px]">{kw}</Badge>
              ))}
            </div>
          )}
        </div>
      )}

      {/* 记忆系统 */}
      {memories.length > 0 && (
        <div className="space-y-2">
          <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
            <Database className="h-3.5 w-3.5" />
            记忆检索
            {memoryHit ? (
              <Badge variant="success" className="text-[10px] py-0 px-1.5">命中</Badge>
            ) : (
              <Badge variant="outline" className="text-[10px] py-0 px-1.5">未命中</Badge>
            )}
          </div>
          {memories.map((m, i) => (
            <StaggerItem key={i} index={i} interval={40} animation="fade-up">
              <div className="rounded-lg border border-cyan-200/50 dark:border-cyan-900/30 bg-cyan-50/30 dark:bg-cyan-950/10 p-2.5 space-y-1 hover:bg-cyan-50/50 dark:hover:bg-cyan-950/20 transition-ui">
                <div className="flex items-center gap-2">
                  <Badge variant="outline" className="text-xs shrink-0 text-cyan-700 dark:text-cyan-400">
                    {String(m.type ?? "memory")}
                  </Badge>
                  {m.score != null && (
                    <span className="text-[10px] text-muted-foreground tabular-nums">
                      相关度 {(Number(m.score) * 100).toFixed(0)}%
                    </span>
                  )}
                </div>
                <p className="text-xs text-muted-foreground line-clamp-3">{String(m.content ?? m.summary ?? "")}</p>
              </div>
            </StaggerItem>
          ))}
        </div>
      )}

      {/* 网络搜索结果 */}
      {webResults.length > 0 && (
        <div className="space-y-2">
          <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
            <Globe className="h-3.5 w-3.5" />
            联网搜索
            <span className="font-mono-sm">{webResults.length} 条</span>
          </div>
          {webResults.map((r, i) => (
            <StaggerItem key={i} index={i} interval={40} animation="fade-up">
              <div className="rounded-lg border border-blue-200/50 dark:border-blue-900/30 bg-blue-50/20 dark:bg-blue-950/10 p-2.5 space-y-1 hover:bg-blue-50/40 dark:hover:bg-blue-950/20 transition-ui">
                <div className="flex items-center gap-2">
                  <Badge variant="outline" className="text-xs shrink-0 text-blue-700 dark:text-blue-400">
                    {String(r.source ?? "web")}
                  </Badge>
                  {r.relevance != null && (
                    <Badge variant={(relevanceColor[String(r.relevance)] as "success" | "secondary" | "outline" | "destructive") ?? "outline"} className="text-xs">
                      {String(r.relevance)}
                    </Badge>
                  )}
                </div>
                <p className="text-sm font-medium line-clamp-1">{String(r.title ?? "")}</p>
                {r.snippet != null && String(r.snippet) && (
                  <p className="text-xs text-muted-foreground line-clamp-2">{String(r.snippet)}</p>
                )}
                {r.url != null && String(r.url) && (
                  <a
                    href={String(r.url)}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-xs text-primary hover:underline"
                  >
                    查看原文 →
                  </a>
                )}
              </div>
            </StaggerItem>
          ))}
        </div>
      )}

      {/* 素材库检索结果 */}
      {kbResults.length > 0 && (
        <div className="space-y-2">
          <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
            <BookOpen className="h-3.5 w-3.5" />
            素材库检索
            <span className="font-mono-sm">{kbResults.length} 条</span>
          </div>
          {kbResults.map((r, i) => (
            <StaggerItem key={i} index={i} interval={40} animation="fade-up">
              <div className="rounded-lg border border-purple-200/50 dark:border-purple-900/30 bg-purple-50/20 dark:bg-purple-950/10 p-2.5 space-y-1 hover:bg-purple-50/40 dark:hover:bg-purple-950/20 transition-ui">
                <div className="flex items-center gap-2">
                  <Badge variant="outline" className="text-xs shrink-0 text-purple-700 dark:text-purple-400">
                    {String(r.source ?? "kb")}
                  </Badge>
                  {r.score != null && (
                    <span className="text-[10px] text-muted-foreground tabular-nums">
                      相关度 {(Number(r.score) * 100).toFixed(0)}%
                    </span>
                  )}
                </div>
                <p className="text-sm font-medium line-clamp-1">{String(r.title ?? "")}</p>
                {r.snippet != null && String(r.snippet) && (
                  <p className="text-xs text-muted-foreground line-clamp-3">{String(r.snippet)}</p>
                )}
                {r.doc_title != null && String(r.doc_title) && (
                  <p className="text-[10px] text-muted-foreground/70">来源文档：{String(r.doc_title)}</p>
                )}
              </div>
            </StaggerItem>
          ))}
        </div>
      )}
    </div>
  );
}

// ─── 版本历史 ────────────────────────────────────────────

interface ArticleVersion {
  version_id: string;
  article_title?: string;
  version_note?: string;
  created_at: string;
}

function VersionHistory({ traceId }: { traceId: string | null }) {
  const [versions, setVersions] = useState<ArticleVersion[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadingVersion, setLoadingVersion] = useState<string | null>(null);

  const fetchVersions = useCallback(async () => {
    if (!traceId) return;
    setLoading(true);
    try {
      const res = await fetch(`/api/v2/sessions/${traceId}/versions`);
      const json = await res.json();
      if (json.success) {
        setVersions(json.data?.versions ?? []);
      }
    } catch {
      // silent fail
    } finally {
      setLoading(false);
    }
  }, [traceId]);

  useEffect(() => {
    if (traceId) {
      fetchVersions();
    } else {
      setVersions([]);
    }
  }, [traceId, fetchVersions]);

  if (!traceId) {
    return (
      <div className="text-center text-xs text-muted-foreground py-8">
        写作完成后将显示版本历史
      </div>
    );
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-8">
        <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (versions.length === 0) {
    return (
      <div className="text-center text-xs text-muted-foreground py-8">
        暂无历史版本
      </div>
    );
  }

  const handleRestore = async (versionId: string) => {
    setLoadingVersion(versionId);
    const ok = await useAgentStore.getState().loadArticleVersion(traceId, versionId);
    setLoadingVersion(null);
    if (ok) {
      toast.info("已切换版本", "文章内容已更新为该版本");
    } else {
      toast.error("加载失败", "无法获取该版本内容");
    }
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground mb-1">
        <History className="h-3.5 w-3.5" />
        版本历史
        <Badge variant="outline" className="text-[10px] py-0 px-1.5 ml-auto">
          {versions.length}
        </Badge>
      </div>
      {versions.map((v, i) => (
        <StaggerItem key={v.version_id} index={i} interval={30} animation="fade-up">
          <button
            onClick={() => handleRestore(v.version_id)}
            disabled={loadingVersion === v.version_id}
            className="w-full text-left rounded-lg border border-border/60 bg-card/50 hover:bg-accent/50 hover:border-primary/30 transition-ui p-2.5 disabled:opacity-50 group"
          >
            <div className="flex items-start justify-between gap-2">
              <div className="flex-1 min-w-0">
                <p className="text-xs font-medium truncate group-hover:text-primary transition-ui">
                  {v.article_title || `版本 ${versions.length - i}`}
                </p>
                <p className="text-[10px] text-muted-foreground mt-0.5">
                  {v.version_note || "自动保存"}
                </p>
                <p className="text-[10px] text-muted-foreground/70">
                  {new Date(v.created_at).toLocaleString("zh-CN")}
                </p>
              </div>
              {loadingVersion === v.version_id ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin text-primary shrink-0 mt-0.5" />
              ) : (
                <FileText className="h-3.5 w-3.5 text-muted-foreground group-hover:text-primary shrink-0 mt-0.5 transition-ui" />
              )}
            </div>
          </button>
        </StaggerItem>
      ))}
    </div>
  );
}

/**
 * 风格信息 — 从 API 动态获取
 */
function StyleInfo({ slug }: { slug: string }) {
  const [info, setInfo] = useState<{
    name: string;
    description: string;
    word_range: { min: number; max: number };
    tags: string[];
    structure?: { type?: string; argument_pattern?: string; argument_variations?: string[] };
    rhetoric?: { required_metaphor?: boolean; required_parallelism?: boolean; required_rhetorical_question?: boolean };
  } | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    const token = useAuthStore.getState().token;
    const headers: Record<string, string> = {};
    if (token) headers.Authorization = `Bearer ${token}`;
    fetch(`/api/v2/styles/${encodeURIComponent(slug)}`, { headers })
      .then((res) => res.json())
      .then((data) => {
        if (cancelled) return;
        const p = data?.data ?? data;
        if (p && p.name) {
          setInfo(p);
        } else {
          setInfo(null);
        }
      })
      .catch(() => { if (!cancelled) setInfo(null); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [slug]);

  if (loading) {
    return (
      <div className="flex items-center justify-center py-8">
        <div className="h-4 w-4 animate-spin rounded-full border-2 border-muted-foreground/30 border-t-muted-foreground" />
      </div>
    );
  }

  if (!info) {
    return (
      <div className="text-center text-xs text-muted-foreground py-8">
        未知风格
      </div>
    );
  }

  const structureTypeMap: Record<string, string> = {
    three_part: "三段式",
    free_form: "自由式",
    custom: "自定义",
  };

  const rhetoricItems: string[] = [];
  if (info.rhetoric?.required_metaphor) rhetoricItems.push("比喻");
  if (info.rhetoric?.required_parallelism) rhetoricItems.push("排比");
  if (info.rhetoric?.required_rhetorical_question) rhetoricItems.push("设问");

  return (
    <div className="space-y-4">
      <div>
        <h4 className="text-sm font-medium mb-1">{info.name}</h4>
        <p className="text-xs text-muted-foreground">{info.description}</p>
      </div>

      <Separator />

      <div className="space-y-2">
        <div className="flex justify-between text-xs">
          <span className="text-muted-foreground">篇幅范围</span>
          <span className="font-medium">{info.word_range?.min ?? "?"}-{info.word_range?.max ?? "?"} 字</span>
        </div>
        <div className="flex justify-between text-xs">
          <span className="text-muted-foreground">结构类型</span>
          <span className="font-medium">{structureTypeMap[info.structure?.type ?? ""] ?? info.structure?.type ?? "—"}</span>
        </div>
        {info.structure?.argument_pattern && (
          <div className="flex justify-between text-xs">
            <span className="text-muted-foreground">递进模式</span>
            <span className="font-medium text-right max-w-[60%]">{info.structure.argument_pattern}</span>
          </div>
        )}
        {info.structure?.argument_variations && info.structure.argument_variations.length > 0 && (
          <div className="space-y-1">
            <span className="text-xs text-muted-foreground">变式选项</span>
            <div className="flex flex-wrap gap-1">
              {info.structure.argument_variations.map((v, i) => (
                <Badge key={i} variant="outline" className="text-[10px]">{v}</Badge>
              ))}
            </div>
          </div>
        )}
        {rhetoricItems.length > 0 && (
          <div className="flex justify-between text-xs">
            <span className="text-muted-foreground">修辞要求</span>
            <span className="font-medium">{rhetoricItems.join(" + ")}</span>
          </div>
        )}
      </div>

      {info.tags && info.tags.length > 0 && (
        <>
          <Separator />
          <div>
            <p className="text-xs text-muted-foreground mb-2">标签</p>
            <div className="flex flex-wrap gap-1.5">
              {info.tags.map((tag) => (
                <Badge key={tag} variant="secondary">{tag}</Badge>
              ))}
            </div>
          </div>
        </>
      )}
    </div>
  );
}
