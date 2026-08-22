/**
 * 写作风格子页面
 */
import { useState, useEffect, useCallback } from "react";
import {
  Palette, Pencil, Loader2, Trash2, Check, ChevronRight,
  AlertCircle, Clock, Plus, Undo2, Upload, type LucideIcon,
} from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Textarea } from "@/components/ui/textarea";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";
import { SimpleModal, SimpleModalFooter, formatDate } from "./shared";

const BUILTIN_STYLE_SLUGS = new Set(["yinyue", "shenlun", "xiaohongshu"]);

interface UserStyle {
  id: string;
  slug: string;
  name: string;
  description: string;
  status: string;
  current_version: number;
  created_at: string;
  updated_at: string;
  config?: StyleConfig;
}

interface StyleConfig {
  slug: string;
  name: string;
  description: string;
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
    metaphor_description: string;
  };
  system_prompt: string;
  writing_standard: string;
  title_guidelines: {
    length: { min: number; max: number };
    style: string;
    forbidden_patterns: string[];
    examples: string[];
  };
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

const STATUS_META: Record<string, { label: string; color: string }> = {
  draft: { label: "草稿", color: "text-muted-foreground" },
  pending_review: { label: "审核中", color: "text-amber-600" },
  approved: { label: "已上架", color: "text-green-600" },
  rejected: { label: "已驳回", color: "text-red-600" },
};

function defaultStyleConfig(slug: string, name: string, description: string): StyleConfig {
  return {
    slug,
    name,
    description,
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
    system_prompt: "",
    writing_standard: "",
    title_guidelines: {
      length: { min: 5, max: 25 },
      style: "",
      forbidden_patterns: [],
      examples: [],
    },
    fact_guard: {
      future_tense_required: [],
      forbidden_results: [],
      user_material_priority: false,
    },
    output_format: {
      use_markdown: true,
      title_prefix: "## ",
    },
  };
}

export function StyleSection() {
  const token = useAuthStore((s) => s.token);
  const isGuest = useAuthStore((s) => s.user?.role === "guest");
  const [styles, setStyles] = useState<UserStyle[]>([]);
  const [loading, setLoading] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [editingStyle, setEditingStyle] = useState<UserStyle | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadStyles = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch("/api/v2/my-styles", {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      const json = await res.json();
      if (json.success) {
        const rawStyles = (json.data?.styles ?? []) as Array<UserStyle & { config?: StyleConfig | string }>;
        // Backend now returns config inline; normalize string→object for safety.
        const withConfig = rawStyles.map((s) => {
          if (!s.config) return s;
          if (typeof s.config === "string") {
            try { return { ...s, config: JSON.parse(s.config) as StyleConfig }; }
            catch { return s; }
          }
          return { ...s, config: s.config as StyleConfig };
        });
        setStyles(withConfig);
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
    if (!isGuest) loadStyles();
  }, [loadStyles, isGuest]);

  // 监听右下角 + 按钮事件
  useEffect(() => {
    const handler = () => setShowCreate(true);
    window.addEventListener("personal-center-add", handler);
    return () => window.removeEventListener("personal-center-add", handler);
  }, []);

  const handleDelete = async (id: string) => {
    if (!confirm("确认删除这个写作风格？此操作不可撤销。")) return;
    try {
      const res = await fetch(`/api/v2/my-styles/${id}`, {
        method: "DELETE",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      const json = await res.json();
      if (json.success) {
        setStyles(styles.filter((s) => s.id !== id));
      } else {
        setError(json.error?.message ?? "删除失败");
      }
    } catch {
      setError("网络错误");
    }
  };

  const handleWithdrawReview = async (id: string) => {
    if (!confirm("确认撤回审核？撤回后风格将回到草稿状态，可继续编辑后重新提交。")) return;
    try {
      const res = await fetch(`/api/v2/my-styles/${id}/withdraw`, {
        method: "POST",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      const json = await res.json();
      if (json.success) {
        await loadStyles();
      } else {
        setError(json.error?.message ?? "撤回审核失败");
      }
    } catch {
      setError("网络错误");
    }
  };

  const handleSubmitReview = async (id: string) => {
    if (!confirm("确认提交审核？审核通过后将上架到全量风格列表。")) return;
    try {
      const res = await fetch(`/api/v2/my-styles/${id}/submit`, {
        method: "POST",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      const json = await res.json();
      if (json.success) {
        await loadStyles();
      } else {
        setError(json.error?.message ?? "提交审核失败");
      }
    } catch {
      setError("网络错误");
    }
  };

  if (isGuest) {
    return (
      <div className="px-6 pt-6 pb-12 space-y-6">
        <Card className="border-amber-200/60 bg-amber-50/50 dark:bg-amber-950/20">
          <CardContent className="py-6 text-center">
            <Palette className="mx-auto h-10 w-10 text-amber-500/50" />
            <p className="mt-3 text-sm text-amber-900 dark:text-amber-200 font-medium">
              游客模式无法管理写作风格
            </p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
              注册账号后可创建和管理自定义写作风格
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="px-6 pt-6 pb-12 space-y-5">
      {error && (
        <div className="flex items-center gap-2 text-xs text-red-600">
          <AlertCircle className="h-3.5 w-3.5" />
          {error}
        </div>
      )}

      

      {loading ? (
        <div className="py-12 text-center text-muted-foreground text-sm">加载中...</div>
      ) : styles.length === 0 ? (
        <div className="py-12 text-center">
          <Palette className="mx-auto h-12 w-12 text-muted-foreground/30" />
          <p className="mt-3 text-sm text-muted-foreground">
            还没有自定义风格。点击右下角 + 按钮开始创建。
          </p>
        </div>
      ) : (
        <div className="space-y-3">
          {styles.map((style) => {
            const statusMeta = STATUS_META[style.status] ?? STATUS_META.draft;
            // 内置风格不可删除
            const isBuiltin = BUILTIN_STYLE_SLUGS.has(style.slug);
            return (
              <Card key={style.id} className="overflow-hidden">
                <CardContent className="py-3.5">
                  <div className="flex items-start justify-between gap-3">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium">{style.name}</span>
                        <Badge
                          variant="outline"
                          className={cn("text-xs", statusMeta.color)}
                        >
                          {statusMeta.label}
                        </Badge>
                        {style.current_version > 0 && (
                          <span className="text-xs text-muted-foreground">
                            v{style.current_version}
                          </span>
                        )}
                        {isBuiltin && (
                          <Badge variant="secondary" className="text-xs text-muted-foreground">
                            内置
                          </Badge>
                        )}
                      </div>
                      <p className="mt-1 text-xs text-muted-foreground line-clamp-1">
                        {style.description || "暂无描述"}
                      </p>
                      {style.config?.word_range && (
                        <div className="mt-1.5 flex items-center gap-3 text-xs text-muted-foreground">
                          <span>{style.config.word_range.min ?? "?"}-{style.config.word_range.max ?? "?"}字</span>
                          {style.config.structure?.type && (
                            <span>结构: {style.config.structure.type}</span>
                          )}
                          {style.config.tags && style.config.tags.length > 0 && (
                            <span>{style.config.tags.join("、")}</span>
                          )}
                        </div>
                      )}
                      <div className="mt-1 text-xs text-muted-foreground">
                        更新于 {formatDate(style.updated_at)}
                      </div>
                    </div>
                    <div className="flex items-center gap-1 shrink-0">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setEditingStyle(style)}
                        title="编辑"
                        disabled={style.status === "pending_review"}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      {(style.status === "draft" || style.status === "rejected") && style.current_version > 0 && (
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => handleSubmitReview(style.id)}
                          title={style.status === "rejected" ? "重新提交审核" : "提交审核"}
                          className="text-purple-600 hover:text-purple-700"
                        >
                          <Upload className="h-3.5 w-3.5" />
                        </Button>
                      )}
                      {style.status === "pending_review" && (
                        <>
                          <Badge variant="outline" className="text-xs gap-1 text-amber-600">
                            <Clock className="h-3 w-3" />
                            审核中
                          </Badge>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => handleWithdrawReview(style.id)}
                            title="撤回审核"
                            className="text-muted-foreground hover:text-amber-600"
                          >
                            <Undo2 className="h-3.5 w-3.5" />
                          </Button>
                        </>
                      )}
                      {style.status !== "pending_review" && !isBuiltin && (
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => handleDelete(style.id)}
                          title="删除"
                          className="text-muted-foreground hover:text-destructive"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      )}
                    </div>
                  </div>
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      {showCreate && (
        <StyleCreateDialog
          token={token}
          onClose={() => setShowCreate(false)}
          onCreated={() => {
            setShowCreate(false);
            loadStyles();
          }}
        />
      )}

      {editingStyle && (
        <StyleEditDialog
          style={editingStyle}
          token={token}
          onClose={() => setEditingStyle(null)}
          onSaved={() => {
            setEditingStyle(null);
            loadStyles();
          }}
        />
      )}
    </div>
  );
}

// ─── 风格创建对话框 ───────────────────────────────────────

function StyleCreateDialog({
  token,
  onClose,
  onCreated,
}: {
  token: string | null;
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
    // Validate slug format
    if (!/^[a-z0-9_]+$/.test(form.slug)) {
      setError("Slug 只能包含小写字母、数字和下划线");
      return;
    }

    setCreating(true);
    setError("");
    try {
      const config = defaultStyleConfig(form.slug, form.name, form.description);
      const res = await fetch("/api/v2/my-styles", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({
          slug: form.slug,
          name: form.name,
          description: form.description,
          config: config,
        }),
      });
      const json = await res.json();
      if (json.success) {
        onCreated();
      } else {
        setError(json.error?.message ?? "创建失败");
      }
    } catch {
      setError("网络错误");
    } finally {
      setCreating(false);
    }
  };

  return (
    <SimpleModal open onClose={onClose} title="新建写作风格" maxWidth="max-w-md">
      <div className="space-y-4">
        <div>
          <Label>Slug（英文标识符）</Label>
          <Input
            className="mt-1.5 font-mono"
            value={form.slug}
            onChange={(e) => setForm({ ...form, slug: e.target.value })}
            placeholder="如 tech_review"
          />
          <p className="mt-1 text-xs text-muted-foreground">只能包含小写字母、数字和下划线</p>
        </div>
        <div>
          <Label>名称</Label>
          <Input
            className="mt-1.5"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            placeholder="如 科技评论风格"
          />
        </div>
        <div>
          <Label>描述</Label>
          <Input
            className="mt-1.5"
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            placeholder="简要描述这个风格的特点"
          />
        </div>
        {error && (
          <div className="flex items-center gap-2 text-sm text-red-600">
            <AlertCircle className="h-4 w-4" />
            {error}
          </div>
        )}
      </div>
      <SimpleModalFooter>
        <Button variant="outline" onClick={onClose}>取消</Button>
        <Button onClick={handleCreate} disabled={creating}>
          {creating ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Plus className="h-4 w-4 mr-2" />}
          创建
        </Button>
      </SimpleModalFooter>
    </SimpleModal>
  );
}

// ─── 风格编辑对话框 ───────────────────────────────────────

function StyleEditDialog({
  style,
  token,
  onClose,
  onSaved,
}: {
  style: UserStyle;
  token: string | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [config, setConfig] = useState<StyleConfig>(
    style.config ?? defaultStyleConfig(style.slug, style.name, style.description)
  );
  const [name, setName] = useState(style.name);
  const [description, setDescription] = useState(style.description);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [activeTab, setActiveTab] = useState<"basic" | "structure" | "prompt">("basic");

  const update = (patch: Partial<StyleConfig>) => setConfig({ ...config, ...patch });

  const handleSave = async () => {
    if (!config.system_prompt.trim()) {
      setError("系统提示词不能为空");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const updatedConfig = { ...config, name, description };
      const res = await fetch(`/api/v2/my-styles/${style.id}`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({
          name,
          description,
          config: updatedConfig,
          changelog: `编辑风格配置`,
        }),
      });
      const json = await res.json();
      if (json.success) {
        onSaved();
      } else {
        setError(json.error?.message ?? "保存失败");
      }
    } catch {
      setError("网络错误");
    } finally {
      setSaving(false);
    }
  };

  const TABS = [
    { key: "basic" as const, label: "基础设置" },
    { key: "structure" as const, label: "结构与修辞" },
    { key: "prompt" as const, label: "系统提示词" },
  ];

  return (
    <SimpleModal open onClose={onClose} title={`编辑风格: ${style.name}`} maxWidth="max-w-2xl">
      {/* Tab Bar */}
      <div className="flex gap-1 border-b -mb-1">
        {TABS.map((tab) => (
          <button
            key={tab.key}
            onClick={() => setActiveTab(tab.key)}
            className={cn(
              "px-3 py-2 text-sm font-medium transition-colors border-b-2 -mb-px",
              activeTab === tab.key
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground"
            )}
          >
            {tab.label}
          </button>
        ))}
      </div>

      <div className="space-y-4 py-2">
          {activeTab === "basic" && (
            <>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <Label>名称</Label>
                  <Input
                    className="mt-1.5"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                  />
                </div>
                <div>
                  <Label>Slug</Label>
                  <Input
                    className="mt-1.5 font-mono"
                    value={style.slug}
                    disabled
                  />
                </div>
              </div>
              <div>
                <Label>描述</Label>
                <Input
                  className="mt-1.5"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                />
              </div>
              <div>
                <Label>标签（逗号分隔）</Label>
                <Input
                  className="mt-1.5"
                  value={config.tags.join(", ")}
                  onChange={(e) =>
                    update({
                      tags: e.target.value
                        .split(",")
                        .map((t) => t.trim())
                        .filter(Boolean),
                    })
                  }
                  placeholder="如 科技, 评论, 深度"
                />
              </div>
              
              <div>
                <Label>字数范围</Label>
                <div className="mt-1.5 flex items-center gap-3">
                  <Input
                    type="number"
                    value={config.word_range.min}
                    onChange={(e) =>
                      update({
                        word_range: { ...config.word_range, min: parseInt(e.target.value) || 0 },
                      })
                    }
                    className="w-24"
                  />
                  <span className="text-sm text-muted-foreground">—</span>
                  <Input
                    type="number"
                    value={config.word_range.max}
                    onChange={(e) =>
                      update({
                        word_range: { ...config.word_range, max: parseInt(e.target.value) || 0 },
                      })
                    }
                    className="w-24"
                  />
                  <span className="text-sm text-muted-foreground">字</span>
                </div>
              </div>
              <div>
                <Label>标题长度范围</Label>
                <div className="mt-1.5 flex items-center gap-3">
                  <Input
                    type="number"
                    value={config.title_guidelines.length.min}
                    onChange={(e) =>
                      update({
                        title_guidelines: {
                          ...config.title_guidelines,
                          length: { ...config.title_guidelines.length, min: parseInt(e.target.value) || 0 },
                        },
                      })
                    }
                    className="w-24"
                  />
                  <span className="text-sm text-muted-foreground">—</span>
                  <Input
                    type="number"
                    value={config.title_guidelines.length.max}
                    onChange={(e) =>
                      update({
                        title_guidelines: {
                          ...config.title_guidelines,
                          length: { ...config.title_guidelines.length, max: parseInt(e.target.value) || 0 },
                        },
                      })
                    }
                    className="w-24"
                  />
                  <span className="text-sm text-muted-foreground">字</span>
                </div>
              </div>
              <div>
                <Label>标题风格要求</Label>
                <Input
                  className="mt-1.5"
                  value={config.title_guidelines.style}
                  onChange={(e) =>
                    update({
                      title_guidelines: { ...config.title_guidelines, style: e.target.value },
                    })
                  }
                  placeholder="如 判断式或设问式"
                />
              </div>
            </>
          )}

          {activeTab === "structure" && (
            <>
              <div>
                <Label>结构类型</Label>
                <Select
                  value={config.structure.type}
                  onValueChange={(v) =>
                    update({ structure: { ...config.structure, type: v } })
                  }
                >
                  <SelectTrigger className="mt-1.5">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="three_part">三段式</SelectItem>
                    <SelectItem value="free_form">自由式</SelectItem>
                    <SelectItem value="custom">自定义</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="grid grid-cols-3 gap-3">
                <div>
                  <Label>开头</Label>
                  <Input
                    className="mt-1.5"
                    value={config.structure.opening}
                    onChange={(e) =>
                      update({ structure: { ...config.structure, opening: e.target.value } })
                    }
                    placeholder="如 现象点题"
                  />
                </div>
                <div>
                  <Label>正文</Label>
                  <Input
                    className="mt-1.5"
                    value={config.structure.body}
                    onChange={(e) =>
                      update({ structure: { ...config.structure, body: e.target.value } })
                    }
                    placeholder="如 分层论述"
                  />
                </div>
                <div>
                  <Label>结尾</Label>
                  <Input
                    className="mt-1.5"
                    value={config.structure.conclusion}
                    onChange={(e) =>
                      update({ structure: { ...config.structure, conclusion: e.target.value } })
                    }
                    placeholder="如 总结升华"
                  />
                </div>
              </div>
              <div>
                <Label>论证模式</Label>
                <Input
                  className="mt-1.5"
                  value={config.structure.argument_pattern}
                  onChange={(e) =>
                    update({ structure: { ...config.structure, argument_pattern: e.target.value } })
                  }
                  placeholder="如 首在-重在-贵在"
                />
              </div>
              
              <div className="space-y-3">
                <Label className="text-sm font-semibold">修辞要求</Label>
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={config.rhetoric.required_metaphor}
                    onChange={(e) =>
                      update({
                        rhetoric: { ...config.rhetoric, required_metaphor: e.target.checked },
                      })
                    }
                    className="rounded"
                  />
                  必须使用核心比喻
                </label>
                {config.rhetoric.required_metaphor && (
                  <Input
                    value={config.rhetoric.metaphor_description}
                    onChange={(e) =>
                      update({
                        rhetoric: { ...config.rhetoric, metaphor_description: e.target.value },
                      })
                    }
                    placeholder="比喻描述"
                  />
                )}
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={config.rhetoric.required_parallelism}
                    onChange={(e) =>
                      update({
                        rhetoric: { ...config.rhetoric, required_parallelism: e.target.checked },
                      })
                    }
                    className="rounded"
                  />
                  必须使用排比
                </label>
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={config.rhetoric.required_rhetorical_question}
                    onChange={(e) =>
                      update({
                        rhetoric: { ...config.rhetoric, required_rhetorical_question: e.target.checked },
                      })
                    }
                    className="rounded"
                  />
                  必须使用设问
                </label>
              </div>
            </>
          )}

          {activeTab === "prompt" && (
            <>
              <div>
                <Label>系统提示词（System Prompt）</Label>
                <Textarea
                  className="mt-1.5 font-mono text-xs min-h-[200px]"
                  value={config.system_prompt}
                  onChange={(e) => update({ system_prompt: e.target.value })}
                  placeholder="你是xxx写作助手。要求：&#10;1. ...&#10;2. ..."
                />
                <p className="mt-1 text-xs text-muted-foreground">
                  这是控制 AI 写作行为的核心提示词，必填
                </p>
              </div>
              <div>
                <Label>写作规范</Label>
                <Textarea
                  className="mt-1.5 text-xs min-h-[80px]"
                  value={config.writing_standard}
                  onChange={(e) => update({ writing_standard: e.target.value })}
                  placeholder="如 篇幅1000-1500字，结构严谨..."
                />
              </div>
              <div>
                <Label>标题参考示例（每行一个）</Label>
                <Textarea
                  className="mt-1.5 text-xs min-h-[60px]"
                  value={config.title_guidelines.examples.join("\n")}
                  onChange={(e) =>
                    update({
                      title_guidelines: {
                        ...config.title_guidelines,
                        examples: e.target.value.split("\n").filter(Boolean),
                      },
                    })
                  }
                  placeholder="如 外卖骑手的红灯困境&#10;城市温度，从一条背篓专线说起"
                />
              </div>
            </>
          )}
        </div>

        {error && (
          <div className="flex items-center gap-2 text-sm text-red-600">
            <AlertCircle className="h-4 w-4" />
            {error}
          </div>
        )}

      <SimpleModalFooter>
        <Button variant="outline" onClick={onClose}>取消</Button>
        <Button onClick={handleSave} disabled={saving}>
          {saving ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Check className="h-4 w-4 mr-2" />}
          保存
        </Button>
      </SimpleModalFooter>
    </SimpleModal>
  );
}
