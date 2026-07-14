/**
 * 灰度配置弹窗 — 管理风格版本的灰度发布策略
 *
 * 功能：
 *  - 设置灰度百分比（0-100%）
 *  - 管理白名单 UID 列表
 *  - 预览特定 UID 会命中哪个版本
 *  - 查看当前灰度统计
 *
 * 后端 API:
 *  GET    /api/v2/admin/styles/{slug}/rollout
 *  PUT    /api/v2/admin/styles/{slug}/rollout
 *  POST   /api/v2/admin/styles/{slug}/rollout/preview
 *
 * 文档来源: docs/06-grayscale-routing.md
 */
import { useState, useEffect, useCallback } from "react";
import {
  Users, Percent, Eye, Save, Plus, X, Loader2, AlertCircle, CheckCircle2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Separator } from "@/components/ui/separator";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

interface RolloutConfig {
  slug: string;
  percentage: number;
  whitelist_uids: string[];
  blacklist_uids: string[];
  active_version: number;
  candidate_version: number;
  strategy: "percentage" | "whitelist" | "all";
  enabled: boolean;
}

interface RolloutPreview {
  uid: string;
  version: number;
  source: "whitelist" | "percentage" | "default";
}

interface RolloutConfigDialogProps {
  slug: string;
  onClose: () => void;
}

export function RolloutConfigDialog({ slug, onClose }: RolloutConfigDialogProps) {
  const [config, setConfig] = useState<RolloutConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [newWhitelistUid, setNewWhitelistUid] = useState("");
  const [previewUid, setPreviewUid] = useState("");
  const [previewResult, setPreviewResult] = useState<RolloutPreview | null>(null);
  const [error, setError] = useState("");

  const loadConfig = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch(`/api/v2/admin/styles/${slug}/rollout`);
      const json = await res.json();
      if (json.success) {
        setConfig(json.data ?? {
          slug,
          percentage: 0,
          whitelist_uids: [],
          blacklist_uids: [],
          active_version: 1,
          candidate_version: 1,
          strategy: "percentage",
          enabled: false,
        });
      } else {
        // Use default config if not found
        setConfig({
          slug,
          percentage: 0,
          whitelist_uids: [],
          blacklist_uids: [],
          active_version: 1,
          candidate_version: 1,
          strategy: "percentage",
          enabled: false,
        });
      }
    } catch {
      setConfig({
        slug,
        percentage: 0,
        whitelist_uids: [],
        blacklist_uids: [],
        active_version: 1,
        candidate_version: 1,
        strategy: "percentage",
        enabled: false,
      });
    } finally {
      setLoading(false);
    }
  }, [slug]);

  useEffect(() => {
    loadConfig();
  }, [loadConfig]);

  const handleSave = async () => {
    if (!config) return;
    setSaving(true);
    setError("");
    try {
      const res = await fetch(`/api/v2/admin/styles/${slug}/rollout`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(config),
      });
      const json = await res.json();
      if (!json.success) {
        setError(json.error?.message ?? "保存失败");
      }
    } catch {
      setError("网络错误");
    } finally {
      setSaving(false);
    }
  };

  const handlePreview = async () => {
    if (!previewUid.trim()) return;
    try {
      const res = await fetch(`/api/v2/admin/styles/${slug}/rollout/preview`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ uid: previewUid.trim() }),
      });
      const json = await res.json();
      if (json.success) {
        setPreviewResult(json.data);
      } else {
        setPreviewResult(null);
      }
    } catch {
      setPreviewResult(null);
    }
  };

  const addWhitelist = () => {
    if (!newWhitelistUid.trim() || !config) return;
    if (config.whitelist_uids.includes(newWhitelistUid.trim())) return;
    setConfig({
      ...config,
      whitelist_uids: [...config.whitelist_uids, newWhitelistUid.trim()],
    });
    setNewWhitelistUid("");
  };

  const removeWhitelist = (uid: string) => {
    if (!config) return;
    setConfig({
      ...config,
      whitelist_uids: config.whitelist_uids.filter((u) => u !== uid),
    });
  };

  if (loading) {
    return (
      <Dialog open onOpenChange={(o) => !o && onClose()}>
        <DialogContent className="max-w-lg">
          <div className="flex items-center justify-center py-8">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          </div>
        </DialogContent>
      </Dialog>
    );
  }

  if (!config) return null;

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Percent className="h-5 w-5 text-blue-500" />
            灰度配置: {slug}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-5">
          {/* 当前状态 */}
          <div className="grid grid-cols-3 gap-3">
            <div className="rounded-lg border p-3 text-center">
              <p className="text-xs text-muted-foreground">当前版本</p>
              <p className="text-lg font-bold text-green-600">v{config.active_version}</p>
            </div>
            <div className="rounded-lg border p-3 text-center">
              <p className="text-xs text-muted-foreground">候选版本</p>
              <p className="text-lg font-bold text-blue-600">v{config.candidate_version}</p>
            </div>
            <div className="rounded-lg border p-3 text-center">
              <p className="text-xs text-muted-foreground">灰度状态</p>
              <Badge variant={config.enabled ? "success" : "secondary"} className="mt-1">
                {config.enabled ? "已启用" : "未启用"}
              </Badge>
            </div>
          </div>

          {/* 策略选择 */}
          <div className="space-y-2">
            <Label>灰度策略</Label>
            <div className="flex gap-2">
              {([
                { value: "percentage", label: "百分比分流" },
                { value: "whitelist", label: "白名单优先" },
                { value: "all", label: "全量切换" },
              ] as const).map((opt) => (
                <Button
                  key={opt.value}
                  size="sm"
                  variant={config.strategy === opt.value ? "default" : "outline"}
                  onClick={() => setConfig({ ...config, strategy: opt.value })}
                >
                  {opt.label}
                </Button>
              ))}
            </div>
          </div>

          {/* 百分比 */}
          {config.strategy === "percentage" && (
            <div className="space-y-2">
              <Label>灰度百分比</Label>
              <div className="flex items-center gap-3">
                <input
                  type="range"
                  min="0"
                  max="100"
                  step="5"
                  value={config.percentage}
                  onChange={(e) => setConfig({ ...config, percentage: parseInt(e.target.value) })}
                  className="flex-1"
                />
                <span className="w-16 text-right text-sm font-medium tabular-nums">
                  {config.percentage}%
                </span>
              </div>
              <p className="text-xs text-muted-foreground">
                {config.percentage}% 的用户将命中 v{config.candidate_version}，其余命中 v{config.active_version}
              </p>
            </div>
          )}

          {/* 启用开关 */}
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant={config.enabled ? "default" : "outline"}
              onClick={() => setConfig({ ...config, enabled: !config.enabled })}
            >
              {config.enabled ? "已启用灰度" : "已禁用灰度"}
            </Button>
            {config.enabled && (
              <span className="text-xs text-green-600">
                <CheckCircle2 className="inline h-3 w-3 mr-1" />
                新用户将按策略分流
              </span>
            )}
          </div>

          <Separator />

          {/* 白名单 */}
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <Users className="h-4 w-4 text-muted-foreground" />
              <Label>白名单 UID（始终命中候选版本）</Label>
            </div>
            <div className="flex gap-2">
              <Input
                value={newWhitelistUid}
                onChange={(e) => setNewWhitelistUid(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    addWhitelist();
                  }
                }}
                placeholder="输入 UID 后回车添加"
                className="text-sm"
              />
              <Button size="sm" variant="outline" onClick={addWhitelist}>
                <Plus className="h-3.5 w-3.5" />
              </Button>
            </div>
            {config.whitelist_uids.length > 0 ? (
              <div className="flex flex-wrap gap-1.5 rounded-lg border p-2">
                {config.whitelist_uids.map((uid) => (
                  <div
                    key={uid}
                    className="flex items-center gap-1 rounded-md bg-blue-50 px-2 py-1 text-xs text-blue-700"
                  >
                    <span>{uid}</span>
                    <button
                      onClick={() => removeWhitelist(uid)}
                      className="text-blue-400 hover:text-blue-600"
                    >
                      <X className="h-3 w-3" />
                    </button>
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">暂无白名单</p>
            )}
          </div>

          <Separator />

          {/* 预览 */}
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <Eye className="h-4 w-4 text-muted-foreground" />
              <Label>UID 预览</Label>
            </div>
            <div className="flex gap-2">
              <Input
                value={previewUid}
                onChange={(e) => setPreviewUid(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    handlePreview();
                  }
                }}
                placeholder="输入 UID 测试命中"
                className="text-sm"
              />
              <Button size="sm" variant="outline" onClick={handlePreview}>
                预览
              </Button>
            </div>
            {previewResult && (
              <div className={cn(
                "flex items-center gap-2 rounded-lg border p-2 text-sm",
                previewResult.version === config.candidate_version
                  ? "border-blue-200 bg-blue-50 text-blue-700"
                  : "border-green-200 bg-green-50 text-green-700"
              )}>
                <CheckCircle2 className="h-4 w-4" />
                <span>UID {previewResult.uid} → v{previewResult.version}</span>
                <Badge variant="outline" className="text-xs ml-auto">
                  {previewResult.source}
                </Badge>
              </div>
            )}
          </div>

          {error && (
            <div className="flex items-center gap-2 text-sm text-red-600">
              <AlertCircle className="h-4 w-4" />
              {error}
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? (
              <Loader2 className="h-4 w-4 mr-2 animate-spin" />
            ) : (
              <Save className="h-4 w-4 mr-2" />
            )}
            保存配置
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
