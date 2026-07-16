/**
 * TopicCard — 单个选题卡片
 */
import { Star, Trash2, Pencil, Clock } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { platformLabel, platformColor } from "@/lib/topic-helpers";
import type { Topic } from "@/lib/types";
import { cn } from "@/lib/utils";

interface TopicCardProps {
  topic: Topic;
  favorited: boolean;
  onOpen: (topic: Topic) => void;
  onToggleFavorite: (topic: Topic) => void;
  onDelete: (topicId: string) => void;
  onEdit?: (topic: Topic) => void;
}

export function TopicCard({ topic, favorited, onOpen, onToggleFavorite, onDelete, onEdit }: TopicCardProps) {
  return (
    <div
      className="group relative cursor-pointer rounded-lg border p-4 transition-shadow hover:shadow-md"
      onClick={() => onOpen(topic)}
    >
      <button
        className="absolute right-3 top-3 opacity-60 transition-opacity hover:opacity-100"
        onClick={(e) => { e.stopPropagation(); onToggleFavorite(topic); }}
      >
        <Star className={cn(
          "h-4 w-4",
          favorited ? "fill-yellow-400 text-yellow-400" : "text-gray-400 dark:text-gray-500"
        )} />
      </button>

      {topic.source === "user" && (
        <div className="absolute right-3 top-8 flex flex-col gap-2 opacity-0 transition-opacity group-hover:opacity-60">
          {onEdit && (
            <button
              className="hover:opacity-100"
              onClick={(e) => { e.stopPropagation(); onEdit(topic); }}
              title="编辑选题"
            >
              <Pencil className="h-4 w-4 text-blue-400 hover:text-blue-600 dark:text-blue-500 dark:hover:text-blue-400" />
            </button>
          )}
          <button
            className="hover:opacity-100"
            onClick={(e) => { e.stopPropagation(); onDelete(topic.id); }}
            title="删除选题"
          >
            <Trash2 className="h-4 w-4 text-red-400 hover:text-red-600 dark:text-red-500 dark:hover:text-red-400" />
          </button>
        </div>
      )}

      <div className="mb-2 flex items-center gap-2">
        {topic.hot_rank > 0 && (
          <Badge className="bg-orange-100 text-orange-700 hover:bg-orange-200 dark:bg-orange-950/50 dark:text-orange-300 dark:hover:bg-orange-900/50">
            #{topic.hot_rank}
          </Badge>
        )}
        <Badge className={platformColor(topic.platform)}>
          {platformLabel(topic.platform)}
        </Badge>
      </div>
      <h3 className="mb-2 pr-6 font-medium">{topic.title}</h3>
      {topic.description && (
        <p className="mb-3 line-clamp-2 text-sm text-muted-foreground">{topic.description}</p>
      )}
      <div className="flex items-center justify-between">
        {topic.fetched_at ? (
          <span className="flex items-center gap-1 text-xs text-muted-foreground">
            <Clock className="h-3 w-3" />
            {new Date(topic.fetched_at).toLocaleDateString()}
          </span>
        ) : topic.favorited_at ? (
          <span className="flex items-center gap-1 text-xs text-yellow-600 dark:text-yellow-500">
            <Star className="h-3 w-3 fill-yellow-400" />
            {new Date(topic.favorited_at).toLocaleDateString()}
          </span>
        ) : <span />}
        <span className="text-sm text-primary opacity-0 transition-opacity group-hover:opacity-100">
          查看详情 →
        </span>
      </div>
    </div>
  );
}
