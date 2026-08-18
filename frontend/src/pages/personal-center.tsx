/**
 * 个人中心 — 居中悬浮面板
 *
 * 左侧菜单 + 右侧内容区，包含「个人信息」「写作风格」「记忆管理」「账号管理」。
 * 居中悬浮，低阴影，点击遮罩区域不关闭。
 */
import { useState, useEffect, useCallback, useRef } from "react";
import { useNavigate } from "react-router-dom";
import {
  Brain, User, X, KeyRound, Fingerprint,
  Trash2, Plus, TrendingUp, Shield, AlertCircle, type LucideIcon,
  Check, ChevronRight, Palette, Pencil, Loader2, Upload, Clock, Settings,
} from "lucide-react";
import { DialogPortal, DialogOverlay } from "@/components/ui/dialog";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog as CreateDialog, DialogContent as CreateDialogContent,
  DialogHeader as CreateDialogHeader, DialogTitle as CreateDialogTitle,
  DialogTrigger as CreateDialogTrigger,
  DialogFooter as CreateDialogFooter,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { useAuthStore } from "@/stores/auth-store";
import { useMemoryStore, type UserMemory } from "@/stores/memory-store";
import { useSettingsStore, type AgentMode } from "@/stores/settings-store";
import { cn } from "@/lib/utils";
import { registerPasskey, getPasskeyErrorMessage } from "@/lib/passkey";

// ─── 菜单项定义 ──────────────────────────────────────────

type MenuKey = "profile" | "styles" | "memory" | "settings" | "account";

interface MenuItem {
  key: MenuKey;
  label: string;
  icon: LucideIcon;
}

const MENU_ITEMS: MenuItem[] = [
  { key: "profile", label: "个人信息", icon: User },
  { key: "styles", label: "写作风格", icon: Palette },
  { key: "memory", label: "记忆管理", icon: Brain },
  { key: "settings", label: "偏好设置", icon: Settings },
  { key: "account", label: "账号管理", icon: KeyRound },
];

const SECTION_META: Record<MenuKey, { title: string; subtitle: string }> = {
  profile: { title: "个人信息", subtitle: "查看和修改你的账号信息" },
  styles: { title: "写作风格", subtitle: "管理你的自定义写作风格" },
  memory: { title: "记忆管理", subtitle: "管理 AI 学习到的写作偏好" },
  settings: { title: "偏好设置", subtitle: "配置默认写作风格和编排模式" },
  account: { title: "账号管理", subtitle: "管理密码和 Passkey 认证" },
};

// ─── 默认（内置）风格 slug 列表 — 不可删除 ──────────────
const BUILTIN_STYLE_SLUGS = new Set(["yinyue", "shenlun", "xiaohongshu"]);

// ─── 主组件 ──────────────────────────────────────────────

export function PersonalCenter() {
  const navigate = useNavigate();
  const [activeMenu, setActiveMenu] = useState<MenuKey>("profile");
  const [open, setOpen] = useState(true);

  const user = useAuthStore((s) => s.user);

  const isGuest = user?.role === "guest";

  const handleClose = () => {
    setOpen(false);
    navigate(-1);
  };

  return (
    <DialogPrimitive.Root open={open} onOpenChange={() => {}}>
      <DialogPortal>
        <DialogOverlay className="bg-black/20 backdrop-blur-[2px]" />
        <DialogPrimitive.Content
          onInteractOutside={(e) => e.preventDefault()}
          className={cn(
            "fixed left-[50%] top-[50%] z-50 flex h-[600px] max-h-[85vh] w-[720px] max-w-[92vw] translate-x-[-50%] translate-y-[-50%] flex-row overflow-hidden rounded-xl border bg-background shadow-md duration-200 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95"
          )}
        >
          {/* ── 左侧菜单 ── */}
          <div className="w-48 shrink-0 border-r bg-muted/30 flex flex-col">
            {/* 用户信息头部 — 与右侧标题区对齐，横线对齐 admin 风格 */}
            <div className="flex h-[60px] items-center gap-2.5 px-4 border-b">
              <div className={cn(
                "flex h-9 w-9 items-center justify-center rounded-full text-xs font-medium shrink-0",
                isGuest ? "bg-amber-100 text-amber-700" : "bg-primary/10 text-primary"
              )}>
                {isGuest ? "客" : (user?.username?.slice(0, 2).toUpperCase() ?? user?.userId?.slice(0, 2).toUpperCase() ?? "?")}
              </div>
              <div className="min-w-0">
                <p className="text-sm font-medium truncate">
                  {isGuest ? "游客" : (user?.username ?? user?.userId?.slice(0, 8) ?? "用户")}
                </p>
                <p className="text-[11px] text-muted-foreground">
                  {isGuest ? "trial mode" : "user"}
                </p>
              </div>
            </div>

            {/* 菜单列表 */}
            <div className="flex-1 p-2 space-y-0.5">
              {MENU_ITEMS.map((item) => {
                const Icon = item.icon;
                const isActive = activeMenu === item.key;
                return (
                  <button
                    key={item.key}
                    onClick={() => setActiveMenu(item.key)}
                    className={cn(
                      "flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-ui",
                      isActive
                        ? "bg-accent text-foreground font-medium"
                        : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"
                    )}
                  >
                    <Icon className="h-4 w-4 shrink-0" />
                    <span className="flex-1 text-left">{item.label}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {/* ── 右侧内容区 ── */}
          <div className="flex-1 overflow-y-auto relative">
            {/* 固定栏 — 左侧标题+副标题，右侧关闭按钮，横线对齐 admin 风格 */}
            <div className="sticky top-0 z-10 flex h-[60px] items-center justify-between px-6 bg-background/80 backdrop-blur-sm border-b">
              <div className="min-w-0">
                <h2 className="text-lg font-semibold leading-tight">{SECTION_META[activeMenu].title}</h2>
                <p className="text-sm text-muted-foreground leading-tight">{SECTION_META[activeMenu].subtitle}</p>
              </div>
              <button
                onClick={handleClose}
                className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            {activeMenu === "profile" && <ProfileSection />}
            {activeMenu === "styles" && <StyleSection />}
            {activeMenu === "memory" && <MemorySection />}
            {activeMenu === "settings" && <SettingsSection />}
            {activeMenu === "account" && <AccountSection />}

            {/* ── 右下角圆形 + 按钮（写作风格/记忆管理用） ── */}
            {(activeMenu === "styles" || activeMenu === "memory") && (
              <FloatingAddButton onClick={() => {
                // 通过自定义事件触发各子组件的添加操作
                const event = new CustomEvent("personal-center-add");
                window.dispatchEvent(event);
              }} />
            )}
          </div>
        </DialogPrimitive.Content>
      </DialogPortal>
    </DialogPrimitive.Root>
  );
}

// ─── 右下角圆形 + 按钮 ──────────────────────────────────

function FloatingAddButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className="fixed bottom-8 right-8 z-20 flex h-12 w-12 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-lg transition-transform hover:scale-105 active:scale-95"
      title="新建"
    >
      <Plus className="h-5 w-5" />
    </button>
  );
}

// ─── 个人信息 ────────────────────────────────────────────

function ProfileSection() {
  const user = useAuthStore((s) => s.user);
  const token = useAuthStore((s) => s.token);
  const login = useAuthStore((s) => s.login);
  const isGuest = user?.role === "guest";

  const [editingName, setEditingName] = useState(false);
  const [newName, setNewName] = useState(user?.username ?? "");
  const [savingName, setSavingName] = useState(false);
  const [nameError, setNameError] = useState<string | null>(null);
  const [nameSuccess, setNameSuccess] = useState(false);

  const handleSaveName = async () => {
    if (newName.length < 2 || newName.length > 64) {
      setNameError("用户名长度需要 2-64 个字符");
      return;
    }
    setSavingName(true);
    setNameError(null);
    try {
      const res = await fetch("/api/v2/auth/update-profile", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({ username: newName }),
      });
      const json = await res.json();
      if (json.success) {
        // 更新本地 store 中的用户名，保持 token 不变
        if (user && token) {
          const expiresAt = useAuthStore.getState().expiresAt ?? Math.floor(Date.now() / 1000) + 3600;
          login(token, user.userId, newName, user.role, expiresAt - Math.floor(Date.now() / 1000));
        }
        setEditingName(false);
        setNameSuccess(true);
        setTimeout(() => setNameSuccess(false), 3000);
      } else {
        setNameError(json.error?.message ?? "修改失败");
      }
    } catch {
      setNameError("网络错误");
    } finally {
      setSavingName(false);
    }
  };

  return (
    <div className="px-6 pb-12 space-y-6">
      
      <div className="space-y-4">
        {/* 用户名 — 支持修改 */}
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <span className="text-sm text-muted-foreground">用户名</span>
            {!isGuest && !editingName && (
              <Button
                variant="ghost"
                size="sm"
                className="h-7 gap-1 text-xs"
                onClick={() => { setEditingName(true); setNewName(user?.username ?? ""); setNameError(null); }}
              >
                <Pencil className="h-3 w-3" />
                修改
              </Button>
            )}
          </div>
          {editingName ? (
            <div className="space-y-2">
              <Input
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="输入新用户名"
                className="max-w-xs"
                autoFocus
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !savingName) handleSaveName();
                  if (e.key === "Escape") setEditingName(false);
                }}
              />
              {nameError && (
                <div className="flex items-center gap-2 text-xs text-red-600">
                  <AlertCircle className="h-3.5 w-3.5" />
                  {nameError}
                </div>
              )}
              <div className="flex gap-2">
                <Button size="sm" onClick={handleSaveName} disabled={savingName || newName.length < 2}>
                  {savingName ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <Check className="h-3.5 w-3.5 mr-1" />}
                  保存
                </Button>
                <Button variant="outline" size="sm" onClick={() => { setEditingName(false); setNameError(null); }}>
                  取消
                </Button>
              </div>
            </div>
          ) : (
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium">{user?.username ?? "-"}</span>
              {nameSuccess && (
                <span className="flex items-center gap-1 text-xs text-green-600">
                  <Check className="h-3 w-3" />
                  已更新
                </span>
              )}
            </div>
          )}
        </div>

        <InfoRow label="用户 ID" value={user?.userId ?? "-"} mono />
        <InfoRow label="角色" value={
          isGuest ? "游客" :
          user?.role === "admin" ? "管理员" : "注册用户"
        } />
        <InfoRow label="状态" value={
          isGuest ? "试用模式（限 1 次完整写作）" : "正常"
        } />
      </div>

      {isGuest && (
        <Card className="border-amber-200/60 bg-amber-50/50 dark:bg-amber-950/20">
          <CardContent className="py-4">
            <div className="flex items-start gap-3">
              <User className="h-5 w-5 text-amber-600 shrink-0 mt-0.5" />
              <div>
                <p className="text-sm font-medium text-amber-900 dark:text-amber-200">
                  升级账号
                </p>
                <p className="text-xs text-amber-700 dark:text-amber-400 mt-1">
                  注册后可无限制使用写作功能，并保留所有历史记录。
                </p>
              </div>
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function InfoRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className={cn(
        "text-sm font-medium",
        mono && "font-mono-sm text-muted-foreground"
      )}>
        {value.length > 20 ? value.slice(0, 20) + "..." : value}
      </span>
    </div>
  );
}

// ─── 写作风格 ────────────────────────────────────────────

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

function StyleSection() {
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
        const rawStyles = (json.data?.styles ?? []) as UserStyle[];
        // Fetch config for each style that has a version
        const withConfig = await Promise.all(
          rawStyles.map(async (s) => {
            if (s.current_version > 0) {
              try {
                const detailRes = await fetch(`/api/v2/my-styles/${s.id}`, {
                  headers: token ? { Authorization: `Bearer ${token}` } : {},
                });
                const detailJson = await detailRes.json();
                if (detailJson.success && detailJson.data?.config) {
                  return { ...s, config: detailJson.data.config as StyleConfig };
                }
              } catch {
                // ignore
              }
            }
            return s;
          })
        );
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
      await fetch(`/api/v2/my-styles/${id}`, {
        method: "DELETE",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      setStyles(styles.filter((s) => s.id !== id));
    } catch {
      setError("删除失败");
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
      <div className="px-6 pb-12 space-y-6">
        
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
    <div className="px-6 pb-12 space-y-5">
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
                      {style.config && (
                        <div className="mt-1.5 flex items-center gap-3 text-xs text-muted-foreground">
                          <span>{style.config.word_range.min}-{style.config.word_range.max}字</span>
                          <span>结构: {style.config.structure.type}</span>
                          {style.config.tags.length > 0 && (
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
                        <Badge variant="outline" className="text-xs gap-1 text-amber-600">
                          <Clock className="h-3 w-3" />
                          审核中
                        </Badge>
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
    <CreateDialog open onOpenChange={(o) => !o && onClose()}>
      <CreateDialogContent className="max-w-md">
        <CreateDialogHeader>
          <CreateDialogTitle>新建写作风格</CreateDialogTitle>
        </CreateDialogHeader>
        <div className="space-y-4 pt-2">
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
        <CreateDialogFooter>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={handleCreate} disabled={creating}>
            {creating ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Plus className="h-4 w-4 mr-2" />}
            创建
          </Button>
        </CreateDialogFooter>
      </CreateDialogContent>
    </CreateDialog>
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
    <CreateDialog open onOpenChange={(o) => !o && onClose()}>
      <CreateDialogContent className="max-w-2xl max-h-[85vh] overflow-y-auto">
        <CreateDialogHeader>
          <CreateDialogTitle>编辑风格: {style.name}</CreateDialogTitle>
        </CreateDialogHeader>

        {/* Tab Bar */}
        <div className="flex gap-1 border-b">
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

        <CreateDialogFooter>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Check className="h-4 w-4 mr-2" />}
            保存
          </Button>
        </CreateDialogFooter>
      </CreateDialogContent>
    </CreateDialog>
  );
}

// ─── 记忆管理 ────────────────────────────────────────────

const TIER_CONFIG = {
  hard: { label: "硬偏好", icon: Shield, color: "text-blue-600" },
  pattern: { label: "行为模式", icon: TrendingUp, color: "text-green-600" },
  feedback: { label: "反馈改进", icon: AlertCircle, color: "text-amber-600" },
};

const CATEGORY_LABELS: Record<string, string> = {
  word_count: "篇幅偏好",
  style: "风格偏好",
  structure: "结构偏好",
  tone: "语气偏好",
  title: "标题偏好",
  topic: "话题偏好",
  argument: "论证模式",
  sentence: "句式偏好",
  mode: "写作模式",
};

function formatDate(dateStr: string): string {
  const d = new Date(dateStr);
  const now = new Date();
  const diffDays = Math.floor((now.getTime() - d.getTime()) / (1000 * 60 * 60 * 24));
  if (diffDays === 0) return "今天";
  if (diffDays === 1) return "昨天";
  if (diffDays < 7) return `${diffDays} 天前`;
  return d.toLocaleDateString("zh-CN");
}

function MemorySection() {
  const { memories, loading, fetchMemories, createMemory, deleteMemory } = useMemoryStore();
  const [showCreate, setShowCreate] = useState(false);
  const [newCategory, setNewCategory] = useState("");
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");

  useEffect(() => {
    fetchMemories();
  }, [fetchMemories]);

  // 监听右下角 + 按钮事件
  useEffect(() => {
    const handler = () => setShowCreate(true);
    window.addEventListener("personal-center-add", handler);
    return () => window.removeEventListener("personal-center-add", handler);
  }, []);

  const handleCreate = async () => {
    if (!newCategory || !newKey || !newValue) return;
    const ok = await createMemory(newCategory, newKey, newValue);
    if (ok) {
      setShowCreate(false);
      setNewCategory("");
      setNewKey("");
      setNewValue("");
    }
  };

  const grouped = memories.reduce((acc, m) => {
    if (!acc[m.tier]) acc[m.tier] = [];
    acc[m.tier].push(m);
    return acc;
  }, {} as Record<string, UserMemory[]>);

  return (
    <div className="px-6 pb-12 space-y-5">
      

      {loading ? (
        <div className="py-12 text-center text-muted-foreground text-sm">加载中...</div>
      ) : memories.length === 0 ? (
        <div className="py-12 text-center">
          <Brain className="mx-auto h-12 w-12 text-muted-foreground/30" />
          <p className="mt-3 text-sm text-muted-foreground">
            暂无记忆数据。写作几次后，AI 会自动学习你的偏好。
          </p>
        </div>
      ) : (
        <div className="space-y-5">
          {(["hard", "pattern", "feedback"] as const).map((tier) => {
            const tierMemories = grouped[tier] ?? [];
            if (tierMemories.length === 0) return null;
            const config = TIER_CONFIG[tier];
            const Icon = config.icon;

            return (
              <div key={tier}>
                <div className="mb-2.5 flex items-center gap-2">
                  <Icon className={cn("h-4 w-4", config.color)} />
                  <h3 className="text-sm font-semibold">{config.label}</h3>
                  <Badge variant="secondary" className="text-xs">{tierMemories.length}</Badge>
                </div>
                <div className="space-y-2">
                  {tierMemories.map((mem) => (
                    <Card key={mem.id} className="overflow-hidden">
                      <CardContent className="flex items-start gap-3 py-3">
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="text-xs text-muted-foreground">
                              {CATEGORY_LABELS[mem.category] ?? mem.category}
                            </span>
                            {mem.status === "candidate" && (
                              <Badge variant="outline" className="text-xs text-amber-600">
                                待确认
                              </Badge>
                            )}
                          </div>
                          <p className="mt-1 text-sm">{mem.value}</p>
                          <div className="mt-1 flex items-center gap-3 text-xs text-muted-foreground">
                            <span>置信度 {Math.round(mem.confidence * 100)}%</span>
                            {mem.occurrences > 1 && <span>出现 {mem.occurrences} 次</span>}
                            <span>更新于 {formatDate(mem.updated_at)}</span>
                          </div>
                        </div>
                        <button
                          onClick={() => deleteMemory(mem.id)}
                          className="text-muted-foreground hover:text-destructive transition-colors shrink-0"
                          title="删除"
                        >
                          <Trash2 className="h-4 w-4" />
                        </button>
                      </CardContent>
                    </Card>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* 记忆创建弹窗 — 由右下角 + 按钮触发 */}
      <CreateDialog open={showCreate} onOpenChange={setShowCreate}>
        <CreateDialogContent>
          <CreateDialogHeader>
            <CreateDialogTitle>添加硬偏好</CreateDialogTitle>
          </CreateDialogHeader>
          <div className="space-y-4 pt-2">
            <div>
              <Label>类别</Label>
              <Input
                className="mt-1.5"
                placeholder="如：word_count, style, tone"
                value={newCategory}
                onChange={(e) => setNewCategory(e.target.value)}
              />
            </div>
            <div>
              <Label>标识</Label>
              <Input
                className="mt-1.5"
                placeholder="如：preferred_length"
                value={newKey}
                onChange={(e) => setNewKey(e.target.value)}
              />
            </div>
            <div>
              <Label>内容</Label>
              <Input
                className="mt-1.5"
                placeholder="如：偏好 800-1000 字"
                value={newValue}
                onChange={(e) => setNewValue(e.target.value)}
              />
            </div>
            <Button className="w-full" onClick={handleCreate} disabled={!newCategory || !newKey || !newValue}>
              创建
            </Button>
          </div>
        </CreateDialogContent>
      </CreateDialog>
    </div>
  );
}

// ─── 偏好设置 ────────────────────────────────────────────

const AGENT_MODE_OPTIONS: { value: AgentMode; label: string; description: string }[] = [
  { value: "harness", label: "智能会话模式", description: "LLM 持续会话 + Harness 路由，支持多轮修改、对话、搜索，适合实际写作场景" },
  { value: "pipeline", label: "流水线模式", description: "固定步骤流水线，稳定可预测，适合标准化写作" },
];

function SettingsSection() {
  const agentMode = useSettingsStore((s) => s.agentMode);
  const setAgentMode = useSettingsStore((s) => s.setAgentMode);
  const token = useAuthStore((s) => s.token);

  // 默认写作风格
  const [defaultStyle, setDefaultStyle] = useState<string>("");
  const [styles, setStyles] = useState<Array<{ slug: string; name: string }>>([]);
  const [loadingStyles, setLoadingStyles] = useState(true);

  // 加载可用风格列表
  useEffect(() => {
    fetch("/api/v2/styles", {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
      .then((res) => res.json())
      .then((json) => {
        if (json.success && json.data?.styles) {
          setStyles(json.data.styles.map((s: { slug: string; name: string }) => ({ slug: s.slug, name: s.name })));
        }
      })
      .catch(() => {})
      .finally(() => setLoadingStyles(false));
  }, [token]);

  // 加载当前默认风格
  useEffect(() => {
    fetch("/api/v2/preferences", {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
      .then((res) => res.json())
      .then((json) => {
        if (json.success && json.data?.default_style) {
          setDefaultStyle(json.data.default_style as string);
        }
      })
      .catch(() => {});
  }, [token]);

  const handleSetDefaultStyle = (slug: string) => {
    setDefaultStyle(slug);
    // 同步到服务器
    fetch("/api/v2/preferences", {
      method: "PUT",
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({ default_style: slug }),
    }).catch(() => {});
  };

  return (
    <div className="px-6 pb-12 space-y-6">
      

      {/* 默认写作风格 */}
      <div>
        <Label className="text-base font-semibold">默认写作风格</Label>
        <p className="mt-1 text-sm text-muted-foreground">
          选择新建写作任务时的默认风格。可在写作时随时切换。
        </p>
        <div className="mt-3">
          {loadingStyles ? (
            <p className="text-sm text-muted-foreground">加载风格列表...</p>
          ) : (
            <Select value={defaultStyle} onValueChange={handleSetDefaultStyle}>
              <SelectTrigger className="w-full max-w-xs">
                <SelectValue placeholder="选择默认写作风格" />
              </SelectTrigger>
              <SelectContent>
                {styles.map((s) => (
                  <SelectItem key={s.slug} value={s.slug}>
                    {s.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>
      </div>

      

      {/* 编排模式 */}
      <div>
        <Label className="text-base font-semibold">默认写作模式</Label>
        <p className="mt-1 text-sm text-muted-foreground">
          选择 AI 执行写作任务的编排方式。设置跟随你的账号，换设备登录也会保持。
        </p>
        <div className="mt-4 space-y-2">
          {AGENT_MODE_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              onClick={() => setAgentMode(opt.value)}
              className={cn(
                "flex w-full items-start gap-3 rounded-lg border p-4 text-left transition-ui",
                agentMode === opt.value
                  ? "border-primary bg-primary/5"
                  : "border-border hover:bg-accent/50"
              )}
            >
              <div className={cn(
                "flex h-5 w-5 shrink-0 items-center justify-center rounded-full border-2 mt-0.5",
                agentMode === opt.value ? "border-primary" : "border-muted-foreground/30"
              )}>
                {agentMode === opt.value && (
                  <div className="h-2.5 w-2.5 rounded-full bg-primary" />
                )}
              </div>
              <div className="flex-1 min-w-0">
                <div className="text-sm font-medium">{opt.label}</div>
                <div className="text-xs text-muted-foreground mt-0.5">{opt.description}</div>
              </div>
            </button>
          ))}
        </div>
      </div>

      

      {/* 提示信息 */}
      <div className="rounded-lg bg-muted/50 p-4">
        <p className="text-xs text-muted-foreground">
          <strong className="text-foreground">提示：</strong>
          智能会话模式（Harness）让 AI 在持续会话中自主搜索、写作、评审和修改，支持多轮对话和定向修改；
          流水线模式（Pipeline）使用固定步骤，更稳定但灵活性较低。
          如果对生成质量不满意，可以尝试切换模式体验差异。
        </p>
      </div>
    </div>
  );
}

// ─── 账号管理 ────────────────────────────────────────────

function AccountSection() {
  const user = useAuthStore((s) => s.user);
  const isGuest = user?.role === "guest";

  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [changing, setChanging] = useState(false);
  const [changeMsg, setChangeMsg] = useState<{ type: "success" | "error"; text: string } | null>(null);

  // Passkey state
  const [passkeys, setPasskeys] = useState<Array<{ id: string; name: string; created_at: string; last_used_at?: string }>>([]);
  const [loadingPasskeys, setLoadingPasskeys] = useState(true);

  // Passkey 注册状态
  const [pkRegOpen, setPkRegOpen] = useState(false);
  const [pkRegName, setPkRegName] = useState("");
  const [pkRegLoading, setPkRegLoading] = useState(false);
  const [pkRegMsg, setPkRegMsg] = useState<{ type: "success" | "error"; text: string } | null>(null);
  const [pkRegCountdown, setPkRegCountdown] = useState(0);
  const countdownTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    if (!isGuest) {
      fetchPasskeys();
    }
  }, [isGuest]);

  // 清理倒计时定时器
  useEffect(() => {
    return () => {
      if (countdownTimerRef.current) clearInterval(countdownTimerRef.current);
    };
  }, []);

  const fetchPasskeys = async () => {
    try {
      const res = await fetch("/api/v2/auth/passkey/list");
      const json = await res.json();
      if (json.success && json.data?.passkeys) {
        setPasskeys(json.data.passkeys);
      }
    } catch {
      // ignore
    } finally {
      setLoadingPasskeys(false);
    }
  };

  const handleChangePassword = async () => {
    setChangeMsg(null);
    if (newPassword !== confirmPassword) {
      setChangeMsg({ type: "error", text: "两次输入的新密码不一致" });
      return;
    }
    if (newPassword.length < 6) {
      setChangeMsg({ type: "error", text: "新密码至少需要 6 个字符" });
      return;
    }

    setChanging(true);
    try {
      const res = await fetch("/api/v2/auth/change-password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
      });
      const json = await res.json();
      if (json.success) {
        setChangeMsg({ type: "success", text: "密码修改成功" });
        setOldPassword("");
        setNewPassword("");
        setConfirmPassword("");
      } else {
        setChangeMsg({ type: "error", text: json.message || "密码修改失败" });
      }
    } catch {
      setChangeMsg({ type: "error", text: "网络错误，请稍后重试" });
    } finally {
      setChanging(false);
    }
  };

  const handleDeletePasskey = async (id: string) => {
    try {
      await fetch(`/api/v2/auth/passkey/${id}`, { method: "DELETE" });
      setPasskeys(passkeys.filter((p) => p.id !== id));
    } catch {
      // ignore
    }
  };

  const handleRegisterPasskey = async () => {
    setPkRegLoading(true);
    setPkRegMsg(null);
    try {
      const result = await registerPasskey({
        name: pkRegName.trim() || undefined,
        userId: user?.userId,
        userName: user?.username || "user",
      });
      setPkRegMsg({ type: "success", text: result.message || "Passkey 注册成功" });
      setPkRegName("");
      // 刷新列表
      fetchPasskeys();
      // 倒计时 3 秒后关闭弹窗
      setPkRegCountdown(3);
      if (countdownTimerRef.current) clearInterval(countdownTimerRef.current);
      countdownTimerRef.current = setInterval(() => {
        setPkRegCountdown((prev) => {
          if (prev <= 1) {
            if (countdownTimerRef.current) clearInterval(countdownTimerRef.current);
            setPkRegOpen(false);
            setPkRegMsg(null);
            return 0;
          }
          return prev - 1;
        });
      }, 1000);
    } catch (e) {
      setPkRegMsg({ type: "error", text: getPasskeyErrorMessage(e) });
    } finally {
      setPkRegLoading(false);
    }
  };

  if (isGuest) {
    return (
    <div className="px-6 pb-12 space-y-6">
      
      <Card className="border-amber-200/60 bg-amber-50/50 dark:bg-amber-950/20">
          <CardContent className="py-6 text-center">
            <KeyRound className="mx-auto h-10 w-10 text-amber-500/50" />
            <p className="mt-3 text-sm text-amber-900 dark:text-amber-200 font-medium">
              游客模式无法管理账号
            </p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
              注册账号后可修改密码和绑定 Passkey
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="px-6 pb-12 space-y-6">
      

      {/* 密码修改 */}
      <div className="space-y-4">
        <div className="flex items-center gap-2">
          <KeyRound className="h-4 w-4 text-muted-foreground" />
          <h3 className="text-sm font-semibold">修改密码</h3>
        </div>
        <div className="space-y-3 max-w-sm">
          <div>
            <Label className="text-xs">当前密码</Label>
            <Input
              type="password"
              className="mt-1"
              value={oldPassword}
              onChange={(e) => setOldPassword(e.target.value)}
              placeholder="输入当前密码"
            />
          </div>
          <div>
            <Label className="text-xs">新密码</Label>
            <Input
              type="password"
              className="mt-1"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder="至少 6 个字符"
            />
          </div>
          <div>
            <Label className="text-xs">确认新密码</Label>
            <Input
              type="password"
              className="mt-1"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              placeholder="再次输入新密码"
            />
          </div>
          {changeMsg && (
            <div className={cn(
              "flex items-center gap-2 text-xs",
              changeMsg.type === "success" ? "text-green-600" : "text-red-600"
            )}>
              {changeMsg.type === "success" ? <Check className="h-3.5 w-3.5" /> : <AlertCircle className="h-3.5 w-3.5" />}
              {changeMsg.text}
            </div>
          )}
          <Button
            size="sm"
            onClick={handleChangePassword}
            disabled={changing || !oldPassword || !newPassword || !confirmPassword}
          >
            {changing ? "修改中..." : "修改密码"}
          </Button>
        </div>
      </div>

      

      {/* Passkey 管理 */}
      <div className="space-y-4">
        <div className="flex items-center gap-2">
          <Fingerprint className="h-4 w-4 text-muted-foreground" />
          <h3 className="text-sm font-semibold">Passkey 认证</h3>
        </div>
        <p className="text-xs text-muted-foreground">
          使用 Face ID、Touch ID 或安全密钥进行无密码登录，更安全便捷。
        </p>

        {loadingPasskeys ? (
          <div className="text-sm text-muted-foreground py-4 text-center">加载中...</div>
        ) : (
          <Card className="border-dashed">
            <CardContent className="py-4 space-y-3">
              {passkeys.length === 0 ? (
                <div className="text-center py-2">
                  <Fingerprint className="mx-auto h-10 w-10 text-muted-foreground/30" />
                  <p className="mt-2 text-sm text-muted-foreground">
                    尚未绑定任何 Passkey
                  </p>
                </div>
              ) : (
                <div className="space-y-2">
                  {passkeys.map((pk) => (
                    <div key={pk.id} className="flex items-center gap-3 rounded-lg border p-2.5">
                      <Fingerprint className="h-5 w-5 text-muted-foreground shrink-0" />
                      <div className="flex-1 min-w-0">
                        <p className="text-sm font-medium">{pk.name || "未命名设备"}</p>
                        <p className="text-xs text-muted-foreground">
                          绑定于 {formatDate(pk.created_at)}
                          {pk.last_used_at && ` · 最近使用 ${formatDate(pk.last_used_at)}`}
                        </p>
                      </div>
                      <button
                        onClick={() => handleDeletePasskey(pk.id)}
                        className="text-muted-foreground hover:text-destructive transition-colors shrink-0"
                        title="删除"
                      >
                        <Trash2 className="h-4 w-4" />
                      </button>
                    </div>
                  ))}
                </div>
              )}
              {/* 绑定新 Passkey 按钮 — 直接放在内框中 */}
              <Button
                variant="outline"
                size="sm"
                className="w-full gap-2"
                onClick={() => {
                  setPkRegMsg(null);
                  setPkRegName("");
                  setPkRegCountdown(0);
                  setPkRegOpen(true);
                }}
              >
                <Plus className="h-3.5 w-3.5" />
                绑定新 Passkey
              </Button>
            </CardContent>
          </Card>
        )}
      </div>

      {/* Passkey 注册弹窗 */}
      <CreateDialog open={pkRegOpen} onOpenChange={setPkRegOpen}>
        <CreateDialogContent>
          <CreateDialogHeader>
            <CreateDialogTitle>绑定新 Passkey</CreateDialogTitle>
          </CreateDialogHeader>
          <div className="space-y-4 py-2">
            <p className="text-sm text-muted-foreground">
              点击下方按钮，使用 Face ID、Touch ID 或安全密钥完成 Passkey 注册。
            </p>
            <div>
              <Label className="text-xs">设备名称（可选）</Label>
              <Input
                className="mt-1"
                placeholder="如 MacBook Touch ID"
                value={pkRegName}
                onChange={(e) => setPkRegName(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && !pkRegLoading && handleRegisterPasskey()}
              />
            </div>
            {pkRegMsg && (
              <div className={cn(
                "flex items-center gap-2 text-sm",
                pkRegMsg.type === "success" ? "text-green-600" : "text-red-600"
              )}>
                {pkRegMsg.type === "success" ? <Check className="h-4 w-4" /> : <AlertCircle className="h-4 w-4" />}
                {pkRegMsg.text}
              </div>
            )}
          </div>
          <CreateDialogFooter>
            <Button
              variant="outline"
              onClick={() => { setPkRegOpen(false); setPkRegMsg(null); setPkRegCountdown(0); }}
              disabled={pkRegLoading || pkRegCountdown > 0}
            >
              取消
            </Button>
            {pkRegCountdown > 0 ? (
              <Button disabled className="gap-2">
                <Check className="h-4 w-4" />
                弹窗将在 {pkRegCountdown} 秒后关闭
              </Button>
            ) : (
              <Button onClick={handleRegisterPasskey} disabled={pkRegLoading}>
                {pkRegLoading ? (
                  <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                ) : (
                  <Fingerprint className="h-4 w-4 mr-2" />
                )}
                开始注册
              </Button>
            )}
          </CreateDialogFooter>
        </CreateDialogContent>
      </CreateDialog>
    </div>
  );
}
