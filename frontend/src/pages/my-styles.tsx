/**
 * 我的风格 — 用户自定义风格管理
 */
import { useState, useEffect, useCallback } from "react";
import { Plus, Trash2, Send, CheckCircle2, Clock, XCircle, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { StyleBuilderDialog } from "@/components/composer/style-builder-dialog";
import { useAuthStore } from "@/stores/auth-store";

interface MyStyle {
  id: string;
  slug: string;
  name: string;
  description: string;
  status: string;
  current_version: number;
  created_at: string;
  updated_at: string;
}

const STATUS_MAP: Record<string, { label: string; icon: typeof CheckCircle2; color: string }> = {
  draft: { label: "草稿", icon: Clock, color: "text-muted-foreground" },
  pending_review: { label: "审核中", icon: Clock, color: "text-blue-500" },
  approved: { label: "已通过", icon: CheckCircle2, color: "text-green-500" },
  rejected: { label: "已驳回", icon: XCircle, color: "text-red-500" },
};

export function MyStylesPage() {
  const [styles, setStyles] = useState<MyStyle[]>([]);
  const [loading, setLoading] = useState(false);
  const [builderOpen, setBuilderOpen] = useState(false);
  const [submitting, setSubmitting] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const token = useAuthStore((s) => s.token);

  const loadStyles = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch("/api/v2/my-styles", {
        headers: { Authorization: `Bearer ${token}` },
      });
      const json = await res.json();
      if (json.success) {
        setStyles(json.data?.styles ?? []);
      } else {
        setError(json.error?.message ?? "加载失败");
      }
    } catch {
      setError("网络错误");
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    loadStyles();
  }, [loadStyles]);

  const handleSubmit = async (id: string) => {
    setSubmitting(id);
    setError(null);
    try {
      const res = await fetch(`/api/v2/my-styles/${id}/submit`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      });
      const json = await res.json();
      if (json.success) {
        await loadStyles();
      } else {
        setError(json.error?.message ?? "提交失败");
      }
    } catch {
      setError("网络错误");
    } finally {
      setSubmitting(null);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("确定删除这个风格吗？")) return;
    setError(null);
    try {
      const res = await fetch(`/api/v2/my-styles/${id}`, {
        method: "DELETE",
        headers: { Authorization: `Bearer ${token}` },
      });
      const json = await res.json();
      if (json.success) {
        await loadStyles();
      } else {
        setError(json.error?.message ?? "删除失败");
      }
    } catch {
      setError("网络错误");
    }
  };

  return (
    <div className="mx-auto max-w-4xl p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">我的风格</h1>
          <p className="text-sm text-muted-foreground mt-1">管理你的自定义写作风格</p>
        </div>
        <Button onClick={() => setBuilderOpen(true)} className="gap-1.5">
          <Plus className="h-4 w-4" />
          AI 创建风格
        </Button>
      </div>

      {error && (
        <div className="rounded-md bg-destructive/10 px-4 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      {loading ? (
        <div className="flex justify-center py-12">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </div>
      ) : styles.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center justify-center py-12">
            <p className="text-muted-foreground text-sm mb-4">还没有自定义风格</p>
            <Button variant="outline" onClick={() => setBuilderOpen(true)} className="gap-1.5">
              <Plus className="h-4 w-4" />
              创建第一个风格
            </Button>
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-4">
          {styles.map((style) => {
            const statusInfo = STATUS_MAP[style.status] ?? STATUS_MAP.draft;
            const StatusIcon = statusInfo.icon;
            return (
              <Card key={style.id}>
                <CardHeader className="flex flex-row items-start justify-between space-y-0">
                  <div className="space-y-1">
                    <CardTitle className="text-base">{style.name}</CardTitle>
                    <p className="text-sm text-muted-foreground">{style.description}</p>
                  </div>
                  <div className="flex items-center gap-2">
                    <Badge variant="outline" className={`gap-1 ${statusInfo.color}`}>
                      <StatusIcon className="h-3 w-3" />
                      {statusInfo.label}
                    </Badge>
                    {style.current_version > 0 && (
                      <Badge variant="secondary">v{style.current_version}</Badge>
                    )}
                  </div>
                </CardHeader>
                <CardContent>
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-muted-foreground">
                      更新于 {new Date(style.updated_at).toLocaleDateString("zh-CN")}
                    </span>
                    <div className="flex gap-2">
                      {style.status === "draft" && style.current_version > 0 && (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => handleSubmit(style.id)}
                          disabled={submitting === style.id}
                          className="gap-1.5"
                        >
                          {submitting === style.id ? (
                            <Loader2 className="h-3 w-3 animate-spin" />
                          ) : (
                            <Send className="h-3 w-3" />
                          )}
                          提交审核
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => handleDelete(style.id)}
                        className="text-muted-foreground hover:text-destructive"
                      >
                        <Trash2 className="h-3 w-3" />
                      </Button>
                    </div>
                  </div>
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      <StyleBuilderDialog
        open={builderOpen}
        onOpenChange={setBuilderOpen}
        onCreated={() => loadStyles()}
      />
    </div>
  );
}
