/**
 * 概览面板 — Admin Dashboard
 * 调用 /api/v2/admin/stats 获取统计数据 + /api/v2/admin/traces 获取最近 trace 列表
 */
import { useState, useEffect, useCallback } from "react";
import { RefreshCw, TrendingUp, FileText, Users, Award, Activity, Clock } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

interface DashboardStats {
  today_writes: number;
  today_tokens: number;
  active_users: number;
  eval_avg_score: number;
  total_writes: number;
  total_tokens: number;
  style_distribution: StyleUsage[];
  recent_traces: TraceSummary[];
  weekly_writes: DailyCount[];
  weekly_tokens: DailyCount[];
}

interface StyleUsage {
  style_slug: string;
  count: number;
  percent: number;
}

interface TraceSummary {
  trace_id: string;
  status: string;
  current_step: string;
  user_input: string;
  style_slug: string;
  mode: string;
  review_score: number | null;
  duration_ms: number | null;
  created_at: string;
  completed_at: string | null;
}

interface DailyCount {
  date: string;
  count: number;
}

const STATUS_COLORS: Record<string, string> = {
  completed: "bg-green-100 text-green-700",
  running: "bg-blue-100 text-blue-700",
  failed: "bg-red-100 text-red-700",
  paused: "bg-yellow-100 text-yellow-700",
  cancelled: "bg-gray-100 text-gray-700",
};

const STYLE_LABELS: Record<string, string> = {
  yinyue: "预设评论风格",
  shenlun: "申论",
  xiaohongshu: "小红书",
  unknown: "未知",
};

export function OverviewPage() {
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [loading, setLoading] = useState(false);

  const loadStats = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch("/api/v2/admin/stats");
      const json = await res.json();
      if (json.success) {
        setStats(json.data);
      }
    } catch (e) {
      console.error("Failed to load stats", e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadStats();
  }, [loadStats]);

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">概览</h2>
        <Button variant="outline" size="sm" onClick={loadStats} disabled={loading}>
          <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} />
          刷新
        </Button>
      </div>

      {/* Metric Cards */}
      <div className="grid grid-cols-4 gap-4">
        <MetricCard
          icon={<FileText className="h-4 w-4" />}
          label="今日写作"
          value={stats ? String(stats.today_writes) : "—"}
          sub={stats ? `总计 ${stats.total_writes}` : ""}
        />
        <MetricCard
          icon={<Activity className="h-4 w-4" />}
          label="今日 Token"
          value={stats ? formatTokenCount(stats.today_tokens) : "—"}
          sub={stats ? `总计 ${formatTokenCount(stats.total_tokens)}` : ""}
        />
        <MetricCard
          icon={<Users className="h-4 w-4" />}
          label="活跃用户 (24h)"
          value={stats ? String(stats.active_users) : "—"}
        />
        <MetricCard
          icon={<Award className="h-4 w-4" />}
          label="评测平均分"
          value={stats ? stats.eval_avg_score.toFixed(2) : "—"}
        />
      </div>

      {/* Charts Row */}
      <div className="grid grid-cols-2 gap-4">
        {/* Weekly Writes */}
        <Card>
          <CardHeader>
            <CardTitle className="text-sm flex items-center gap-2">
              <TrendingUp className="h-4 w-4" />
              写作量趋势 (7天)
            </CardTitle>
          </CardHeader>
          <CardContent>
            {stats?.weekly_writes && stats.weekly_writes.length > 0 ? (
              <MiniBarChart data={stats.weekly_writes} />
            ) : (
              <p className="text-sm text-muted-foreground text-center py-8">暂无数据</p>
            )}
          </CardContent>
        </Card>

        {/* Weekly Tokens */}
        <Card>
          <CardHeader>
            <CardTitle className="text-sm flex items-center gap-2">
              <Activity className="h-4 w-4" />
              Token 用量趋势 (7天)
            </CardTitle>
          </CardHeader>
          <CardContent>
            {stats?.weekly_tokens && stats.weekly_tokens.length > 0 ? (
              <MiniBarChart data={stats.weekly_tokens} />
            ) : (
              <p className="text-sm text-muted-foreground text-center py-8">暂无数据</p>
            )}
          </CardContent>
        </Card>
      </div>

      {/* Style Distribution */}
      {stats?.style_distribution && stats.style_distribution.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">风格使用分布</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {stats.style_distribution.map((style) => (
              <div key={style.style_slug} className="flex items-center gap-4">
                <span className="w-24 text-sm text-muted-foreground">
                  {STYLE_LABELS[style.style_slug] ?? style.style_slug}
                </span>
                <div className="flex-1 h-6 bg-muted rounded-full overflow-hidden">
                  <div
                    className="h-full bg-primary rounded-full flex items-center justify-end px-2"
                    style={{ width: `${Math.max(style.percent, 5)}%` }}
                  >
                    <span className="text-xs text-primary-foreground font-medium">
                      {style.percent.toFixed(0)}%
                    </span>
                  </div>
                </div>
                <span className="text-xs text-muted-foreground w-12 text-right">{style.count} 次</span>
              </div>
            ))}
          </CardContent>
        </Card>
      )}

      {/* Recent Traces */}
      <Card>
        <CardHeader>
          <CardTitle className="text-sm flex items-center gap-2">
            <Clock className="h-4 w-4" />
            最近写作记录
          </CardTitle>
        </CardHeader>
        <CardContent>
          {stats?.recent_traces && stats.recent_traces.length > 0 ? (
            <div className="space-y-2">
              {stats.recent_traces.map((trace) => (
                <div
                  key={trace.trace_id}
                  className="flex items-center justify-between border rounded-lg p-3 hover:bg-accent/50 transition-colors"
                >
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 mb-1">
                      <Badge
                        variant="outline"
                        className={STATUS_COLORS[trace.status] ?? ""}
                      >
                        {trace.status}
                      </Badge>
                      <span className="text-xs text-muted-foreground">
                        {STYLE_LABELS[trace.style_slug] ?? trace.style_slug}
                      </span>
                      <span className="text-xs text-muted-foreground">{trace.mode}</span>
                    </div>
                    <p className="text-sm truncate">{trace.user_input}</p>
                  </div>
                  <div className="flex items-center gap-4 text-xs text-muted-foreground">
                    {trace.review_score !== null && trace.review_score !== undefined && (
                      <span className="font-medium text-green-600">
                        评分 {(trace.review_score * 100).toFixed(0)}
                      </span>
                    )}
                    {trace.duration_ms && (
                      <span>{(trace.duration_ms / 1000).toFixed(1)}s</span>
                    )}
                    <span>{formatRelativeTime(trace.created_at)}</span>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground text-center py-8">
              暂无写作记录
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function MetricCard({
  icon,
  label,
  value,
  sub,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  sub?: string;
}) {
  return (
    <div className="rounded-lg border bg-background p-4">
      <div className="flex items-center gap-2 text-muted-foreground">
        {icon}
        <span className="text-sm">{label}</span>
      </div>
      <p className="mt-1 text-2xl font-bold">{value}</p>
      {sub && <p className="mt-1 text-xs text-muted-foreground">{sub}</p>}
    </div>
  );
}

function MiniBarChart({ data }: { data: DailyCount[] }) {
  const max = Math.max(...data.map((d) => d.count), 1);

  return (
    <div className="flex items-end justify-between gap-2 h-32">
      {data.map((d) => (
        <div key={d.date} className="flex-1 flex flex-col items-center gap-1">
          <div className="w-full flex-1 flex items-end">
            <div
              className="w-full bg-primary/80 rounded-t-sm transition-all hover:bg-primary"
              style={{ height: `${(d.count / max) * 100}%`, minHeight: d.count > 0 ? "4px" : "0" }}
              title={`${d.count}`}
            />
          </div>
          <span className="text-xs text-muted-foreground">
            {d.date.slice(5)}
          </span>
        </div>
      ))}
    </div>
  );
}

function formatTokenCount(tokens: number): string {
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(1)}M`;
  if (tokens >= 1_000) return `${(tokens / 1_000).toFixed(1)}K`;
  return String(tokens);
}

function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  const diffHr = Math.floor(diffMin / 60);
  const diffDay = Math.floor(diffHr / 24);

  if (diffMin < 1) return "刚刚";
  if (diffMin < 60) return `${diffMin} 分钟前`;
  if (diffHr < 24) return `${diffHr} 小时前`;
  if (diffDay < 7) return `${diffDay} 天前`;
  return date.toLocaleDateString("zh-CN");
}
