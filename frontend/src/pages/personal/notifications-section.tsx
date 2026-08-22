/**
 * 通知设置子页面
 */
import { useState, useEffect } from "react";
import {
  Bell, BellOff, Send, Loader2, AlertCircle, Check,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";
import { SimpleModal, SimpleModalFooter } from "./shared";

export function NotificationsSection() {
  const user = useAuthStore((s) => s.user);
  const token = useAuthStore((s) => s.token);
  const isGuest = user?.role === "guest";

  const [testing, setTesting] = useState(false);
  const [lastResult, setLastResult] = useState<{ type: "success" | "error"; text: string } | null>(null);

  const handleTestNotification = async () => {
    setTesting(true);
    setLastResult(null);
    try {
      const res = await fetch("/api/v2/sse/test-notify", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({
          title: "测试通知",
          body: "如果你能看到这条消息，说明 SSE 在线通知工作正常！",
        }),
      });
      const json = await res.json();
      if (json.success) {
        setLastResult({ type: "success", text: "测试通知已发送，请留意页面内的通知提示" });
      } else {
        setLastResult({ type: "error", text: json.error?.message ?? "发送失败" });
      }
    } catch {
      setLastResult({ type: "error", text: "网络错误，请检查连接" });
    } finally {
      setTesting(false);
    }
  };

  if (isGuest) {
    return (
      <div className="px-6 pt-6 pb-12 space-y-6">
        <Card className="border-amber-200/60 bg-amber-50/50 dark:bg-amber-950/20">
          <CardContent className="py-6 text-center">
            <BellOff className="mx-auto h-10 w-10 text-amber-500/50" />
            <p className="mt-3 text-sm text-amber-900 dark:text-amber-200 font-medium">
              游客模式无法使用在线通知
            </p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
              注册账号后可开启写作完成实时提醒
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="px-6 pt-6 pb-12 space-y-6">
      {/* SSE 状态展示 */}
      <div className="flex items-center justify-between py-3 px-4 rounded-lg bg-muted/30">
        <div className="flex items-center gap-2.5">
          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-green-100 dark:bg-green-950/30">
            <Bell className="h-4 w-4 text-green-600" />
          </div>
          <div>
            <p className="text-sm font-medium">在线通知已启用</p>
            <p className="text-xs text-muted-foreground">写作完成时会在页面内实时弹出提醒</p>
          </div>
        </div>
        <Badge variant="default">SSE</Badge>
      </div>

      {/* 测试结果 */}
      {lastResult && (
        <div className={cn(
          "flex items-center gap-2 text-xs rounded-lg px-3 py-2",
          lastResult.type === "success"
            ? "bg-green-50 dark:bg-green-950/20 text-green-700 dark:text-green-400"
            : "bg-red-50 dark:bg-red-950/20 text-red-600 dark:text-red-400"
        )}>
          {lastResult.type === "success" ? <Check className="h-3.5 w-3.5 shrink-0" /> : <AlertCircle className="h-3.5 w-3.5 shrink-0" />}
          {lastResult.text}
        </div>
      )}

      {/* 测试按钮 */}
      <div className="flex gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={handleTestNotification}
          disabled={testing}
        >
          {testing ? (
            <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />
          ) : (
            <Send className="h-3.5 w-3.5 mr-1.5" />
          )}
          发送测试通知
        </Button>
      </div>

      {/* 说明 */}
      <div className="text-xs text-muted-foreground space-y-1.5 pt-2">
        <p>• 在线通知通过 SSE（Server-Sent Events）实时推送，无需第三方服务</p>
        <p>• 写作完成后，页面内会立即弹出通知提醒</p>
        <p>• 请保持页面打开以接收通知；关闭页面后通知将在下次打开时显示</p>
        <p>• 通知内容包括写作完成提醒、管理员广播消息等</p>
      </div>
    </div>
  );
}
