/**
 * AuthModal — 登录/注册弹窗（替代原登录页）
 *
 * 支持：
 *  - 账号密码登录（含 Passkey 快捷登录按钮）
 *  - 注册（游客升级或新注册，含确认密码 + 实时校验）
 *
 * 由 AuthProvider 统一管理开关，其他组件通过 useAuthModal 调用。
 */
import { useState, useEffect, useRef, useCallback } from "react";
import { Loader2, User, ArrowRight, Fingerprint, UserPlus, Sparkles, Check, AlertCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { authStore } from "@/stores/auth-store";
import {
  isWebAuthnSupported,
  isPlatformAuthenticatorAvailable,
  loginWithPasskey,
  getPasskeyErrorMessage,
} from "@/lib/passkey";
import { useAuthStore } from "@/stores/auth-store";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

interface AuthModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** 当游客达到限制时，传入 guest token 用于升级注册 */
  guestToken?: string;
  /** 默认显示的 Tab */
  defaultTab?: "login" | "register";
}

export function AuthModal({ open, onOpenChange, guestToken, defaultTab = "login" }: AuthModalProps) {
  // 根据 guestToken 和 defaultTab 计算初始 Tab
  const effectiveDefaultTab = guestToken ? "register" : defaultTab;

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [activeTab, setActiveTab] = useState<"login" | "register">(effectiveDefaultTab);

  // Passkey 支持检测
  const [passkeySupported, setPasskeySupported] = useState(false);
  const [platformAuthAvailable, setPlatformAuthAvailable] = useState(false);

  // 同意条款
  const [agreed, setAgreed] = useState(false);

  // 密码登录模式
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const passwordRef = useRef<HTMLInputElement>(null);

  // 注册模式
  const [regUsername, setRegUsername] = useState("");
  const [regPassword, setRegPassword] = useState("");
  const [regConfirmPassword, setRegConfirmPassword] = useState("");
  const regPasswordRef = useRef<HTMLInputElement>(null);
  const regConfirmRef = useRef<HTMLInputElement>(null);

  // 注册字段触碰标记（用于延迟显示校验）
  const [touched, setTouched] = useState({
    username: false,
    password: false,
    confirm: false,
  });

  useEffect(() => {
    setPasskeySupported(isWebAuthnSupported());
    isPlatformAuthenticatorAvailable().then(setPlatformAuthAvailable);
  }, []);

  // 弹窗打开时同步 activeTab
  useEffect(() => {
    if (open) {
      setActiveTab(guestToken ? "register" : (defaultTab || "login"));
    }
  }, [open, guestToken, defaultTab]);

  // 弹窗关闭时重置所有输入和状态
  const resetAll = useCallback(() => {
    setError("");
    setUsername("");
    setPassword("");
    setRegUsername("");
    setRegPassword("");
    setRegConfirmPassword("");
    setAgreed(false);
    setTouched({ username: false, password: false, confirm: false });
  }, []);

  useEffect(() => {
    if (!open) {
      resetAll();
    }
  }, [open, resetAll]);

  // ─── 注册表单校验 ────────────────────────────────────────

  const regUsernameValid = regUsername.trim().length >= 2 && regUsername.trim().length <= 64;
  const regPasswordValid = regPassword.length >= 6;
  const regConfirmValid = regConfirmPassword === regPassword && regConfirmPassword.length > 0;

  // 密码强度
  const passwordStrength = (() => {
    if (!regPassword) return 0;
    let score = 0;
    if (regPassword.length >= 6) score++;
    if (regPassword.length >= 10) score++;
    if (/[a-z]/.test(regPassword) && /[A-Z]/.test(regPassword)) score++;
    if (/\d/.test(regPassword)) score++;
    if (/[^a-zA-Z\d]/.test(regPassword)) score++;
    return Math.min(score, 3); // 0=弱, 1=中, 2=强, 3=很强
  })();

  const STRENGTH_LABELS = ["", "弱", "中", "强"];
  const STRENGTH_COLORS = [
    "",
    "bg-red-400",
    "bg-amber-400",
    "bg-green-500",
  ];

  const canRegister = regUsernameValid && regPasswordValid && regConfirmValid && agreed;

  // ─── 登录表单校验 ────────────────────────────────────────

  const canLogin = username.trim().length > 0 && password.trim().length > 0 && agreed;

  // ─── Tab 切换 ─────────────────────────────────────────────

  const handleTabChange = (value: string) => {
    setActiveTab(value as "login" | "register");
    setError("");
  };

  // ─── 登录处理 ─────────────────────────────────────────────

  const handlePasswordLogin = async () => {
    if (!username.trim() || !password.trim()) return;
    setLoading(true);
    setError("");
    const result = await authStore.login({
      username: username.trim(),
      password: password.trim(),
    });
    if (result.ok) {
      onOpenChange(false);
    } else {
      setError(result.message || "用户名或密码错误");
    }
    setLoading(false);
  };

  const handlePasskeyLogin = async () => {
    setLoading(true);
    setError("");
    try {
      const result = await loginWithPasskey();
      useAuthStore.getState().login(
        result.token,
        result.user_id,
        result.username || "",
        result.role,
        result.expires_in
      );
      onOpenChange(false);
    } catch (e) {
      setError(getPasskeyErrorMessage(e));
    }
    setLoading(false);
  };

  // ─── 注册处理 ─────────────────────────────────────────────

  const handleRegister = async () => {
    if (!regUsernameValid || !regPasswordValid || !regConfirmValid) return;
    setLoading(true);
    setError("");

    const body: Record<string, unknown> = {
      username: regUsername.trim(),
      password: regPassword,
    };
    if (guestToken) {
      body.guest_token = guestToken;
    }

    const result = await authStore.register(body);
    if (result.ok) {
      onOpenChange(false);
    } else {
      // 根据后端错误码做精准定位
      if (result.code === "username_taken" || result.code === "bad_request") {
        setError(result.message);
        setTouched((t) => ({ ...t, username: true }));
      } else {
        setError(result.message || "注册失败，请重试");
      }
    }
    setLoading(false);
  };

  // ─── 标题动态化 ───────────────────────────────────────────

  const dialogTitle = activeTab === "register" ? "注册账号" : "笔润智谈 · 登录";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[420px]">
        <DialogHeader>
          <DialogTitle className="text-center text-xl">
            {dialogTitle}
          </DialogTitle>
        </DialogHeader>

        {guestToken && (
          <div className="flex items-start gap-2.5 rounded-lg bg-amber-50 dark:bg-amber-950/20 px-3 py-2.5">
            <Sparkles className="h-4 w-4 text-amber-600 dark:text-amber-400 shrink-0 mt-0.5" />
            <div className="text-sm text-amber-700 dark:text-amber-400">
              <p className="font-medium">注册后解锁完整功能</p>
              <p className="text-xs mt-0.5">无限次写作 · 数据永久保留 · 个性化记忆</p>
            </div>
          </div>
        )}

        {error && (
          <div className="flex items-start gap-2 rounded-lg bg-red-50 dark:bg-red-950/20 px-3 py-2 text-sm text-red-600 dark:text-red-400 anim-shake">
            <AlertCircle className="h-4 w-4 shrink-0 mt-0.5" />
            <span>{error}</span>
          </div>
        )}

        <Tabs
          defaultValue={effectiveDefaultTab}
          value={activeTab}
          onValueChange={handleTabChange}
          className="w-full"
        >
          <TabsList className="grid w-full grid-cols-2">
            <TabsTrigger value="login" className="text-xs">登录</TabsTrigger>
            <TabsTrigger value="register" className="text-xs">注册</TabsTrigger>
          </TabsList>

          {/* ─── 登录 Tab ──────────────────────────────────── */}
          <TabsContent value="login" className="space-y-3 pt-4">
            <div>
              <Label className="flex items-center gap-1.5">
                <User className="h-3.5 w-3.5" /> 用户名
              </Label>
              <Input
                className="mt-1.5"
                placeholder="输入用户名"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    passwordRef.current?.focus();
                  }
                }}
              />
            </div>
            <div>
              <Label>密码</Label>
              <Input
                ref={passwordRef}
                className="mt-1.5"
                type="password"
                placeholder="••••••••"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && handlePasswordLogin()}
              />
            </div>

            {/* 同意条款 */}
            <ConsentCheckbox id="consent-login" checked={agreed} onCheckedChange={setAgreed} />

            <Button
              className="w-full"
              onClick={handlePasswordLogin}
              disabled={loading || !canLogin}
            >
              {loading ? (
                <Loader2 className="h-4 w-4 mr-2 animate-spin" />
              ) : (
                <ArrowRight className="h-4 w-4 mr-2" />
              )}
              登录
            </Button>

            {/* Passkey 登录 */}
            {passkeySupported && (
              <>
                <div className="flex items-center gap-2 py-0.5">
                  <div className="h-px flex-1 bg-border" />
                  <span className="text-xs text-muted-foreground px-1">或</span>
                  <div className="h-px flex-1 bg-border" />
                </div>
                <Button
                  variant="outline"
                  className="w-full"
                  onClick={handlePasskeyLogin}
                  disabled={loading || !agreed}
                >
                  {loading ? (
                    <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                  ) : (
                    <Fingerprint className="h-4 w-4 mr-2" />
                  )}
                  使用 Passkey 登录
                </Button>
                {!platformAuthAvailable && (
                  <p className="text-xs text-muted-foreground text-center">
                    将弹窗引导你选择安全密钥。
                  </p>
                )}
              </>
            )}
          </TabsContent>

          {/* ─── 注册 Tab ──────────────────────────────────── */}
          <TabsContent value="register" className="space-y-3 pt-4">
            {guestToken && (
              <p className="text-xs text-muted-foreground">
                注册后将自动保留你的写作记录和反馈数据。
              </p>
            )}

            {/* 用户名 */}
            <div>
              <Label className="flex items-center gap-1.5">
                <UserPlus className="h-3.5 w-3.5" /> 用户名
              </Label>
              <Input
                className="mt-1.5"
                placeholder="2-64 个字符"
                value={regUsername}
                onChange={(e) => setRegUsername(e.target.value)}
                onBlur={() => setTouched((t) => ({ ...t, username: true }))}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    regPasswordRef.current?.focus();
                  }
                }}
              />
              {touched.username && regUsername && !regUsernameValid && (
                <p className="text-xs text-red-500 mt-1">用户名需 2-64 个字符</p>
              )}
              {touched.username && regUsernameValid && (
                <p className="text-xs text-green-500 mt-1 flex items-center gap-1">
                  <Check className="h-3 w-3" /> 用户名可用
                </p>
              )}
            </div>

            {/* 密码 */}
            <div>
              <Label>密码</Label>
              <Input
                ref={regPasswordRef}
                className="mt-1.5"
                type="password"
                placeholder="至少 6 位"
                value={regPassword}
                onChange={(e) => setRegPassword(e.target.value)}
                onBlur={() => setTouched((t) => ({ ...t, password: true }))}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    regConfirmRef.current?.focus();
                  }
                }}
              />
              {touched.password && regPassword && !regPasswordValid && (
                <p className="text-xs text-red-500 mt-1">密码至少 6 位</p>
              )}
              {/* 密码强度指示器 */}
              {regPassword && regPasswordValid && (
                <div className="flex items-center gap-1.5 mt-1.5">
                  <div className="flex gap-1 flex-1">
                    {[1, 2, 3].map((i) => (
                      <div
                        key={i}
                        className={`h-1 flex-1 rounded-full transition-ui ${
                          i <= passwordStrength ? STRENGTH_COLORS[passwordStrength] : "bg-muted"
                        }`}
                      />
                    ))}
                  </div>
                  <span className="text-xs text-muted-foreground w-6">
                    {STRENGTH_LABELS[passwordStrength]}
                  </span>
                </div>
              )}
            </div>

            {/* 确认密码 */}
            <div>
              <Label>确认密码</Label>
              <Input
                ref={regConfirmRef}
                className="mt-1.5"
                type="password"
                placeholder="再次输入密码"
                value={regConfirmPassword}
                onChange={(e) => setRegConfirmPassword(e.target.value)}
                onBlur={() => setTouched((t) => ({ ...t, confirm: true }))}
                onKeyDown={(e) => e.key === "Enter" && handleRegister()}
              />
              {touched.confirm && regConfirmPassword && !regConfirmValid && (
                <p className="text-xs text-red-500 mt-1">两次密码不一致</p>
              )}
              {touched.confirm && regConfirmValid && (
                <p className="text-xs text-green-500 mt-1 flex items-center gap-1">
                  <Check className="h-3 w-3" /> 密码一致
                </p>
              )}
            </div>

            {/* 同意条款 */}
            <ConsentCheckbox id="consent-register" checked={agreed} onCheckedChange={setAgreed} />

            <Button
              className="w-full"
              onClick={handleRegister}
              disabled={loading || !canRegister}
            >
              {loading ? (
                <Loader2 className="h-4 w-4 mr-2 animate-spin" />
              ) : (
                <UserPlus className="h-4 w-4 mr-2" />
              )}
              {guestToken ? "注册并保留数据" : "注册"}
            </Button>

            {/* 已有账号 → 去登录 */}
            <p className="text-center text-xs text-muted-foreground">
              已有账号？
              <button
                type="button"
                className="ml-1 text-foreground underline underline-offset-2 hover:text-primary transition-ui"
                onClick={() => handleTabChange("login")}
              >
                去登录
              </button>
            </p>
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}

// ─── 同意条款勾选项（登录/注册共用） ─────────────────────────

function ConsentCheckbox({
  checked,
  onCheckedChange,
  id,
}: {
  checked: boolean;
  onCheckedChange: (v: boolean) => void;
  id: string;
}) {
  return (
    <div className="flex items-start gap-2 py-0.5">
      <Checkbox
        id={id}
        checked={checked}
        onCheckedChange={(v) => onCheckedChange(v === true)}
        className="mt-0.5"
      />
      <label
        htmlFor={id}
        className="text-xs text-muted-foreground leading-relaxed cursor-pointer select-none"
      >
        我已阅读并同意
        <a
          href="/terms"
          target="_blank"
          rel="noopener noreferrer"
          className="text-foreground underline underline-offset-2 hover:text-primary transition-ui"
          onClick={(e) => e.stopPropagation()}
        >
          《使用条款》
        </a>
        和
        <a
          href="/privacy"
          target="_blank"
          rel="noopener noreferrer"
          className="text-foreground underline underline-offset-2 hover:text-primary transition-ui"
          onClick={(e) => e.stopPropagation()}
        >
          《隐私政策》
        </a>
      </label>
    </div>
  );
}
