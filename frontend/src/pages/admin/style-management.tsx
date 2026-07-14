/**
 * 风格管理 — Admin Dashboard
 * Profile CRUD + 发布流程
 */
import { useState, useEffect, useCallback } from "react";
import {
  Plus, Pencil, Send, Archive, History, Save, X, AlertCircle, CheckCircle2, Loader2, GitBranch,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter,
} from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { RolloutConfigDialog } from "./rollout-config";

interface AdminStyle {
  slug: string;
  name: string;
  description: string;
  version: number;
  status: string;
  tags: string[];
  word_range: [number, number];
}

interface StyleDetail {
  slug: string;
  name: string;
  description: string;
  version: number;
  status: string;
  tags: string[];
  word_range: { min: number; max: number; hard_limit: boolean };
  structure: {
    type: string;
    opening: string;
    body: string;
    conclusion: string;
    argument_pattern: string;
    argument_count: { min: number; max: number };
  };
  rhetoric: {
    required_metaphor: boolean;
    required_parallelism: boolean;
    required_rhetorical_question: boolean;
  };
  title_guidelines: {
    length: { min: number; max: number };
    forbidden_patterns: string[];
    examples: string[];
  };
  system_prompt: string;
  writing_standard: string;
  fact_guard: {
    future_tense_required: string[];
    forbidden_results: string[];
    user_material_priority: boolean;
  };
  output_format: {
    use_markdown: boolean;
    title_prefix: string;
  };
}

const STATUS_COLORS: Record<string, string> = {
  published: "bg-green-100 text-green-700 border-green-200",
  draft: "bg-yellow-100 text-yellow-700 border-yellow-200",
  archived: "bg-gray-100 text-gray-500 border-gray-200",
};

const STRUCTURE_TYPES = [
  { value: "three_part", label: "三段式闭环" },
  { value: "free_form", label: "自由结构" },
  { value: "custom", label: "自定义" },
];

export function StyleManagementPage() {
  const [styles, setStyles] = useState<AdminStyle[]>([]);
  const [loading, setLoading] = useState(false);
  const [editingSlug, setEditingSlug] = useState<string | null>(null);
  const [editDetail, setEditDetail] = useState<StyleDetail | null>(null);
  const [saving, setSaving] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [publishDialog, setPublishDialog] = useState<{ slug: string; name: string; version: number } | null>(null);
  const [versionHistory, setVersionHistory] = useState<{ slug: string; versions: any[] } | null>(null);
  const [rolloutSlug, setRolloutSlug] = useState<string | null>(null);

  const loadStyles = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch("/api/v2/admin/styles");
      const json = await res.json();
      if (json.success) {
        setStyles(json.data?.styles ?? []);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  const loadDetail = async (slug: string) => {
    try {
      const res = await fetch(`/api/v2/admin/styles/${slug}`);
      const json = await res.json();
      if (json.success) {
        setEditDetail(json.data);
        setEditingSlug(slug);
      }
    } catch (e) {
      console.error("Failed to load detail", e);
    }
  };

  const handleSave = async () => {
    if (!editDetail || !editingSlug) return;
    setSaving(true);
    try {
      const res = await fetch(`/api/v2/admin/styles/${editingSlug}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(editDetail),
      });
      const json = await res.json();
      if (json.success) {
        await loadStyles();
      }
    } finally {
      setSaving(false);
    }
  };

  const handlePublish = async (changelog: string) => {
    if (!publishDialog) return;
    try {
      const res = await fetch(`/api/v2/admin/styles/${publishDialog.slug}/publish`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ changelog }),
      });
      const json = await res.json();
      if (json.success) {
        await loadStyles();
        setPublishDialog(null);
      }
    } catch (e) {
      console.error("Failed to publish", e);
    }
  };

  const handleArchive = async (slug: string) => {
    if (!confirm(`确认归档风格 "${slug}"？`)) return;
    try {
      await fetch(`/api/v2/admin/styles/${slug}/archive`, { method: "POST" });
      await loadStyles();
    } catch (e) {
      console.error("Failed to archive", e);
    }
  };

  const loadVersions = async (slug: string) => {
    try {
      const res = await fetch(`/api/v2/admin/styles/${slug}/versions`);
      const json = await res.json();
      if (json.success) {
        setVersionHistory({ slug, versions: json.data?.versions ?? [] });
      }
    } catch (e) {
      console.error("Failed to load versions", e);
    }
  };

  useEffect(() => {
    loadStyles();
  }, [loadStyles]);

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">风格管理</h2>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="h-4 w-4 mr-2" />
          新建风格
        </Button>
      </div>

      {/* Style List */}
      <div className="rounded-lg border">
        <table className="w-full">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="text-left p-3 text-sm font-medium">名称</th>
              <th className="text-left p-3 text-sm font-medium">Slug</th>
              <th className="text-left p-3 text-sm font-medium">版本</th>
              <th className="text-left p-3 text-sm font-medium">状态</th>
              <th className="text-left p-3 text-sm font-medium">字数范围</th>
              <th className="text-right p-3 text-sm font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={6} className="p-8 text-center text-muted-foreground">
                  <Loader2 className="h-4 w-4 animate-spin mx-auto" />
                </td>
              </tr>
            ) : styles.length === 0 ? (
              <tr>
                <td colSpan={6} className="p-8 text-center text-muted-foreground text-sm">
                  暂无风格配置
                </td>
              </tr>
            ) : (
              styles.map((style) => (
                <tr key={style.slug} className="border-b last:border-0 hover:bg-accent/30">
                  <td className="p-3">
                    <div className="font-medium text-sm">{style.name}</div>
                    {style.tags && style.tags.length > 0 && (
                      <div className="flex gap-1 mt-1">
                        {style.tags.map((tag) => (
                          <Badge key={tag} variant="outline" className="text-xs py-0">
                            {tag}
                          </Badge>
                        ))}
                      </div>
                    )}
                  </td>
                  <td className="p-3 text-sm text-muted-foreground font-mono">{style.slug}</td>
                  <td className="p-3 text-sm">v{style.version}</td>
                  <td className="p-3">
                    <Badge variant="outline" className={STATUS_COLORS[style.status] ?? ""}>
                      {style.status === "published" ? "已发布" : style.status === "draft" ? "草稿" : "已归档"}
                    </Badge>
                  </td>
                  <td className="p-3 text-sm text-muted-foreground">
                    {style.word_range[0]} - {style.word_range[1]}
                  </td>
                  <td className="p-3">
                    <div className="flex items-center justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => loadDetail(style.slug)}
                        title="编辑"
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setPublishDialog({ slug: style.slug, name: style.name, version: style.version })}
                        title="发布"
                        disabled={style.status === "archived"}
                      >
                        <Send className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => loadVersions(style.slug)}
                        title="版本历史"
                      >
                        <History className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setRolloutSlug(style.slug)}
                        title="灰度配置"
                      >
                        <GitBranch className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => handleArchive(style.slug)}
                        title="归档"
                        disabled={style.status === "archived"}
                      >
                        <Archive className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* Edit Dialog */}
      {editingSlug && editDetail && (
        <StyleEditDialog
          detail={editDetail}
          onChange={setEditDetail}
          onClose={() => { setEditingSlug(null); setEditDetail(null); }}
          onSave={handleSave}
          saving={saving}
        />
      )}

      {/* Create Dialog */}
      {showCreate && (
        <StyleCreateDialog
          onClose={() => setShowCreate(false)}
          onCreated={async () => {
            setShowCreate(false);
            await loadStyles();
          }}
        />
      )}

      {/* Publish Confirmation Dialog */}
      {publishDialog && (
        <PublishDialog
          info={publishDialog}
          onClose={() => setPublishDialog(null)}
          onConfirm={handlePublish}
        />
      )}

      {/* Version History Dialog */}
      {versionHistory && (
        <VersionHistoryDialog
          info={versionHistory}
          onClose={() => setVersionHistory(null)}
        />
      )}

      {/* Rollout Config Dialog */}
      {rolloutSlug && (
        <RolloutConfigDialog
          slug={rolloutSlug}
          onClose={() => setRolloutSlug(null)}
        />
      )}
    </div>
  );
}

// ─── Style Edit Dialog ───────────────────────────────────

function StyleEditDialog({
  detail, onChange, onClose, onSave, saving,
}: {
  detail: StyleDetail;
  onChange: (d: StyleDetail) => void;
  onClose: () => void;
  onSave: () => void;
  saving: boolean;
}) {
  const update = (patch: Partial<StyleDetail>) => onChange({ ...detail, ...patch });

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-3xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>编辑风格: {detail.name}</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          {/* Basic Info */}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <Label>名称</Label>
              <Input
                value={detail.name}
                onChange={(e) => update({ name: e.target.value })}
              />
            </div>
            <div>
              <Label>Slug (不可修改)</Label>
              <Input value={detail.slug} disabled className="font-mono" />
            </div>
          </div>

          <div>
            <Label>描述</Label>
            <Input
              value={detail.description}
              onChange={(e) => update({ description: e.target.value })}
            />
          </div>

          {/* Word Range */}
          <div className="grid grid-cols-3 gap-4">
            <div>
              <Label>最小字数</Label>
              <Input
                type="number"
                value={detail.word_range.min}
                onChange={(e) => update({
                  word_range: { ...detail.word_range, min: parseInt(e.target.value) || 0 },
                })}
              />
            </div>
            <div>
              <Label>最大字数</Label>
              <Input
                type="number"
                value={detail.word_range.max}
                onChange={(e) => update({
                  word_range: { ...detail.word_range, max: parseInt(e.target.value) || 0 },
                })}
              />
            </div>
            <div>
              <Label>硬性限制</Label>
              <Select
                value={detail.word_range.hard_limit ? "true" : "false"}
                onValueChange={(v) => update({
                  word_range: { ...detail.word_range, hard_limit: v === "true" },
                })}
              >
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="true">是</SelectItem>
                  <SelectItem value="false">否</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          {/* Structure */}
          <div className="border-t pt-4">
            <h4 className="text-sm font-medium mb-3">结构配置</h4>
            <div className="grid grid-cols-2 gap-4">
              <div>
                <Label>结构类型</Label>
                <Select
                  value={detail.structure.type}
                  onValueChange={(v) => update({
                    structure: { ...detail.structure, type: v },
                  })}
                >
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {STRUCTURE_TYPES.map((t) => (
                      <SelectItem key={t.value} value={t.value}>{t.label}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div>
                <Label>递进模式</Label>
                <Input
                  value={detail.structure.argument_pattern}
                  onChange={(e) => update({
                    structure: { ...detail.structure, argument_pattern: e.target.value },
                  })}
                />
              </div>
            </div>
          </div>

          {/* System Prompt */}
          <div className="border-t pt-4">
            <Label>系统提示词</Label>
            <Textarea
              value={detail.system_prompt}
              onChange={(e) => update({ system_prompt: e.target.value })}
              rows={6}
              className="font-mono text-sm"
            />
          </div>

          {/* Writing Standard */}
          <div>
            <Label>写作规范</Label>
            <Textarea
              value={detail.writing_standard}
              onChange={(e) => update({ writing_standard: e.target.value })}
              rows={3}
            />
          </div>

          {/* Title Guidelines */}
          <div className="border-t pt-4">
            <h4 className="text-sm font-medium mb-3">标题规范</h4>
            <div className="grid grid-cols-2 gap-4 mb-2">
              <div>
                <Label>最小长度</Label>
                <Input
                  type="number"
                  value={detail.title_guidelines.length.min}
                  onChange={(e) => update({
                    title_guidelines: {
                      ...detail.title_guidelines,
                      length: { ...detail.title_guidelines.length, min: parseInt(e.target.value) || 0 },
                    },
                  })}
                />
              </div>
              <div>
                <Label>最大长度</Label>
                <Input
                  type="number"
                  value={detail.title_guidelines.length.max}
                  onChange={(e) => update({
                    title_guidelines: {
                      ...detail.title_guidelines,
                      length: { ...detail.title_guidelines.length, max: parseInt(e.target.value) || 0 },
                    },
                  })}
                />
              </div>
            </div>
            <div>
              <Label>禁止模式 (逗号分隔)</Label>
              <Input
                value={detail.title_guidelines.forbidden_patterns.join(", ")}
                onChange={(e) => update({
                  title_guidelines: {
                    ...detail.title_guidelines,
                    forbidden_patterns: e.target.value.split(",").map((s) => s.trim()).filter(Boolean),
                  },
                })}
              />
            </div>
          </div>

          {/* Fact Guard */}
          <div className="border-t pt-4">
            <h4 className="text-sm font-medium mb-3">事实与时态红线</h4>
            <div className="space-y-2">
              <div>
                <Label>待发生时态 (逗号分隔)</Label>
                <Input
                  value={detail.fact_guard.future_tense_required.join(", ")}
                  onChange={(e) => update({
                    fact_guard: {
                      ...detail.fact_guard,
                      future_tense_required: e.target.value.split(",").map((s) => s.trim()).filter(Boolean),
                    },
                  })}
                />
              </div>
              <div>
                <Label>禁止结果表述 (逗号分隔)</Label>
                <Input
                  value={detail.fact_guard.forbidden_results.join(", ")}
                  onChange={(e) => update({
                    fact_guard: {
                      ...detail.fact_guard,
                      forbidden_results: e.target.value.split(",").map((s) => s.trim()).filter(Boolean),
                    },
                  })}
                />
              </div>
            </div>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            <X className="h-4 w-4 mr-2" /> 取消
          </Button>
          <Button onClick={onSave} disabled={saving}>
            {saving ? (
              <Loader2 className="h-4 w-4 mr-2 animate-spin" />
            ) : (
              <Save className="h-4 w-4 mr-2" />
            )}
            保存草稿
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ─── Style Create Dialog ─────────────────────────────────

function StyleCreateDialog({
  onClose, onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const [form, setForm] = useState({
    slug: "",
    name: "",
    description: "",
  });
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");

  const handleCreate = async () => {
    if (!form.slug || !form.name) {
      setError("Slug 和名称为必填项");
      return;
    }

    setCreating(true);
    setError("");
    try {
      const res = await fetch("/api/v2/admin/styles", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          slug: form.slug,
          name: form.name,
          description: form.description,
          version: 1,
          tags: [],
          word_range: { min: 500, max: 1500, hard_limit: true },
          structure: {
            type: "free_form",
            opening: "",
            body: "",
            conclusion: "",
            argument_pattern: "",
            argument_count: { min: 1, max: 3 },
          },
          rhetoric: {
            required_metaphor: false,
            required_parallelism: false,
            required_rhetorical_question: false,
            metaphor_description: "",
          },
          title_guidelines: {
            length: { min: 5, max: 25 },
            style: "",
            forbidden_patterns: [],
            examples: [],
          },
          system_prompt: "",
          writing_standard: "",
          fact_guard: {
            future_tense_required: [],
            forbidden_results: [],
            user_material_priority: false,
          },
          output_format: {
            use_markdown: true,
            title_prefix: "## ",
            separator: "",
            include_modification_notes: false,
            note_label: "",
          },
          length_profiles: {},
        }),
      });
      const json = await res.json();
      if (json.success) {
        onCreated();
      } else {
        setError(json.error?.message ?? "创建失败");
      }
    } catch (e) {
      setError("网络错误");
    } finally {
      setCreating(false);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>新建风格</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div>
            <Label>Slug (英文标识符)</Label>
            <Input
              value={form.slug}
              onChange={(e) => setForm({ ...form, slug: e.target.value })}
              placeholder="如 custom_style"
              className="font-mono"
            />
          </div>
          <div>
            <Label>名称</Label>
            <Input
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="如 自定义评论风格"
            />
          </div>
          <div>
            <Label>描述</Label>
            <Input
              value={form.description}
              onChange={(e) => setForm({ ...form, description: e.target.value })}
              placeholder="风格描述"
            />
          </div>
          {error && (
            <div className="flex items-center gap-2 text-sm text-red-600">
              <AlertCircle className="h-4 w-4" />
              {error}
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={handleCreate} disabled={creating}>
            {creating ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Plus className="h-4 w-4 mr-2" />}
            创建
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ─── Publish Confirmation Dialog ─────────────────────────

function PublishDialog({
  info, onClose, onConfirm,
}: {
  info: { slug: string; name: string; version: number };
  onClose: () => void;
  onConfirm: (changelog: string) => void;
}) {
  const [changelog, setChangelog] = useState("");
  const [publishing, setPublishing] = useState(false);

  const handlePublish = async () => {
    setPublishing(true);
    await onConfirm(changelog);
    setPublishing(false);
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>确认发布 {info.name}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="rounded-lg bg-muted/50 p-4 space-y-2">
            <div className="flex items-center gap-2 text-sm">
              <CheckCircle2 className="h-4 w-4 text-green-500" />
              将生成新版本号 v{info.version + 1}
            </div>
            <div className="flex items-center gap-2 text-sm">
              <Archive className="h-4 w-4 text-muted-foreground" />
              当前 v{info.version} 将自动归档
            </div>
            <div className="flex items-center gap-2 text-sm">
              <Send className="h-4 w-4 text-blue-500" />
              将自动触发评测任务（如存在评测集）
            </div>
          </div>
          <div>
            <Label>变更说明 (可选)</Label>
            <Textarea
              value={changelog}
              onChange={(e) => setChangelog(e.target.value)}
              placeholder="本次发布的主要变更..."
              rows={3}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={handlePublish} disabled={publishing}>
            {publishing ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Send className="h-4 w-4 mr-2" />}
            确认发布
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ─── Version History Dialog ──────────────────────────────

function VersionHistoryDialog({
  info, onClose,
}: {
  info: { slug: string; versions: any[] };
  onClose: () => void;
}) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>版本历史: {info.slug}</DialogTitle>
        </DialogHeader>
        <div className="rounded-lg border">
          <table className="w-full">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="text-left p-3 text-sm font-medium">版本</th>
                <th className="text-left p-3 text-sm font-medium">状态</th>
                <th className="text-left p-3 text-sm font-medium">变更说明</th>
                <th className="text-left p-3 text-sm font-medium">发布时间</th>
              </tr>
            </thead>
            <tbody>
              {info.versions.length === 0 ? (
                <tr>
                  <td colSpan={4} className="p-6 text-center text-muted-foreground text-sm">
                    暂无版本记录
                  </td>
                </tr>
              ) : (
                info.versions.map((v, i) => (
                  <tr key={i} className="border-b last:border-0">
                    <td className="p-3 text-sm font-medium">v{v.version}</td>
                    <td className="p-3">
                      <Badge variant="outline" className={STATUS_COLORS[v.status] ?? ""}>
                        {v.status === "published" ? "已发布" : v.status === "archived" ? "已归档" : v.status}
                      </Badge>
                    </td>
                    <td className="p-3 text-sm text-muted-foreground">
                      {v.changelog || "—"}
                    </td>
                    <td className="p-3 text-sm text-muted-foreground">
                      {v.published_at ? new Date(v.published_at).toLocaleString("zh-CN") : "—"}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>关闭</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
