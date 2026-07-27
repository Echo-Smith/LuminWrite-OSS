/**
 * 右侧面板 — Trace 时间线 / 检索素材 / 风格信息
 */
import { useState } from "react";
import { ChevronRight, Clock, Globe, Palette, FileText } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { useAgentStore } from "@/stores/agent-store";
import type { ToolCallPart } from "@/stores/agent-store";
import { cn } from "@/lib/utils";
import { MarkdownContent } from "@/components/assistant-ui/markdown-content";
import { StaggerItem } from "@/components/animation";
import { AgentStepCard } from "@/components/tools/agent-step-card";

interface DetailPanelProps {
  onClose?: () => void;
}

export function DetailPanel({ onClose }: DetailPanelProps) {
  const [activeTab, setActiveTab] = useState("preview");

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

  // 从消息中提取文章文本
  const textParts = lastAssistant?.parts.filter((p) => p.type === "text") as { text: string }[] ?? [];
  const articleText = textParts.map((p) => p.text).join("");

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
            <TabsTrigger value="preview" className="flex-1">
              <FileText className="h-3.5 w-3.5 mr-1" />
              预览
            </TabsTrigger>
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
          <TabsContent value="preview" className="p-3 m-0">
            <ArticlePreview article={articleText} />
          </TabsContent>
          <TabsContent value="trace" className="p-3 m-0">
            <TraceTimeline parts={toolCallParts} />
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

/**
 * 文章预览
 */
function ArticlePreview({ article }: { article: string }) {
  if (!article || !article.trim()) {
    return (
      <div className="text-center text-xs text-muted-foreground py-8">
        文章生成后将在此预览
      </div>
    );
  }
  return (
    <div className="prose prose-sm max-w-none dark:prose-invert">
      <MarkdownContent content={article} />
    </div>
  );
}

/**
 * 流程时间线 — 使用完整的 AgentStepCard 展示详细信息
 */
function TraceTimeline({ parts }: { parts: ToolCallPart[] }) {
  if (parts.length === 0) {
    return (
      <div className="text-center text-xs text-muted-foreground py-8">
        开始写作后这里将显示 Agent 流程
      </div>
    );
  }

  const totalDuration = parts.reduce((sum, p) => sum + (p.durationMs ?? 0), 0);
  const runningCount = parts.filter((p) => p.status === "running").length;
  const completedCount = parts.filter((p) => p.status === "complete").length;
  const degradedCount = parts.filter((p) => p.status === "degraded").length;

  return (
    <div className="space-y-3">
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

      <div className="space-y-2">
        {parts.map((part, i) => (
          <StaggerItem key={i} index={i} interval={60} animation="fade-up">
            <AgentStepCard part={part} defaultOpen={part.status === "running"} />
          </StaggerItem>
        ))}
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
