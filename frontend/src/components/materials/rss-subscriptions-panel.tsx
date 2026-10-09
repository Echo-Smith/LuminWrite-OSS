/**
 * RSS 订阅管理面板 — 素材中心左侧「RSS 订阅」入口
 *
 * 订阅源 = 自动更新的素材文件夹：新条目由后端周期性抓取（默认 15 分钟，
 * 每源 3 条、全局 20 条/次），以素材形式落入目标文件夹，可被写作检索。
 */
import { useCallback, useEffect, useRef, useState } from "react";
import {
  Rss, Plus, Trash2, RefreshCw, Loader2, AlertTriangle, FolderOpen, Pencil, Upload, Download,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { SimpleModal } from "@/pages/personal/shared";
import { useAuthStore } from "@/stores/auth-store";
import { useToastStore } from "@/stores/toast-store";
import { cn } from "@/lib/utils";
import {
  type RSSSubscription,
  listRSSSubscriptions, createRSSSubscription, updateRSSSubscription,
  deleteRSSSubscription, refreshRSSSubscription, exportRSSOPML, importRSSOPML,
  RSS_ERROR_LABELS,
} from "@/lib/rss-api";

export interface MaterialFolderOption {
  id: string;
  name: string;
}

interface RSSSubscriptionsPanelProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  folders: MaterialFolderOption[];
  onSubscriptionsChanged?: () => void;
}

function formatTime(ts?: string): string {
  if (!ts) return "从未";
  const d = new Date(ts);
  return d.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

export function RSSSubscriptionsPanel({ open, onOpenChange, folders, onSubscriptionsChanged }: RSSSubscriptionsPanelProps) {
  const token = useAuthStore((s) => s.token);
  const isGuest = useAuthStore((s) => s.user?.role === "guest");
  const notify = (
    type: "success" | "error" | "warning" | "info",
    title: string,
    description?: string,
  ) => useToastStore.getState().add({ type, title, description, duration: 3000 });

  const [subs, setSubs] = useState<RSSSubscription[]>([]);
  const [loading, setLoading] = useState(false);
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<RSSSubscription | null>(null);
  const [deleting, setDeleting] = useState<RSSSubscription | null>(null);
  const [refreshing, setRefreshing] = useState<string | null>(null);
  const opmlInputRef = useRef<HTMLInputElement>(null);

  // 表单状态
  const [feedUrl, setFeedUrl] = useState("");
  const [title, setTitle] = useState("");
  const [folderId, setFolderId] = useState("");
  const [maxItems, setMaxItems] = useState("3");
  const [fetchFullText, setFetchFullText] = useState(false);
  const [isActive, setIsActive] = useState(true);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    if (!token || isGuest) return;
    setLoading(true);
    try {
      setSubs(await listRSSSubscriptions());
    } catch (e) {
      notify("error", "订阅列表加载失败", (e as Error).message);
    } finally {
      setLoading(false);
    }
  }, [token, isGuest]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (open) void load();
  }, [open, load]);

  const resetForm = () => {
    setFeedUrl(""); setTitle(""); setFolderId(""); setMaxItems("3");
    setFetchFullText(false); setIsActive(true);
    setEditing(null);
  };

  const openAdd = () => { resetForm(); setShowAdd(true); };

  const openEdit = (sub: RSSSubscription) => {
    setEditing(sub);
    setTitle(sub.title);
    setFolderId(sub.target_folder_id ?? "");
    setMaxItems(String(sub.max_items_per_tick || 3));
    setFetchFullText(sub.fetch_full_text ?? false);
    setIsActive(sub.is_active);
    setShowAdd(true);
  };

  const handleSave = async () => {
    setSaving(true);
    try {
      if (editing) {
        await updateRSSSubscription(editing.id, {
          title, target_folder_id: folderId,
          max_items_per_tick: Number(maxItems) || 3,
          fetch_full_text: fetchFullText, is_active: isActive,
        });
        notify("success", "订阅已更新");
      } else {
        if (!feedUrl.trim()) { notify("warning", "请填写订阅地址"); setSaving(false); return; }
        await createRSSSubscription({
          feed_url: feedUrl.trim(),
          target_folder_id: folderId || undefined,
          max_items_per_tick: Number(maxItems) || 3,
          fetch_full_text: fetchFullText,
        });
        notify("success", "订阅成功，首批条目正在入库");
      }
      setShowAdd(false);
      resetForm();
      await load();
      onSubscriptionsChanged?.();
    } catch (e) {
      const err = e as Error & { code?: string };
      notify("error", RSS_ERROR_LABELS[err.code ?? ""] ?? "保存失败", err.message);
    } finally {
      setSaving(false);
    }
  };

  const handleRefresh = async (sub: RSSSubscription) => {
    setRefreshing(sub.id);
    try {
      const r = await refreshRSSSubscription(sub.id);
      if (r.error) {
        notify("error", "更新失败", r.error);
      } else if (r.not_modified) {
        notify("info", "暂无新内容");
      } else {
        notify("success", `新增 ${r.imported} 条素材`, r.skipped > 0 ? `${r.skipped} 条已存在或跳过` : undefined);
      }
      await load();
      onSubscriptionsChanged?.();
    } catch (e) {
      notify("error", "更新失败", (e as Error).message);
    } finally {
      setRefreshing(null);
    }
  };

  const handleDelete = async () => {
    if (!deleting) return;
    try {
      await deleteRSSSubscription(deleting.id);
      notify("success", "订阅已删除", "已导入的素材会保留");
      setDeleting(null);
      await load();
    } catch (e) {
      notify("error", "删除失败", (e as Error).message);
    }
  };

  if (isGuest) {
    return (
      <SimpleModal open={open} onClose={() => onOpenChange(false)} title="RSS 订阅" maxWidth="max-w-lg">
        <p className="text-sm text-muted-foreground text-center py-6">注册账号后可订阅 RSS 源，新文章自动进入你的知识库。</p>
      </SimpleModal>
    );
  }

  return (
    <SimpleModal open={open} onClose={() => onOpenChange(false)} title="RSS 订阅" maxWidth="max-w-2xl">
      <div className="space-y-4">
        <div className="flex items-start justify-between gap-3">
          <p className="text-xs text-muted-foreground">
            订阅源的新文章会自动抓取并入库为素材（默认每 15 分钟，每源每次最多 3 条），
            落入下方指定的文件夹，可被写作时的知识库检索使用。
          </p>
          <div className="flex gap-1.5 shrink-0">
            <Button
              size="sm" variant="outline" className="h-7 gap-1 text-xs"
              disabled={subs.length === 0}
              onClick={async () => {
                try { await exportRSSOPML(); }
                catch (e) { notify("error", "导出失败", (e as Error).message); }
              }}
            >
              <Download className="h-3 w-3" /> 导出 OPML
            </Button>
            <Button
              size="sm" variant="outline" className="h-7 gap-1 text-xs"
              onClick={() => opmlInputRef.current?.click()}
            >
              <Upload className="h-3 w-3" /> 导入 OPML
            </Button>
            <input
              ref={opmlInputRef}
              type="file" accept=".opml,.xml" className="hidden"
              onChange={async (e) => {
                const file = e.target.files?.[0];
                e.target.value = "";
                if (!file) return;
                try {
                  const xml = await file.text();
                  const r = await importRSSOPML(xml);
                  notify("success", `导入完成：新增 ${r.imported} 个订阅`,
                    r.skipped > 0 ? `${r.skipped} 个已存在或无效` : undefined);
                  await load();
                  onSubscriptionsChanged?.();
                  if (r.errors?.length) {
                    notify("warning", `${r.failed} 个源导入失败`, r.errors.slice(0, 3).join("；"));
                  }
                } catch (err) {
                  notify("error", "导入失败", (err as Error).message);
                }
              }}
            />
          </div>
        </div>

        {loading ? (
          <div className="flex justify-center py-8">
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
          </div>
        ) : subs.length === 0 ? (
          <div className="py-8 text-center">
            <Rss className="mx-auto h-10 w-10 text-muted-foreground/30" />
            <p className="mt-3 text-sm text-muted-foreground">还没有订阅任何源</p>
            <Button className="mt-4" size="sm" onClick={openAdd}>
              <Plus className="mr-1.5 h-4 w-4" /> 添加订阅
            </Button>
          </div>
        ) : (
          <div className="space-y-2">
            {subs.map((sub) => (
              <Card key={sub.id} className={cn(!sub.is_active && "opacity-60")}>
                <CardContent className="flex items-start gap-3 py-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <Rss className="h-3.5 w-3.5 text-orange-500 shrink-0" />
                      <span className="text-sm font-medium truncate">{sub.title || sub.feed_url}</span>
                      {!sub.is_active && <Badge variant="secondary" className="text-[10px]">已暂停</Badge>}
                      {sub.fetch_full_text && <Badge variant="outline" className="text-[10px] text-blue-600 border-blue-300">全文</Badge>}
                      {sub.fail_count > 0 && (
                        <Badge variant="outline" className="text-[10px] text-amber-600 border-amber-300">
                          <AlertTriangle className="mr-0.5 h-2.5 w-2.5" /> 连续失败 {sub.fail_count}
                        </Badge>
                      )}
                    </div>
                    <p className="mt-0.5 text-[11px] text-muted-foreground truncate font-mono">{sub.feed_url}</p>
                    <div className="mt-1 flex items-center gap-3 text-[11px] text-muted-foreground">
                      <span className="flex items-center gap-1">
                        <FolderOpen className="h-3 w-3" />
                        {folders.find((f) => f.id === sub.target_folder_id)?.name ?? "未分组"}
                      </span>
                      <span>上次抓取 {formatTime(sub.last_fetched_at)}</span>
                      <span>每源 {sub.max_items_per_tick} 条/次</span>
                    </div>
                    {sub.last_error && (
                      <p className="mt-1 text-[11px] text-red-600 truncate" title={sub.last_error}>
                        最近错误：{sub.last_error}
                      </p>
                    )}
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <button
                      onClick={() => void handleRefresh(sub)}
                      disabled={refreshing === sub.id}
                      className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
                      title="立即更新"
                    >
                      {refreshing === sub.id ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
                    </button>
                    <button
                      onClick={() => openEdit(sub)}
                      className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                      title="编辑"
                    >
                      <Pencil className="h-4 w-4" />
                    </button>
                    <button
                      onClick={() => setDeleting(sub)}
                      className="p-1.5 rounded-md text-muted-foreground hover:text-destructive transition-colors"
                      title="删除订阅"
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </div>
                </CardContent>
              </Card>
            ))}
            <Button variant="outline" className="w-full" size="sm" onClick={openAdd}>
              <Plus className="mr-1.5 h-4 w-4" /> 添加订阅
            </Button>
          </div>
        )}
      </div>

      {/* 添加/编辑弹窗 */}
      <Dialog open={showAdd} onOpenChange={(v) => { if (!v) resetForm(); }}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>{editing ? "编辑订阅" : "添加 RSS 订阅"}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            {!editing && (
              <div>
                <Label>订阅地址（RSS / Atom）</Label>
                <Input
                  className="mt-1.5 font-mono text-sm"
                  placeholder="https://example.com/feed.xml"
                  value={feedUrl}
                  onChange={(e) => setFeedUrl(e.target.value)}
                />
              </div>
            )}
            <div>
              <Label>名称{editing ? "" : "（可选，留空自动获取）"}</Label>
              <Input
                className="mt-1.5"
                placeholder="如：少数派"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
              />
            </div>
            <div>
              <Label>新文章放入文件夹</Label>
              <select
                className="mt-1.5 flex h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm"
                value={folderId}
                onChange={(e) => setFolderId(e.target.value)}
              >
                <option value="">未分组</option>
                {folders.map((f) => (
                  <option key={f.id} value={f.id}>{f.name}</option>
                ))}
              </select>
            </div>
            <div>
              <Label>每次最多抓取条数（1-20）</Label>
              <Input
                className="mt-1.5"
                type="number" min={1} max={20}
                value={maxItems}
                onChange={(e) => setMaxItems(e.target.value)}
              />
            </div>
            <div className="flex items-center justify-between rounded-lg border px-3 py-2.5">
              <div>
                <p className="text-sm font-medium">抓取全文</p>
                <p className="text-xs text-muted-foreground">
                  摘要源自动抓取原文正文（更慢、消耗更多额度）
                </p>
              </div>
              <Switch checked={fetchFullText} onCheckedChange={setFetchFullText} />
            </div>
            {editing && (
              <div className="flex items-center justify-between rounded-lg border px-3 py-2.5">
                <div>
                  <p className="text-sm font-medium">启用订阅</p>
                  <p className="text-xs text-muted-foreground">暂停后不再自动抓取</p>
                </div>
                <Switch checked={isActive} onCheckedChange={setIsActive} />
              </div>
            )}
            <Button className="w-full" onClick={handleSave} disabled={saving || (!editing && !feedUrl.trim())}>
              {saving ? "保存中..." : editing ? "保存" : "订阅"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      {/* 删除确认 */}
      <Dialog open={!!deleting} onOpenChange={(v) => { if (!v) setDeleting(null); }}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>删除订阅</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-foreground">
            确定取消订阅「{deleting?.title || deleting?.feed_url}」？已导入的素材会保留在知识库中。
          </p>
          <div className="flex gap-2">
            <Button variant="outline" className="flex-1" onClick={() => setDeleting(null)}>取消</Button>
            <Button variant="destructive" className="flex-1" onClick={handleDelete}>删除订阅</Button>
          </div>
        </DialogContent>
      </Dialog>
    </SimpleModal>
  );
}
