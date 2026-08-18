/**
 * 反馈分析页面 — Admin Dashboard
 */
import { useState, useEffect, useCallback } from "react";
import { RefreshCw, Sparkles, TrendingUp, ThumbsUp, MessageSquare, Star } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { adminFetch, adminMutate } from "@/lib/admin-api";
import { AdminPageHeader } from "@/components/admin";
import { toast } from "@/stores/toast-store";

interface Aggregation {
  style_slug: string;
  profile_version: number;
  total_feedback: number;
  total_adopted: number;
  avg_rating: number;
  weighted_score: number;
  dimension_scores: Record<string, number>;
  segment_breakdown: Record<string, {
    count: number;
    good: number;
    bad: number;
    suggestion: number;
    comments: string[];
  }>;
  improvement_suggestions: string;
  ready_for_iteration: boolean;
  period_start: string;
  period_end: string;
}

interface AggregationListItem {
  style_slug: string;
  profile_version: number;
  total_feedback: number;
  total_adopted: number;
  avg_rating: number;
  weighted_score: number;
  ready_for_iteration: boolean;
  created_at: string;
}

const DIMENSION_LABELS: Record<string, string> = {
  title: "标题",
  paragraph: "段落",
  sentence: "句子",
  overall: "整体",
};

export function FeedbackAnalysisPage() {
  const [aggregations, setAggregations] = useState<AggregationListItem[]>([]);
  const [selected, setSelected] = useState<Aggregation | null>(null);
  const [loading, setLoading] = useState(false);
  const [generating, setGenerating] = useState(false);

  const loadAggregations = useCallback(async () => {
    setLoading(true);
    const { success, data } = await adminFetch<{ aggregations: AggregationListItem[] }>("/api/v2/feedback/aggregation", { silent: true });
    if (success && data) setAggregations(data.aggregations ?? []);
    setLoading(false);
  }, []);

  const loadDetail = async (style: string, version: number) => {
    const { success, data } = await adminFetch<Aggregation>(`/api/v2/feedback/aggregation/${style}/${version}`, { silent: true });
    if (success && data) setSelected(data);
  };

  const handleAggregate = async () => {
    setLoading(true);
    const styles = ["yinyue", "shenlun", "xiaohongshu"];
    for (const style of styles) {
      await adminMutate("/api/v2/feedback/aggregate", {
        method: "POST",
        body: JSON.stringify({ style_slug: style, profile_version: 1 }),
        silent: true,
      });
    }
    toast.success("反馈已重新聚合");
    await loadAggregations();
  };

  const handleGenerateSuggestions = async () => {
    if (!selected) return;
    setGenerating(true);
    const { success } = await adminMutate(`/api/v2/feedback/suggestions/${selected.style_slug}/${selected.profile_version}`, {
      method: "POST",
      successTitle: "建议已生成",
    });
    if (success) await loadDetail(selected.style_slug, selected.profile_version);
    setGenerating(false);
  };

  useEffect(() => {
    loadAggregations();
  }, [loadAggregations]);

  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="反馈分析"
        action={
          <Button variant="outline" size="sm" onClick={handleAggregate} disabled={loading}>
            <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} />
            重新聚合
          </Button>
        }
      />

      {/* 指标卡片 */}
      <div className="grid grid-cols-4 gap-4">
        <MetricCard
          icon={<MessageSquare className="h-4 w-4" />}
          label="总反馈数"
          value={selected ? String(selected.total_feedback) : "—"}
        />
        <MetricCard
          icon={<ThumbsUp className="h-4 w-4" />}
          label="录用数"
          value={selected ? String(selected.total_adopted) : "—"}
        />
        <MetricCard
          icon={<Star className="h-4 w-4" />}
          label="平均评分"
          value={selected ? selected.avg_rating.toFixed(2) : "—"}
        />
        <MetricCard
          icon={<TrendingUp className="h-4 w-4" />}
          label="加权得分"
          value={selected ? selected.weighted_score.toFixed(2) : "—"}
        />
      </div>

      {/* 选择器 */}
      <div className="flex items-center gap-4">
        <span className="text-sm text-muted-foreground">选择风格/版本：</span>
        <Select
          onValueChange={(val) => {
            const [style, ver] = val.split(":");
            loadDetail(style, parseInt(ver));
          }}
        >
          <SelectTrigger className="w-72">
            <SelectValue placeholder="选择聚合记录" />
          </SelectTrigger>
          <SelectContent>
            {aggregations.map((agg) => (
              <SelectItem key={`${agg.style_slug}:${agg.profile_version}`} value={`${agg.style_slug}:${agg.profile_version}`}>
                {agg.style_slug} v{agg.profile_version} — {agg.total_feedback} 条反馈
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {selected?.ready_for_iteration && (
          <Badge variant="default" className="bg-green-500">达到迭代阈值</Badge>
        )}
      </div>

      {/* 维度得分 */}
      {selected?.dimension_scores && Object.keys(selected.dimension_scores).length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">维度得分</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              {Object.entries(selected.dimension_scores).map(([dim, score]) => (
                <div key={dim} className="flex items-center gap-4">
                  <span className="w-20 text-sm text-muted-foreground">
                    {DIMENSION_LABELS[dim] ?? dim}
                  </span>
                  <div className="flex-1 h-6 bg-muted rounded-full overflow-hidden">
                    <div
                      className="h-full bg-primary rounded-full flex items-center justify-end px-2"
                      style={{ width: `${(score / 5) * 100}%` }}
                    >
                      <span className="text-xs text-primary-foreground font-medium">{score.toFixed(2)}</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* 分段反馈明细 */}
      {selected?.segment_breakdown && Object.keys(selected.segment_breakdown).length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">分段反馈明细</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-4">
              {Object.entries(selected.segment_breakdown).map(([segType, data]) => (
                <div key={segType} className="border rounded-lg p-3">
                  <div className="flex items-center gap-2 mb-2">
                    <span className="font-medium text-sm">{DIMENSION_LABELS[segType] ?? segType}</span>
                    <Badge variant="secondary">{data.count} 条</Badge>
                    {data.good > 0 && <Badge className="bg-green-100 text-green-700">好评 {data.good}</Badge>}
                    {data.bad > 0 && <Badge className="bg-red-100 text-red-700">差评 {data.bad}</Badge>}
                    {data.suggestion > 0 && <Badge className="bg-blue-100 text-blue-700">建议 {data.suggestion}</Badge>}
                  </div>
                  {data.comments?.length > 0 && (
                    <div className="space-y-1">
                      {data.comments.slice(0, 5).map((c, i) => (
                        <p key={i} className="text-xs text-muted-foreground">"{c}"</p>
                      ))}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* 改进建议 */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base">改进建议</CardTitle>
          <Button variant="outline" size="sm" onClick={handleGenerateSuggestions} disabled={!selected || generating}>
            <Sparkles className={`h-4 w-4 mr-2 ${generating ? "animate-spin" : ""}`} />
            {generating ? "生成中..." : "生成建议"}
          </Button>
        </CardHeader>
        <CardContent>
          {selected?.improvement_suggestions ? (
            <p className="text-sm whitespace-pre-wrap leading-relaxed">{selected.improvement_suggestions}</p>
          ) : (
            <p className="text-sm text-muted-foreground">点击"生成建议"使用 LLM 分析反馈数据并生成改进建议。</p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function MetricCard({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <div className="rounded-lg border bg-background p-4">
      <div className="flex items-center gap-2 text-muted-foreground">
        {icon}
        <span className="text-sm">{label}</span>
      </div>
      <p className="mt-1 text-2xl font-bold">{value}</p>
    </div>
  );
}
