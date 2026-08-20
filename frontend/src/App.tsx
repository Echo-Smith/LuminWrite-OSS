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
import { EditorialBoard } from "@/pages/editorial/editorial-board";
import { MyStylesPage } from "@/pages/my-styles";
import { TermsPage } from "@/pages/legal/terms";
import { PrivacyPage } from "@/pages/legal/privacy";
import { ToastContainer } from "@/components/ui/toast";
import { useSSENotifications } from "@/hooks/use-sse-notifications";
import { PageTransition } from "@/components/animation";

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

  // 全局 SSE 通知监听（文章完成、管理员广播等）
  useSSENotifications();

  return (
    <TooltipProvider>
      <BrowserRouter>
        <Routes>
          {/* 用户端 — 需登录（游客自动登录也算已认证） */}
          <Route
            path="/write"
            element={
              <ProtectedRoute>
                <PageTransition>
                  <WritingWorkspace />
                </PageTransition>
              </ProtectedRoute>
            }
          />
          <Route
            path="/write/:topicId"
            element={
              <ProtectedRoute>
                <PageTransition>
                  <WritingWorkspace />
                </PageTransition>
              </ProtectedRoute>
            }
          />
          <Route
            path="/topics"
            element={
              <ProtectedRoute>
                <PageTransition>
                  <TopicCenter />
                </PageTransition>
              </ProtectedRoute>
            }
          />
          <Route
            path="/profile"
            element={
              <ProtectedRoute>
                <PageTransition>
                  <WritingWorkspace />
                  <PersonalCenter />
                </PageTransition>
              </ProtectedRoute>
            }
          />

          <Route
            path="/my-styles"
            element={
              <ProtectedRoute>
                <PageTransition>
                  <MyStylesPage />
                </PageTransition>
              </ProtectedRoute>
            }
          />

          {/* /materials 已合并到 /topics 的素材 Tab，重定向 */}
          <Route path="/materials" element={<Navigate to="/topics" replace />} />

          {/* Admin — 需管理员 */}
          <Route
            path="/admin/*"
            element={
              <ProtectedRoute requireAdmin>
                <AdminDashboard />
              </ProtectedRoute>
            }
          />

          {/* 编辑部 — 需登录 */}
          <Route
            path="/editorial"
            element={
              <ProtectedRoute>
                <PageTransition>
                  <EditorialBoard />
                </PageTransition>
              </ProtectedRoute>
            }
          />

          {/* 法律页面 — 公开访问，无需登录 */}
          <Route path="/terms" element={<PageTransition><TermsPage /></PageTransition>} />
          <Route path="/privacy" element={<PageTransition><PrivacyPage /></PageTransition>} />

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

      {/* 全局 Toast 通知 */}
      <ToastContainer />
    </TooltipProvider>
  );
}
