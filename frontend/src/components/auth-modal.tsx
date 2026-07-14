/**
 * AuthModal — 登录/注册弹窗（替代原登录页）
 *
 * 支持：
 *  - 账号密码登录
 *  - 注册（游客升级或新注册）
 *  - API Key 登录
 *  - Passkey 登录
 *
 * 由 AuthProvider 统一管理开关，其他组件通过 useAuthModal 调用。
 */
import { useState, useEffect } from "react";
import { Loader2, KeyRound, User, ArrowRight, Fingerprint, UserPlus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { authStore, useAuthStore } from "@/stores/auth-store";
import {
  isWebAuthnSupported,
  isPlatformAuthenticatorAvailable,
  loginWithPasskey,
} from "@/lib/passkey";
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
  defaultTab?: "login" | "register" | "apikey" | "passkey";
}

export function AuthModal({ open, onOpenChange, guestToken, defaultTab = "login" }: AuthModalProps) {
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

  // 注册模式
  const [regUsername, setRegUsername] = useState("");
  const [regPassword, setRegPassword] = useState("");

  useEffect(() => {
    setPasskeySupported(isWebAuthnSupported());
    isPlatformAuthenticatorAvailable().then(setPlatformAuthAvailable);
  }, []);

  // 重置错误当弹窗关闭
  useEffect(() => {
    if (!open) setError("");
  }, [open]);

  const handlePasswordLogin = async () => {
    if (!username.trim() || !password.trim()) return;
    setLoading(true);
    setError("");
    const ok = await authStore.login({
      username: username.trim(),
      password: password.trim(),
    });
    if (ok) {
      onOpenChange(false);
    } else {
      setError("用户名或密码错误");
    }
    setLoading(false);
  };

  const handleRegister = async () => {
    if (!regUsername.trim() || !regPassword.trim()) return;
    setLoading(true);
    setError("");

    // If we have a guest token, upgrade the guest account
    const body: Record<string, unknown> = {
      username: regUsername.trim(),
      password: regPassword.trim(),
    };
    if (guestToken) {
      body.guest_token = guestToken;
    }

    const ok = await authStore.register(body);
    if (ok) {
      onOpenChange(false);
    } else {
      setError(guestToken ? "注册失败，请重试" : "用户名已存在或注册失败");
    }
    setLoading(false);
  };

  const handleAPIKeyLogin = async () => {
    if (!apiKey.trim()) return;
    setLoading(true);
    setError("");
    const ok = await authStore.login({ api_key: apiKey.trim() });
    if (ok) {
      onOpenChange(false);
    } else {
      setError("API Key 无效");
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
        result.role,
        result.expires_in
      );
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Passkey 登录失败");
    }
    setLoading(false);
  };

  // If guest token is provided, default to register tab
  const effectiveDefaultTab = guestToken ? "register" : defaultTab;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[420px]">
        <DialogHeader>
          <DialogTitle className="text-center text-xl">
            {guestToken ? "注册账号" : "笔润智谈 · 登录"}
          </DialogTitle>
        </DialogHeader>

        {guestToken && (
          <div className="rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-700">
            游客模式仅支持 1 次完整写作，注册后可无限使用且数据自动保留。
          </div>
        )}

        {error && (
          <div className="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600">
            {error}
          </div>
        )}

        <Tabs defaultValue={effectiveDefaultTab} className="w-full">
          <TabsList className={`grid w-full ${passkeySupported ? "grid-cols-4" : "grid-cols-3"}`}>
            {passkeySupported && (
              <TabsTrigger value="passkey" className="text-xs">Passkey</TabsTrigger>
            )}
            <TabsTrigger value="login" className="text-xs">登录</TabsTrigger>
            <TabsTrigger value="register" className="text-xs">注册</TabsTrigger>
            <TabsTrigger value="apikey" className="text-xs">API Key</TabsTrigger>
          </TabsList>

          {/* Passkey 登录 */}
          {passkeySupported && (
            <TabsContent value="passkey" className="space-y-3 pt-4">
              <div className="rounded-lg bg-blue-50 px-3 py-2 text-xs text-blue-600">
                <Fingerprint className="inline h-3.5 w-3.5 mr-1" />
                {platformAuthAvailable
                  ? "点击下方按钮使用 Touch ID / Face ID / Windows Hello 登录。"
                  : "将弹窗引导你选择安全密钥。"}
              </div>
              <Button className="w-full" onClick={handlePasskeyLogin} disabled={loading}>
                {loading ? (
                  <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                ) : (
                  <Fingerprint className="h-4 w-4 mr-2" />
                )}
                使用 Passkey 登录
              </Button>
            </TabsContent>
          )}

          {/* 账号密码登录 */}
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

          {/* 注册 */}
          <TabsContent value="register" className="space-y-3 pt-4">
            {guestToken && (
              <p className="text-xs text-muted-foreground">
                注册后将自动保留你的写作记录和反馈数据。
              </p>
            )}
            <div>
              <Label className="flex items-center gap-1.5">
                <UserPlus className="h-3.5 w-3.5" /> 用户名
              </Label>
              <Input
                className="mt-1.5"
                placeholder="2-64 个字符"
                value={regUsername}
                onChange={(e) => setRegUsername(e.target.value)}
              />
            </div>
            <div>
              <Label>密码</Label>
              <Input
                className="mt-1.5"
                type="password"
                placeholder="至少 6 位"
                value={regPassword}
                onChange={(e) => setRegPassword(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && handleRegister()}
              />
            </div>
            <Button
              className="w-full"
              onClick={handleRegister}
              disabled={loading || !regUsername.trim() || !regPassword.trim()}
            >
              {loading ? (
                <Loader2 className="h-4 w-4 mr-2 animate-spin" />
              ) : (
                <UserPlus className="h-4 w-4 mr-2" />
              )}
              {guestToken ? "注册并保留数据" : "注册"}
            </Button>
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
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}
