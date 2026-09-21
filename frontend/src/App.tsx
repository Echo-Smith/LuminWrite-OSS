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
import { MaterialsCenter } from "@/pages/materials-center";
import { AdminDashboard } from "@/pages/admin-dashboard";
import { PersonalCenter } from "@/pages/personal-center";
import { WritingProjectsPage } from "@/pages/writing-projects"; // 新的轻量级写作项目管理器
// import { EditorialBoard } from "@/pages/editorial/editorial-board"; // 已废弃，保留用于迁移参考
import { MyStylesPage } from "@/pages/my-styles";
import PricingPage from "@/pages/pricing";
import BillingResultPage from "@/pages/billing-result";
import { ToastContainer } from "@/components/ui/toast";
import { useSSENotifications } from "@/hooks/use-sse-notifications";
import { useWorkflowSSE } from "@/hooks/use-workflow-sse";
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

  // 全局 Workflow/Editorial SSE 监听（编辑部 DAG 事件 → workflow-store / editorial-store）
  useWorkflowSSE();

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

          {/* 知识库 — 独立访问域（原素材库，不再重定向到 /topics） */}
          <Route
            path="/materials"
            element={
              <ProtectedRoute>
                <PageTransition>
                  <MaterialsCenter />
                </PageTransition>
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

          {/* 工作台 — 需登录 */}
          <Route
            path="/workspace"
            element={
              <ProtectedRoute>
                <PageTransition>
                  <WritingProjectsPage />
                </PageTransition>
              </ProtectedRoute>
            }
          />
          {/* 兼容旧路由 /editorial → /workspace */}
          <Route path="/editorial" element={<Navigate to="/workspace" replace />} />

          {/* /workflow 已集成到 /write 的工作台模式中，重定向 */}
          <Route path="/workflow" element={<Navigate to="/write" replace />} />

          {/* 定价页 — 公开访问 */}
          <Route path="/pricing" element={<PageTransition><PricingPage /></PageTransition>} />

          {/* 支付结果页 — 支付宝 return_url 回跳 */}
          <Route path="/billing/result" element={<BillingResultPage />} />

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
