/**
 * Admin Dashboard 布局
 *
 * 使用 AdminLayout 组件：
 * - 侧边栏折叠/展开（桌面端 localStorage 持久化）
 * - 移动端抽屉模式
 * - 面包屑自动生成
 */
import { useState, useEffect, useCallback } from "react";
import { AdminLayout, type AdminPageKey } from "@/components/admin";
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

// localStorage key for sidebar collapsed state
const SIDEBAR_COLLAPSED_KEY = "luminbuddy_admin_sidebar_collapsed";

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
      {activePage === "overview" && <OverviewPage />}
      {activePage === "styles" && <StyleManagementPage />}
      {activePage === "pending-styles" && <PendingStylesPage />}
      {activePage === "traces" && <TraceHistoryPage />}
      {activePage === "models" && <ModelConfigsPage />}
      {activePage === "keys" && <APIKeysPage />}
      {activePage === "cron" && <CronJobsPage />}
      {activePage === "feedback" && <FeedbackAnalysisPage />}
      {activePage === "evaluation" && <EvaluationPage />}
      {activePage === "usage" && <TokenUsagePage />}
      {activePage === "sensitive" && <SensitiveWordsPage />}
      {activePage === "kb" && <KnowledgeBasePage />}
      {activePage === "audit" && <AuditLogsPage />}
      {activePage === "security" && <SecurityAuditPage />}
      {activePage === "evolution" && <EvolutionPage />}
      {activePage === "rbac" && <RbacPage />}
      {activePage === "sandbox" && <MCPSandboxPage />}
    </AdminLayout>
  );
}
