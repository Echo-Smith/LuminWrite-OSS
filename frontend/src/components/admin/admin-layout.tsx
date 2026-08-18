/**
 * AdminLayout — Admin 页面统一布局框架
 *
 * 功能：
 * - 侧边栏（桌面端折叠/展开 + 移动端抽屉）
 * - 面包屑导航（自动从 activePage 生成）
 * - 内容区滚动 + 统一 padding
 * - 顶部工具栏（折叠按钮 + 移动端菜单按钮 + 面包屑）
 */
import { type ReactNode } from "react";
import { Menu } from "lucide-react";
import { AdminSidebar, CollapseToggle, getPageLabel, type AdminPageKey } from "./admin-sidebar";

interface AdminLayoutProps {
  activePage: AdminPageKey;
  onPageChange: (page: AdminPageKey) => void;
  /** 桌面端侧边栏折叠状态 */
  collapsed: boolean;
  /** 切换折叠状态 */
  onToggleCollapsed: () => void;
  /** 移动端抽屉是否打开 */
  mobileOpen: boolean;
  /** 关闭移动端抽屉 */
  onMobileClose: () => void;
  /** 页面内容 */
  children: ReactNode;
}

export function AdminLayout({
  activePage,
  onPageChange,
  collapsed,
  onToggleCollapsed,
  mobileOpen,
  onMobileClose,
  children,
}: AdminLayoutProps) {
  return (
    <div className="flex h-screen bg-muted/30 overflow-hidden">
      {/* 侧边栏 */}
      <AdminSidebar
        activePage={activePage}
        onPageChange={onPageChange}
        collapsed={collapsed}
        mobileOpen={mobileOpen}
        onMobileClose={onMobileClose}
      />

      {/* 主内容区 */}
      <div className="flex-1 flex flex-col overflow-hidden">
        {/* 顶部工具栏 */}
        <header className="flex items-center gap-3 border-b bg-background px-4 py-2.5 shrink-0">
          {/* 移动端菜单按钮 */}
          <button
            onClick={onMobileClose}
            className="md:hidden text-muted-foreground hover:text-foreground"
          >
            <Menu className="h-5 w-5" />
          </button>

          {/* 桌面端折叠按钮 */}
          <CollapseToggle collapsed={collapsed} onToggle={onToggleCollapsed} />

          {/* 面包屑 */}
          <div className="flex items-center gap-1.5 text-sm text-muted-foreground">
            <span className="text-muted-foreground/60">Admin</span>
            <span className="text-muted-foreground/40">/</span>
            <span className="text-foreground font-medium">{getPageLabel(activePage)}</span>
          </div>
        </header>

        {/* 内容区 */}
        <main className="flex-1 overflow-y-auto">
          {children}
        </main>
      </div>
    </div>
  );
}
