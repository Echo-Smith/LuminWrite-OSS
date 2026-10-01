/**
 * 用量统计子页面 — 个人维度
 *
 * 数据来自 GET /api/v2/usage（agent_traces 按 user_id 聚合的 token 用量）。
 * 纯 CSS 条形图，不引入图表库。
 */
import { useEffect, useMemo, useState } from "react";
import { BarChart3 } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";
import { type UserUsageStats, getMyUsage } from "@/lib/model-keys-api";

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

const WINDOW_OPTIONS = [
  { days: 7, label: "近 7 天" },
  { days: 30, label: "近 30 天" },
  { days: 90, label: "近 90 天" },
];

export function UsageSection() {
  const isGuest = useAuthStore((s) => s.user?.role === "guest");
  const [days, setDays] = useState(30);
  const [stats, setStats] = useState<UserUsageStats | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (isGuest) return;
    let cancelled = false;
    setLoading(true);
    getMyUsage(days)
      .then((s) => { if (!cancelled) setStats(s); })
      .catch(() => { if (!cancelled) setStats(null); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [days, isGuest]);

  // 补齐窗口内没有写作的日期，让条形序列连续
  const series = useMemo(() => {
    if (!stats) return [];
    const byDate = new Map(stats.daily.map((d) => [d.date, d]));
    const out: { date: string; traces: number; tokens: number }[] = [];
    const now = new Date();
    for (let i = days - 1; i >= 0; i--) {
      const d = new Date(now);
      d.setDate(d.getDate() - i);
      const key = d.toISOString().slice(0, 10);
      const hit = byDate.get(key);
      out.push({ date: key, traces: hit?.traces ?? 0, tokens: hit?.tokens ?? 0 });
    }
    return out;
  }, [stats, days]);

  const maxTokens = useMemo(() => Math.max(1, ...series.map((d) => d.tokens)), [series]);

  if (isGuest) {
    return (
      <div className="px-6 pt-6 pb-12 space-y-6">
        <Card className="border-amber-200/60 bg-amber-50/50 dark:bg-amber-950/20">
          <CardContent className="py-6 text-center">
            <BarChart3 className="mx-auto h-10 w-10 text-amber-500/50" />
            <p className="mt-3 text-sm text-amber-900 dark:text-amber-200 font-medium">
              游客模式无法查看用量统计
            </p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
              注册后可查看自己的写作次数与 token 消耗
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="px-6 pt-6 pb-12 space-y-5">
      {/* 时间窗切换 */}
      <div className="flex gap-1.5">
        {WINDOW_OPTIONS.map((opt) => (
          <button
            key={opt.days}
            onClick={() => setDays(opt.days)}
            className={cn(
              "rounded-lg px-3 py-1.5 text-xs transition-ui",
              days === opt.days
                ? "bg-accent text-foreground font-medium"
                : "text-muted-foreground hover:bg-accent/50"
            )}
          >
            {opt.label}
          </button>
        ))}
      </div>

      {loading ? (
        <div className="py-12 text-center text-muted-foreground text-sm">加载中...</div>
      ) : !stats ? (
        <div className="py-12 text-center text-sm text-muted-foreground">用量数据加载失败，请稍后重试</div>
      ) : (
        <>
          {/* 汇总卡片 */}
          <div className="grid grid-cols-2 gap-3">
            <Card>
              <CardContent className="py-4">
                <p className="text-xs text-muted-foreground">写作次数</p>
                <p className="mt-1 text-2xl font-semibold">{stats.total_traces}</p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="py-4">
                <p className="text-xs text-muted-foreground">Token 消耗</p>
                <p className="mt-1 text-2xl font-semibold">{formatTokens(stats.total_tokens)}</p>
              </CardContent>
            </Card>
          </div>

          {/* 按日条形图 */}
          <Card>
            <CardContent className="py-4">
              <p className="text-xs text-muted-foreground mb-3">按日 Token 用量</p>
              <div className="flex h-28 items-end gap-[2px]">
                {series.map((d) => (
                  <div
                    key={d.date}
                    className="group relative flex-1 min-w-[3px]"
                    title={`${d.date}：${d.traces} 次 / ${d.tokens} tokens`}
                  >
                    <div
                      className={cn(
                        "w-full rounded-t-sm transition-colors",
                        d.tokens > 0 ? "bg-primary/70 group-hover:bg-primary" : "bg-muted"
                      )}
                      style={{ height: `${Math.max(d.tokens > 0 ? 4 : 2, (d.tokens / maxTokens) * 104)}px` }}
                    />
                  </div>
                ))}
              </div>
              <div className="mt-1.5 flex justify-between text-[10px] text-muted-foreground">
                <span>{series[0]?.date}</span>
                <span>{series[series.length - 1]?.date}</span>
              </div>
            </CardContent>
          </Card>

          {stats.total_traces === 0 && (
            <p className="text-center text-xs text-muted-foreground">
              该时间窗内还没有写作记录。完成一次写作后这里会出现数据。
            </p>
          )}
        </>
      )}
    </div>
  );
}
