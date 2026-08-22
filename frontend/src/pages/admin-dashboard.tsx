/**
 * Admin Dashboard 布局
 *
 * 使用 AdminLayout 组件：
 * - 侧边栏折叠/展开（桌面端 localStorage 持久化）
 * - 移动端抽屉模式
 * - 面包屑自动生成
 */
import { useState, useEffect, useCallback, type ReactNode } from "react";
import { AdminLayout, type AdminPageKey, hasPagePermission, getPageLabel } from "@/components/admin";
import { useAuthStore } from "@/stores/auth-store";
import { ShieldX } from "lucide-react";
import { OverviewPage } from "./admin/overview";
import { StyleManagementPage } from "./admin/style-management";
import { TraceHistoryPage } from "./admin/trace-history";
import { SensitiveWordsPage } from "./admin/sensitive-words";
import { FeedbackAnalysisPage } from "./admin/feedback-analysis";
import { EvaluationPage } from "./admin/evaluation-panel";
import { ModelConfigsPage } from "./admin/model-configs";
import { APIKeysPage } from "./admin/api-keys";
import { TokenUsagePage } from "./admin/token-usage";
import { CronJobsPage } from "./admin/cron-jobs";
import { PendingStylesPage } from "./admin/pending-styles";
import { KnowledgeBasePage } from "./admin/knowledge-base";
import { AuditLogsPage } from "./admin/audit-logs";
import { SecurityAuditPage } from "./admin/security-audit";
import { EvolutionPage } from "./admin/evolution";
import { RbacPage } from "./admin/rbac";
import { MCPSandboxPage } from "./admin/mcp-sandbox";
import { AgentCardsPage } from "./admin/agent-cards";
import { BillingPage } from "./admin/billing";

// localStorage key for sidebar collapsed state
const SIDEBAR_COLLAPSED_KEY = "luminbuddy_admin_sidebar_collapsed";

/** 无权限访问提示组件 */
function NoPermissionPage({ pageLabel }: { pageLabel: string }) {
  return (
    <div className="flex flex-col items-center justify-center h-full min-h-[60vh] gap-4 text-center">
      <ShieldX className="h-12 w-12 text-muted-foreground" />
      <div>
        <h2 className="text-lg font-semibold text-foreground">无访问权限</h2>
        <p className="text-sm text-muted-foreground mt-1">
          您没有访问「{pageLabel}」的权限，请联系管理员分配对应角色。
        </p>
      </div>
    </div>
  );
}

/** 根据权限包裹页面，无权限时显示拒绝提示 */
function GuardedPage({ page, children }: { page: AdminPageKey; children: ReactNode }) {
  const hasPermission = useAuthStore((s) => s.hasPermission);

  if (!hasPagePermission(hasPermission, page)) {
    return <NoPermissionPage pageLabel={getPageLabel(page)} />;
  }
  return <>{children}</>;
}

export function AdminDashboard() {
  const [activePage, setActivePage] = useState<AdminPageKey>("overview");
  const [collapsed, setCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);

  // 从 localStorage 恢复折叠状态
  useEffect(() => {
    const stored = localStorage.getItem(SIDEBAR_COLLAPSED_KEY);
    if (stored === "true") setCollapsed(true);
  }, []);

  const handleToggleCollapsed = useCallback(() => {
    setCollapsed((prev) => {
      const next = !prev;
      localStorage.setItem(SIDEBAR_COLLAPSED_KEY, String(next));
      return next;
    });
  }, []);

  const handleMobileClose = useCallback(() => setMobileOpen(false), []);

  return (
    <AdminLayout
      activePage={activePage}
      onPageChange={setActivePage}
      collapsed={collapsed}
      onToggleCollapsed={handleToggleCollapsed}
      mobileOpen={mobileOpen}
      onMobileClose={handleMobileClose}
    >
      {activePage === "overview" && <GuardedPage page="overview"><OverviewPage /></GuardedPage>}
      {activePage === "styles" && <GuardedPage page="styles"><StyleManagementPage /></GuardedPage>}
      {activePage === "pending-styles" && <GuardedPage page="pending-styles"><PendingStylesPage /></GuardedPage>}
      {activePage === "traces" && <GuardedPage page="traces"><TraceHistoryPage /></GuardedPage>}
      {activePage === "models" && <GuardedPage page="models"><ModelConfigsPage /></GuardedPage>}
      {activePage === "keys" && <GuardedPage page="keys"><APIKeysPage /></GuardedPage>}
      {activePage === "cron" && <GuardedPage page="cron"><CronJobsPage /></GuardedPage>}
      {activePage === "feedback" && <GuardedPage page="feedback"><FeedbackAnalysisPage /></GuardedPage>}
      {activePage === "evaluation" && <GuardedPage page="evaluation"><EvaluationPage /></GuardedPage>}
      {activePage === "usage" && <GuardedPage page="usage"><TokenUsagePage /></GuardedPage>}
      {activePage === "sensitive" && <GuardedPage page="sensitive"><SensitiveWordsPage /></GuardedPage>}
      {activePage === "kb" && <GuardedPage page="kb"><KnowledgeBasePage /></GuardedPage>}
      {activePage === "audit" && <GuardedPage page="audit"><AuditLogsPage /></GuardedPage>}
      {activePage === "security" && <GuardedPage page="security"><SecurityAuditPage /></GuardedPage>}
      {activePage === "evolution" && <GuardedPage page="evolution"><EvolutionPage /></GuardedPage>}
      {activePage === "rbac" && <GuardedPage page="rbac"><RbacPage /></GuardedPage>}
      {activePage === "sandbox" && <GuardedPage page="sandbox"><MCPSandboxPage /></GuardedPage>}
      {activePage === "agent-cards" && <GuardedPage page="agent-cards"><AgentCardsPage /></GuardedPage>}
      {activePage === "billing" && <GuardedPage page="billing"><BillingPage /></GuardedPage>}
    </AdminLayout>
  );
}
