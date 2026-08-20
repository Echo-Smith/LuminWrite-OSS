import { useState, useEffect, useCallback } from "react";
import { ShieldAlert, RefreshCw, Shield, Layers, AlertTriangle } from "lucide-react";
import { adminFetch } from "@/lib/admin-api";
import { AdminPageHeader, AdminLoading, AdminEmptyState } from "@/components/admin";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

interface RecentInterception {
  source: string;
  pattern_count: number;
  timestamp: string;
}

interface SecurityAuditData {
  external_content_interceptions: number;
  user_input_interceptions: number;
  unique_sources: number;
  total_interceptions: number;
  recent_interceptions: RecentInterception[];
  defense_layers: string[];
}

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

  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="安全审计"
        description="Prompt Injection 拦截统计与安全防御状态"
        action={<Button variant="outline" size="sm" onClick={loadData} disabled={loading}>
          <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} /> 刷新
        </Button>}
      />

      {loading ? <AdminLoading /> : !data ? (
        <AdminEmptyState icon={ShieldAlert} title="无法加载安全审计数据" description="请检查后端服务状态" />
      ) : (
        <>
          {/* 统计卡片 */}
          <div className="grid gap-4 md:grid-cols-4">
            <Card>
              <CardContent className="p-4">
                <div className="flex items-center gap-2 mb-2">
                  <ShieldAlert className="h-4 w-4 text-red-500" />
                  <span className="text-sm font-medium">总拦截次数</span>
                </div>
                <p className="text-2xl font-bold">{data.total_interceptions}</p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-4">
                <div className="flex items-center gap-2 mb-2">
                  <Layers className="h-4 w-4 text-blue-500" />
                  <span className="text-sm font-medium">外部内容拦截</span>
                </div>
                <p className="text-2xl font-bold">{data.external_content_interceptions}</p>
                <p className="text-xs text-muted-foreground mt-1">搜索结果/MCP 输出</p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-4">
                <div className="flex items-center gap-2 mb-2">
                  <AlertTriangle className="h-4 w-4 text-amber-500" />
                  <span className="text-sm font-medium">用户输入拦截</span>
                </div>
                <p className="text-2xl font-bold">{data.user_input_interceptions}</p>
                <p className="text-xs text-muted-foreground mt-1">伪造系统标签</p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-4">
                <div className="flex items-center gap-2 mb-2">
                  <Shield className="h-4 w-4 text-emerald-500" />
                  <span className="text-sm font-medium">受影响信源</span>
                </div>
                <p className="text-2xl font-bold">{data.unique_sources}</p>
                <p className="text-xs text-muted-foreground mt-1">唯一来源数</p>
              </CardContent>
            </Card>
          </div>

          {/* 防御层级 */}
          <Card>
            <CardContent className="p-4">
              <div className="flex items-center gap-2 mb-3">
                <Shield className="h-4 w-4" />
                <h3 className="text-sm font-semibold">防御层级</h3>
              </div>
              <div className="space-y-2">
                {data.defense_layers.map((layer, i) => (
                  <div key={i} className="flex items-center gap-2 text-sm">
                    <Badge variant="outline" className="bg-emerald-50 text-emerald-700 border-emerald-200">L{i + 1}</Badge>
                    <span className="text-muted-foreground">{layer}</span>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>

          {/* 最近拦截记录 */}
          <Card>
            <CardContent className="p-4">
              <div className="flex items-center gap-2 mb-3">
                <ShieldAlert className="h-4 w-4" />
                <h3 className="text-sm font-semibold">最近拦截记录</h3>
                <Badge variant="secondary" className="ml-auto">{data.recent_interceptions.length} 条</Badge>
              </div>
              {data.recent_interceptions.length === 0 ? (
                <p className="text-sm text-muted-foreground py-4 text-center">暂无拦截记录 — 一切正常</p>
              ) : (
                <div className="space-y-2">
                  {data.recent_interceptions.map((entry, i) => (
                    <div key={i} className="flex items-center gap-3 text-xs border-b border-border/50 pb-2">
                      <Badge variant="outline" className="bg-red-50 text-red-700 border-red-200">
                        {entry.pattern_count} 个模式
                      </Badge>
                      <span className="font-mono text-muted-foreground">{entry.source}</span>
                      <span className="ml-auto text-muted-foreground">
                        {new Date(entry.timestamp).toLocaleString("zh-CN")}
                      </span>
                    </div>
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
