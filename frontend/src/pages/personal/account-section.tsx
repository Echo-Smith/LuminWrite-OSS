/**
 * 账号管理子页面
 */
import { useState, useEffect, useRef } from "react";
import {
  KeyRound, Fingerprint, Trash2, Shield, AlertCircle,
  Check, Loader2, Clock, Plus,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { useAuthStore } from "@/stores/auth-store";
import { useBillingStore } from "@/stores/billing-store";
import { cn } from "@/lib/utils";
import { registerPasskey, getPasskeyErrorMessage } from "@/lib/passkey";
import { SimpleModal, SimpleModalFooter, formatDate } from "./shared";

export function AccountSection() {
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

  useEffect(() => {
    if (!isGuest) {
      fetchPasskeys();
    }
  }, [isGuest]);

  const fetchPasskeys = async () => {
    try {
      const token = useAuthStore.getState().token;
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
      const token = useAuthStore.getState().token;
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
      // 刷新列表
      fetchPasskeys();
    } catch (e) {
      setPkRegMsg({ type: "error", text: getPasskeyErrorMessage(e) });
    } finally {
      setPkRegLoading(false);
    }
  };

  if (isGuest) {
    return (
      <div className="px-6 pt-6 pb-12 space-y-6">
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
        <DeactivateSection />
      </div>
    );
  }

  return (
    <div className="px-6 pt-6 pb-12 space-y-6">

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

// ─── 注销账号 ────────────────────────────────────────────

function DeactivateSection() {
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const isGuest = user?.role === "guest";

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
    if (!isGuest) loadBalance();
  }, [isGuest, loadBalance]);

  // cleanup
  useEffect(() => {
    return () => {
      if (countdownRef.current) clearInterval(countdownRef.current);
    };
  }, []);

  const hasPoints = !isGuest && pointBalance > 0;
  const canSubmit = isGuest || (password.length > 0 && (!hasPoints || confirmForfeit));
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

    // If has points and hasn't confirmed forfeiture, start countdown
    if (hasPoints && !confirmForfeit) {
      setErrorMsg("请先确认放弃剩余点数");
      return;
    }

    // Start 5-second countdown on first click
    if (!countdownActive) {
      startCountdown();
      return;
    }

    // After countdown reaches 0, actual deactivate
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

      // Success — clear state and redirect
      logout();
      setOpen(false);
      resetDialog();
      // Reload to create a new guest session
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
          {/* 警告 */}
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
                {!isGuest && <li>• 积分余额和消费记录</li>}
              </ul>
            </div>
          </div>

          {/* 点数警告 */}
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

          {/* 密码输入（非 guest）*/}
          {!isGuest && (
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
          )}

          {/* 错误信息 */}
          {errorMsg && (
            <div className="flex items-center gap-2 text-xs text-red-600 dark:text-red-400">
              <AlertCircle className="h-3.5 w-3.5 shrink-0" />
              {errorMsg}
            </div>
          )}

          {/* 倒计时提示 */}
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

