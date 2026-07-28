/**
 * 右侧面板 — Agent 协同流程 / 检索素材 / 风格信息
 *
 * 删除了冗余的"预览"Tab（文章已在主区域展示）
 * 流程Tab优化为多Agent协同视图，按阶段分组展示
 */
import { useState } from "react";
import { ChevronRight, Clock, Globe, Palette, Bot, Brain, Search, PenLine, ShieldCheck, Sparkles, Database, type LucideIcon } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { useAgentStore } from "@/stores/agent-store";
import type { ToolCallPart } from "@/stores/agent-store";
import { cn } from "@/lib/utils";
import { StaggerItem } from "@/components/animation";
import { AgentStepCard } from "@/components/tools/agent-step-card";

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
    steps: ["memory_gate"],
  },
  {
    name: "素材研究",
    icon: Search,
    color: "text-amber-600 dark:text-amber-400",
    bgColor: "bg-amber-50 dark:bg-amber-950/30",
    steps: ["query_plan", "search", "relevance", "compress"],
  },
  {
    name: "结构规划",
    icon: Bot,
    color: "text-purple-600 dark:text-purple-400",
    bgColor: "bg-purple-50 dark:bg-purple-950/30",
    steps: ["outline"],
  },
  {
    name: "写作生成",
    icon: PenLine,
    color: "text-indigo-600 dark:text-indigo-400",
    bgColor: "bg-indigo-50 dark:bg-indigo-950/30",
    steps: ["write", "chat"],
  },
  {
    name: "质量保障",
    icon: ShieldCheck,
    color: "text-green-600 dark:text-green-400",
    bgColor: "bg-green-50 dark:bg-green-950/30",
    steps: ["post_review", "auto_fix"],
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
  const searchStep = toolCallParts.find((p) => p.toolName === "search");
  const searchResults = searchStep?.result as { results?: Array<Record<string, unknown>> } | undefined;
  const results = searchResults?.results ?? [];

  return (
    <div className="flex h-full w-80 flex-col border-l bg-surface anim-slide-left">
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
          </TabsList>
        </div>

        <ScrollArea className="flex-1">
          <TabsContent value="trace" className="p-3 m-0">
            <AgentCollaborationFlow parts={toolCallParts} />
          </TabsContent>
          <TabsContent value="sources" className="p-3 m-0">
            <SourcesList results={results} />
          </TabsContent>
          <TabsContent value="style" className="p-3 m-0">
            <StyleInfo slug={session?.style ?? "yinyue"} />
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
 * 检索素材列表
 */
function SourcesList({ results }: { results: Array<Record<string, unknown>> }) {
  if (results.length === 0) {
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
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground font-mono-sm">{results.length} 条素材</p>
      {results.map((r, i) => (
        <StaggerItem key={i} index={i} interval={40} animation="fade-up">
          <div className="rounded-lg border p-2.5 space-y-1 hover:bg-accent/50 transition-ui ">
            <div className="flex items-center gap-2">
              <Badge variant="outline" className="text-xs shrink-0">
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
  );
}

/**
 * 风格信息
 */
function StyleInfo({ slug }: { slug: string }) {
  // 静态展示（未来从 API 加载）
  const styleMap: Record<string, { name: string; desc: string; range: string; tags: string[] }> = {
    yinyue: { name: "预设评论风格", desc: "植根于杭州时评专栏的深度评论风格", range: "1000-1500 字", tags: ["政论", "民生", "深度评论"] },
    shenlun: { name: "应用文风格", desc: "[style description removed]", range: "800-1200 字", tags: ["申论", "公考"] },
    xiaohongshu: { name: "小红书风格", desc: "轻松种草风格", range: "300-800 字", tags: ["社交媒体", "种草"] },
  };

  const info = styleMap[slug];

  if (!info) {
    return (
      <div className="text-center text-xs text-muted-foreground py-8">
        未知风格
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div>
        <h4 className="text-sm font-medium mb-1">{info.name}</h4>
        <p className="text-xs text-muted-foreground">{info.desc}</p>
      </div>

      <Separator />

      <div className="space-y-2">
        <div className="flex justify-between text-xs">
          <span className="text-muted-foreground">篇幅范围</span>
          <span className="font-medium">{info.range}</span>
        </div>
        <div className="flex justify-between text-xs">
          <span className="text-muted-foreground">结构类型</span>
          <span className="font-medium">三段式闭环</span>
        </div>
        <div className="flex justify-between text-xs">
          <span className="text-muted-foreground">递进模式</span>
          <span className="font-medium">首在-重在-贵在</span>
        </div>
        <div className="flex justify-between text-xs">
          <span className="text-muted-foreground">修辞要求</span>
          <span className="font-medium">比喻+排比+设问</span>
        </div>
      </div>

      <Separator />

      <div>
        <p className="text-xs text-muted-foreground mb-2">标签</p>
        <div className="flex flex-wrap gap-1.5">
          {info.tags.map((tag) => (
            <Badge key={tag} variant="secondary">{tag}</Badge>
          ))}
        </div>
      </div>
    </div>
  );
}
