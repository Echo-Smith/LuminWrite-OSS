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
import { STEP_LABELS } from "@/lib/constants";
import type { ToolCallPart } from "@/stores/agent-store";
import { cn } from "@/lib/utils";
import { MarkdownContent } from "@/components/assistant-ui/markdown-content";

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
    <div className="flex h-full w-80 flex-col border-l bg-background">
      {/* 头部 */}
      <div className="flex items-center justify-between px-4 py-3 border-b">
        <h3 className="text-sm font-medium">详情面板</h3>
        {onClose && (
          <button
            onClick={onClose}
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
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
 * 流程时间线
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

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between text-xs">
        <span className="text-muted-foreground">总耗时</span>
        <span className="font-medium tabular-nums">
          {(totalDuration / 1000).toFixed(1)}s
        </span>
      </div>

      <Separator />

      <div className="relative space-y-3">
        {parts.map((part, i) => (
          <div key={i} className="relative flex gap-3">
            {/* 时间线竖线 */}
            {i < parts.length - 1 && (
              <div className="absolute left-2 top-6 bottom-[-12px] w-px bg-border" />
            )}

            {/* 状态圆点 */}
            <div
              className={cn(
                "relative z-10 mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border-2",
                part.status === "complete" && "border-green-500 bg-green-500",
                part.status === "running" && "border-blue-500 bg-blue-500 animate-pulse",
                part.status === "error" && "border-red-500 bg-red-500"
              )}
            >
              {part.status === "complete" && (
                <span className="text-white text-[8px]">✓</span>
              )}
            </div>

            {/* 内容 */}
            <div className="flex-1 min-w-0 pb-1">
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium">
                  {STEP_LABELS[part.toolName] ?? part.toolName}
                </span>
                {part.durationMs && (
                  <span className="text-xs text-muted-foreground tabular-nums">
                    {(part.durationMs / 1000).toFixed(1)}s
                  </span>
                )}
              </div>
              {part.status === "running" && (
                <p className="text-xs text-blue-600 mt-0.5">执行中...</p>
              )}
              {part.error && (
                <p className="text-xs text-red-600 mt-0.5">{part.error}</p>
              )}
            </div>
          </div>
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
      <p className="text-xs text-muted-foreground">{results.length} 条素材</p>
      {results.map((r, i) => (
        <div
          key={i}
          className="rounded-lg border p-2.5 space-y-1 hover:bg-accent/50 transition-colors"
        >
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
