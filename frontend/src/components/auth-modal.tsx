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
import { Loader2, User, ArrowRight, Fingerprint, UserPlus, Sparkles, Check, AlertCircle, Mail, KeyRound } from "lucide-react";
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

  // 邮箱验证码注册（可选）
  const [regEmail, setRegEmail] = useState("");
  const [regEmailCode, setRegEmailCode] = useState("");
  const [codeSending, setCodeSending] = useState(false);
  const [codeCountdown, setCodeCountdown] = useState(0);

  // 忘记密码模式
  const [showForgotPassword, setShowForgotPassword] = useState(false);
  const [fpEmail, setFpEmail] = useState("");
  const [fpCode, setFpCode] = useState("");
  const [fpNewPassword, setFpNewPassword] = useState("");
  const [fpStep, setFpStep] = useState<1 | 2>(1); // 1=发送验证码, 2=重置密码
  const [fpCountdown, setFpCountdown] = useState(0);

  // 注册字段触碰标记（用于延迟显示校验）
  const [touched, setTouched] = useState({
    username: false,
    password: false,
    confirm: false,
    email: false,
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
    setRegEmail("");
    setRegEmailCode("");
    setCodeCountdown(0);
    setShowForgotPassword(false);
    setFpEmail("");
    setFpCode("");
    setFpNewPassword("");
    setFpStep(1);
    setFpCountdown(0);
    setAgreed(false);
    setTouched({ username: false, password: false, confirm: false, email: false });
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

  // 邮箱校验（可选填，但填了就必须填验证码）
  const regEmailValid = !regEmail || /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(regEmail.trim());
  const regEmailCodeValid = !regEmail || regEmailCode.trim().length >= 4;
  const canRegister = regUsernameValid && regPasswordValid && regConfirmValid && agreed && regEmailValid && regEmailCodeValid;

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
    // 邮箱验证码可选填
    if (regEmail.trim()) {
      body.email = regEmail.trim();
      body.email_code = regEmailCode.trim();
    }

    const result = await authStore.register(body);
    if (result.ok) {
      onOpenChange(false);
    } else {
      // 根据后端错误码做精准定位
      if (result.code === "username_taken" || result.code === "bad_request") {
        setError(result.message);
        setTouched((t) => ({ ...t, username: true }));
      } else if (result.code === "email_taken") {
        setError(result.message);
        setTouched((t) => ({ ...t, email: true }));
      } else if (result.code === "invalid_code") {
        setError(result.message || "验证码不正确或已过期");
      } else {
        setError(result.message || "注册失败，请重试");
      }
    }
    setLoading(false);
  };

  // ─── 发送验证码（注册时） ──────────────────────────────────
  const handleSendCode = async () => {
    if (!regEmail.trim() || !regEmailValid) {
      setTouched((t) => ({ ...t, email: true }));
      return;
    }
    setCodeSending(true);
    setError("");
    try {
      const res = await fetch("/api/v2/auth/send-code", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: regEmail.trim(), purpose: "register" }),
      });
      const json = await res.json();
      if (json.success) {
        setCodeCountdown(60);
      } else {
        const code = json.error?.code || "send_failed";
        const msg = code === "email_taken" ? "该邮箱已被注册" :
          code === "rate_limited" ? "请等待 60 秒后再试" :
          code === "email_disabled" ? "邮箱验证功能未开启" :
          json.error?.message || "验证码发送失败";
        setError(msg);
      }
    } catch {
      setError("网络错误，请检查连接");
    } finally {
      setCodeSending(false);
    }
  };

  // ─── 倒计时效果 ──────────────────────────────────────────
  useEffect(() => {
    if (codeCountdown > 0) {
      const timer = setTimeout(() => setCodeCountdown(codeCountdown - 1), 1000);
      return () => clearTimeout(timer);
    }
  }, [codeCountdown]);

  useEffect(() => {
    if (fpCountdown > 0) {
      const timer = setTimeout(() => setFpCountdown(fpCountdown - 1), 1000);
      return () => clearTimeout(timer);
    }
  }, [fpCountdown]);

  // ─── 忘记密码：发送验证码 ────────────────────────────────
  const handleForgotSendCode = async () => {
    if (!fpEmail.trim()) return;
    setCodeSending(true);
    setError("");
    try {
      const res = await fetch("/api/v2/auth/forgot-password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: fpEmail.trim() }),
      });
      const json = await res.json();
      if (json.success) {
        setFpCountdown(60);
        setFpStep(2);
      } else {
        const code = json.error?.code || "send_failed";
        const msg = code === "rate_limited" ? "请等待 60 秒后再试" :
          code === "email_disabled" ? "邮箱验证功能未开启" :
          json.error?.message || "验证码发送失败";
        setError(msg);
      }
    } catch {
      setError("网络错误，请检查连接");
    } finally {
      setCodeSending(false);
    }
  };

  // ─── 忘记密码：重置密码 ──────────────────────────────────
  const handleResetPassword = async () => {
    if (!fpEmail.trim() || !fpCode.trim() || fpNewPassword.length < 6) return;
    setLoading(true);
    setError("");
    try {
      const res = await fetch("/api/v2/auth/reset-password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          email: fpEmail.trim(),
          code: fpCode.trim(),
          new_password: fpNewPassword,
        }),
      });
      const json = await res.json();
      if (json.success) {
        setShowForgotPassword(false);
        setActiveTab("login");
        setError("");
        // 切回登录并提示
        setUsername("");
        setPassword("");
      } else {
        const code = json.error?.code || "reset_failed";
        const msg = code === "invalid_code" ? "验证码不正确或已过期" :
          code === "not_found" ? "邮箱未注册" :
          code === "weak_password" ? "密码至少 6 位" :
          json.error?.message || "重置失败";
        setError(msg);
      }
    } catch {
      setError("网络错误，请检查连接");
    } finally {
      setLoading(false);
    }
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

            {/* 忘记密码入口 */}
            <p className="text-center text-xs text-muted-foreground">
              <button
                type="button"
                className="text-foreground underline underline-offset-2 hover:text-primary transition-ui"
                onClick={() => { setShowForgotPassword(true); setError(""); }}
              >
                忘记密码？
              </button>
            </p>
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

            {/* 邮箱验证码（可选） */}
            <div className="space-y-2 pt-2 border-t">
              <div className="flex items-center gap-1.5">
                <Mail className="h-3.5 w-3.5 text-muted-foreground" />
                <span className="text-xs text-muted-foreground">邮箱绑定（可选，用于找回密码）</span>
              </div>
              <Input
                className="text-sm"
                placeholder="your@email.com"
                value={regEmail}
                onChange={(e) => setRegEmail(e.target.value)}
                onBlur={() => setTouched((t) => ({ ...t, email: true }))}
                type="email"
              />
              {touched.email && regEmail && !regEmailValid && (
                <p className="text-xs text-red-500">邮箱格式不正确</p>
              )}
              {regEmail.trim() && regEmailValid && (
                <div className="flex gap-2">
                  <Input
                    className="text-sm flex-1"
                    placeholder="验证码"
                    value={regEmailCode}
                    onChange={(e) => setRegEmailCode(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && handleRegister()}
                  />
                  <Button
                    variant="outline"
                    size="sm"
                    className="shrink-0"
                    onClick={handleSendCode}
                    disabled={codeSending || codeCountdown > 0}
                  >
                    {codeSending ? (
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : codeCountdown > 0 ? (
                      `${codeCountdown}s`
                    ) : (
                      "发送验证码"
                    )}
                  </Button>
                </div>
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

        {/* ─── 忘记密码弹窗 ──────────────────────────────────── */}
        {showForgotPassword && (
          <div className="fixed inset-0 z-[60] flex items-center justify-center" onClick={() => setShowForgotPassword(false)}>
            <div className="absolute inset-0 bg-black/40 backdrop-blur-[2px]" />
            <div
              className="relative z-10 w-[92vw] max-w-[400px] rounded-xl border bg-background shadow-lg p-6 animate-in fade-in-0 zoom-in-95 duration-150"
              onClick={(e) => e.stopPropagation()}
            >
              <div className="flex items-center justify-between mb-4">
                <h3 className="text-base font-semibold flex items-center gap-1.5">
                  <KeyRound className="h-4 w-4" /> 忘记密码
                </h3>
                <button
                  onClick={() => setShowForgotPassword(false)}
                  className="text-muted-foreground hover:text-foreground"
                >
                  ✕
                </button>
              </div>

              {fpStep === 1 ? (
                <div className="space-y-3">
                  <p className="text-xs text-muted-foreground">
                    输入注册邮箱，我们将发送验证码到该邮箱。
                  </p>
                  <Input
                    type="email"
                    placeholder="your@email.com"
                    value={fpEmail}
                    onChange={(e) => setFpEmail(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && handleForgotSendCode()}
                  />
                  <Button
                    className="w-full"
                    onClick={handleForgotSendCode}
                    disabled={codeSending || !fpEmail.trim()}
                  >
                    {codeSending ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Mail className="h-4 w-4 mr-2" />}
                    发送验证码
                  </Button>
                </div>
              ) : (
                <div className="space-y-3">
                  <p className="text-xs text-green-600 flex items-center gap-1">
                    <Check className="h-3.5 w-3.5" /> 验证码已发送至 {fpEmail}
                  </p>
                  <Input
                    placeholder="验证码"
                    value={fpCode}
                    onChange={(e) => setFpCode(e.target.value)}
                  />
                  <Input
                    type="password"
                    placeholder="新密码（至少 6 位）"
                    value={fpNewPassword}
                    onChange={(e) => setFpNewPassword(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && handleResetPassword()}
                  />
                  <Button
                    className="w-full"
                    onClick={handleResetPassword}
                    disabled={loading || !fpCode.trim() || fpNewPassword.length < 6}
                  >
                    {loading ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <KeyRound className="h-4 w-4 mr-2" />}
                    重置密码
                  </Button>
                  <button
                    type="button"
                    className="w-full text-xs text-muted-foreground hover:text-foreground"
                    onClick={() => { setFpStep(1); setFpCode(""); setFpNewPassword(""); }}
                  >
                    重新发送验证码 {fpCountdown > 0 && `(${fpCountdown}s)`}
                  </button>
                </div>
              )}
            </div>
          </div>
        )}
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
