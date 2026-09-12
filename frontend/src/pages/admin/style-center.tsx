/**
 * 风格管理（合并入口）
 *
 * - 风格列表：风格 Profile CRUD 与发布流程（style.create）
 * - 社区审核：用户提交的社区风格审核（style.review）
 */
import { AdminTabbedPage } from "@/components/admin";
import { StyleManagementPage } from "./style-management";
import { PendingStylesPage } from "./pending-styles";

export function StyleCenterPage() {
  return (
    <AdminTabbedPage
      tabs={[
        { key: "styles", label: "风格列表", permission: "style.create", content: <StyleManagementPage /> },
        { key: "pending-styles", label: "社区审核", permission: "style.review", content: <PendingStylesPage /> },
      ]}
    />
  );
}
