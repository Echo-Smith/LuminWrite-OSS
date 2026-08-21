/**
 * RBAC 角色权限管理 — Admin Dashboard
 *
 * 功能：
 * 1. 角色列表（含权限数、用户数）
 * 2. 权限管理（为角色分配权限）
 * 3. 用户角色管理（查看/分配/移除用户角色）
 */
import { useState, useEffect, useCallback } from "react";
import {
  Shield, Users, Key, Plus, Trash2, Check, Loader2, RefreshCw, ChevronRight,
} from "lucide-react";
import { adminFetch, adminMutate } from "@/lib/admin-api";
import { AdminPageHeader, AdminLoading, AdminEmptyState } from "@/components/admin";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter,
} from "@/components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

// ─── Types ──────────────────────────────────────────────

interface Role {
  id: string;
  name: string;
  description: string;
  is_system: boolean;
  perm_count: number;
  user_count: number;
  created_at: string;
  updated_at: string;
}

interface Permission {
  id: string;
  key: string;
  description: string;
  created_at?: string;
}

interface UserWithRoles {
  id: string;
  uid: string;
  name: string;
  role: string;
  created_at: string;
  roles: { id: string; name: string }[];
}

interface UserRoleAssignment {
  id: string;
  name: string;
  description: string;
  is_system: boolean;
  assigned_at: string;
}

// ─── Component ──────────────────────────────────────────

export function RbacPage() {
  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="角色权限管理"
        description="RBAC — 细粒度权限控制、角色分配、用户管理"
      />
      <Tabs defaultValue="roles">
        <TabsList>
          <TabsTrigger value="roles" className="gap-2">
            <Shield className="h-4 w-4" />
            角色与权限
          </TabsTrigger>
          <TabsTrigger value="users" className="gap-2">
            <Users className="h-4 w-4" />
            用户角色
          </TabsTrigger>
        </TabsList>
        <TabsContent value="roles" className="mt-4">
          <RolesTab />
        </TabsContent>
        <TabsContent value="users" className="mt-4">
          <UsersTab />
        </TabsContent>
      </Tabs>
    </div>
  );
}

// ─── Roles Tab ──────────────────────────────────────────

function RolesTab() {
  const [roles, setRoles] = useState<Role[]>([]);
  const [permissions, setPermissions] = useState<Permission[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedRole, setSelectedRole] = useState<Role | null>(null);
  const [rolePerms, setRolePerms] = useState<Set<string>>(new Set());
  const [permDialogOpen, setPermDialogOpen] = useState(false);
  const [saving, setSaving] = useState(false);

  const loadData = useCallback(async () => {
    setLoading(true);
    const [rolesRes, permsRes] = await Promise.all([
      adminFetch<{ roles: Role[] }>("/api/v2/admin/rbac/roles", { silent: true }),
      adminFetch<{ permissions: Permission[] }>("/api/v2/admin/rbac/permissions", { silent: true }),
    ]);
    if (rolesRes.success && rolesRes.data) setRoles(rolesRes.data.roles ?? []);
    if (permsRes.success && permsRes.data) setPermissions(permsRes.data.permissions ?? []);
    setLoading(false);
  }, []);

  useEffect(() => { loadData(); }, [loadData]);

  const openPermDialog = async (role: Role) => {
    setSelectedRole(role);
    const { success, data } = await adminFetch<{ permissions: Permission[] }>(
      `/api/v2/admin/rbac/roles/${role.id}/permissions`, { silent: true },
    );
    if (success && data) {
      setRolePerms(new Set(data.permissions.map((p) => p.key)));
    } else {
      setRolePerms(new Set());
    }
    setPermDialogOpen(true);
  };

  const togglePerm = (key: string) => {
    setRolePerms((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const savePermissions = async () => {
    if (!selectedRole) return;
    setSaving(true);
    const { success } = await adminMutate(
      `/api/v2/admin/rbac/roles/${selectedRole.id}/permissions`,
      {
        method: "PUT",
        body: JSON.stringify({ permission_keys: Array.from(rolePerms) }),
        successTitle: "权限已更新",
        successDesc: `${selectedRole.name} 角色的权限已保存`,
      },
    );
    setSaving(false);
    if (success) {
      setPermDialogOpen(false);
      loadData();
    }
  };

  if (loading) return <AdminLoading />;

  return (
    <div className="space-y-4">
      {/* Roles Grid */}
      {roles.length === 0 ? (
        <AdminEmptyState
          icon={Shield}
          title="暂无角色"
          description="数据库迁移后角色将自动创建"
        />
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {roles.map((role) => (
            <Card key={role.id} className="hover:shadow-md transition-shadow">
              <CardHeader className="flex flex-row items-center justify-between pb-3">
                <div className="flex items-center gap-3">
                  <div className={`flex h-10 w-10 items-center justify-center rounded-lg ${
                    role.is_system ? "bg-blue-100 dark:bg-blue-950" : "bg-muted"
                  }`}>
                    <Shield className={`h-5 w-5 ${role.is_system ? "text-blue-600" : "text-muted-foreground"}`} />
                  </div>
                  <div>
                    <CardTitle className="text-base">{role.name}</CardTitle>
                    {role.is_system && <Badge variant="secondary" className="text-[10px] mt-0.5">系统角色</Badge>}
                  </div>
                </div>
                <Button variant="ghost" size="sm" onClick={() => openPermDialog(role)}>
                  <Key className="h-4 w-4 mr-1" />
                  权限
                </Button>
              </CardHeader>
              <CardContent className="space-y-2">
                <p className="text-sm text-muted-foreground">{role.description}</p>
                <div className="flex items-center gap-4 text-xs text-muted-foreground">
                  <span className="flex items-center gap-1">
                    <Key className="h-3 w-3" />
                    {role.perm_count} 权限
                  </span>
                  <span className="flex items-center gap-1">
                    <Users className="h-3 w-3" />
                    {role.user_count} 用户
                  </span>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* Permission Assignment Dialog */}
      <Dialog open={permDialogOpen} onOpenChange={setPermDialogOpen}>
        <DialogContent className="max-w-2xl max-h-[80vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              管理权限 — {selectedRole?.name}
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-2 py-2">
            {permissions.map((perm) => (
              <div
                key={perm.id}
                className={`flex items-center justify-between rounded-lg border p-3 cursor-pointer transition-colors ${
                  rolePerms.has(perm.key)
                    ? "border-primary/50 bg-primary/5"
                    : "border-border hover:bg-accent/50"
                }`}
                onClick={() => togglePerm(perm.key)}
              >
                <div className="flex items-center gap-3">
                  <div className={`flex h-5 w-5 items-center justify-center rounded border ${
                    rolePerms.has(perm.key)
                      ? "border-primary bg-primary text-primary-foreground"
                      : "border-muted-foreground/30"
                  }`}>
                    {rolePerms.has(perm.key) && <Check className="h-3 w-3" />}
                  </div>
                  <div>
                    <p className="text-sm font-medium font-mono">{perm.key}</p>
                    <p className="text-xs text-muted-foreground">{perm.description}</p>
                  </div>
                </div>
              </div>
            ))}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPermDialogOpen(false)}>
              取消
            </Button>
            <Button onClick={savePermissions} disabled={saving}>
              {saving && <Loader2 className="h-4 w-4 mr-1 animate-spin" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ─── Users Tab ─────────────────────────────────────────

function UsersTab() {
  const [users, setUsers] = useState<UserWithRoles[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedUser, setSelectedUser] = useState<UserWithRoles | null>(null);
  const [userRoles, setUserRoles] = useState<UserRoleAssignment[]>([]);
  const [userPerms, setUserPerms] = useState<string[]>([]);
  const [allRoles, setAllRoles] = useState<Role[]>([]);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [assignRoleId, setAssignRoleId] = useState("");

  const loadUsers = useCallback(async () => {
    setLoading(true);
    const { success, data } = await adminFetch<{ users: UserWithRoles[] }>(
      "/api/v2/admin/rbac/users", { silent: true },
    );
    if (success && data) setUsers(data.users ?? []);
    setLoading(false);
  }, []);

  const loadRoles = useCallback(async () => {
    const { success, data } = await adminFetch<{ roles: Role[] }>(
      "/api/v2/admin/rbac/roles", { silent: true },
    );
    if (success && data) setAllRoles(data.roles ?? []);
  }, []);

  useEffect(() => { loadUsers(); loadRoles(); }, [loadUsers, loadRoles]);

  const openUserDialog = async (user: UserWithRoles) => {
    setSelectedUser(user);
    const { success, data } = await adminFetch<{
      roles: UserRoleAssignment[];
      permissions: string[];
    }>(`/api/v2/admin/rbac/users/${user.id}/roles`, { silent: true });
    if (success && data) {
      setUserRoles(data.roles ?? []);
      setUserPerms(data.permissions ?? []);
    }
    setDialogOpen(true);
  };

  const assignRole = async () => {
    if (!selectedUser || !assignRoleId) return;
    const { success } = await adminMutate(
      `/api/v2/admin/rbac/users/${selectedUser.id}/roles`,
      {
        method: "POST",
        body: JSON.stringify({ role_id: assignRoleId }),
        successTitle: "角色已分配",
      },
    );
    if (success) {
      setAssignRoleId("");
      // Reload user roles
      openUserDialog(selectedUser);
      loadUsers();
    }
  };

  const removeRole = async (roleId: string) => {
    if (!selectedUser) return;
    if (!window.confirm("确认移除此角色？")) return;
    const { success } = await adminMutate(
      `/api/v2/admin/rbac/users/${selectedUser.id}/roles/${roleId}`,
      { method: "DELETE", successTitle: "角色已移除" },
    );
    if (success && selectedUser) {
      openUserDialog(selectedUser);
      loadUsers();
    }
  };

  if (loading) return <AdminLoading />;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          共 {users.length} 个用户
        </p>
        <Button variant="ghost" size="sm" onClick={loadUsers}>
          <RefreshCw className="h-4 w-4 mr-1" />
          刷新
        </Button>
      </div>

      {users.length === 0 ? (
        <AdminEmptyState icon={Users} title="暂无用户" description="注册用户后将显示在此列表中" />
      ) : (
        <Card>
          <div className="divide-y">
            {users.map((user) => (
              <div
                key={user.id}
                className="flex items-center justify-between p-4 hover:bg-accent/50 cursor-pointer transition-colors"
                onClick={() => openUserDialog(user)}
              >
                <div className="flex items-center gap-3">
                  <div className="flex h-9 w-9 items-center justify-center rounded-full bg-muted text-sm font-medium">
                    {(user.name || user.uid || "?").slice(0, 2).toUpperCase()}
                  </div>
                  <div>
                    <p className="text-sm font-medium">{user.name || user.uid || "未知"}</p>
                    <p className="text-xs text-muted-foreground font-mono">{user.id.slice(0, 8)}</p>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Badge variant={user.role === "admin" ? "default" : "secondary"} className="text-xs">
                    {user.role}
                  </Badge>
                  {user.roles.map((r) => (
                    <Badge key={r.id} variant="outline" className="text-xs">
                      {r.name}
                    </Badge>
                  ))}
                  <ChevronRight className="h-4 w-4 text-muted-foreground" />
                </div>
              </div>
            ))}
          </div>
        </Card>
      )}

      {/* User Role Management Dialog */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>
              角色管理 — {selectedUser?.name || selectedUser?.uid}
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            {/* Current roles */}
            <div className="space-y-2">
              <p className="text-sm font-medium">当前角色</p>
              {userRoles.length === 0 ? (
                <p className="text-sm text-muted-foreground">暂无角色分配</p>
              ) : (
                <div className="space-y-2">
                  {userRoles.map((role) => (
                    <div
                      key={role.id}
                      className="flex items-center justify-between rounded-lg border p-2"
                    >
                      <div>
                        <p className="text-sm font-medium">{role.name}</p>
                        <p className="text-xs text-muted-foreground">{role.description}</p>
                      </div>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 hover:text-destructive"
                        onClick={() => removeRole(role.id)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Effective permissions */}
            <div className="space-y-2">
              <p className="text-sm font-medium">有效权限 ({userPerms.length})</p>
              <div className="flex flex-wrap gap-1">
                {userPerms.length === 0 || userPerms[0] === "*" ? (
                  userPerms[0] === "*" ? (
                    <Badge className="bg-green-100 text-green-700">全部权限 (admin)</Badge>
                  ) : (
                    <p className="text-sm text-muted-foreground">暂无权限</p>
                  )
                ) : (
                  userPerms.map((p) => (
                    <Badge key={p} variant="outline" className="font-mono text-[10px]">
                      {p}
                    </Badge>
                  ))
                )}
              </div>
            </div>

            {/* Assign new role */}
            <div className="space-y-2">
              <p className="text-sm font-medium">分配新角色</p>
              <div className="flex gap-2">
                <select
                  className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm"
                  value={assignRoleId}
                  onChange={(e) => setAssignRoleId(e.target.value)}
                >
                  <option value="">选择角色...</option>
                  {allRoles
                    .filter((r) => !userRoles.some((ur) => ur.id === r.id))
                    .map((r) => (
                      <option key={r.id} value={r.id}>
                        {r.name} — {r.description}
                      </option>
                    ))}
                </select>
                <Button size="sm" onClick={assignRole} disabled={!assignRoleId}>
                  <Plus className="h-4 w-4 mr-1" />
                  分配
                </Button>
              </div>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
