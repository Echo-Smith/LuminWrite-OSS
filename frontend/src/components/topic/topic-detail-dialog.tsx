/**
 * TopicDetailDialog — 选题详情弹窗（含 AI 写作角度、趋势图、相关文章）
 */
import {
  Star, TrendingUp, Lightbulb, ArrowRight, Loader2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription,
} from "@/components/ui/dialog";
import { platformLabel, platformColor, styleLabel } from "@/lib/topic-helpers";
import type { Topic, WritingAngle, RelatedArticle, TrendPoint } from "@/lib/types";
import { cn } from "@/lib/utils";

interface TopicDetailDialogProps {
  topic: Topic | null;
  loading: boolean;
  writingAngles: WritingAngle[];
  relatedArticles: RelatedArticle[];
  trendData: TrendPoint[];
  isFavorited: boolean;
  onToggleFavorite: () => void;
  onClose: () => void;
  onStartWriting: (angle?: WritingAngle) => void;
}

export function TopicDetailDialog({
  topic, loading, writingAngles, relatedArticles, trendData,
  isFavorited, onToggleFavorite, onClose, onStartWriting,
}: TopicDetailDialogProps) {
  if (!topic) return null;

  return (
    <Dialog open={!!topic} onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-hidden">
        <DialogHeader>
          <div className="flex items-start justify-between gap-2">
            <div className="flex-1">
              <div className="mb-2 flex items-center gap-2">
                {topic.hot_rank > 0 && (
                  <Badge className="bg-orange-100 text-orange-700 dark:bg-orange-950/50 dark:text-orange-300">
                    #{topic.hot_rank}
                  </Badge>
                )}
                <Badge className={platformColor(topic.platform)}>
                  {platformLabel(topic.platform)}
                </Badge>
                <Badge variant="secondary">{platformLabel(topic.source)}</Badge>
              </div>
              <DialogTitle className="text-xl">{topic.title}</DialogTitle>
              {topic.description && (
                <DialogDescription className="mt-1">{topic.description}</DialogDescription>
              )}
            </div>
            <button onClick={onToggleFavorite} className="flex-shrink-0 pt-1">
              <Star className={cn(
                "h-5 w-5 transition-colors",
                isFavorited
                  ? "fill-yellow-400 text-yellow-400"
                  : "text-gray-400 hover:text-yellow-400 dark:text-gray-500 dark:hover:text-yellow-400"
              )} />
            </button>
          </div>
        </DialogHeader>

        <ScrollArea className="max-h-[60vh]">
          <div className="space-y-4 pr-2">
            {loading && (
              <div className="flex items-center justify-center py-8">
                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
                <span className="ml-2 text-sm text-muted-foreground">AI 正在分析写作角度...</span>
              </div>
            )}

            {!loading && trendData.length > 1 && <TrendChart points={trendData} />}

            {!loading && writingAngles.length > 0 && (
              <div>
                <h3 className="mb-2 flex items-center gap-1.5 text-sm font-semibold">
                  <Lightbulb className="h-4 w-4 text-purple-600 dark:text-purple-400" />
                  AI 写作角度建议
                </h3>
                <div className="space-y-2">
                  {writingAngles.map((angle, i) => (
                    <div key={i} className="flex items-start gap-3 rounded-lg border p-3 transition-colors hover:bg-muted/50">
                      <div className="flex-1">
                        <div className="flex items-center gap-2">
                          <span className="font-medium text-sm">{angle.angle}</span>
                          <Badge variant="outline" className="text-xs">{styleLabel(angle.style)}</Badge>
                          {angle.word_count > 0 && (
                            <span className="text-xs text-muted-foreground">{angle.word_count}字</span>
                          )}
                        </div>
                        <p className="mt-1 text-xs text-muted-foreground">{angle.rationale}</p>
                      </div>
                      <Button size="sm" variant="ghost" className="flex-shrink-0 gap-1" onClick={() => onStartWriting(angle)}>
                        写作 <ArrowRight className="h-3 w-3" />
                      </Button>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {!loading && relatedArticles.length > 0 && (
              <div>
                <h3 className="mb-2 flex items-center gap-1.5 text-sm font-semibold">
                  <TrendingUp className="h-4 w-4 text-blue-600 dark:text-blue-400" />
                  相关文章 ({relatedArticles.length})
                </h3>
                <div className="space-y-2">
                  {relatedArticles.map((article) => (
                    <div key={article.trace_id} className="rounded-lg border p-3">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium">
                          {article.article_title || "未命名文章"}
                        </span>
                        {article.style_slug && (
                          <Badge variant="secondary" className="text-xs">{styleLabel(article.style_slug)}</Badge>
                        )}
                      </div>
                      {article.article_preview && (
                        <p className="mt-1 text-xs text-muted-foreground line-clamp-2">{article.article_preview}</p>
                      )}
                      <span className="mt-1 block text-xs text-muted-foreground">
                        {new Date(article.created_at).toLocaleString()}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {!loading && writingAngles.length === 0 && (
              <div className="py-4 text-center text-sm text-muted-foreground">
                <Lightbulb className="mx-auto mb-2 h-8 w-8 opacity-20" />
                暂无 AI 写作角度建议
              </div>
            )}
          </div>
        </ScrollArea>

        <div className="flex justify-between border-t pt-3">
          <Button variant="ghost" onClick={onToggleFavorite} className="gap-1.5">
            <Star className={cn("h-4 w-4", isFavorited && "fill-yellow-400 text-yellow-400")} />
            {isFavorited ? "已收藏" : "收藏"}
          </Button>
          <Button onClick={() => onStartWriting()} className="gap-1.5">
            直接写作 <ArrowRight className="h-4 w-4" />
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// ─── Trend Chart (SVG Sparkline) ─────────────────────────

function TrendChart({ points }: { points: TrendPoint[] }) {
  if (points.length < 2) return null;

  const width = 500;
  const height = 100;
  const padding = 20;

  const valid = points.filter((p) => p.hot_rank !== null && p.hot_rank !== undefined);
  if (valid.length < 2) return null;

  const minRank = Math.min(...valid.map((p) => p.hot_rank!));
  const maxRank = Math.max(...valid.map((p) => p.hot_rank!));
  const rankRange = maxRank - minRank || 1;
  const xStep = (width - padding * 2) / (points.length - 1 || 1);

  const pathData = points.map((p, i) => {
    if (p.hot_rank === null || p.hot_rank === undefined) return null;
    const x = padding + i * xStep;
    const y = padding + ((p.hot_rank - minRank) / rankRange) * (height - padding * 2);
    return `${i === 0 ? "M" : "L"} ${x.toFixed(1)} ${y.toFixed(1)}`;
  }).filter(Boolean).join(" ");

  const areaPath = pathData
    ? `${pathData} L ${(padding + (points.length - 1) * xStep).toFixed(1)} ${height - padding} L ${padding} ${height - padding} Z`
    : "";

  return (
    <div className="rounded-lg border bg-gradient-to-b from-orange-50/50 to-transparent p-3 dark:from-orange-950/20 dark:to-transparent">
      <h3 className="mb-2 flex items-center gap-1.5 text-sm font-semibold">
        <TrendingUp className="h-4 w-4 text-orange-600 dark:text-orange-400" />
        热度趋势（48小时）
      </h3>
      <svg viewBox={`0 0 ${width} ${height}`} className="w-full" style={{ height: "100px" }}>
        {areaPath && <path d={areaPath} fill="rgba(251, 146, 60, 0.15)" />}
        <path d={pathData} fill="none" stroke="rgb(251, 146, 60)" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
        {points.map((p, i) => {
          if (p.hot_rank === null || p.hot_rank === undefined) return null;
          const x = padding + i * xStep;
          const y = padding + ((p.hot_rank - minRank) / rankRange) * (height - padding * 2);
          return (
            <circle key={i} cx={x} cy={y} r="3" fill="rgb(251, 146, 60)" className="stroke-background" strokeWidth="1.5" />
          );
        })}
        <text x={padding - 5} y={padding + 4} textAnchor="end" className="fill-muted-foreground text-[10px]">#{minRank}</text>
        <text x={padding - 5} y={height - padding + 4} textAnchor="end" className="fill-muted-foreground text-[10px]">#{maxRank}</text>
      </svg>
      <div className="mt-1 flex justify-between text-xs text-muted-foreground">
        <span>{new Date(points[0].timestamp).toLocaleString()}</span>
        <span>最新排名: #{points[points.length - 1].hot_rank ?? "—"}</span>
        <span>{new Date(points[points.length - 1].timestamp).toLocaleString()}</span>
      </div>
    </div>
  );
}
