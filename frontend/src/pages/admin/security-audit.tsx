import { useState, useEffect, useCallback } from "react";
import {
  ShieldAlert, Shield, RefreshCw, AlertTriangle, Layers, Globe,
  TrendingUp, Activity, User, FileSearch, Lock, Eye,
} from "lucide-react";
import { adminFetch } from "@/lib/admin-api";
import { AdminPageHeader, AdminLoading, AdminEmptyState } from "@/components/admin";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

// ─── Types ──────────────────────────────────────────────

interface RecentInterception {
  source: string;
  pattern_count: number;
  timestamp: string;
  event_type?: string;
  categories?: string[];
  snippet?: string;
}

interface TrendPoint {
  hour: string;
  count: number;
  ext_count: number;
  user_count: number;
}

interface CategoryBreakdown {
  category: string;
  count: number;
}

interface TopSource {
  source: string;
  count: number;
}

interface DBStats {
  available: boolean;
  total_all?: number;
  total_24h?: number;
  total_7d?: number;
  external_24h?: number;
  user_input_24h?: number;
  trend_24h?: TrendPoint[];
  category_breakdown?: CategoryBreakdown[];
  top_sources?: TopSource[];
}

interface MCPSandboxSummary {
  available: boolean;
  active_policies?: number;
  total_violations?: number;
  violations_24h?: number;
  violation_types_7d?: { type: string; count: number }[];
}

interface DefenseLayer {
  name: string;
  desc: string;
  status: string;
}

interface SecurityAuditData {
  external_content_interceptions: number;
  user_input_interceptions: number;
  unique_sources: number;
  total_interceptions: number;
  recent_interceptions: RecentInterception[];
  db_stats: DBStats;
  mcp_sandbox: MCPSandboxSummary;
  defense_layers: DefenseLayer[];
  pattern_categories: Record<string, string>;
}

// ─── Labels & Colors ────────────────────────────────────

const EVENT_TYPE_LABELS: Record<string, string> = {
  external_content: "外部内容",
  user_input: "用户输入",
};

const EVENT_TYPE_COLORS: Record<string, string> = {
  external_content: "text-blue-600 bg-blue-50 border-blue-200",
  user_input: "text-amber-600 bg-amber-50 border-amber-200",
};

const CATEGORY_COLORS: Record<string, string> = {
  direct_override: "bg-red-500",
  identity_override: "bg-orange-500",
  prompt_extraction: "bg-purple-500",
  fake_system_msg: "bg-blue-500",
  credential_extract: "bg-pink-500",
  instruction_chain: "bg-cyan-500",
};

const VIOLATION_TYPE_LABELS: Record<string, string> = {
  denied: "策略拒绝",
  blocked_domain: "域名拦截",
  arg_too_large: "参数过大",
  timeout: "执行超时",
  rate_limit: "频率限制",
};

// ─── Sub-components ──────────────────────────────────────

function StatCard({
  icon: Icon, label, value, sublabel, color,
}: {
  icon: React.ElementType; label: string; value: number | string;
  sublabel?: string; color: string;
}) {
  return (
    <Card>
      <CardContent className="p-4">
        <div className="flex items-center gap-2 mb-2">
          <Icon className={`h-4 w-4 ${color}`} />
          <span className="text-sm font-medium">{label}</span>
        </div>
        <p className="text-2xl font-bold">{value}</p>
        {sublabel && <p className="text-xs text-muted-foreground mt-1">{sublabel}</p>}
      </CardContent>
    </Card>
  );
}

function MiniBarChart({ data, maxVal }: { data: TrendPoint[]; maxVal: number }) {
  if (!data || data.length === 0) {
    return <p className="text-sm text-muted-foreground py-4 text-center">暂无趋势数据</p>;
  }
  return (
    <div className="flex items-end gap-1 h-24 px-1">
      {data.map((pt, i) => {
        const height = maxVal > 0 ? (pt.count / maxVal) * 100 : 0;
        return (
          <div key={i} className="flex-1 flex flex-col items-center group relative">
            <div
              className="w-full rounded-t-sm bg-gradient-to-t from-blue-400 to-blue-500 transition-all hover:from-blue-500 hover:to-blue-600"
              style={{ height: `${Math.max(height, 2)}%` }}
              title={`${pt.hour} | ${pt.count} 次`}
            />
            <span className="text-[9px] text-muted-foreground mt-1 hidden group-hover:block absolute -top-6">
              {pt.hour}
            </span>
          </div>
        );
      })}
    </div>
  );
}

function CategoryBar({ category, count, maxCount, labels }: {
  category: string; count: number; maxCount: number; labels: Record<string, string>;
}) {
  const pct = maxCount > 0 ? (count / maxCount) * 100 : 0;
  const color = CATEGORY_COLORS[category] ?? "bg-gray-400";
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-xs">
        <span className="font-medium">{labels[category] ?? category}</span>
        <span className="text-muted-foreground">{count}</span>
      </div>
      <div className="h-2 rounded-full bg-muted overflow-hidden">
        <div className={`h-full rounded-full ${color} transition-all`} style={{ width: `${Math.max(pct, 3)}%` }} />
      </div>
    </div>
  );
}

function DefenseLayerBadge({ layer }: { layer: DefenseLayer }) {
  const isActive = layer.status === "active";
  return (
    <div className="flex items-center gap-2 text-sm py-1.5 border-b border-border/40 last:border-0">
      <span className={`inline-block w-2 h-2 rounded-full ${isActive ? "bg-emerald-500" : "bg-red-500"}`} />
      <div className="flex-1">
        <span className="font-medium">{layer.name}</span>
        <span className="text-xs text-muted-foreground ml-2">{layer.desc}</span>
      </div>
      <Badge variant="outline" className={isActive ? "bg-emerald-50 text-emerald-700 border-emerald-200" : "bg-red-50 text-red-700 border-red-200"}>
        {isActive ? "运行中" : "已停用"}
      </Badge>
    </div>
  );
}

function InterceptionRow({ entry, categories }: {
  entry: RecentInterception; categories: Record<string, string>;
}) {
  const [expanded, setExpanded] = useState(false);
  return (
    <div className="border-b border-border/50 pb-2 mb-2 last:border-0 last:mb-0">
      <div className="flex items-center gap-2 text-xs">
        {entry.event_type && (
          <Badge variant="outline" className={`text-[10px] ${EVENT_TYPE_COLORS[entry.event_type] ?? ""}`}>
            {EVENT_TYPE_LABELS[entry.event_type] ?? entry.event_type}
          </Badge>
        )}
        <Badge variant="outline" className="bg-red-50 text-red-700 border-red-200 text-[10px]">
          {entry.pattern_count} 模式
        </Badge>
        {entry.categories?.map((cat, i) => (
          <Badge key={i} variant="outline" className="text-[10px] bg-muted">
            {categories[cat] ?? cat}
          </Badge>
        ))}
        <span className="font-mono text-muted-foreground truncate flex-1">{entry.source}</span>
        <span className="text-muted-foreground whitespace-nowrap">
          {new Date(entry.timestamp).toLocaleString("zh-CN")}
        </span>
        {entry.snippet && (
          <button onClick={() => setExpanded(!expanded)} className="text-blue-500 hover:text-blue-600">
            <Eye className="h-3 w-3" />
          </button>
        )}
      </div>
      {expanded && entry.snippet && (
        <p className="text-xs mt-1 p-2 rounded bg-muted/50 font-mono text-muted-foreground line-clamp-3">
          {entry.snippet}
        </p>
      )}
    </div>
  );
}

// ─── Main Component ──────────────────────────────────────

export function SecurityAuditPage() {
  const [data, setData] = useState<SecurityAuditData | null>(null);
  const [loading, setLoading] = useState(true);

  const loadData = useCallback(async () => {
    setLoading(true);
    const { success, data } = await adminFetch<SecurityAuditData>("/api/v2/admin/security/audit", { silent: true });
    if (success && data) {
      setData(data);
    } else {
      setData(null);
    }
    setLoading(false);
  }, []);

  useEffect(() => { loadData(); }, [loadData]);

  const trendMax = data?.db_stats?.trend_24h
    ? Math.max(...data.db_stats.trend_24h.map(t => t.count), 1)
    : 1;

  const categoryMax = data?.db_stats?.category_breakdown
    ? Math.max(...data.db_stats.category_breakdown.map(c => c.count), 1)
    : 1;

  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="安全审计"
        description="Prompt Injection 拦截统计 · MCP 沙箱违规 · 安全防御状态"
        action={<Button variant="outline" size="sm" onClick={loadData} disabled={loading}>
          <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} /> 刷新
        </Button>}
      />

      {loading ? <AdminLoading /> : !data ? (
        <AdminEmptyState icon={ShieldAlert} title="无法加载安全审计数据" description="请检查后端服务状态" />
      ) : (
        <>
          {/* ── Row 1: Real-time Stats (in-memory, since process start) ── */}
          <div className="grid gap-4 md:grid-cols-4">
            <StatCard
              icon={ShieldAlert} label="总拦截 (实时)" value={data.total_interceptions}
              sublabel="进程启动以来" color="text-red-500"
            />
            <StatCard
              icon={FileSearch} label="外部内容拦截" value={data.external_content_interceptions}
              sublabel="搜索结果 / MCP 输出" color="text-blue-500"
            />
            <StatCard
              icon={User} label="用户输入拦截" value={data.user_input_interceptions}
              sublabel="伪造系统标签" color="text-amber-500"
            />
            <StatCard
              icon={Globe} label="受影响信源" value={data.unique_sources}
              sublabel="唯一来源数" color="text-emerald-500"
            />
          </div>

          {/* ── Row 2: DB-persisted Stats ── */}
          {data.db_stats?.available && (
            <div className="grid gap-4 md:grid-cols-4">
              <StatCard
                icon={Layers} label="历史拦截总量" value={data.db_stats.total_all ?? 0}
                sublabel="数据库持久化" color="text-purple-500"
              />
              <StatCard
                icon={Activity} label="24h 拦截" value={data.db_stats.total_24h ?? 0}
                sublabel={`外部 ${data.db_stats.external_24h ?? 0} · 用户 ${data.db_stats.user_input_24h ?? 0}`}
                color="text-orange-500"
              />
              <StatCard
                icon={TrendingUp} label="7天拦截" value={data.db_stats.total_7d ?? 0}
                sublabel="最近一周" color="text-cyan-500"
              />
              <StatCard
                icon={Lock} label="MCP 活跃策略" value={data.mcp_sandbox?.available ? (data.mcp_sandbox.active_policies ?? 0) : "—"}
                sublabel={data.mcp_sandbox?.available ? `${data.mcp_sandbox.total_violations ?? 0} 总违规` : "沙箱不可用"}
                color="text-indigo-500"
              />
            </div>
          )}

          {/* ── Row 3: 24h Trend + Category Breakdown ── */}
          <div className="grid gap-4 md:grid-cols-2">
            {/* Trend Chart */}
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <TrendingUp className="h-4 w-4" /> 24小时拦截趋势
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-2">
                <MiniBarChart data={data.db_stats?.trend_24h ?? []} maxVal={trendMax} />
                {data.db_stats?.trend_24h && data.db_stats.trend_24h.length > 0 && (
                  <p className="text-xs text-muted-foreground mt-2 text-center">
                    共 {data.db_stats.trend_24h.reduce((s, t) => s + t.count, 0)} 次拦截
                  </p>
                )}
              </CardContent>
            </Card>

            {/* Category Breakdown */}
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <AlertTriangle className="h-4 w-4" /> 攻击类型分布 (7天)
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-2 space-y-3">
                {(data.db_stats?.category_breakdown ?? []).length === 0 ? (
                  <p className="text-sm text-muted-foreground py-4 text-center">暂无攻击分类数据</p>
                ) : (
                  (data.db_stats?.category_breakdown ?? []).map((cat) => (
                    <CategoryBar
                      key={cat.category}
                      category={cat.category}
                      count={cat.count}
                      maxCount={categoryMax}
                      labels={data.pattern_categories}
                    />
                  ))
                )}
              </CardContent>
            </Card>
          </div>

          {/* ── Row 4: Defense Layers + MCP Sandbox ── */}
          <div className="grid gap-4 md:grid-cols-2">
            {/* Defense Layers */}
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <Shield className="h-4 w-4" /> 防御层级
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-2">
                {data.defense_layers.map((layer, i) => (
                  <DefenseLayerBadge key={i} layer={layer} />
                ))}
              </CardContent>
            </Card>

            {/* MCP Sandbox Summary */}
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <Lock className="h-4 w-4" /> MCP 沙箱概况
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-2">
                {data.mcp_sandbox?.available ? (
                  <>
                    <div className="grid grid-cols-3 gap-2 mb-3">
                      <div className="text-center">
                        <p className="text-xl font-bold text-indigo-600">{data.mcp_sandbox.active_policies ?? 0}</p>
                        <p className="text-xs text-muted-foreground">活跃策略</p>
                      </div>
                      <div className="text-center">
                        <p className="text-xl font-bold text-red-600">{data.mcp_sandbox.total_violations ?? 0}</p>
                        <p className="text-xs text-muted-foreground">总违规</p>
                      </div>
                      <div className="text-center">
                        <p className="text-xl font-bold text-orange-600">{data.mcp_sandbox.violations_24h ?? 0}</p>
                        <p className="text-xs text-muted-foreground">24h违规</p>
                      </div>
                    </div>
                    {(data.mcp_sandbox.violation_types_7d ?? []).length > 0 && (
                      <div className="space-y-1.5">
                        <p className="text-xs font-medium text-muted-foreground">违规类型 (7天)</p>
                        {data.mcp_sandbox.violation_types_7d!.map((vt, i) => (
                          <div key={i} className="flex items-center justify-between text-xs">
                            <span>{VIOLATION_TYPE_LABELS[vt.type] ?? vt.type}</span>
                            <Badge variant="secondary">{vt.count}</Badge>
                          </div>
                        ))}
                      </div>
                    )}
                  </>
                ) : (
                  <p className="text-sm text-muted-foreground py-4 text-center">MCP 沙箱不可用</p>
                )}
              </CardContent>
            </Card>
          </div>

          {/* ── Row 5: Top Sources ── */}
          {data.db_stats?.top_sources && data.db_stats.top_sources.length > 0 && (
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm flex items-center gap-2">
                  <Globe className="h-4 w-4" /> 高频拦截来源 (7天 Top 10)
                </CardTitle>
              </CardHeader>
              <CardContent className="p-4 pt-2">
                <div className="space-y-1.5">
                  {data.db_stats.top_sources.map((src, i) => (
                    <div key={i} className="flex items-center gap-2 text-xs">
                      <span className="w-6 text-muted-foreground text-right">{i + 1}.</span>
                      <span className="font-mono text-muted-foreground truncate flex-1">{src.source}</span>
                      <Badge variant="secondary">{src.count}</Badge>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          )}

          {/* ── Row 6: Recent Interceptions ── */}
          <Card>
            <CardHeader className="pb-2">
              <div className="flex items-center gap-2">
                <ShieldAlert className="h-4 w-4" />
                <CardTitle className="text-sm">最近拦截记录</CardTitle>
                <Badge variant="secondary" className="ml-auto">{data.recent_interceptions.length} 条</Badge>
              </div>
            </CardHeader>
            <CardContent className="p-4 pt-2">
              {data.recent_interceptions.length === 0 ? (
                <p className="text-sm text-muted-foreground py-4 text-center">
                  <Shield className="h-8 w-8 mx-auto mb-2 text-emerald-500" />
                  暂无拦截记录 — 一切正常
                </p>
              ) : (
                <div>
                  {data.recent_interceptions.map((entry, i) => (
                    <InterceptionRow key={i} entry={entry} categories={data.pattern_categories} />
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}
