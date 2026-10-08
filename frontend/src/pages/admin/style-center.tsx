/**
 * 风格管理（合并入口）
 *
 * - 风格列表：风格 Profile CRUD 与发布流程（style.create）
 * - 社区审核：用户提交的社区风格审核（style.review）；关注册的
 *   单人部署没有投稿来源，该 tab 经 deployment meta 隐藏。
 */
import { AdminTabbedPage } from "@/components/admin-ui";
import { StyleManagementPage } from "./style-management";
import { PendingStylesPage } from "./pending-styles";
import { useAuthStore } from "@/stores/auth-store";

export function StyleCenterPage() {
  const registrationDisabled = useAuthStore((s) => s.registrationDisabled);

  const tabs = [
    { key: "styles", label: "风格列表", permission: "style.create", content: <StyleManagementPage /> },
    ...(registrationDisabled
      ? []
      : [{ key: "pending-styles", label: "社区审核", permission: "style.review", content: <PendingStylesPage /> }]),
  ];

  return <AdminTabbedPage tabs={tabs} />;
}
