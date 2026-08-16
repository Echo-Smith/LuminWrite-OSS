/**
 * TopicCard — 单个选题卡片
 */
import { Trash2, Pencil, Clock, Database } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { platformLabel, platformColor } from "@/lib/topic-helpers";
import type { Topic } from "@/lib/types";
import { useState, useEffect } from "react";
import { listTopicMaterials } from "@/lib/material-api";

interface TopicCardProps {
  topic: Topic;
  favorited: boolean;
  onOpen: (topic: Topic) => void;
  onDelete: (topicId: string) => void;
  onEdit?: (topic: Topic) => void;
}

export function TopicCard({ topic, favorited, onOpen, onDelete, onEdit }: TopicCardProps) {
  const [materialCount, setMaterialCount] = useState<number | null>(null);

  useEffect(() => {
    if (!topic.id) return;
    let cancelled = false;
    listTopicMaterials(topic.id)
      .then(({ total }) => { if (!cancelled) setMaterialCount(total); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [topic.id]);

  return (
    <div
      className="group relative cursor-pointer rounded-lg border p-4 transition-shadow hover:shadow-md"
      onClick={() => onOpen(topic)}
    >
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
        <div className="flex items-center gap-2">
          {topic.fetched_at ? (
            <span className="flex items-center gap-1 text-xs text-muted-foreground">
              <Clock className="h-3 w-3" />
              {new Date(topic.fetched_at).toLocaleDateString()}
            </span>
          ) : topic.favorited_at ? (
            <span className="flex items-center gap-1 text-xs text-yellow-600 dark:text-yellow-500">
              <Clock className="h-3 w-3" />
              {new Date(topic.favorited_at).toLocaleDateString()}
            </span>
          ) : <span />}
          {materialCount !== null && materialCount > 0 && (
            <Badge variant="outline" className="text-[10px] gap-0.5 text-emerald-600 dark:text-emerald-400">
              <Database className="h-2.5 w-2.5" />
              {materialCount}
            </Badge>
          )}
        </div>
        <span className="text-sm text-primary opacity-0 transition-opacity group-hover:opacity-100">
          查看详情 →
        </span>
      </div>
    </div>
  );
}
