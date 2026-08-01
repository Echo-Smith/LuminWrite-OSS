/**
 * TopicDetailDialog — 选题详情弹窗（含 AI 写作角度、趋势图、相关文章、关联素材）
 */
import { useState, useEffect, useCallback, useRef } from "react";
import {
  Star, TrendingUp, Lightbulb, ArrowRight, Loader2,
  Database, Plus, Trash2, Zap, FileText, Search,
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
import {
  type TopicMaterialAssociation,
  type UserMaterial,
  listTopicMaterials, removeMaterialAssociation, autoAssociateMaterials,
  listMaterials, associateMaterial,
} from "@/lib/material-api";

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

// ─── Topic Materials Section ────────────────────────────

function TopicMaterialsSection({ topicId, topicTitle }: { topicId: string; topicTitle: string }) {
  const [associations, setAssociations] = useState<TopicMaterialAssociation[]>([]);
  const [loading, setLoading] = useState(false);
  const [autoLoading, setAutoLoading] = useState(false);
  const [showPicker, setShowPicker] = useState(false);
  const [availableMaterials, setAvailableMaterials] = useState<UserMaterial[]>([]);
  const [pickerLoading, setPickerLoading] = useState(false);

  const loadAssociations = useCallback(async () => {
    setLoading(true);
    try {
      const { associations } = await listTopicMaterials(topicId);
      setAssociations(associations);
      // 如果当前无关联素材且未触发过自动关联，自动触发一次
      if (associations.length === 0 && !autoTriggeredRef.current) {
        autoTriggeredRef.current = true;
        setLoading(false);
        setAutoLoading(true);
        try {
          await autoAssociateMaterials(topicId, topicTitle, 5);
          const { associations: updated } = await listTopicMaterials(topicId);
          setAssociations(updated);
        } catch {
          // 忽略自动关联失败
        } finally {
          setAutoLoading(false);
        }
        return;
      }
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, [topicId, topicTitle]);

  const autoTriggeredRef = useRef(false);

  useEffect(() => {
    loadAssociations();
  }, [loadAssociations]);

  const handleAutoAssociate = async () => {
    setAutoLoading(true);
    try {
      await autoAssociateMaterials(topicId, topicTitle, 5);
      await loadAssociations();
    } catch {
      // ignore
    } finally {
      setAutoLoading(false);
    }
  };

  const handleRemoveAssociation = async (materialId: string) => {
    try {
      await removeMaterialAssociation(topicId, materialId);
      await loadAssociations();
    } catch {
      // ignore
    }
  };

  const handleShowPicker = async () => {
    setShowPicker(!showPicker);
    if (!showPicker) {
      setPickerLoading(true);
      try {
        const { materials } = await listMaterials(1, 50);
        const associatedIds = new Set(associations.map((a) => a.material_id));
        setAvailableMaterials(materials.filter((m) => !associatedIds.has(m.id)));
      } catch {
        // ignore
      } finally {
        setPickerLoading(false);
      }
    }
  };

  const handleManualAssociate = async (materialId: string) => {
    try {
      await associateMaterial(topicId, materialId);
      await loadAssociations();
      setShowPicker(false);
    } catch {
      // ignore
    }
  };

  return (
    <div>
      <div className="mb-2 flex items-center justify-between">
        <h3 className="flex items-center gap-1.5 text-sm font-semibold">
          <Database className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />
          关联素材 ({associations.length})
        </h3>
        <div className="flex gap-1">
          <Button size="sm" variant="ghost" onClick={handleAutoAssociate} disabled={autoLoading} title="自动关联">
            {autoLoading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Zap className="h-3.5 w-3.5" />}
            自动
          </Button>
          <Button size="sm" variant="ghost" onClick={handleShowPicker} title="手动关联">
            <Plus className="h-3.5 w-3.5" />
            手动
          </Button>
        </div>
      </div>

      {/* Associated Materials */}
      {loading ? (
        <div className="flex items-center gap-2 py-3 text-sm text-muted-foreground">
          <Loader2 className="h-3.5 w-3.5 animate-spin" /> 加载中...
        </div>
      ) : associations.length === 0 ? (
        <div className="py-3 text-center text-sm text-muted-foreground">
          <Database className="mx-auto mb-1 h-6 w-6 opacity-20" />
          暂无关联素材
        </div>
      ) : (
        <div className="space-y-1.5">
          {associations.map((assoc) => (
            <div key={assoc.id} className="flex items-start gap-2 rounded-lg border p-2">
              <FileText className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-1.5">
                  <span className="text-sm font-medium truncate">
                    {assoc.material?.title || "未知素材"}
                  </span>
                  <Badge variant="outline" className="text-xs">
                    {assoc.association_type === "auto" ? "自动" : "手动"}
                  </Badge>
                  {assoc.relevance_score ? (
                    <span className="text-xs text-muted-foreground">
                      {(assoc.relevance_score * 100).toFixed(0)}%
                    </span>
                  ) : null}
                </div>
                {assoc.material?.content_preview && (
                  <p className="text-xs text-muted-foreground line-clamp-1 mt-0.5">
                    {assoc.material.content_preview}
                  </p>
                )}
              </div>
              <Button
                size="sm"
                variant="ghost"
                className="flex-shrink-0 h-6 w-6 p-0 text-muted-foreground hover:text-destructive"
                onClick={() => handleRemoveAssociation(assoc.material_id)}
              >
                <Trash2 className="h-3 w-3" />
              </Button>
            </div>
          ))}
        </div>
      )}

      {/* Material Picker */}
      {showPicker && (
        <div className="mt-2 rounded-lg border bg-muted/30 p-2 space-y-1.5 max-h-48 overflow-y-auto">
          {pickerLoading ? (
            <div className="text-center py-2">
              <Loader2 className="h-4 w-4 animate-spin mx-auto text-muted-foreground" />
            </div>
          ) : availableMaterials.length === 0 ? (
            <p className="text-center text-xs text-muted-foreground py-2">
              没有可选素材，请先在「我的素材库」中上传
            </p>
          ) : (
            availableMaterials.map((mat) => (
              <button
                key={mat.id}
                onClick={() => handleManualAssociate(mat.id)}
                className="w-full flex items-center gap-2 rounded-md p-2 text-left hover:bg-accent transition-colors"
              >
                <FileText className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                <div className="flex-1 min-w-0">
                  <span className="text-sm font-medium truncate block">{mat.title}</span>
                  <span className="text-xs text-muted-foreground truncate block">
                    {mat.content_preview || mat.file_name || "—"}
                  </span>
                </div>
                <Plus className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
              </button>
            ))
          )}
        </div>
      )}
    </div>
  );
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
              {topic.url && (
                <a
                  href={topic.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="mt-1.5 inline-flex items-center gap-1 text-xs text-blue-600 hover:underline dark:text-blue-400"
                >
                  <FileText className="h-3 w-3" />
                  查看原文
                </a>
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

            {/* Topic-Material Association Section */}
            {!loading && topic.id && (
              <TopicMaterialsSection topicId={topic.id} topicTitle={topic.title} />
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
