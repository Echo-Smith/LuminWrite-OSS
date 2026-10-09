/**
 * PackageInstallDialog — 包安装弹窗（下载安装，docs/34）
 *
 * 两个来源：内置目录（随发布包内置）/ 粘贴包地址（公网 http/https zip）。
 * 安装不携带凭据：服务包只注册 MCP 服务器，密钥在 MCP 服务界面单独配置。
 */
import { useEffect, useState } from "react";
import { Package, Download, Loader2, Trash2, Link2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { useToastStore } from "@/stores/toast-store";
import { SimpleModal } from "@/pages/personal/shared";
import {
  type CatalogEntry, type InstalledPackage, type PackageKind,
  listCatalog, listInstalledPackages, installPackage, uninstallPackage,
  PACKAGE_ERROR_LABELS,
} from "@/lib/packages-api";

const KIND_LABELS: Record<PackageKind, string> = {
  style: "风格",
  skill: "技能",
  service: "服务",
};

interface PackageInstallDialogProps {
  open: boolean;
  onClose: () => void;
  /** 只展示某类包（风格库/技能/MCP 区各自打开） */
  kind?: PackageKind;
  /** 安装或卸载成功后刷新外部列表 */
  onChanged?: () => void;
}

export function PackageInstallDialog({ open, onClose, kind, onChanged }: PackageInstallDialogProps) {
  const notify = (
    type: "success" | "error" | "warning" | "info",
    title: string,
    description?: string,
  ) => useToastStore.getState().add({ type, title, description, duration: 3000 });

  const [catalog, setCatalog] = useState<CatalogEntry[]>([]);
  const [installed, setInstalled] = useState<InstalledPackage[]>([]);
  const [loading, setLoading] = useState(false);
  const [installing, setInstalling] = useState<string | null>(null);
  const [url, setUrl] = useState("");

  const reload = async () => {
    setLoading(true);
    try {
      const [catalogList, installedList] = await Promise.all([
        listCatalog(),
        listInstalledPackages(kind),
      ]);
      setCatalog(kind ? catalogList.filter((c) => c.kind === kind) : catalogList);
      setInstalled(installedList);
    } catch {
      // 目录加载失败不阻塞已安装列表展示
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (open) void reload();
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleInstall = async (input: { source: "builtin" | "url"; slug?: string; url?: string }, label: string) => {
    const key = input.slug ?? input.url ?? "";
    setInstalling(key);
    try {
      await installPackage(input);
      notify("success", `${label} 安装成功`, "包体已存入固定目录，注册信息可在此管理");
      await reload();
      onChanged?.();
    } catch (e) {
      const err = e as Error & { code?: string };
      notify("error", PACKAGE_ERROR_LABELS[err.code ?? ""] ?? "安装失败", err.message);
    } finally {
      setInstalling(null);
    }
  };

  const handleUninstall = async (pkg: InstalledPackage) => {
    setInstalling(pkg.id);
    try {
      await uninstallPackage(pkg.id);
      notify("success", `已卸载 ${pkg.title || pkg.slug}`);
      await reload();
      onChanged?.();
    } catch (e) {
      notify("error", "卸载失败", (e as Error).message);
    } finally {
      setInstalling(null);
    }
  };

  const isInstalled = (slug: string) => installed.some((p) => p.slug === slug);

  return (
    <SimpleModal open={open} onClose={onClose} title={kind ? `安装${KIND_LABELS[kind]}包` : "安装包"} maxWidth="max-w-xl">
      <div className="space-y-5">
        {/* 已安装 */}
        <div>
          <h4 className="text-sm font-medium mb-2">已安装</h4>
          {installed.length === 0 ? (
            <p className="text-xs text-muted-foreground">还没有安装任何{kind ? KIND_LABELS[kind] : ""}包。</p>
          ) : (
            <div className="space-y-1.5">
              {installed.map((pkg) => (
                <div key={pkg.id} className="flex items-center gap-2 rounded-lg border p-2.5">
                  <Package className="h-4 w-4 text-muted-foreground shrink-0" />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-1.5">
                      <span className="text-sm font-medium truncate">{pkg.title || pkg.slug}</span>
                      <Badge variant="outline" className="text-[10px]">v{pkg.version}</Badge>
                      <Badge variant="secondary" className="text-[10px]">{pkg.source === "builtin" ? "内置" : "URL"}</Badge>
                    </div>
                    <p className="text-[11px] text-muted-foreground truncate font-mono">{pkg.install_path}</p>
                  </div>
                  <Button
                    size="sm" variant="ghost" className="shrink-0 text-muted-foreground hover:text-destructive"
                    disabled={installing === pkg.id}
                    onClick={() => void handleUninstall(pkg)}
                  >
                    {installing === pkg.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5" />}
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* 内置目录 */}
        <div>
          <h4 className="text-sm font-medium mb-2">内置目录</h4>
          {loading ? (
            <div className="flex justify-center py-4">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : catalog.length === 0 ? (
            <p className="text-xs text-muted-foreground">目录为空。</p>
          ) : (
            <div className="space-y-1.5">
              {catalog.map((entry) => {
                const done = isInstalled(entry.slug);
                return (
                  <div key={`${entry.kind}/${entry.slug}`} className="flex items-center gap-2 rounded-lg border p-2.5">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-1.5">
                        <span className="text-sm font-medium truncate">{entry.title}</span>
                        <Badge variant="outline" className="text-[10px]">{KIND_LABELS[entry.kind]}</Badge>
                        <Badge variant="secondary" className="text-[10px]">v{entry.version}</Badge>
                      </div>
                      <p className="text-xs text-muted-foreground line-clamp-1">{entry.description}</p>
                    </div>
                    <Button
                      size="sm" className="shrink-0 gap-1.5"
                      disabled={done || installing === entry.slug}
                      onClick={() => void handleInstall({ source: "builtin", slug: entry.slug }, entry.title)}
                    >
                      {installing === entry.slug ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      ) : (
                        <Download className="h-3.5 w-3.5" />
                      )}
                      {done ? "已安装" : "安装"}
                    </Button>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        {/* 包地址安装 */}
        <div>
          <h4 className="text-sm font-medium mb-2">从包地址安装</h4>
          <p className="text-xs text-muted-foreground mb-2">
            粘贴公网可用的 zip 包地址（http/https）。服务端会校验地址安全性后下载解压。
          </p>
          <div className="flex gap-2">
            <Input
              className="font-mono text-xs"
              placeholder="https://example.com/packages/my-pack.zip"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
            />
            <Button
              className="shrink-0 gap-1.5"
              disabled={!url.trim() || installing === url.trim()}
              onClick={() => void handleInstall({ source: "url", url: url.trim() }, "包")}
            >
              {installing === url.trim() ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Link2 className="h-3.5 w-3.5" />}
              安装
            </Button>
          </div>
        </div>
      </div>
    </SimpleModal>
  );
}
