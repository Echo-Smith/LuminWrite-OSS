/**
 * 审计中心（合并入口）
 *
 * - 操作日志：管理员写操作的追加式日志（audit.view）
 * - 安全审计：安全事件审计（security.view）
 */
import { AdminTabbedPage } from "@/components/admin";
import { AuditLogsPage } from "./audit-logs";
import { SecurityAuditPage } from "./security-audit";

export function AuditCenterPage() {
  return (
    <AdminTabbedPage
      tabs={[
        { key: "logs", label: "操作日志", permission: "audit.view", content: <AuditLogsPage /> },
        { key: "security", label: "安全审计", permission: "security.view", content: <SecurityAuditPage /> },
      ]}
    />
  );
}
