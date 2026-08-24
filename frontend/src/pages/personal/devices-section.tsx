/**
 * 设备管理子页面
 */
import { useState, useEffect, useCallback } from "react";
import {
  Monitor, Smartphone, Tablet, Globe, Loader2, AlertCircle,
  Clock, Trash2,
} from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";

interface DeviceSession {
  id: string;
  jti: string;
  device_name: string;
  device_type: string;
  ip_address: string;
  created_at: string;
  last_active_at: string;
  expires_at: string;
  is_current: boolean;
}

export function DevicesSection() {
  const user = useAuthStore((s) => s.user);
  const isGuest = user?.role === "guest";

  const [sessions, setSessions] = useState<DeviceSession[]>([]);
  const [onlineCount, setOnlineCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchSessions = useCallback(async () => {
    try {
      setError(null);
      const token = useAuthStore.getState().token;
      const res = await fetch("/api/v2/auth/sessions", {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      const json = await res.json();
      if (json.success && json.data) {
        setSessions(json.data.sessions ?? []);
        setOnlineCount(json.data.online_count ?? 0);
      } else {
        setError(json.message || "获取设备列表失败");
      }
    } catch {
      setError("网络错误，请稍后重试");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!isGuest) {
      fetchSessions();
      const interval = setInterval(fetchSessions, 30_000);
      return () => clearInterval(interval);
    } else {
      setLoading(false);
    }
  }, [isGuest, fetchSessions]);

  const getDeviceIcon = (deviceType: string, isCurrent: boolean) => {
    if (isCurrent) return Monitor;
    switch (deviceType) {
      case "mobile": return Smartphone;
      case "tablet": return Tablet;
      default: return Monitor;
    }
  };

  const formatTime = (iso: string) => {
    const date = new Date(iso);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMin = Math.floor(diffMs / 60_000);
    const diffHr = Math.floor(diffMs / 3_600_000);
    const diffDay = Math.floor(diffMs / 86_400_000);
    if (diffMin < 1) return "刚刚";
    if (diffMin < 60) return `${diffMin} 分钟前`;
    if (diffHr < 24) return `${diffHr} 小时前`;
    if (diffDay < 7) return `${diffDay} 天前`;
    return date.toLocaleDateString("zh-CN", { month: "short", day: "numeric" });
  };

  const formatExpiry = (iso: string) => {
    const date = new Date(iso);
    const now = new Date();
    const diffMs = date.getTime() - now.getTime();
    const diffHr = Math.floor(diffMs / 3_600_000);
    const diffMin = Math.floor((diffMs % 3_600_000) / 60_000);
    if (diffHr > 0) return `${diffHr}小时${diffMin}分`;
    if (diffMin > 0) return `${diffMin}分钟`;
    return "即将过期";
  };

  if (isGuest) {
    return (
      <div className="px-6 pt-6 pb-12 space-y-6">
        <Card className="border-amber-200/60 bg-amber-50/50 dark:bg-amber-950/20">
          <CardContent className="py-6 text-center">
            <Monitor className="mx-auto h-10 w-10 text-amber-500/50" />
            <p className="mt-3 text-sm text-amber-900 dark:text-amber-200 font-medium">
              游客模式无法查看设备管理
            </p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
              注册账号后可管理多设备登录
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (loading) {
    return (
      <div className="px-6 pt-6 pb-12 flex items-center justify-center" style={{ minHeight: "200px" }}>
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="px-6 pt-6 pb-12 space-y-6">
        <Card className="border-red-200/60 bg-red-50/50 dark:bg-red-950/20">
          <CardContent className="py-6 text-center">
            <AlertCircle className="mx-auto h-8 w-8 text-red-500/60" />
            <p className="mt-2 text-sm text-red-700 dark:text-red-300">{error}</p>
            <Button variant="outline" size="sm" className="mt-3" onClick={fetchSessions}>
              重试
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="px-6 pt-6 pb-12 space-y-6">
      {/* 概览统计 */}
      <div className="flex items-center gap-3">
        <div className="flex items-center gap-2 rounded-lg border px-3 py-2">
          <Globe className="h-4 w-4 text-green-500" />
          <div>
            <p className="text-xs text-muted-foreground">在线连接</p>
            <p className="text-sm font-semibold">{onlineCount}</p>
          </div>
        </div>
        <div className="flex items-center gap-2 rounded-lg border px-3 py-2">
          <Monitor className="h-4 w-4 text-blue-500" />
          <div>
            <p className="text-xs text-muted-foreground">活跃会话</p>
            <p className="text-sm font-semibold">{sessions.length}</p>
          </div>
        </div>
      </div>

      {/* 设备列表 */}
      {sessions.length === 0 ? (
        <Card>
          <CardContent className="py-8 text-center">
            <Monitor className="mx-auto h-10 w-10 text-muted-foreground/40" />
            <p className="mt-2 text-sm text-muted-foreground">暂无活跃设备</p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-2">
          {sessions.map((session) => {
            const DeviceIcon = getDeviceIcon(session.device_type, session.is_current);
            return (
              <Card key={session.id} className={cn(
                "border-border/60 transition-colors",
                session.is_current && "ring-1 ring-primary/30 bg-primary/5"
              )}>
                <CardContent className="flex items-center gap-3 py-3">
                  <div className={cn(
                    "flex h-10 w-10 shrink-0 items-center justify-center rounded-lg",
                    session.is_current ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground"
                  )}>
                    <DeviceIcon className="h-5 w-5" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <p className="text-sm font-medium truncate">
                        {session.device_name || "Unknown Device"}
                      </p>
                      {session.is_current && (
                        <Badge variant="secondary" className="text-[10px] py-0 px-1.5 h-4">
                          当前设备
                        </Badge>
                      )}
                    </div>
                    <div className="flex items-center gap-3 mt-0.5 text-xs text-muted-foreground">
                      {session.ip_address && (
                        <span className="flex items-center gap-1">
                          <Globe className="h-3 w-3" />
                          {session.ip_address}
                        </span>
                      )}
                      <span className="flex items-center gap-1">
                        <Clock className="h-3 w-3" />
                        {formatTime(session.last_active_at)}
                      </span>
                      <span className="text-muted-foreground/70">
                        剩余 {formatExpiry(session.expires_at)}
                      </span>
                    </div>
                  </div>
                  {/* 下线按钮 — 灰色不可用，功能近期开发 */}
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled
                    className="h-8 shrink-0 text-muted-foreground/50"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                    下线
                  </Button>
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      {/* 说明 */}
      <div className="space-y-3 pt-2">
        <div className="flex items-start gap-2 rounded-lg border border-blue-200/50 bg-blue-50/40 dark:bg-blue-950/10 px-3 py-2">
          <Clock className="h-3.5 w-3.5 text-blue-500 shrink-0 mt-0.5" />
          <p className="text-xs text-blue-700 dark:text-blue-300 leading-relaxed">
            下线管理功能正在开发中，届时可远程踢出其他设备。
          </p>
        </div>
        <div className="text-[10px] text-muted-foreground space-y-1.5">
          <p>• 列表显示所有未过期的登录会话</p>
          <p>• "在线连接"统计当前通过 SSE 保持实时推送的连接数</p>
          <p>• 会话在 Token 过期后自动清除</p>
        </div>
      </div>
    </div>
  );
}

