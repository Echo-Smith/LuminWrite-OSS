/**
 * 应用根组件 — 路由配置 + 认证初始化 + AuthModal
 */
import { useEffect } from "react";
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ProtectedRoute } from "@/components/protected-route";
import { AuthModal } from "@/components/auth-modal";
import { useAuthStore } from "@/stores/auth-store";
import { useAuthModal } from "@/stores/auth-modal-store";
import { WritingWorkspace } from "@/pages/writing-workspace";
import { TopicCenter } from "@/pages/topic-center";
import { AdminDashboard } from "@/pages/admin-dashboard";
import { PersonalCenter } from "@/pages/personal-center";

export function App() {
  const init = useAuthStore((s) => s.init);
  const modalOpen = useAuthModal((s) => s.open);
  const guestToken = useAuthModal((s) => s.guestToken);
  const defaultTab = useAuthModal((s) => s.defaultTab);
  const closeAuth = useAuthModal((s) => s.closeAuth);

  // 应用启动时恢复登录态或自动创建游客
  useEffect(() => {
    init();
  }, [init]);

  return (
    <TooltipProvider>
      <BrowserRouter>
        <Routes>
          {/* 用户端 — 需登录（游客自动登录也算已认证） */}
          <Route
            path="/write"
            element={
              <ProtectedRoute>
                <WritingWorkspace />
              </ProtectedRoute>
            }
          />
          <Route
            path="/write/:topicId"
            element={
              <ProtectedRoute>
                <WritingWorkspace />
              </ProtectedRoute>
            }
          />
          <Route
            path="/topics"
            element={
              <ProtectedRoute>
                <TopicCenter />
              </ProtectedRoute>
            }
          />
          <Route
            path="/profile"
            element={
              <ProtectedRoute>
                <WritingWorkspace />
                <PersonalCenter />
              </ProtectedRoute>
            }
          />

          {/* Admin — 需管理员 */}
          <Route
            path="/admin/*"
            element={
              <ProtectedRoute requireAdmin>
                <AdminDashboard />
              </ProtectedRoute>
            }
          />

          {/* 默认重定向 */}
          <Route path="/" element={<Navigate to="/write" replace />} />
          <Route path="/login" element={<Navigate to="/write" replace />} />
          <Route path="*" element={<Navigate to="/write" replace />} />
        </Routes>

        {/* 全局 Auth Modal */}
        <AuthModal
          open={modalOpen}
          onOpenChange={(v) => { if (!v) closeAuth(); }}
          guestToken={guestToken}
          defaultTab={defaultTab}
        />
      </BrowserRouter>
    </TooltipProvider>
  );
}
