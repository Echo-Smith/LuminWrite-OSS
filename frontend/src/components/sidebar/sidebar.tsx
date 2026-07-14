/**
 * 左侧栏 — 会话列表 + 选题入口 + 用户区
 *
 * 游客显示「你好，游客」+ 注册按钮
 * 正式用户显示用户名 + 退出按钮
 */
import { Plus, MessageSquare, Trash2, Compass, Settings, LogOut, UserPlus, Brain } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { useAgentStore } from "@/stores/agent-store";
import { useAuthStore } from "@/stores/auth-store";
import { useAuthModal } from "@/stores/auth-modal-store";
import { useNavigate } from "react-router-dom";
import { cn } from "@/lib/utils";

export function Sidebar() {
  const navigate = useNavigate();
  const sessions = useAgentStore((s) => s.sessions);
  const activeSessionId = useAgentStore((s) => s.activeSessionId);
  const createSession = useAgentStore((s) => s.createSession);
  const switchSession = useAgentStore((s) => s.switchSession);
  const deleteSession = useAgentStore((s) => s.deleteSession);

  // 认证状态
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const openAuth = useAuthModal((s) => s.openAuth);

  const isGuest = user?.role === "guest";
  const isAdmin = user?.role === "admin";

  const handleRegister = () => {
    const token = useAuthStore.getState().token;
    openAuth({
      guestToken: token ?? undefined,
      defaultTab: "register",
    });
  };

  return (
    <div className="flex h-full w-64 flex-col border-r bg-muted/30">
      {/* 顶部品牌区 */}
      <div className="flex items-center gap-2 px-4 py-3">
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary">
          <span className="text-sm font-bold text-primary-foreground">笔</span>
        </div>
        <div className="flex-1 min-w-0">
          <h1 className="text-sm font-bold truncate">笔润智谈</h1>
          <p className="text-xs text-muted-foreground">V2 写作平台</p>
        </div>
      </div>

      <Separator />

      {/* 新建写作 */}
      <div className="p-3">
        <Button
          className="w-full justify-start gap-2"
          onClick={() => {
            createSession();
          }}
        >
          <Plus className="h-4 w-4" />
          新建写作
        </Button>
      </div>

      {/* 导航入口 */}
      <div className="px-3 pb-2 space-y-1">
        <Button
          variant="ghost"
          size="sm"
          className="w-full justify-start gap-2 text-muted-foreground"
          onClick={() => navigate("/topics")}
        >
          <Compass className="h-4 w-4" />
          选题中心
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="w-full justify-start gap-2 text-muted-foreground"
          onClick={() => navigate("/settings/memory")}
        >
          <Brain className="h-4 w-4" />
          记忆管理
        </Button>
        {isAdmin && (
          <Button
            variant="ghost"
            size="sm"
            className="w-full justify-start gap-2 text-muted-foreground"
            onClick={() => navigate("/admin")}
          >
            <Settings className="h-4 w-4" />
            管理后台
          </Button>
        )}
      </div>

      <Separator />

      {/* 会话列表 */}
      <ScrollArea className="flex-1">
        <div className="p-2 space-y-1">
          {sessions.length === 0 ? (
            <div className="px-2 py-8 text-center text-xs text-muted-foreground">
              暂无写作记录
            </div>
          ) : (
            sessions.map((session) => (
              <div
                key={session.id}
                onClick={() => switchSession(session.id)}
                className={cn(
                  "group flex items-center gap-2 rounded-lg px-3 py-2 cursor-pointer transition-colors",
                  session.id === activeSessionId
                    ? "bg-accent"
                    : "hover:bg-accent/50"
                )}
              >
                <MessageSquare className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                <span className="flex-1 truncate text-sm">{session.title}</span>
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    deleteSession(session.id);
                  }}
                  className="opacity-0 group-hover:opacity-100 transition-opacity text-muted-foreground hover:text-destructive"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
            ))
          )}
        </div>
      </ScrollArea>

      <Separator />

      {/* 底部用户区 */}
      <div className="flex items-center gap-2 p-3">
        <Avatar className="h-8 w-8">
          <AvatarFallback className={cn(
            "text-xs",
            isGuest ? "bg-amber-100 text-amber-700" : "bg-muted"
          )}>
            {isGuest ? "客" : (user?.userId?.slice(0, 2).toUpperCase() ?? "?")}
          </AvatarFallback>
        </Avatar>
        <div className="flex-1 min-w-0">
          {isGuest ? (
            <>
              <p className="text-sm font-medium truncate">你好，游客</p>
              <p className="text-xs text-amber-600">试用模式 · 限 1 次写作</p>
            </>
          ) : (
            <>
              <p className="text-sm font-medium truncate">{user?.userId ?? "用户"}</p>
              <p className="text-xs text-muted-foreground">
                {isAdmin ? "管理员" : "已登录"}
              </p>
            </>
          )}
        </div>

        {isGuest ? (
          <button
            onClick={handleRegister}
            className="text-primary hover:text-primary/80 transition-colors"
            title="注册账号"
          >
            <UserPlus className="h-4 w-4" />
          </button>
        ) : (
          user && (
            <button
              onClick={() => {
                logout();
              }}
              className="text-muted-foreground hover:text-destructive transition-colors"
              title="退出登录"
            >
              <LogOut className="h-4 w-4" />
            </button>
          )
        )}
      </div>
    </div>
  );
}
