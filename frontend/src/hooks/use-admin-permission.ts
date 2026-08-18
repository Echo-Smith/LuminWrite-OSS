/**
 * useAdminPermission - fine-grained permission hook
 */
import { useAuthStore } from "@/stores/auth-store";

export type AdminResource = "model_config" | "api_key" | "cron_job" | "style" | "sensitive_word" | "pending_style" | "evaluation" | "kb" | "mcp_server" | "audit_log";

export type AdminAction = "view" | "create" | "update" | "delete" | "batch_delete" | "batch_toggle";

const PERMISSION_MATRIX: Record<string, Record<AdminAction, boolean>> = {
  admin: { view: true, create: true, update: true, delete: true, batch_delete: true, batch_toggle: true },
  user: { view: true, create: false, update: false, delete: false, batch_delete: false, batch_toggle: false },
  guest: { view: false, create: false, update: false, delete: false, batch_delete: false, batch_toggle: false },
};

export function useAdminPermission() {
  const user = useAuthStore((s) => s.user);
  const role = user?.role ?? "guest";
  const can = (_r: AdminResource, action: AdminAction): boolean => {
    const perms = PERMISSION_MATRIX[role];
    return perms ? (perms[action] ?? false) : false;
  };
  const canDo = (action: AdminAction): boolean => {
    const perms = PERMISSION_MATRIX[role];
    return perms ? (perms[action] ?? false) : false;
  };
  return { can, canDo, isAdmin: role === "admin", isReadOnly: role !== "admin", role };
}