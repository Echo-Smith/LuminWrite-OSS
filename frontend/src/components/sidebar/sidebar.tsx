/**
 * 左侧栏 — 会话列表 + 选题入口 + 用户区
 *
 * 支持折叠/展开（收起为窄图标条）
 * 底部用户区点击弹出 Popover：个人中心 / 管理后台
 */
import { useState } from "react";
import {
  Plus, Trash2, Compass,
  Settings, Sun, Moon, LogOut, UserPlus,
  PanelLeftClose, PanelLeftOpen,
  ChevronRight, User, AlertTriangle, Newspaper,
} from "lucide-react";
import { BrandIcon } from "@/components/brand-icon";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription,
} from "@/components/ui/dialog";
import { useAgentStore } from "@/stores/agent-store";
import { useAuthStore } from "@/stores/auth-store";
import { useAuthModal } from "@/stores/auth-modal-store";
import { useTheme } from "@/hooks/use-theme";
import { useNavigate } from "react-router-dom";
import { cn } from "@/lib/utils";
import { StaggerItem } from "@/components/animation";

interface SidebarProps {
  collapsed: boolean;
  onToggle: () => void;
}

export function Sidebar({ collapsed, onToggle }: SidebarProps) {
  const navigate = useNavigate();
  const sessions = useAgentStore((s) => s.sessions);
  const activeSessionId = useAgentStore((s) => s.activeSessionId);
  const createSession = useAgentStore((s) => s.createSession);
  const switchSession = useAgentStore((s) => s.switchSession);
  const deleteSession = useAgentStore((s) => s.deleteSession);

  // 删除确认状态
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; title: string } | null>(null);
  const [deleting, setDeleting] = useState(false);

  const handleConfirmDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    // 调用后端 soft delete API
    try {
      await fetch(`/api/v2/sessions/${deleteTarget.id}`, { method: "DELETE" });
    } catch {
      // 即使 API 失败也本地删除
    }
    deleteSession(deleteTarget.id);
    setDeleting(false);
    setDeleteTarget(null);
  };

  // 认证状态
  const user = useAuthStore((s) => s.user);
  const logout = useAuthStore((s) => s.logout);
  const openAuth = useAuthModal((s) => s.openAuth);

  // 主题
  const { theme, toggle: toggleTheme } = useTheme();

  const isGuest = user?.role === "guest";
  const isAdmin = useAuthStore((s) => s.hasAdminAccess());

  const handleRegister = () => {
    const token = useAuthStore.getState().token;
    openAuth({
      guestToken: token ?? undefined,
      defaultTab: "register",
    });
  };

  // ─── 折叠态 ──────────────────────────────────────────────
  if (collapsed) {
    return (
      <div className="flex h-full w-14 flex-col items-center border-r bg-surface py-3 gap-2 anim-slide-right">
        {/* 展开按钮 */}
        <button
          onClick={onToggle}
          className="flex h-9 w-9 items-center justify-center rounded-lg text-muted-foreground hover:bg-accent hover:text-foreground transition-ui"
          title="展开侧栏"
        >
          <PanelLeftOpen className="h-4 w-4" />
        </button>

        {/* 新建 */}
        <button
          onClick={() => createSession()}
          className="group flex h-9 w-9 items-center justify-center rounded-lg bg-primary text-primary-foreground hover:bg-primary/90 transition-transform-precise hover:scale-105 active:scale-95"
          title="新建写作"
        >
          <Plus className="h-4 w-4 transition-transform group-hover:rotate-90" />
        </button>

        <Separator className="w-8" />

        {/* 导航图标 */}
        <button
          onClick={() => navigate("/topics")}
          className="flex h-9 w-9 items-center justify-center rounded-lg text-muted-foreground hover:bg-accent hover:text-foreground transition-ui"
          title="选题中心"
        >
          <Compass className="h-4 w-4" />
        </button>

        <button
          onClick={() => navigate("/editorial")}
          className="flex h-9 w-9 items-center justify-center rounded-lg text-muted-foreground hover:bg-accent hover:text-foreground transition-ui"
          title="编辑部"
        >
          <Newspaper className="h-4 w-4" />
        </button>

        {/* 占位 */}
        <div className="flex-1" />

        {/* 底部用户头像 — 点击弹出面板 */}
        <Popover>
          <PopoverTrigger asChild>
            <button
              className="flex h-9 w-9 items-center justify-center transition-transform-precise hover:scale-105"
              title={isGuest ? "登录/注册账号" : (user?.username ?? user?.userId ?? "用户")}
            >
              <Avatar className="h-8 w-8">
                <AvatarFallback className={cn(
                  "text-xs font-medium",
                  isGuest ? "bg-amber-100 text-amber-700" : "bg-muted text-foreground"
                )}>
                  {isGuest ? "客" : (user?.username?.slice(0, 2).toUpperCase() ?? user?.userId?.slice(0, 2).toUpperCase() ?? "?")}
                </AvatarFallback>
              </Avatar>
            </button>
          </PopoverTrigger>
          <PopoverContent side="right" align="end" className="w-56">
            <UserMenuContent
              isGuest={isGuest}
              isAdmin={isAdmin}
              theme={theme}
              onToggleTheme={toggleTheme}
              onNavigate={(path) => navigate(path)}
              onLogout={() => {
                logout();
                // 重新初始化（自动创建游客 session），然后返回写作页
                useAuthStore.getState().init();
                navigate("/write", { replace: true });
              }}
              onRegister={handleRegister}
            />
          </PopoverContent>
        </Popover>
      </div>
    );
  }

  // ─── 展开态 ──────────────────────────────────────────────
  return (
    <div className="flex h-full w-64 flex-col border-r bg-surface anim-slide-right">
      {/* 顶部品牌区 + 折叠按钮 */}
      <div className="flex items-center gap-2.5 px-3 py-3.5">
        <BrandIcon size="md" showLabel subtitle="V2 · writing agent" />
        <button
          onClick={onToggle}
          className="flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground transition-ui shrink-0"
          title="收起侧栏"
        >
          <PanelLeftClose className="h-4 w-4" />
        </button>
      </div>

      {/* 新建写作 */}
      <div className="p-3">
        <Button
          className="w-full justify-start gap-2 group transition-transform-precise hover:scale-[1.02] active:scale-[0.98]"
          onClick={() => createSession()}
        >
          <Plus className="h-4 w-4 transition-transform group-hover:rotate-90" />
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
          选题与素材
        </Button>
      </div>

      <Separator />

      {/* 会话列表 */}
      <ScrollArea className="flex-1">
        <div className="p-3 space-y-1">
          {sessions.length === 0 ? (
            <div className="px-3 py-10 text-center">
              <p className="text-xs text-muted-foreground">暂无写作记录</p>
              <p className="text-[11px] text-muted-foreground/60 mt-1">点击上方按钮开始创作</p>
            </div>
          ) : (
            <>
              {/* 7 天内分区 */}
              <p className="px-3 pb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground/50">
                7 天内
              </p>
              {sessions.map((session, i) => (
                <StaggerItem
                  key={session.id}
                  index={i}
                  interval={20}
                  animation="slide-right"
                  as="div"
                  className={cn(
                    "group flex items-center gap-2 rounded-lg px-3 py-2 cursor-pointer transition-ui overflow-hidden min-w-0",
                    session.id === activeSessionId
                      ? "bg-accent text-foreground"
                      : "hover:bg-accent/50 text-muted-foreground"
                  )}
                  onClick={() => {
                    // 直接切换会话（不再弹出确认对话框）
                    switchSession(session.id);
                  }}
                >
                  {/* 状态圆点 — 进行中显示黄色脉冲 */}
                  {(() => {
                    const isRunning = session.status === "running" || session.status === "paused";
                    const dotColor = isRunning
                      ? "bg-amber-400"
                      : session.status === "error"
                        ? "bg-red-400"
                        : "bg-blue-400";
                    return (<span className={cn("h-2 w-2 shrink-0 rounded-full", dotColor, isRunning && "animate-pulse")} />);
                  })()}
                  <span className="flex-1 min-w-0 truncate text-sm">{session.title}</span>
                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      setDeleteTarget({ id: session.id, title: session.title });
                    }}
                    className="shrink-0 opacity-0 group-hover:opacity-100 transition-ui text-muted-foreground hover:text-destructive"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </StaggerItem>
              ))}
            </>
          )}
        </div>
      </ScrollArea>

      {/* ── 底部用户区 — 点击弹出 Popover 面板 ── */}
      <div className="border-t">
        <Popover>
          <PopoverTrigger asChild>
            <button className="group flex w-full items-center gap-2.5 p-3 transition-ui hover:bg-accent/50">
              <Avatar className="h-8 w-8 shrink-0">
                <AvatarFallback className={cn(
                  "text-xs font-medium",
                  isGuest ? "bg-amber-100 text-amber-700" : "bg-muted text-foreground"
                )}>
                  {isGuest ? "客" : (user?.username?.slice(0, 2).toUpperCase() ?? user?.userId?.slice(0, 2).toUpperCase() ?? "?")}
                </AvatarFallback>
              </Avatar>
              <div className="flex-1 min-w-0 text-left">
                {isGuest ? (
                  <>
                    <p className="text-sm font-medium truncate">你好，游客</p>
                    <p className="text-[11px] text-amber-600 font-mono-sm">trial · 1 attempt</p>
                  </>
                ) : (
                  <>
                    <p className="text-sm font-medium truncate">{user?.username ?? user?.userId ?? "用户"}</p>
                    <p className="text-[11px] text-muted-foreground font-mono-sm">
                      {isAdmin ? "admin" : "user"}
                    </p>
                  </>
                )}
              </div>
              <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground transition-ui group-hover:translate-x-0.5" />
            </button>
          </PopoverTrigger>
          <PopoverContent side="top" align="start" className="w-[232px]">
            <UserMenuContent
              isGuest={isGuest}
              isAdmin={isAdmin}
              theme={theme}
              onToggleTheme={toggleTheme}
              onNavigate={(path) => navigate(path)}
              onLogout={() => {
                logout();
                useAuthStore.getState().init();
                navigate("/write", { replace: true });
              }}
              onRegister={handleRegister}
            />
          </PopoverContent>
        </Popover>
      </div>

      {/* 删除确认对话框 */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertTriangle className="h-4 w-4 text-amber-500" />
              确认删除
            </DialogTitle>
            <DialogDescription>
              确定要删除「{deleteTarget?.title}」吗？删除后将不再显示在历史记录中。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button variant="outline" size="sm" onClick={() => setDeleteTarget(null)} disabled={deleting}>
              取消
            </Button>
            <Button variant="destructive" size="sm" onClick={handleConfirmDelete} disabled={deleting}>
              {deleting ? "删除中..." : "确认删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* 写作任务持续运行通知条 */}
      <RunningSessionBar />
    </div>
  );
}

// ════════════════════════════════════════════════════════════
// 写作任务持续运行通知条 — 当非活跃会话有写作在进行时显示
// ════════════════════════════════════════════════════════════

function RunningSessionBar() {
  const sessions = useAgentStore((s) => s.sessions);
  const activeSessionId = useAgentStore((s) => s.activeSessionId);
  const switchSession = useAgentStore((s) => s.switchSession);

  // 找到非当前活跃的、正在运行或暂停的会话
  const runningSession = sessions.find(
    (s) => s.id !== activeSessionId && (s.status === "running" || s.status === "paused")
  );

  if (!runningSession) return null;

  return (
    <div
      className="absolute bottom-0 left-0 right-0 z-20 flex items-center gap-2 border-t bg-amber-50/95 dark:bg-amber-950/50 px-3 py-2 backdrop-blur-sm cursor-pointer transition-ui hover:bg-amber-100 dark:hover:bg-amber-900/60 anim-slide-up"
      onClick={() => switchSession(runningSession.id)}
    >
      <span className="flex h-2 w-2 shrink-0 animate-pulse rounded-full bg-amber-500" />
      <span className="flex-1 min-w-0 truncate text-xs font-medium text-amber-900 dark:text-amber-200">
        「{runningSession.title}」正在写作中
      </span>
      <span className="text-xs text-amber-700 dark:text-amber-400 shrink-0">
        点击返回 →
      </span>
    </div>
  );
}

// ════════════════════════════════════════════════════════════
// 用户菜单面板内容 — 共用于折叠态和展开态
// ════════════════════════════════════════════════════════════

interface UserMenuContentProps {
  isGuest: boolean;
  isAdmin: boolean;
  theme: "light" | "dark";
  onToggleTheme: () => void;
  onNavigate: (path: string) => void;
  onLogout: () => void;
  onRegister: () => void;
}

function UserMenuContent({
  isGuest,
  isAdmin,
  theme,
  onToggleTheme,
  onNavigate,
  onLogout,
  onRegister,
}: UserMenuContentProps) {
  return (
    <div className="space-y-0.5">
      {/* 个人中心 */}
      <MenuRow
        icon={User}
        label="个人中心"
        onClick={() => onNavigate("/profile")}
      />

      {/* 管理后台（仅管理员） */}
      {isAdmin && (
        <MenuRow
          icon={Settings}
          label="管理后台"
          onClick={() => onNavigate("/admin")}
        />
      )}

      <div className="h-px bg-border/60 my-1" />

      {/* 深浅模式切换 */}
      <MenuRow
        icon={theme === "dark" ? Sun : Moon}
        label={theme === "dark" ? "浅色模式" : "深色模式"}
        onClick={onToggleTheme}
      />

      <div className="h-px bg-border/60 my-1" />

      {/* 游客：注册 / 非游客：退出登录 */}
      {isGuest ? (
        <MenuRow
          icon={UserPlus}
          label="登录/注册账号"
          onClick={onRegister}
        />
      ) : (
        <MenuRow
          icon={LogOut}
          label="退出登录"
          onClick={onLogout}
          destructive
        />
      )}
    </div>
  );
}

// ─── 菜单行 ────────────────────────────────────────────────
function MenuRow({
  icon: Icon,
  label,
  onClick,
  trailing,
  destructive,
}: {
  icon: typeof User;
  label: string;
  onClick: () => void;
  trailing?: string;
  destructive?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm transition-ui",
        destructive
          ? "text-destructive hover:bg-destructive/5"
          : "text-foreground hover:bg-accent"
      )}
    >
      <Icon className="h-4 w-4 shrink-0" />
      <span className="flex-1 text-left">{label}</span>
      {trailing && (
        <span className="text-[11px] text-muted-foreground font-mono-sm">{trailing}</span>
      )}
    </button>
  );
}
