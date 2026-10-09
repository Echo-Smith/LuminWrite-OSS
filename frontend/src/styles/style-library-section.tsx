/**
 * 风格库 Section — 内置/全局风格浏览与一键导入
 *
 * 数据来自 GET /api/v2/styles（全局 + 用户自定义）；本区只展示全局部分
 * （不带「自定义」标签），支持一键导入为「我的风格」（POST /api/v2/my-styles，
 * 优先携带完整 config；slug 冲突时自动加后缀）。
 */
import { useState, useEffect, useCallback } from "react";
import { Library, Download, Loader2, Check } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/stores/auth-store";
import { useStyleListStore } from "@/stores/style-list-store";
import { toast } from "@/stores/toast-store";
import type { StyleOption } from "@/lib/types";

interface StyleLibrarySectionProps {
  /** 导入成功后刷新「我的风格」区 */
  onImported?: () => void;
}

export function StyleLibrarySection({ onImported }: StyleLibrarySectionProps) {
  const token = useAuthStore((s) => s.token);
  const isGuest = useAuthStore((s) => s.user?.role === "guest");
  const listVersion = useStyleListStore((s) => s.version);

  const [styles, setStyles] = useState<StyleOption[]>([]);
  const [loading, setLoading] = useState(true);
  const [importing, setImporting] = useState<string | null>(null);
  const [imported, setImported] = useState<Set<string>>(new Set());

  const load = useCallback(() => {
    const headers: Record<string, string> = {};
    if (token) headers.Authorization = `Bearer ${token}`;
    fetch("/api/v2/styles", { headers })
      .then((res) => res.json())
      .then((data) => {
        const all = (data?.data?.styles ?? data?.styles ?? []) as StyleOption[];
        // 仅全局/内置（自定义标签为用户自己的风格，在「我的风格」区管理）
        setStyles(all.filter((s) => !s.tags?.includes("自定义")));
      })
      .catch(() => setStyles([]))
      .finally(() => setLoading(false));
  }, [token]);

  useEffect(() => { load(); }, [load, listVersion]);

  const handleImport = async (style: StyleOption) => {
    setImporting(style.slug);
    try {
      // 取完整配置（含 system_prompt 等），失败也能按名称/描述导入空壳
      let config: unknown;
      try {
        const detailRes = await fetch(`/api/v2/styles/${encodeURIComponent(style.slug)}`, {
          headers: token ? { Authorization: `Bearer ${token}` } : {},
        });
        const detail = await detailRes.json();
        const d = detail?.data ?? detail;
        if (d?.config) config = d.config;
        else if (d?.system_prompt) config = d;
      } catch {
        // 忽略：走空壳导入
      }

      // slug 冲突时加后缀（后端对同用户 slug 唯一）
      let slug = style.slug;
      if (imported.has(style.slug)) slug = `${style.slug}_${Date.now() % 100000}`;

      const body: Record<string, unknown> = {
        slug,
        name: style.name,
        description: style.description ?? "",
      };
      if (config) body.config = config;

      const res = await fetch("/api/v2/my-styles", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify(body),
      });
      const json = await res.json();
      if (json.success) {
        setImported((prev) => new Set(prev).add(style.slug));
        useStyleListStore.getState().requestRefresh();
        toast.success("已导入到我的风格", style.name);
        onImported?.();
      } else {
        toast.error("导入失败", json.error?.message ?? "请稍后重试");
      }
    } catch {
      toast.error("导入失败", "网络错误");
    } finally {
      setImporting(null);
    }
  };

  if (loading) {
    return (
      <div className="flex justify-center py-12">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (styles.length === 0) {
    return (
      <div className="py-12 text-center">
        <Library className="mx-auto h-12 w-12 text-muted-foreground/30" />
        <p className="mt-3 text-sm text-muted-foreground">暂无内置风格</p>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        内置风格开箱即用；点击「导入」可复制到我的风格后自由改造。
      </p>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        {styles.map((style) => {
          const done = imported.has(style.slug);
          return (
            <Card key={style.slug} className="overflow-hidden">
              <CardContent className="p-4 space-y-2">
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="flex items-center gap-1.5">
                      <span className="text-sm font-medium truncate">{style.name}</span>
                      <span className="text-xs text-muted-foreground">v{style.version}</span>
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground line-clamp-2">
                      {style.description || "暂无描述"}
                    </p>
                  </div>
                  <Button
                    size="sm"
                    variant={done ? "outline" : "default"}
                    disabled={isGuest || importing === style.slug}
                    onClick={() => void handleImport(style)}
                    className="shrink-0 gap-1.5"
                  >
                    {importing === style.slug ? (
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : done ? (
                      <Check className="h-3.5 w-3.5" />
                    ) : (
                      <Download className="h-3.5 w-3.5" />
                    )}
                    {done ? "已导入" : "导入"}
                  </Button>
                </div>
                <div className="flex flex-wrap items-center gap-1.5">
                  <Badge variant="secondary" className="text-[10px]">
                    {style.word_range?.[0] ?? "?"}-{style.word_range?.[1] ?? "?"} 字
                  </Badge>
                  {(style.tags ?? []).map((tag) => (
                    <Badge key={tag} variant="outline" className="text-[10px]">{tag}</Badge>
                  ))}
                </div>
              </CardContent>
            </Card>
          );
        })}
      </div>
      {isGuest && (
        <p className="text-center text-xs text-amber-600">注册账号后可导入风格</p>
      )}
    </div>
  );
}
