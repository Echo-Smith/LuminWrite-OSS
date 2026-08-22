/**
 * AdminSidebar — Admin 侧边栏导航
 *
 * 功能：
 * - lucide-react 图标（替代 emoji）
 * - 桌面端折叠/展开（icon-only 模式）
 * - 移动端抽屉模式（遮罩 + 滑出）
 * - 当前页面高亮
 * - 底部操作区：返回工作台 / 退出登录
 */
import { useNavigate } from "react-router-dom";
import { type LucideIcon, ArrowLeft, LogOut, PanelLeftClose, PanelLeft, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";

export type AdminPageKey =
  | "overview"
  | "styles"
  | "pending-styles"
  | "traces"
  | "models"
  | "keys"
  | "cron"
  | "evaluation"
  | "feedback"
  | "usage"
  | "sensitive"
  | "kb"
  | "audit"
  | "security"
  | "evolution"
  | "rbac"
  | "sandbox";

interface NavItem {
  key: AdminPageKey;
  label: string;
  icon: LucideIcon;
}

import {
  LayoutDashboard,
  PenLine,
  SearchCheck,
  ListTree,
  Cpu,
  KeyRound,
  Clock,
  ClipboardCheck,
  MessageSquareText,
  TrendingUp,
  Shield,
  BookOpen,
  ScrollText,
  ShieldAlert,
  GitBranch,
  Users,
  ShieldCheck,
} from "lucide-react";

export const NAV_ITEMS: NavItem[] = [
  { key: "overview", label: "概览", icon: LayoutDashboard },
  { key: "styles", label: "风格管理", icon: PenLine },
  { key: "pending-styles", label: "社区审核", icon: SearchCheck },
  { key: "traces", label: "Trace 历史", icon: ListTree },
  { key: "models", label: "模型配置", icon: Cpu },
  { key: "keys", label: "MCP 服务密钥", icon: KeyRound },
  { key: "cron", label: "定时任务", icon: Clock },
  { key: "evaluation", label: "评测面板", icon: ClipboardCheck },
  { key: "feedback", label: "反馈分析", icon: MessageSquareText },
  { key: "usage", label: "用量统计", icon: TrendingUp },
  { key: "sensitive", label: "敏感词库", icon: Shield },
  { key: "kb", label: "知识库", icon: BookOpen },
  { key: "audit", label: "审计日志", icon: ScrollText },
  { key: "security", label: "安全审计", icon: ShieldAlert },
  { key: "evolution", label: "自演进", icon: GitBranch },
  { key: "rbac", label: "角色权限", icon: Users },
  { key: "sandbox", label: "MCP 沙箱", icon: ShieldCheck },
];

/** 根据 page key 获取 label（供面包屑使用） */
export function getPageLabel(key: AdminPageKey): string {
  return NAV_ITEMS.find((item) => item.key === key)?.label ?? key;
}

interface AdminSidebarProps {
  activePage: AdminPageKey;
  onPageChange: (page: AdminPageKey) => void;
  /** 桌面端折叠状态 */
  collapsed: boolean;
  /** 移动端抽屉是否打开 */
  mobileOpen: boolean;
  /** 关闭移动端抽屉 */
  onMobileClose: () => void;
}

export function AdminSidebar({
  activePage,
  onPageChange,
  collapsed,
  mobileOpen,
  onMobileClose,
}: AdminSidebarProps) {
  const navigate = useNavigate();
  const logout = useAuthStore((s) => s.logout);

  const handlePageChange = (page: AdminPageKey) => {
    onPageChange(page);
    onMobileClose();
  };

  // 侧边栏内容
  const sidebarContent = (
    <nav
      className={cn(
        "flex flex-col border-r bg-background transition-[width] duration-200 h-full",
        collapsed ? "w-16" : "w-56",
        // 移动端：固定宽度，抽屉模式
        "max-md:w-56",
      )}
    >
      {/* Logo / 标题 */}
      <div className="border-b px-4 py-4 flex items-center justify-between">
        {collapsed ? (
          <div className="mx-auto h-8 w-8 rounded-lg bg-primary/10 flex items-center justify-center">
            <span className="text-sm font-bold text-primary">A</span>
          </div>
        ) : (
          <div>
            <h1 className="text-base font-bold">Writing Agent V2</h1>
            <p className="text-xs text-muted-foreground">Admin Dashboard</p>
          </div>
        )}
        {/* 移动端关闭按钮 */}
        <button
          onClick={onMobileClose}
          className="md:hidden text-muted-foreground hover:text-foreground"
        >
          <X className="h-5 w-5" />
        </button>
      </div>

      {/* 导航项 */}
      <div className="flex-1 overflow-y-auto py-2">
        {NAV_ITEMS.map((item) => {
          const Icon = item.icon;
          const isActive = activePage === item.key;
          return (
            <button
              key={item.key}
              onClick={() => handlePageChange(item.key)}
              title={collapsed ? item.label : undefined}
              className={cn(
                "flex w-full items-center gap-3 text-sm transition-colors group relative",
                collapsed ? "justify-center px-2 py-2.5" : "px-4 py-2.5",
                isActive
                  ? "bg-muted text-foreground font-medium"
                  : "text-muted-foreground hover:bg-accent",
              )}
            >
              <Icon className={cn("h-4 w-4 shrink-0", isActive && "text-primary")} />
              {!collapsed && <span>{item.label}</span>}
              {/* 激活指示条 */}
              {isActive && (
                <span className="absolute left-0 w-1 h-8 bg-primary rounded-r-full" />
              )}
            </button>
          );
        })}
      </div>

      <Separator />

      {/* 底部操作区 */}
      <div className="p-3 space-y-1">
        <Button
          variant="ghost"
          size="sm"
          className={cn(
            "justify-start gap-2 text-muted-foreground",
            collapsed && "justify-center px-0",
          )}
          onClick={() => navigate("/write")}
          title={collapsed ? "返回工作台" : undefined}
        >
          <ArrowLeft className="h-4 w-4 shrink-0" />
          {!collapsed && <span>返回工作台</span>}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className={cn(
            "justify-start gap-2 text-muted-foreground hover:text-destructive",
            collapsed && "justify-center px-0",
          )}
          onClick={() => {
            logout();
            navigate("/login", { replace: true });
          }}
          title={collapsed ? "退出登录" : undefined}
        >
          <LogOut className="h-4 w-4 shrink-0" />
          {!collapsed && <span>退出登录</span>}
        </Button>
      </div>
    </nav>
  );

  return (
    <>
      {/* 桌面端侧边栏 */}
      <div className="hidden md:block h-full">
        {sidebarContent}
      </div>

      {/* 移动端抽屉 */}
      {mobileOpen && (
        <div className="md:hidden fixed inset-0 z-50 flex">
          {/* 遮罩 */}
          <div
            className="absolute inset-0 bg-black/40 animate-in fade-in-0"
            onClick={onMobileClose}
          />
          {/* 侧边栏 */}
          <div className="relative h-full animate-in slide-in-from-left duration-200">
            {sidebarContent}
          </div>
        </div>
      )}
    </>
  );
}

/**
 * 折叠/展开按钮（桌面端）
 */
interface CollapseToggleProps {
  collapsed: boolean;
  onToggle: () => void;
}

export function CollapseToggle({ collapsed, onToggle }: CollapseToggleProps) {
  return (
    <button
      onClick={onToggle}
      className="hidden md:flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors px-2 py-1 rounded-md hover:bg-accent"
      title={collapsed ? "展开侧边栏" : "折叠侧边栏"}
    >
      {collapsed ? <PanelLeft className="h-3.5 w-3.5" /> : <PanelLeftClose className="h-3.5 w-3.5" />}
      {!collapsed && <span>折叠</span>}
    </button>
  );
}
