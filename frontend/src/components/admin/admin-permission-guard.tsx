import { type ReactNode } from "react";
import { useAdminPermission, type AdminAction, type AdminResource } from "@/hooks/use-admin-permission";

interface AdminPermissionGuardProps {
  resource: AdminResource;
  action: AdminAction;
  children: ReactNode;
  fallback?: ReactNode;
}

export function AdminPermissionGuard({ resource, action, children, fallback = null }: AdminPermissionGuardProps) {
  const { can } = useAdminPermission();
  if (!can(resource, action)) return <>{fallback}</>;
  return <>{children}</>;
}