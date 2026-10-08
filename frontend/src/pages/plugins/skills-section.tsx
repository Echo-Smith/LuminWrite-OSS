/**
 * 技能插件 Section — 已加载的运行时技能/工具插件
 *
 * 数据来自 GET /api/v2/admin/tool-plugins（运行时动态加载的 HTTP 工具插件）。
 * 仓库根 skills/ 包（写作技能）随部署分发，其加载状态同样在此可见。
 */
import { useCallback, useEffect, useState } from "react";
import { Puzzle, RefreshCw, Wrench } from "lucide-react";
import { adminFetch } from "@/lib/admin-api";
import { AdminLoading, AdminEmptyState } from "@/components/admin-ui";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

interface ToolPlugin {
  name: string;
  description?: string;
  version?: string;
  tools?: Array<{ name?: string; description?: string }>;
}

interface PluginEntry {
  name: string;
  plugin?: ToolPlugin;
  tool_count?: number;
}

function pluginTools(entry: PluginEntry): Array<{ name?: string }> {
  if (entry.plugin?.tools) return entry.plugin.tools;
  if (Array.isArray((entry as unknown as { tools?: [] }).tools)) {
    return (entry as unknown as { tools: [] }).tools;
  }
  return [];
}

function pluginField(entry: PluginEntry, field: "description" | "version"): string {
  const fromPlugin = entry.plugin?.[field];
  if (fromPlugin) return String(fromPlugin);
  const direct = (entry as unknown as Record<string, unknown>)[field];
  return direct ? String(direct) : "";
}

export function SkillsSection() {
  const [plugins, setPlugins] = useState<PluginEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);

  const load = useCallback(async (refresh = false) => {
    if (refresh) {
      setRefreshing(true);
    } else {
      setLoading(true);
    }
    try {
      const { success, data } = await adminFetch<{ plugins: PluginEntry[]; total: number }>(
        "/api/v2/admin/tool-plugins",
        { silent: true },
      );
      if (success && data) setPlugins(data.plugins ?? []);
    } catch {
      // silent
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const totalTools = plugins.reduce((sum, p) => sum + pluginTools(p).length, 0);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-xs text-muted-foreground">
          已加载技能插件 {plugins.length} 个 · 工具 {totalTools} 个
        </p>
        <Button variant="outline" size="sm" onClick={() => void load(true)} disabled={refreshing}>
          <RefreshCw className={`h-4 w-4 mr-1.5 ${refreshing ? "animate-spin" : ""}`} /> 刷新
        </Button>
      </div>

      {loading ? (
        <AdminLoading />
      ) : plugins.length === 0 ? (
        <AdminEmptyState
          icon={Puzzle}
          title="暂无已加载的技能插件"
          description="技能插件是运行时动态加载的 HTTP 工具包。可通过 POST /api/v2/admin/tool-plugins 加载，或随部署的 skills/ 包提供。"
        />
      ) : (
        <div className="space-y-2">
          {plugins.map((entry, i) => {
            const tools = pluginTools(entry);
            return (
              <Card key={entry.name || i}>
                <CardContent className="p-4 space-y-2">
                  <div className="flex items-center gap-2 flex-wrap">
                    <Puzzle className="h-4 w-4 text-primary" />
                    <span className="text-sm font-medium">{entry.name}</span>
                    {pluginField(entry, "version") && (
                      <Badge variant="outline" className="text-[10px]">v{pluginField(entry, "version")}</Badge>
                    )}
                    <Badge variant="secondary" className="text-[10px] ml-auto">
                      <Wrench className="mr-0.5 h-3 w-3" /> {tools.length} 工具
                    </Badge>
                  </div>
                  {pluginField(entry, "description") && (
                    <p className="text-xs text-muted-foreground">{pluginField(entry, "description")}</p>
                  )}
                  {tools.length > 0 && (
                    <div className="flex flex-wrap gap-1.5 pt-1">
                      {tools.map((t, j) => (
                        <Badge key={j} variant="outline" className="text-[10px] font-normal">
                          {t.name ?? `tool-${j + 1}`}
                        </Badge>
                      ))}
                    </div>
                  )}
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}
    </div>
  );
}
