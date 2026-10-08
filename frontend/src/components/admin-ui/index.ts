/**
 * Admin UI 原语统一导出
 *
 * 表格 / 确认与表单对话框 / 加载与空态 / 页头 / 批量操作 / 标签页容器 /
 * 权限守卫 —— 消费方：插件页、审计与治理控制台、个人中心管理 section。
 */

export { AdminTable, type AdminColumn } from "./admin-table";
export { AdminConfirmDialog } from "./admin-confirm-dialog";
export { AdminFormDialog } from "./admin-form-dialog";
export { AdminEmptyState, AdminErrorState } from "./admin-empty-state";
export { AdminLoading, AdminTableSkeleton } from "./admin-loading-skeleton";
export { AdminPageHeader } from "./admin-page-header";
export { AdminBulkActions } from "./admin-bulk-actions";
export { AdminPermissionGuard } from "./admin-permission-guard";
export { AdminTabbedPage, type AdminTabDef } from "./admin-tabbed-page";
