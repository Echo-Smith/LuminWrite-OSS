/**
 * RecommendationStrip — AI 推荐选题（豆包风格：垂直列排、细边框按钮）
 */
import { Lightbulb, Loader2, RefreshCw } from "lucide-react";
import type { Topic } from "@/lib/types";
import { cn } from "@/lib/utils";

interface RecommendationStripProps {
  recommendations: Topic[];
  loading: boolean;
  onOpen: (topic: Topic) => void;
  onRefresh?: () => void;
}

export function RecommendationStrip({ recommendations, loading, onOpen, onRefresh }: RecommendationStripProps) {
  if (recommendations.length === 0 && !loading) return null;

  return (
    <div className="mb-6">
      {/* 标题行 */}
      <div className="mb-3 flex items-center gap-2 px-1">
        <Lightbulb className="h-4 w-4 text-muted-foreground/60" />
        <span className="text-[12px] text-muted-foreground/60">AI 推荐选题</span>
        {loading && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground/60" />}
        {onRefresh && (
          <button
            onClick={onRefresh}
            disabled={loading}
            className="ml-auto flex items-center gap-1 h-7 rounded-lg px-2 text-xs text-muted-foreground transition-ui hover:bg-accent hover:text-foreground disabled:opacity-50"
          >
            <RefreshCw className={cn("h-3 w-3", loading && "animate-spin")} />
            换一批
          </button>
        )}
      </div>
      {/* 垂直列排推荐按钮 */}
      <div className="flex flex-col gap-2.5">
        {recommendations.map((topic) => (
          <button
            key={topic.id ?? topic.title}
            onClick={() => onOpen(topic)}
            className="flex w-full items-center gap-2 h-[42px] rounded-xl border border-border/5 px-3 text-left text-sm text-foreground transition-ui hover:bg-accent/60"
          >
            {topic.hot_rank > 0 && (
              <span className="shrink-0 rounded-md bg-orange-100 px-1.5 py-0.5 text-[11px] font-medium text-orange-700 dark:bg-orange-950/50 dark:text-orange-300">
                #{topic.hot_rank}
              </span>
            )}
            <span className="truncate font-medium">{topic.title}</span>
            {topic.recommendation_reason && (
              <span className="ml-auto hidden truncate text-xs text-muted-foreground/60 sm:inline">
                {topic.recommendation_reason}
              </span>
            )}
          </button>
        ))}
      </div>
    </div>
  );
}
