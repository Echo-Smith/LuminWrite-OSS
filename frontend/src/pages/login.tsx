/**
 * 登录页面 — 支持 Passkey / API Key / 账号密码 / 访客 四种模式
 */
import { useState, useEffect } from "react";
import { useNavigate, useLocation } from "react-router-dom";
import { Loader2, KeyRound, User, ArrowRight, Fingerprint, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { authStore } from "@/stores/auth-store";
import {
  isWebAuthnSupported,
  isPlatformAuthenticatorAvailable,
  loginWithPasskey,
  registerPasskey,
  getPasskeyErrorMessage,
} from "@/lib/passkey";
import { useAuthStore } from "@/stores/auth-store";
import { FadeIn } from "@/components/animation";

export function LoginPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  // Passkey 支持检测
  const [passkeySupported, setPasskeySupported] = useState(false);
  const [platformAuthAvailable, setPlatformAuthAvailable] = useState(false);

  // API Key 模式
  const [apiKey, setApiKey] = useState("");

  // 密码模式
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  // Guest 模式 — 不需要输入，直接创建访客
  // (原来的 user_id 模式已被移除，访客必须走 /auth/guest 端点创建真实记录)

  // Passkey 注册模式
  const [passkeyName, setPasskeyName] = useState("");
  const [passkeyRegUserId, setPasskeyRegUserId] = useState("");

  // 认证状态（用于判断是否已登录，已登录才能注册 passkey）
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated());

  const redirectTo = (location.state as { from?: string })?.from || "/write";

  // 检测浏览器 Passkey 支持
  useEffect(() => {
    setPasskeySupported(isWebAuthnSupported());
    isPlatformAuthenticatorAvailable().then(setPlatformAuthAvailable);
  }, []);

  const handleAPIKeyLogin = async () => {
    if (!apiKey.trim()) return;
    setLoading(true);
    setError("");
    const result = await authStore.login({ api_key: apiKey.trim() });
    if (result.ok) {
      navigate(redirectTo, { replace: true });
    } else {
      setError(result.message || "API Key 无效，请检查后重试");
    }
    setLoading(false);
  };

  const handlePasswordLogin = async () => {
    if (!username.trim() || !password.trim()) return;
    setLoading(true);
    setError("");
    const result = await authStore.login({
      username: username.trim(),
      password: password.trim(),
    });
    if (result.ok) {
      navigate(redirectTo, { replace: true });
    } else {
      setError(result.message || "用户名或密码错误");
    }
    setLoading(false);
  };

  const handleGuestLogin = async () => {
    setLoading(true);
    setError("");
    const result = await authStore.guestLogin();
    if (result.ok) {
      navigate(redirectTo, { replace: true });
    } else {
      setError(result.message || "访客登录失败，请确认后端服务正在运行");
    }
    setLoading(false);
  };

  const handlePasskeyLogin = async () => {
    setLoading(true);
    setError("");
    try {
      const result = await loginWithPasskey();
      // 使用 passkey 返回的 token 登录
      useAuthStore.getState().login(
        result.token,
        result.user_id,
        result.username || "",
        result.role,
        result.expires_in
      );
      navigate(redirectTo, { replace: true });
    } catch (e) {
      setError(getPasskeyErrorMessage(e));
    }
    setLoading(false);
  };

  const handlePasskeyRegister = async () => {
    setLoading(true);
    setError("");
    try {
      const result = await registerPasskey({
        name: passkeyName.trim() || undefined,
        userId: passkeyRegUserId.trim() || undefined,
        userName: passkeyRegUserId.trim() || "admin",
      });
      setError("");
      alert(result.message || "Passkey 注册成功！现在可以使用 Passkey 登录了。");
    } catch (e) {
      setError(getPasskeyErrorMessage(e));
    }
    setLoading(false);
  };

  // 决定默认 Tab
  const defaultTab = passkeySupported && platformAuthAvailable ? "passkey" : "apikey";

  return (
    <div className="relative flex h-screen items-center justify-center overflow-hidden">
      {/* 背景层 */}
      <div className="absolute inset-0 bg-dot-pattern opacity-50" />
      <div className="absolute inset-0 bg-gradient-to-br from-primary/5 via-transparent to-primary/5" />
      <div className="absolute top-0 left-1/2 -translate-x-1/2 w-[600px] h-[400px] bg-primary/10 rounded-full blur-[120px] opacity-30" />

      <FadeIn direction="scale" className="relative z-10">
      <Card className="w-[420px] shadow-xl border-border/60">
        <CardHeader className="space-y-3 text-center">
          <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-xl bg-brand-gradient shadow-md">
            <span className="text-lg font-bold text-white">笔</span>
          </div>
          <CardTitle className="text-xl tracking-tight">笔润智谈 · 登录</CardTitle>
          <p className="text-sm text-muted-foreground">选择适合你的登录方式</p>
        </CardHeader>
        <CardContent>
          {error && (
            <div className="mb-4 rounded-lg bg-destructive/5 border border-destructive/20 px-3 py-2 text-sm text-destructive anim-shake">
              {error}
            </div>
          )}

          <Tabs defaultValue={defaultTab} className="w-full">
            <TabsList className="grid w-full grid-cols-4">
              <TabsTrigger value="passkey" className="text-xs" disabled={!passkeySupported}>
                Passkey
              </TabsTrigger>
              <TabsTrigger value="apikey" className="text-xs">API Key</TabsTrigger>
              <TabsTrigger value="password" className="text-xs">账号密码</TabsTrigger>
              <TabsTrigger value="guest" className="text-xs">访客</TabsTrigger>
            </TabsList>

            {/* Passkey 登录 */}
            <TabsContent value="passkey" className="space-y-3 pt-4">
              {!passkeySupported ? (
                <div className="rounded-lg bg-amber-50 dark:bg-amber-950/20 px-3 py-3 text-sm text-amber-700 dark:text-amber-400">
                  当前浏览器不支持 Passkey / WebAuthn。
                  请使用 Chrome 67+、Safari 14+、Edge 87+ 或 Firefox 122+。
                </div>
              ) : (
                <>
                  <div className="rounded-lg bg-primary/5 px-3 py-2 text-xs text-primary">
                    <Fingerprint className="inline h-3.5 w-3.5 mr-1" />
                    {platformAuthAvailable
                      ? "检测到设备支持生物识别，点击下方按钮使用 Touch ID / Face ID / Windows Hello 登录。"
                      : "将弹窗引导你选择安全密钥或设备认证器。"}
                  </div>
                  <Button
                    className="w-full"
                    onClick={handlePasskeyLogin}
                    disabled={loading}
                  >
                    {loading ? (
                      <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                    ) : (
                      <Fingerprint className="h-4 w-4 mr-2" />
                    )}
                    使用 Passkey 登录
                  </Button>

                  <div className="border-t pt-3">
                    <Label className="text-xs text-muted-foreground">
                      首次使用？先注册 Passkey
                    </Label>
                    <div className="mt-1.5 space-y-2">
                      <Input
                        className="text-sm"
                        placeholder="用户标识（如 admin）"
                        value={passkeyRegUserId}
                        onChange={(e) => setPasskeyRegUserId(e.target.value)}
                      />
                      <Input
                        className="text-sm"
                        placeholder="设备名称（如 MacBook Touch ID，可选）"
                        value={passkeyName}
                        onChange={(e) => setPasskeyName(e.target.value)}
                      />
                      <Button
                        variant="outline"
                        className="w-full"
                        onClick={handlePasskeyRegister}
                        disabled={loading || !passkeyRegUserId.trim()}
                      >
                        {loading ? (
                          <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                        ) : (
                          <Plus className="h-4 w-4 mr-2" />
                        )}
                        注册新 Passkey
                      </Button>
                    </div>
                  </div>
                </>
              )}
            </TabsContent>

            {/* API Key */}
            <TabsContent value="apikey" className="space-y-3 pt-4">
              <div>
                <Label className="flex items-center gap-1.5">
                  <KeyRound className="h-3.5 w-3.5" /> API Key
                </Label>
                <Input
                  className="mt-1.5"
                  type="password"
                  placeholder="输入你的 API Key"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && handleAPIKeyLogin()}
                />
              </div>
              <Button
                className="w-full"
                onClick={handleAPIKeyLogin}
                disabled={loading || !apiKey.trim()}
              >
                {loading ? (
                  <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                ) : (
                  <ArrowRight className="h-4 w-4 mr-2" />
                )}
                登录
              </Button>
            </TabsContent>

            {/* 密码 */}
            <TabsContent value="password" className="space-y-3 pt-4">
              <div>
                <Label className="flex items-center gap-1.5">
                  <User className="h-3.5 w-3.5" /> 用户名
                </Label>
                <Input
                  className="mt-1.5"
                  placeholder="admin"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
              </div>
              <div>
                <Label>密码</Label>
                <Input
                  className="mt-1.5"
                  type="password"
                  placeholder="••••••••"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && handlePasswordLogin()}
                />
              </div>
              <p className="text-xs text-muted-foreground">
                默认管理员：用户名 <code className="rounded bg-muted px-1">admin</code>，
                密码为 <code className="rounded bg-muted px-1">ADMIN_TOKEN</code> 环境变量的值。
              </p>
              <Button
                className="w-full"
                onClick={handlePasswordLogin}
                disabled={loading || !username.trim() || !password.trim()}
              >
                {loading ? (
                  <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                ) : (
                  <ArrowRight className="h-4 w-4 mr-2" />
                )}
                登录
              </Button>
            </TabsContent>

            {/* 访客 */}
            <TabsContent value="guest" className="space-y-3 pt-4">
              <p className="text-xs text-muted-foreground">
                访客模式可使用写作功能，但无法管理后台。点击下方按钮将自动创建访客账号，登录后 token 保存在本地。
              </p>
              <Button
                variant="outline"
                className="w-full"
                onClick={handleGuestLogin}
                disabled={loading}
              >
                {loading ? (
                  <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                ) : (
                  <ArrowRight className="h-4 w-4 mr-2" />
                )}
                以访客身份继续
              </Button>
            </TabsContent>
          </Tabs>
        </CardContent>
      </Card>
      </FadeIn>
    </div>
  );
}
