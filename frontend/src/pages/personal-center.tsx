/**
 * 个人中心 — 居中悬浮面板
 *
 * 左侧菜单 + 右侧内容区，包含「个人信息」「记忆管理」「账号管理」。
 * 居中悬浮，低阴影，点击遮罩区域不关闭。
 */
import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import {
  Brain, User, X, KeyRound, Fingerprint,
  Trash2, Plus, TrendingUp, Shield, AlertCircle, type LucideIcon,
  Check, ChevronRight,
} from "lucide-react";
import { DialogPortal, DialogOverlay } from "@/components/ui/dialog";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import {
  Dialog as CreateDialog, DialogContent as CreateDialogContent,
  DialogHeader as CreateDialogHeader, DialogTitle as CreateDialogTitle,
  DialogTrigger as CreateDialogTrigger,
} from "@/components/ui/dialog";
import { useAuthStore } from "@/stores/auth-store";
import { useMemoryStore, type UserMemory } from "@/stores/memory-store";
import { cn } from "@/lib/utils";

// ─── 菜单项定义 ──────────────────────────────────────────

type MenuKey = "profile" | "memory" | "account";

interface MenuItem {
  key: MenuKey;
  label: string;
  icon: LucideIcon;
}

const MENU_ITEMS: MenuItem[] = [
  { key: "profile", label: "个人信息", icon: User },
  { key: "memory", label: "记忆管理", icon: Brain },
  { key: "account", label: "账号管理", icon: KeyRound },
];

const SECTION_META: Record<MenuKey, { title: string; subtitle: string }> = {
  profile: { title: "个人信息", subtitle: "查看你的账号信息" },
  memory: { title: "记忆管理", subtitle: "AI 根据你的写作习惯自动学习，也可手动管理" },
  account: { title: "账号管理", subtitle: "管理你的账号安全" },
};

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
            {/* 用户信息头部 — 与右侧标题区对齐 */}
            <div className="flex h-[60px] items-center gap-2.5 px-4 border-b">
              <div className={cn(
                "flex h-9 w-9 items-center justify-center rounded-full text-xs font-medium shrink-0",
                isGuest ? "bg-amber-100 text-amber-700" : "bg-primary/10 text-primary"
              )}>
                {isGuest ? "客" : (user?.userId?.slice(0, 2).toUpperCase() ?? "?")}
              </div>
              <div className="min-w-0">
                <p className="text-sm font-medium truncate">
                  {isGuest ? "游客" : (user?.userId?.slice(0, 8) ?? "用户") + "..."}
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
          <div className="flex-1 overflow-y-auto">
            {/* 固定栏 — 左侧标题+副标题，右侧关闭按钮 */}
            <div className="sticky top-0 z-10 flex h-[60px] items-center justify-between px-6 bg-background/80 backdrop-blur-sm">
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
            {activeMenu === "memory" && <MemorySection />}
            {activeMenu === "account" && <AccountSection />}
          </div>
        </DialogPrimitive.Content>
      </DialogPortal>
    </DialogPrimitive.Root>
  );
}

// ─── 个人信息 ────────────────────────────────────────────

function ProfileSection() {
  const user = useAuthStore((s) => s.user);
  const isGuest = user?.role === "guest";

  return (
    <div className="px-6 pb-12 space-y-6">
      <Separator />
      <div className="space-y-4">
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
      <div className="flex items-center justify-end">
        <CreateDialog open={showCreate} onOpenChange={setShowCreate}>
          <CreateDialogTrigger asChild>
            <Button size="sm" className="gap-2">
              <Plus className="h-4 w-4" />
              添加偏好
            </Button>
          </CreateDialogTrigger>
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

      <Separator />

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

  useEffect(() => {
    if (!isGuest) {
      fetchPasskeys();
    }
  }, [isGuest]);

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

  if (isGuest) {
    return (
    <div className="px-6 pb-12 space-y-6">
      <Separator />
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
      <Separator />

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

      <Separator />

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
        ) : passkeys.length === 0 ? (
          <Card className="border-dashed">
            <CardContent className="py-6 text-center">
              <Fingerprint className="mx-auto h-10 w-10 text-muted-foreground/30" />
              <p className="mt-2 text-sm text-muted-foreground">
                尚未绑定任何 Passkey
              </p>
            </CardContent>
          </Card>
        ) : (
          <div className="space-y-2">
            {passkeys.map((pk) => (
              <Card key={pk.id}>
                <CardContent className="flex items-center gap-3 py-3">
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
                </CardContent>
              </Card>
            ))}
          </div>
        )}

        <Button
          variant="outline"
          size="sm"
          className="gap-2"
          onClick={() => {
            // Navigate to passkey registration flow
            window.location.href = "/login?mode=passkey_register";
          }}
        >
          <Plus className="h-3.5 w-3.5" />
          绑定新 Passkey
        </Button>
      </div>
    </div>
  );
}
