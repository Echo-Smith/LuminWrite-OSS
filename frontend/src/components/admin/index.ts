/**
 * 部署级管理面组件（概览页专属 + 布局壳）
 *
 * 通用 UI 原语（表格/对话框/加载/空态/标签页等）已迁移至
 * @/components/admin-ui；本目录仅保留概览页专属卡片与
 * AdminDashboard 的布局壳（随 admin dashboard 生命周期走）。
 */

export { AdminLayout } from "./admin-layout";
export { AdminSidebar, CollapseToggle, getPageLabel, hasPagePermission, type AdminPageKey } from "./admin-sidebar";
export { AdminAlertsCard } from "./admin-alerts-card";
export { AgentArchitectureCard } from "./agent-architecture";
