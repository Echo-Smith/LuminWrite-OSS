/**
 * Admin 组件统一导出
 *
 * 使用方式：
 *   import { AdminTable, AdminFormDialog, AdminConfirmDialog, AdminEmptyState, AdminLoading } from "@/components/admin";
 */

export { AdminTable, type AdminColumn } from "./admin-table";
export { AdminConfirmDialog } from "./admin-confirm-dialog";
export { AdminFormDialog } from "./admin-form-dialog";
export { AdminEmptyState, AdminErrorState } from "./admin-empty-state";
export { AdminLoading, AdminTableSkeleton } from "./admin-loading-skeleton";
export { AdminLayout } from "./admin-layout";
export { AdminSidebar, CollapseToggle, getPageLabel, hasPagePermission, type AdminPageKey } from "./admin-sidebar";
export { AdminPageHeader } from "./admin-page-header";
export { AdminBulkActions } from "./admin-bulk-actions";
export { AdminPermissionGuard } from "./admin-permission-guard";
