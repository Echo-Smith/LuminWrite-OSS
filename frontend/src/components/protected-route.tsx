/**
 * 路由守卫 — 未登录时打开 AuthModal（不再重定向到 /login）
 *
 * 游客自动登录后也会通过此守卫。
 * 需要管理员权限的路由如果用户不是管理员，重定向到 /write。
 */
import { type ReactNode, useEffect } from "react";
import { Navigate } from "react-router-dom";
import { useAuthStore } from "@/stores/auth-store";
import { useAuthModal } from "@/stores/auth-modal-store";

interface ProtectedRouteProps {
  children: ReactNode;
  requireAdmin?: boolean;
}

export function ProtectedRoute({ children, requireAdmin = false }: ProtectedRouteProps) {
  const { token, user, initialized } = useAuthStore();
  const openAuth = useAuthModal((s) => s.openAuth);

  // 等待 init 完成
  if (!initialized) {
    return (
      <div className="flex h-screen items-center justify-center">
        <div className="animate-pulse text-muted-foreground">加载中...</div>
      </div>
    );
  }

  // 未登录 — 打开 AuthModal（而非重定向到 /login）
  if (!token) {
    return (
      <>
        <div className="flex h-screen items-center justify-center">
          <div className="animate-pulse text-muted-foreground">正在初始化...</div>
        </div>
        <AuthModalTrigger onOpen={openAuth} />
      </>
    );
  }

  // 需要管理员权限但不是管理员
  if (requireAdmin && !useAuthStore.getState().hasAdminAccess()) {
    return <Navigate to="/write" replace />;
  }

  return <>{children}</>;
}

/** 触发 AuthModal 打开的小组件 */
function AuthModalTrigger({ onOpen }: { onOpen: () => void }) {
  useEffect(() => {
    onOpen();
  }, [onOpen]);
  return null;
}
