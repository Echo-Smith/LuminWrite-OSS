/**
 * RecommendationStrip — AI 推荐选题横幅（仅“全部选题”视图显示）
 */
import { Lightbulb, Loader2, RefreshCw } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
    <div className="mb-6 rounded-xl border bg-gradient-to-r from-purple-50 to-blue-50 p-4 dark:from-purple-950/30 dark:to-blue-950/30">
      <div className="mb-3 flex items-center gap-2">
        <Lightbulb className="h-4 w-4 text-purple-600 dark:text-purple-400" />
        <span className="text-sm font-medium text-purple-900 dark:text-purple-200">AI 推荐选题</span>
        {loading && <Loader2 className="h-3 w-3 animate-spin text-purple-400" />}
        {onRefresh && (
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto h-7 gap-1 text-purple-700 hover:bg-purple-100 dark:text-purple-300 dark:hover:bg-purple-900/30"
            onClick={onRefresh}
            disabled={loading}
          >
            <RefreshCw className={cn("h-3 w-3", loading && "animate-spin")} />
            换一批
          </Button>
        )}
      </div>
      <div className="flex gap-2 overflow-x-auto pb-1">
        {recommendations.map((topic) => (
          <div
            key={topic.id ?? topic.title}
            className="flex-shrink-0 cursor-pointer rounded-lg border bg-white px-3 py-2 shadow-sm transition-shadow hover:shadow-md dark:bg-card dark:border-border"
            onClick={() => onOpen(topic)}
          >
            <div className="flex items-center gap-2">
              {topic.hot_rank > 0 && (
                <Badge className="bg-orange-100 text-orange-700 dark:bg-orange-950/50 dark:text-orange-300">
                  #{topic.hot_rank}
                </Badge>
              )}
              <span className="text-sm font-medium">{topic.title}</span>
            </div>
            {topic.recommendation_reason && (
              <p className="mt-1 max-w-xs truncate text-xs text-muted-foreground">
                {topic.recommendation_reason}
              </p>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
