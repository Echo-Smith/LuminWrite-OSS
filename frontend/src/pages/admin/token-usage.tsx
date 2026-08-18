/**
 * 用量统计 — Admin Dashboard
 */
import { useState, useEffect, useCallback } from "react";
import { TrendingUp, Coins, Calendar, Loader2 } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { adminFetch } from "@/lib/admin-api";
import { AdminPageHeader } from "@/components/admin";

interface TokenUsageStats {
  total_tokens: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_estimated_cost: number;
  today_tokens: number;
  by_model: { model_name: string; provider: string; total_tokens: number; call_count: number; estimated_cost: number }[];
  by_provider: { provider: string; total_tokens: number; call_count: number; estimated_cost: number }[];
  daily_tokens: { date: string; count: number }[];
}

function formatNumber(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(1) + "K";
  return n.toString();
}

export function TokenUsagePage() {
  const [stats, setStats] = useState<TokenUsageStats | null>(null);
  const [loading, setLoading] = useState(false);
  const [days, setDays] = useState("30");

  const load = useCallback(async () => {
    setLoading(true);
    const { success, data } = await adminFetch<TokenUsageStats>(`/api/v2/admin/token-usage?days=${days}`, { silent: true });
    if (success && data) setStats(data);
    setLoading(false);
  }, [days]);

  useEffect(() => { load(); }, [load]);

  const maxDaily = Math.max(...(stats?.daily_tokens?.map((d) => d.count) ?? [1]), 1);

  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="用量统计"
        action={
          <Select value={days} onValueChange={setDays}>
            <SelectTrigger className="w-32"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="7">近 7 天</SelectItem>
              <SelectItem value="30">近 30 天</SelectItem>
              <SelectItem value="90">近 90 天</SelectItem>
            </SelectContent>
          </Select>
        }
      />

      {loading ? (
        <div className="flex justify-center p-12"><Loader2 className="h-6 w-6 animate-spin" /></div>
      ) : stats ? (
        <>
          {/* Summary Cards */}
          <div className="grid grid-cols-4 gap-4">
            <Card>
              <CardHeader className="pb-2"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><Coins className="h-3 w-3" /> 总 Token</CardTitle></CardHeader>
              <CardContent><div className="text-2xl font-bold">{formatNumber(stats.total_tokens)}</div></CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2"><CardTitle className="text-xs text-muted-foreground">Prompt Tokens</CardTitle></CardHeader>
              <CardContent><div className="text-2xl font-bold">{formatNumber(stats.total_prompt_tokens)}</div></CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2"><CardTitle className="text-xs text-muted-foreground">Completion Tokens</CardTitle></CardHeader>
              <CardContent><div className="text-2xl font-bold">{formatNumber(stats.total_completion_tokens)}</div></CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><TrendingUp className="h-3 w-3" /> 今日 Token</CardTitle></CardHeader>
              <CardContent><div className="text-2xl font-bold">{formatNumber(stats.today_tokens)}</div></CardContent>
            </Card>
          </div>

          {/* Daily Chart */}
          <Card>
            <CardHeader><CardTitle className="text-sm flex items-center gap-2"><Calendar className="h-4 w-4" /> 每日 Token 用量</CardTitle></CardHeader>
            <CardContent>
              {stats.daily_tokens && stats.daily_tokens.length > 0 ? (
                <div className="flex items-end gap-1 h-40">
                  {stats.daily_tokens.map((d) => (
                    <div key={d.date} className="flex-1 flex flex-col items-center gap-1">
                      <div className="w-full bg-primary/20 rounded-t" style={{ height: `${(d.count / maxDaily) * 100}%`, minHeight: "2px" }} title={`${d.date}: ${d.count}`} />
                      <span className="text-[10px] text-muted-foreground rotate-45 origin-left">{d.date.slice(5)}</span>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-sm text-muted-foreground text-center py-8">暂无数据</p>
              )}
            </CardContent>
          </Card>

          <div className="grid grid-cols-2 gap-4">
            {/* By Model */}
            <Card>
              <CardHeader><CardTitle className="text-sm">按模型分布</CardTitle></CardHeader>
              <CardContent>
                {stats.by_model && stats.by_model.length > 0 ? (
                  <table className="w-full">
                    <thead>
                      <tr className="border-b"><th className="text-left p-2 text-xs">模型</th><th className="text-right p-2 text-xs">Tokens</th><th className="text-right p-2 text-xs">调用数</th></tr>
                    </thead>
                    <tbody>
                      {stats.by_model.map((m, i) => (
                        <tr key={i} className="border-b last:border-0">
                          <td className="p-2 text-sm"><Badge variant="outline" className="mr-1 text-xs">{m.provider}</Badge>{m.model_name}</td>
                          <td className="p-2 text-sm text-right font-mono">{formatNumber(m.total_tokens)}</td>
                          <td className="p-2 text-sm text-right">{m.call_count}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                ) : <p className="text-sm text-muted-foreground text-center py-4">暂无数据</p>}
              </CardContent>
            </Card>

            {/* By Provider */}
            <Card>
              <CardHeader><CardTitle className="text-sm">按供应商分布</CardTitle></CardHeader>
              <CardContent>
                {stats.by_provider && stats.by_provider.length > 0 ? (
                  <table className="w-full">
                    <thead>
                      <tr className="border-b"><th className="text-left p-2 text-xs">供应商</th><th className="text-right p-2 text-xs">Tokens</th><th className="text-right p-2 text-xs">调用数</th></tr>
                    </thead>
                    <tbody>
                      {stats.by_provider.map((p, i) => (
                        <tr key={i} className="border-b last:border-0">
                          <td className="p-2 text-sm font-medium">{p.provider}</td>
                          <td className="p-2 text-sm text-right font-mono">{formatNumber(p.total_tokens)}</td>
                          <td className="p-2 text-sm text-right">{p.call_count}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                ) : <p className="text-sm text-muted-foreground text-center py-4">暂无数据</p>}
              </CardContent>
            </Card>
          </div>
        </>
      ) : (
        <p className="text-center text-muted-foreground py-12">暂无数据</p>
      )}
    </div>
  );
}
