/**
 * 技能 Section — 写作风格悬浮窗的「技能」区
 *
 * 两层结构（docs/34）：
 *   1. 已安装技能包：下载安装到固定目录 data/packages/skill/<slug>/<version>/，
 *      注册信息来自 installed_packages，可卸载；
 *   2. 高级：运行时工具插件（tool-plugins，HTTP 工具包动态加载），保持原有
 *      只读列举。
 */
import { useCallback, useEffect, useState } from "react";
import { Puzzle, RefreshCw, Wrench, Package, Download, Trash2, Loader2, ChevronDown } from "lucide-react";
import { adminFetch } from "@/lib/admin-api";
import { AdminLoading, AdminEmptyState } from "@/components/admin-ui";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useToastStore } from "@/stores/toast-store";
import { PackageInstallDialog } from "@/components/styles/package-install-dialog";
import { cn } from "@/lib/utils";
import {
  type InstalledPackage,
  listInstalledPackages, uninstallPackage, PACKAGE_ERROR_LABELS,
} from "@/lib/packages-api";

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
  const notify = (
    type: "success" | "error" | "warning" | "info",
    title: string,
    description?: string,
  ) => useToastStore.getState().add({ type, title, description, duration: 3000 });

  const [packages, setPackages] = useState<InstalledPackage[]>([]);
  const [loading, setLoading] = useState(true);
  const [showInstall, setShowInstall] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);

  // 高级区：运行时工具插件
  const [showPlugins, setShowPlugins] = useState(false);
  const [plugins, setPlugins] = useState<PluginEntry[]>([]);
  const [pluginsLoading, setPluginsLoading] = useState(false);

  const loadPackages = useCallback(async () => {
    setLoading(true);
    try {
      setPackages(await listInstalledPackages("skill"));
    } catch {
      // silent
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void loadPackages(); }, [loadPackages]);

  const loadPlugins = useCallback(async () => {
    setPluginsLoading(true);
    try {
      const { success, data } = await adminFetch<{ plugins: PluginEntry[]; total: number }>(
        "/api/v2/admin/tool-plugins",
        { silent: true },
      );
      if (success && data) setPlugins(data.plugins ?? []);
    } catch {
      // silent
    } finally {
      setPluginsLoading(false);
    }
  }, []);

  useEffect(() => {
    if (showPlugins && plugins.length === 0) void loadPlugins();
  }, [showPlugins, plugins.length, loadPlugins]);

  const handleUninstall = async (pkg: InstalledPackage) => {
    if (!confirm(`确定卸载技能包「${pkg.title || pkg.slug}」？固定目录下的包体会一并删除。`)) return;
    setBusy(pkg.id);
    try {
      await uninstallPackage(pkg.id);
      notify("success", `已卸载 ${pkg.title || pkg.slug}`);
      await loadPackages();
    } catch (e) {
      const err = e as Error & { code?: string };
      notify("error", PACKAGE_ERROR_LABELS[err.code ?? ""] ?? "卸载失败", err.message);
    } finally {
      setBusy(null);
    }
  };

  const totalTools = plugins.reduce((sum, p) => sum + pluginTools(p).length, 0);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-xs text-muted-foreground">
          已安装技能包 {packages.length} 个 · 安装到固定目录，随版本共存
        </p>
        <Button size="sm" className="gap-1.5" onClick={() => setShowInstall(true)}>
          <Download className="h-4 w-4" /> 安装技能包
        </Button>
      </div>

      {loading ? (
        <AdminLoading />
      ) : packages.length === 0 ? (
        <AdminEmptyState
          icon={Package}
          title="还没有安装技能包"
          description="技能包以 zip 形式下载安装到固定目录（SKILL.md + 脚本）。从内置目录或包地址安装。"
        />
      ) : (
        <div className="space-y-2">
          {packages.map((pkg) => (
            <Card key={pkg.id}>
              <CardContent className="p-4 space-y-2">
                <div className="flex items-center gap-2 flex-wrap">
                  <Package className="h-4 w-4 text-primary" />
                  <span className="text-sm font-medium">{pkg.title || pkg.slug}</span>
                  <Badge variant="outline" className="text-[10px]">v{pkg.version}</Badge>
                  <Badge variant="secondary" className="text-[10px]">{pkg.source === "builtin" ? "内置" : "URL"}</Badge>
                  <Button
                    size="sm" variant="ghost"
                    className="ml-auto h-7 gap-1 text-xs text-muted-foreground hover:text-destructive"
                    disabled={busy === pkg.id}
                    onClick={() => void handleUninstall(pkg)}
                  >
                    {busy === pkg.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <Trash2 className="h-3 w-3" />}
                    卸载
                  </Button>
                </div>
                {pkg.description && (
                  <p className="text-xs text-muted-foreground">{pkg.description}</p>
                )}
                <p className="text-[11px] text-muted-foreground truncate font-mono">{pkg.install_path}</p>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* 高级：运行时工具插件 */}
      <div className="rounded-lg border">
        <button
          onClick={() => setShowPlugins((v) => !v)}
          className="flex w-full items-center justify-between px-4 py-2.5 text-sm transition-ui hover:bg-accent/30"
        >
          <span className="flex items-center gap-2 font-medium">
            <Wrench className="h-4 w-4 text-muted-foreground" />
            高级：运行时工具插件
          </span>
          <ChevronDown className={cn("h-4 w-4 text-muted-foreground transition-transform", showPlugins && "rotate-180")} />
        </button>
        {showPlugins && (
          <div className="space-y-3 border-t px-4 py-3">
            <div className="flex items-center justify-between">
              <p className="text-xs text-muted-foreground">
                已加载技能插件 {plugins.length} 个 · 工具 {totalTools} 个
              </p>
              <Button variant="outline" size="sm" onClick={() => void loadPlugins()} disabled={pluginsLoading}>
                <RefreshCw className={cn("h-4 w-4 mr-1.5", pluginsLoading && "animate-spin")} /> 刷新
              </Button>
            </div>
            {pluginsLoading ? (
              <AdminLoading />
            ) : plugins.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                暂无已加载的插件。可通过 POST /api/v2/admin/tool-plugins 加载。
              </p>
            ) : (
              <div className="space-y-2">
                {plugins.map((entry, i) => {
                  const tools = pluginTools(entry);
                  return (
                    <div key={entry.name || i} className="rounded-lg border p-3 space-y-1.5">
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
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        )}
      </div>

      <PackageInstallDialog
        open={showInstall}
        onClose={() => setShowInstall(false)}
        kind="skill"
        onChanged={() => void loadPackages()}
      />
    </div>
  );
}
