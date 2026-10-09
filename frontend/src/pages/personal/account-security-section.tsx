/**
 * 账号与安全子页面 — 个人信息 + 账号管理合并
 *
 * 一个入口涵盖四组：身份信息（用户名 / ID / 角色）、邮箱（账号恢复凭证，
 * 归安全域）、登录凭证（密码 + Passkey）、危险区（注销）。
 * 游客态只展示升级卡片。
 */
import { useState, useEffect, useRef } from "react";
import {
  User, Pencil, AlertCircle, Check, Loader2, Mail,
  KeyRound, Fingerprint, Trash2, Plus,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAuthStore } from "@/stores/auth-store";
import { useBillingStore } from "@/stores/billing-store";
import { cn } from "@/lib/utils";
import { registerPasskey, getPasskeyErrorMessage } from "@/lib/passkey";
import { SimpleModal, SimpleModalFooter, formatDate } from "./shared";

export function AccountSecuritySection() {
  const user = useAuthStore((s) => s.user);
  const token = useAuthStore((s) => s.token);
  const login = useAuthStore((s) => s.login);
  const isGuest = user?.role === "guest";

  // ── 身份信息 ──
  const [editingName, setEditingName] = useState(false);
  const [newName, setNewName] = useState(user?.username ?? "");
  const [savingName, setSavingName] = useState(false);
  const [nameError, setNameError] = useState<string | null>(null);
  const [nameSuccess, setNameSuccess] = useState(false);

  // ── 邮箱 ──
  const [boundEmail, setBoundEmail] = useState("");
  const [emailLoading, setEmailLoading] = useState(true);
  const [emailEditing, setEmailEditing] = useState(false);
  const [emailInput, setEmailInput] = useState("");
  const [emailCode, setEmailCode] = useState("");
  const [emailSaving, setEmailSaving] = useState(false);
  const [emailError, setEmailError] = useState<string | null>(null);
  const [emailSuccess, setEmailSuccess] = useState(false);
  const [codeCountdown, setCodeCountdown] = useState(0);
  const [codeSending, setCodeSending] = useState(false);

  // ── 密码 ──
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [changing, setChanging] = useState(false);
  const [changeMsg, setChangeMsg] = useState<{ type: "success" | "error"; text: string } | null>(null);

  // ── Passkey ──
  const [passkeys, setPasskeys] = useState<Array<{ id: string; name: string; created_at: string; last_used_at?: string }>>([]);
  const [loadingPasskeys, setLoadingPasskeys] = useState(true);
  const [pkRegOpen, setPkRegOpen] = useState(false);
  const [pkRegName, setPkRegName] = useState("");
  const [pkRegLoading, setPkRegLoading] = useState(false);
  const [pkRegMsg, setPkRegMsg] = useState<{ type: "success" | "error"; text: string } | null>(null);

  useEffect(() => {
    if (!isGuest) {
      fetchEmail();
      fetchPasskeys();
    }
  }, [isGuest]); // eslint-disable-line react-hooks/exhaustive-deps

  // 验证码倒计时
  useEffect(() => {
    if (codeCountdown > 0) {
      const timer = setTimeout(() => setCodeCountdown(codeCountdown - 1), 1000);
      return () => clearTimeout(timer);
    }
  }, [codeCountdown]);

  // ─── 邮箱 handlers ───────────────────────────────────────

  const fetchEmail = async () => {
    try {
      const res = await fetch("/api/v2/auth/my-email", {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      const json = await res.json();
      if (json.success && json.data?.email) {
        setBoundEmail(json.data.email);
      }
    } catch {
      // ignore
    } finally {
      setEmailLoading(false);
    }
  };

  const handleSendCode = async () => {
    if (!emailInput.trim() || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(emailInput.trim())) {
      setEmailError("邮箱格式不正确");
      return;
    }
    setCodeSending(true);
    setEmailError(null);
    try {
      const res = await fetch("/api/v2/auth/send-code", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: emailInput.trim(), purpose: "bind" }),
      });
      const json = await res.json();
      if (json.success) {
        setCodeCountdown(60);
      } else {
        const code = json.error?.code || "send_failed";
        setEmailError(
          code === "email_taken" ? "该邮箱已被其他账号绑定" :
          code === "rate_limited" ? "请等待 60 秒后再试" :
          json.error?.message || "验证码发送失败"
        );
      }
    } catch {
      setEmailError("网络错误");
    } finally {
      setCodeSending(false);
    }
  };

  const handleBindEmail = async () => {
    if (!emailInput.trim() || !emailCode.trim()) return;
    setEmailSaving(true);
    setEmailError(null);
    try {
      const res = await fetch("/api/v2/auth/bind-email", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({ email: emailInput.trim(), code: emailCode.trim() }),
      });
      const json = await res.json();
      if (json.success) {
        setBoundEmail(emailInput.trim());
        setEmailEditing(false);
        setEmailInput("");
        setEmailCode("");
        setEmailSuccess(true);
        setTimeout(() => setEmailSuccess(false), 3000);
      } else {
        setEmailError(json.error?.message || "绑定失败");
      }
    } catch {
      setEmailError("网络错误");
    } finally {
      setEmailSaving(false);
    }
  };

  // ─── 用户名 handlers ─────────────────────────────────────

  const handleSaveName = async () => {
    if (newName.length < 2 || newName.length > 64) {
      setNameError("用户名长度需要 2-64 个字符");
      return;
    }
    if (newName === (user?.username ?? "")) {
      setEditingName(false);
      setNameError(null);
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

  // ─── 密码 handlers ───────────────────────────────────────

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
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
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

  // ─── Passkey handlers ────────────────────────────────────

  const fetchPasskeys = async () => {
    try {
      const res = await fetch("/api/v2/auth/passkey/list", {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
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

  const handleDeletePasskey = async (id: string) => {
    try {
      await fetch(`/api/v2/auth/passkey/${id}`, {
        method: "DELETE",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
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
      fetchPasskeys();
    } catch (e) {
      setPkRegMsg({ type: "error", text: getPasskeyErrorMessage(e) });
    } finally {
      setPkRegLoading(false);
    }
  };

  // ─── 游客态 ──────────────────────────────────────────────

  if (isGuest) {
    return (
      <div className="px-6 pt-6 pb-12 space-y-6">
        <Card className="border-amber-200/60 bg-amber-50/50 dark:bg-amber-950/20">
          <CardContent className="py-6 text-center">
            <User className="mx-auto h-10 w-10 text-amber-500/50" />
            <p className="mt-3 text-sm text-amber-900 dark:text-amber-200 font-medium">
              游客模式无法管理账号
            </p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
              注册账号后可修改用户名、绑定邮箱、修改密码与 Passkey，并保留所有历史记录。
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="px-6 pt-6 pb-12 space-y-6">

      {/* ── 身份信息 ── */}
      <div className="space-y-4">
        <div className="flex items-center gap-2">
          <User className="h-4 w-4 text-muted-foreground" />
          <h3 className="text-sm font-semibold">身份信息</h3>
        </div>
        <div className="space-y-4">
          {editingName ? (
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <span className="text-sm text-muted-foreground">用户名</span>
                <div className="flex items-center gap-2 max-w-[200px]">
                  <Input
                    value={newName}
                    onChange={(e) => setNewName(e.target.value)}
                    placeholder="输入新用户名"
                    autoFocus
                    disabled={savingName}
                    className="h-7 text-sm"
                    onBlur={() => {
                      if (newName.length >= 2 && newName !== (user?.username ?? "")) {
                        handleSaveName();
                      } else {
                        setEditingName(false);
                        setNameError(null);
                      }
                    }}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && !savingName) handleSaveName();
                      if (e.key === "Escape") { setEditingName(false); setNameError(null); }
                    }}
                  />
                  <Button
                    size="icon"
                    className="h-7 w-7 shrink-0"
                    onClick={handleSaveName}
                    onMouseDown={(e) => e.preventDefault()}
                    disabled={savingName || newName.length < 2}
                    title="确认修改"
                  >
                    {savingName ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Check className="h-3.5 w-3.5" />}
                  </Button>
                </div>
              </div>
              {nameError && (
                <div className="flex items-center gap-2 text-xs text-red-600">
                  <AlertCircle className="h-3.5 w-3.5" />
                  {nameError}
                </div>
              )}
            </div>
          ) : (
            <div
              className="flex items-center justify-between rounded-md -mx-1 px-1 py-0.5 transition-ui cursor-pointer hover:bg-accent/50"
              onClick={() => {
                setEditingName(true);
                setNewName(user?.username ?? "");
                setNameError(null);
              }}
            >
              <span className="text-sm text-muted-foreground">用户名</span>
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium">{user?.username ?? "-"}</span>
                <Pencil className="h-3 w-3 text-muted-foreground/50" />
                {nameSuccess && (
                  <span className="flex items-center gap-1 text-xs text-green-600">
                    <Check className="h-3 w-3" />
                    已更新
                  </span>
                )}
              </div>
            </div>
          )}

          <InfoRow label="用户 ID" value={user?.userId ?? "-"} mono />
          <InfoRow label="角色" value={user?.role === "admin" ? "管理员" : "注册用户"} />
          <InfoRow label="状态" value="正常" />
        </div>
      </div>

      {/* ── 邮箱（账号恢复凭证）── */}
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <Mail className="h-4 w-4 text-muted-foreground" />
          <h3 className="text-sm font-semibold">邮箱</h3>
        </div>
        {emailLoading ? (
          <div className="text-sm text-muted-foreground">加载中...</div>
        ) : emailEditing ? (
          <div className="space-y-2 max-w-sm">
            <Input
              type="email"
              className="text-sm"
              placeholder="your@email.com"
              value={emailInput}
              onChange={(e) => setEmailInput(e.target.value)}
            />
            {emailInput.trim() && (
              <div className="flex gap-2">
                <Input
                  className="text-sm flex-1"
                  placeholder="验证码"
                  value={emailCode}
                  onChange={(e) => setEmailCode(e.target.value)}
                />
                <Button
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={handleSendCode}
                  disabled={codeSending || codeCountdown > 0}
                >
                  {codeSending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> :
                   codeCountdown > 0 ? `${codeCountdown}s` : "发送验证码"}
                </Button>
              </div>
            )}
            {emailError && (
              <div className="flex items-center gap-2 text-xs text-red-600">
                <AlertCircle className="h-3.5 w-3.5" /> {emailError}
              </div>
            )}
            <div className="flex gap-2">
              <Button
                size="sm"
                onClick={handleBindEmail}
                disabled={emailSaving || !emailInput.trim() || !emailCode.trim()}
              >
                {emailSaving ? "绑定中..." : "确认绑定"}
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => { setEmailEditing(false); setEmailInput(""); setEmailCode(""); setEmailError(null); }}
              >
                取消
              </Button>
            </div>
          </div>
        ) : boundEmail ? (
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium">{boundEmail}</span>
            {emailSuccess ? (
              <span className="flex items-center gap-1 text-xs text-green-600">
                <Check className="h-3 w-3" /> 已绑定
              </span>
            ) : (
              <button
                onClick={() => { setEmailEditing(true); setEmailInput(""); setEmailCode(""); setEmailError(null); }}
                className="text-muted-foreground hover:text-foreground"
              >
                <Pencil className="h-3 w-3" />
              </button>
            )}
          </div>
        ) : (
          <div className="flex items-center justify-between">
            <span className="text-sm text-muted-foreground">未绑定</span>
            <Button
              variant="outline"
              size="sm"
              onClick={() => { setEmailEditing(true); setEmailInput(""); setEmailCode(""); setEmailError(null); }}
            >
              绑定邮箱
            </Button>
          </div>
        )}
        {!boundEmail && !emailEditing && (
          <p className="text-xs text-amber-600 flex items-start gap-1.5">
            <AlertCircle className="h-3.5 w-3.5 shrink-0 mt-0.5" />
            绑定邮箱后可使用“忘记密码”功能。如未绑定邮箱且忘记密码，请联系管理员重置。
          </p>
        )}
      </div>

      {/* ── 修改密码 ── */}
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

      {/* ── Passkey 认证 ── */}
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
              <Button
                variant="outline"
                size="sm"
                className="w-full gap-2"
                onClick={() => {
                  setPkRegMsg(null);
                  setPkRegName("");
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
      <SimpleModal open={pkRegOpen} onClose={() => { setPkRegOpen(false); setPkRegMsg(null); }} title="绑定新 Passkey">
          <div className="space-y-4">
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
          <SimpleModalFooter>
            {pkRegMsg?.type === "success" ? (
              <Button onClick={() => { setPkRegOpen(false); setPkRegMsg(null); }} className="gap-2">
                <Check className="h-4 w-4" />
                完成
              </Button>
            ) : (
              <>
                <Button
                  variant="outline"
                  onClick={() => { setPkRegOpen(false); setPkRegMsg(null); }}
                  disabled={pkRegLoading}
                >
                  取消
                </Button>
                <Button onClick={handleRegisterPasskey} disabled={pkRegLoading}>
                  {pkRegLoading ? (
                    <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                  ) : (
                    <Fingerprint className="h-4 w-4 mr-2" />
                  )}
                  开始注册
                </Button>
              </>
            )}
          </SimpleModalFooter>
      </SimpleModal>

      {/* ── 注销账号 ── */}
      <DeactivateSection />
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

// ─── 注销账号 ────────────────────────────────────────────

function DeactivateSection() {
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);

  // billing balance
  const balanceInfo = useBillingStore((s) => s.balance);
  const loadBalance = useBillingStore((s) => s.loadBalance);
  const pointBalance = balanceInfo?.point_balance ?? 0;

  // dialog state
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [confirmForfeit, setConfirmForfeit] = useState(false);
  const [countdown, setCountdown] = useState(0);
  const [deactivating, setDeactivating] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const countdownRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // load balance on mount
  useEffect(() => {
    loadBalance();
  }, [loadBalance]);

  // cleanup
  useEffect(() => {
    return () => {
      if (countdownRef.current) clearInterval(countdownRef.current);
    };
  }, []);

  const hasPoints = pointBalance > 0;
  const canSubmit = password.length > 0 && (!hasPoints || confirmForfeit);
  const countdownActive = countdown > 0;

  const startCountdown = () => {
    setCountdown(5);
    countdownRef.current = setInterval(() => {
      setCountdown((prev) => {
        if (prev <= 1) {
          if (countdownRef.current) clearInterval(countdownRef.current);
          return 0;
        }
        return prev - 1;
      });
    }, 1000);
  };

  const resetDialog = () => {
    setPassword("");
    setConfirmForfeit(false);
    setCountdown(0);
    setErrorMsg(null);
    if (countdownRef.current) {
      clearInterval(countdownRef.current);
      countdownRef.current = null;
    }
  };

  const handleClose = () => {
    if (deactivating) return;
    setOpen(false);
    resetDialog();
  };

  const handleDeactivate = async () => {
    setErrorMsg(null);

    if (hasPoints && !confirmForfeit) {
      setErrorMsg("请先确认放弃剩余点数");
      return;
    }

    if (!countdownActive) {
      startCountdown();
      return;
    }

    setDeactivating(true);
    try {
      const token = useAuthStore.getState().token;
      const res = await fetch("/api/v2/auth/deactivate", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({
          password: password || undefined,
          confirm_forfeit_points: confirmForfeit,
        }),
      });
      const json = await res.json();

      if (!res.ok || !json.success) {
        const code = json.error?.code || "deactivate_failed";
        const messages: Record<string, string> = {
          password_required: "请输入密码",
          wrong_password: "密码不正确",
          no_password: "该账号未设置密码",
          points_exist: "账号内还有剩余点数，需确认放弃后才能注销",
          not_found: "用户不存在或已删除",
        };
        setErrorMsg(messages[code] || json.error?.message || "注销失败，请重试");
        setDeactivating(false);
        return;
      }

      logout();
      setOpen(false);
      resetDialog();
      window.location.href = "/";
    } catch {
      setErrorMsg("网络错误，请检查连接");
      setDeactivating(false);
    }
  };

  return (
    <>
      <div className="space-y-4">
        <div className="flex items-center gap-2">
          <Trash2 className="h-4 w-4 text-destructive" />
          <h3 className="text-sm font-semibold text-destructive">注销账号</h3>
        </div>
        <Card className="border-destructive/20 bg-destructive/5">
          <CardContent className="py-4 space-y-3">
            <p className="text-xs text-muted-foreground">
              注销后，你的账号和所有关联数据（写作历史、记忆、风格、Passkey 等）将被永久删除，此操作不可逆。
            </p>
            {hasPoints && (
              <div className="flex items-start gap-2 rounded-lg bg-amber-50 dark:bg-amber-950/20 p-3">
                <AlertCircle className="h-4 w-4 text-amber-600 dark:text-amber-400 shrink-0 mt-0.5" />
                <div>
                  <p className="text-xs font-medium text-amber-900 dark:text-amber-200">
                    账号内还有 {pointBalance.toFixed(2)} 点数
                  </p>
                  <p className="text-xs text-amber-700 dark:text-amber-400 mt-0.5">
                    注销后点数将永久作废，无法退还或转移。
                  </p>
                </div>
              </div>
            )}
            <Button
              variant="outline"
              size="sm"
              className="text-destructive border-destructive/30 hover:bg-destructive/10"
              onClick={() => setOpen(true)}
            >
              <Trash2 className="h-3.5 w-3.5 mr-1.5" />
              注销账号
            </Button>
          </CardContent>
        </Card>
      </div>

      {/* 注销确认弹窗 */}
      <SimpleModal
        open={open}
        onClose={handleClose}
        title="确认注销账号"
        maxWidth="max-w-md"
      >
        <div className="space-y-5">
          <div className="flex items-start gap-2.5 rounded-lg bg-destructive/5 p-3.5">
            <AlertCircle className="h-5 w-5 text-destructive shrink-0 mt-0.5" />
            <div className="space-y-1">
              <p className="text-sm font-medium text-destructive">此操作不可逆</p>
              <p className="text-xs text-muted-foreground">
                注销后以下数据将被永久删除：
              </p>
              <ul className="text-xs text-muted-foreground space-y-0.5 ml-3">
                <li>• 账号信息和登录凭证</li>
                <li>• 所有写作历史和文章版本</li>
                <li>• AI 记忆和偏好设置</li>
                <li>• 自定义写作风格</li>
                <li>• 已绑定的 Passkey</li>
                <li>• 积分余额和消费记录</li>
              </ul>
            </div>
          </div>

          {hasPoints && (
            <div className="space-y-2">
              <div className="flex items-start gap-2 rounded-lg bg-amber-50 dark:bg-amber-950/20 p-3">
                <AlertCircle className="h-4 w-4 text-amber-600 dark:text-amber-400 shrink-0 mt-0.5" />
                <div>
                  <p className="text-sm font-medium text-amber-900 dark:text-amber-200">
                    剩余点数：{pointBalance.toFixed(2)} 点
                  </p>
                  <p className="text-xs text-amber-700 dark:text-amber-400 mt-0.5">
                    注销后点数将永久作废，无法退还或转移。如需退款，请联系管理员后再注销。
                  </p>
                </div>
              </div>
              <label className="flex items-start gap-2.5 cursor-pointer select-none">
                <input
                  type="checkbox"
                  className="mt-0.5 h-4 w-4 rounded border-amber-400 text-amber-600 focus:ring-amber-500"
                  checked={confirmForfeit}
                  onChange={(e) => setConfirmForfeit(e.target.checked)}
                  disabled={countdownActive || deactivating}
                />
                <span className="text-sm text-amber-900 dark:text-amber-200">
                  我已了解，确认放弃全部剩余点数（{pointBalance.toFixed(2)} 点）
                </span>
              </label>
            </div>
          )}

          <div className="space-y-2">
            <Label className="text-xs">输入密码确认身份</Label>
            <Input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="输入你的登录密码"
              disabled={deactivating || countdownActive}
            />
          </div>

          {errorMsg && (
            <div className="flex items-center gap-2 text-xs text-red-600 dark:text-red-400">
              <AlertCircle className="h-3.5 w-3.5 shrink-0" />
              {errorMsg}
            </div>
          )}

          {countdownActive && (
            <div className="flex items-center gap-2 text-sm text-amber-600 dark:text-amber-400">
              <Loader2 className="h-4 w-4 animate-spin" />
              请等待 {countdown} 秒后再点击确认注销…
            </div>
          )}
        </div>

        <SimpleModalFooter>
          <Button
            variant="outline"
            onClick={handleClose}
            disabled={deactivating}
          >
            取消
          </Button>
          <Button
            variant="destructive"
            onClick={handleDeactivate}
            disabled={!canSubmit || deactivating || (hasPoints && !confirmForfeit) || (countdownActive && countdown > 0)}
          >
            {deactivating ? (
              <>
                <Loader2 className="h-4 w-4 mr-1.5 animate-spin" />
                注销中...
              </>
            ) : countdownActive && countdown > 0 ? (
              `请等待 ${countdown}s`
            ) : countdown === 0 && countdownRef.current !== null ? (
              "确认注销"
            ) : (
              "开始注销"
            )}
          </Button>
        </SimpleModalFooter>
      </SimpleModal>
    </>
  );
}
