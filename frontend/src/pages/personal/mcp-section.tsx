/**
 * MCP 已安装服务 Section — 个人中心
 *
 * 查看/管理部署中已安装的 MCP 服务：状态、工具清单、重连。
 * 服务密钥与沙箱策略在「插件」页（左侧栏）管理。
 */
import { useCallback, useEffect, useState } from "react";
import { RefreshCw, Server, Wrench, Unplug, Loader2 } from "lucide-react";
import { adminFetch, adminMutate } from "@/lib/admin-api";
import { AdminLoading, AdminEmptyState } from "@/components/admin";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { useToastStore } from "@/stores/toast-store";

interface McpServer {
  id: string;
  name: string;
  transport?: string;
  command?: string;
  url?: string;
  description?: string;
  is_active?: boolean;
  status?: string;
  connected?: boolean;
  tool_count?: number;
  tools?: Array<{ name?: string }>;
}

function statusBadge(s: McpServer) {
  const connected = s.connected ?? s.status === "connected";
  return (
    <Badge
      variant="outline"
      className={
        connected
          ? "text-[10px] text-green-600 border-green-300"
          : "text-[10px] text-muted-foreground"
      }
    >
      {connected ? "已连接" : (s.status ?? "未连接")}
    </Badge>
  );
}

export function McpSection() {
  const notify = (
    type: "success" | "error",
    title: string,
    description?: string,
  ) => useToastStore.getState().add({ type, title, description, duration: 3000 });

  const [servers, setServers] = useState<McpServer[]>([]);
  const [loading, setLoading] = useState(true);
  const [reconnecting, setReconnecting] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const { success, data } = await adminFetch<{ servers: McpServer[] }>(
        "/api/v2/admin/mcp/servers",
        { silent: true },
      );
      if (success && data) setServers(data.servers ?? []);
    } catch {
      // silent
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const reconnect = async (s: McpServer) => {
    setReconnecting(s.id);
    try {
      const result = await adminMutate(`/api/v2/admin/mcp/servers/${s.id}/reconnect`, {
        method: "POST",
        successTitle: `${s.name} 已重连`,
      });
      if (result.success) await load();
    } catch (e) {
      notify("error", "重连失败", e instanceof Error ? e.message : undefined);
    } finally {
      setReconnecting(null);
    }
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-xs text-muted-foreground">已安装的 MCP 服务与工具状态</p>
        <button
          onClick={() => void load()}
          className="text-muted-foreground hover:text-foreground transition-colors"
          title="刷新"
        >
          <RefreshCw className={`h-4 w-4 ${loading ? "animate-spin" : ""}`} />
        </button>
      </div>

      {loading ? (
        <AdminLoading />
      ) : servers.length === 0 ? (
        <AdminEmptyState
          icon={Server}
          title="暂无已安装的 MCP 服务"
          description="在左侧栏「插件 → 服务密钥」中添加 MCP 服务后，这里会显示连接状态与工具清单。"
        />
      ) : (
        <div className="space-y-2">
          {servers.map((s) => (
            <Card key={s.id} className="overflow-hidden">
              <CardContent className="flex items-start gap-3 py-3">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-sm font-medium">{s.name}</span>
                    {statusBadge(s)}
                    {s.transport && <Badge variant="secondary" className="text-[10px]">{s.transport}</Badge>}
                  </div>
                  {s.description && (
                    <p className="mt-1 text-xs text-muted-foreground">{s.description}</p>
                  )}
                  {(s.command || s.url) && (
                    <p className="mt-1 text-[11px] font-mono text-muted-foreground/70 truncate">
                      {s.command || s.url}
                    </p>
                  )}
                  {s.tools && s.tools.length > 0 && (
                    <div className="mt-1.5 flex flex-wrap gap-1">
                      {s.tools.slice(0, 8).map((t, i) => (
                        <span key={i} className="inline-flex items-center gap-0.5 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                          <Wrench className="h-2.5 w-2.5" /> {t.name ?? "?"}
                        </span>
                      ))}
                      {s.tools.length > 8 && (
                        <span className="text-[10px] text-muted-foreground">+{s.tools.length - 8}</span>
                      )}
                    </div>
                  )}
                </div>
                <button
                  onClick={() => void reconnect(s)}
                  disabled={reconnecting === s.id}
                  className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50 shrink-0"
                  title="重连"
                >
                  {reconnecting === s.id ? <Loader2 className="h-4 w-4 animate-spin" /> : <Unplug className="h-4 w-4" />}
                </button>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
