/**
 * AdminAlertsCard — 运维告警卡片（概览页）
 *
 * 数据来自后台 ops patrol worker 写入的 admin_alerts 表，60s 轮询；
 * 支持按状态过滤、确认（ack）/ 解决（resolve），新告警实时提示
 * 由 use-sse-notifications 的 admin:alert 监听负责。
 */
import { useEffect, useState } from "react";
import { AlertTriangle, Check, CircleCheck, ChevronDown, ChevronUp } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { adminFetch, adminMutate } from "@/lib/admin-api";
import { useAdminPoll } from "@/hooks/use-admin-poll";

interface AdminAlert {
  id: string;
  severity: string;
  check_type: string;
  title: string;
  detail: string;
  status: string;
  created_at: string;
}

interface AlertsResponse {
  alerts: AdminAlert[];
  total: number;
  pending_count: number;
  acked_count: number;
}

interface PatrolConfig {
  enabled: boolean;
  updated_by?: string;
  updated_at?: string;
}

const SEVERITY_STYLES: Record<string, { label: string; className: string }> = {
  critical: { label: "严重", className: "bg-red-100 text-red-700" },
  warning: { label: "警告", className: "bg-amber-100 text-amber-700" },
  info: { label: "提示", className: "bg-blue-100 text-blue-700" },
};

const STATUS_LABELS: Record<string, string> = {
  pending: "待处理",
  acked: "已确认",
  resolved: "已解决",
};

export function AdminAlertsCard() {
  const [statusFilter, setStatusFilter] = useState("pending");
  const [expanded, setExpanded] = useState<string | null>(null);
  const [patrolEnabled, setPatrolEnabled] = useState<boolean | null>(null);

  const { data, loading, refresh } = useAdminPoll<AlertsResponse>(
    `/api/v2/admin/alerts?status=${statusFilter}&page_size=20`,
    { interval: 60_000 },
  );

  // 巡检运行时开关：只拉取一次（变更即时可见于本端，其他管理员下次进入生效）
  useEffect(() => {
    adminFetch<PatrolConfig>("/api/v2/admin/patrol/config", { silent: true }).then(({ success, data }) => {
      if (success && data) setPatrolEnabled(data.enabled);
    });
  }, []);

  const togglePatrol = async (checked: boolean) => {
    const { success } = await adminMutate("/api/v2/admin/patrol/config", {
      method: "PUT",
      body: JSON.stringify({ enabled: checked }),
      successTitle: checked ? "自动巡检已开启" : "自动巡检已暂停",
      successDesc: checked ? undefined : "历史告警仍可查看，后台停止产生新告警",
    });
    if (success) setPatrolEnabled(checked);
  };

  const handleAck = async (id: string) => {
    const { success } = await adminMutate(`/api/v2/admin/alerts/${id}/ack`, {
      method: "POST",
      successTitle: "已确认",
    });
    if (success) refresh();
  };

  const handleResolve = async (id: string) => {
    const { success } = await adminMutate(`/api/v2/admin/alerts/${id}/resolve`, {
      method: "POST",
      successTitle: "已解决",
    });
    if (success) refresh();
  };

  const alerts = data?.alerts ?? [];
  const pendingCount = data?.pending_count ?? 0;

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle className="text-sm flex items-center gap-2">
            <AlertTriangle className="h-4 w-4" />
            运维告警
            {pendingCount > 0 ? (
              <Badge variant="outline" className="bg-red-50 text-red-700 border-red-200">
                {pendingCount} 条待处理
              </Badge>
            ) : (
              <Badge variant="outline" className="bg-green-50 text-green-700 border-green-200">
                暂无待处理
              </Badge>
            )}
          </CardTitle>
          <div className="flex items-center gap-3">
            <div
              className="flex items-center gap-1.5"
              title="关闭后停止产生新告警，历史告警与操作仍可用；部署级开关见 OPS_PATROL_ENABLED"
            >
              <span className="text-xs text-muted-foreground">自动巡检</span>
              <Switch
                checked={patrolEnabled ?? true}
                disabled={patrolEnabled === null}
                onCheckedChange={togglePatrol}
              />
            </div>
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger className="w-28 h-8 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="pending">待处理</SelectItem>
                <SelectItem value="acked">已确认</SelectItem>
                <SelectItem value="resolved">已解决</SelectItem>
                <SelectItem value="all">全部</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        {loading && alerts.length === 0 ? (
          <p className="text-sm text-muted-foreground text-center py-6">加载中...</p>
        ) : alerts.length === 0 ? (
          <p className="text-sm text-muted-foreground text-center py-6">
            {statusFilter === "pending" ? "系统运行正常，暂无待处理告警" : "暂无告警记录"}
          </p>
        ) : (
          <div className="space-y-2">
            {alerts.map((alert) => {
              const sev = SEVERITY_STYLES[alert.severity] ?? SEVERITY_STYLES.info;
              const isExpanded = expanded === alert.id;
              return (
                <div key={alert.id} className="rounded-lg border p-3 space-y-1.5">
                  <div className="flex items-start gap-2">
                    <Badge variant="outline" className={`text-[10px] shrink-0 ${sev.className}`}>
                      {sev.label}
                    </Badge>
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium">{alert.title}</p>
                      <p className={`text-xs text-muted-foreground mt-0.5 whitespace-pre-wrap ${isExpanded ? "" : "line-clamp-2"}`}>
                        {alert.detail}
                      </p>
                      <div className="flex items-center gap-2 mt-1 text-[10px] text-muted-foreground">
                        <span>{STATUS_LABELS[alert.status] ?? alert.status}</span>
                        <span>{formatRelativeTime(alert.created_at)}</span>
                        {alert.detail.length > 80 && (
                          <button
                            onClick={() => setExpanded(isExpanded ? null : alert.id)}
                            className="flex items-center gap-0.5 hover:text-foreground"
                          >
                            {isExpanded ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
                            {isExpanded ? "收起" : "展开"}
                          </button>
                        )}
                      </div>
                    </div>
                    <div className="flex items-center gap-1.5 shrink-0">
                      {alert.status === "pending" && (
                        <Button variant="outline" size="sm" className="h-7 text-xs" onClick={() => handleAck(alert.id)}>
                          <Check className="h-3 w-3 mr-1" />
                          确认
                        </Button>
                      )}
                      {alert.status !== "resolved" && (
                        <Button variant="outline" size="sm" className="h-7 text-xs" onClick={() => handleResolve(alert.id)}>
                          <CircleCheck className="h-3 w-3 mr-1" />
                          解决
                        </Button>
                      )}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr);
  const diffMs = Date.now() - date.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  const diffHr = Math.floor(diffMin / 60);
  const diffDay = Math.floor(diffHr / 24);
  if (diffMin < 1) return "刚刚";
  if (diffMin < 60) return `${diffMin} 分钟前`;
  if (diffHr < 24) return `${diffHr} 小时前`;
  if (diffDay < 30) return `${diffDay} 天前`;
  return date.toLocaleDateString("zh-CN");
}
