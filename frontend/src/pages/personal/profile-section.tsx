/**
 * 个人信息子页面
 */
import { useState } from "react";
import {
  User, Pencil, AlertCircle, Check, Loader2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent } from "@/components/ui/card";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";

export function ProfileSection() {
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
    // 如果值没有变化，直接退出编辑
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
    <div className="px-6 pt-6 pb-12 space-y-6">
      <div className="space-y-4">
        {/* 用户名 — 点击进入编辑模式 */}
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
                    // 点击空白区域时，如果有有效输入则保存，否则取消
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
            className={cn(
              "flex items-center justify-between rounded-md -mx-1 px-1 py-0.5 transition-ui",
              !isGuest && "cursor-pointer hover:bg-accent/50"
            )}
            onClick={() => {
              if (!isGuest) {
                setEditingName(true);
                setNewName(user?.username ?? "");
                setNameError(null);
              }
            }}
          >
            <span className="text-sm text-muted-foreground">用户名</span>
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium">{user?.username ?? "-"}</span>
              {!isGuest && (
                <Pencil className="h-3 w-3 text-muted-foreground/50" />
              )}
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
