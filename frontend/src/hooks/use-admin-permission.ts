/**
 * useAdminPermission - fine-grained permission hook
 */
import { useAuthStore } from "@/stores/auth-store";

export type AdminResource = "model_config" | "api_key" | "cron_job" | "style" | "sensitive_word" | "pending_style" | "evaluation" | "kb" | "mcp_server" | "audit_log";

export type AdminAction = "view" | "create" | "update" | "delete" | "batch_delete" | "batch_toggle";

// 管理面对所有注册成员开放（单用户/家庭语义）；仅 guest 不可见不可操作。
const FULL: Record<AdminAction, boolean> = {
  view: true, create: true, update: true, delete: true, batch_delete: true, batch_toggle: true,
};
const NONE: Record<AdminAction, boolean> = {
  view: false, create: false, update: false, delete: false, batch_delete: false, batch_toggle: false,
};

const PERMISSION_MATRIX: Record<string, Record<AdminAction, boolean>> = {
  admin: FULL,
  user: FULL,
  guest: NONE,
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
  return { can, canDo, isAdmin: role !== "guest", isReadOnly: role === "guest", role };
}