/**
 * AdminTabbedPage — 合并型管理页的标签容器
 *
 * 用途：将多个相关性高的管理页合并为一个侧边栏入口（如「审计中心」= 操作日志 + 安全审计），
 * 各子页面保持独立组件，通过权限控制每个标签是否可见：
 * - 仅一个标签可见时不渲染标签栏，直接呈现该子页面
 * - 页面级入口权限（任一标签权限命中）由 admin-sidebar 的 PAGE_PERMISSIONS 把关
 */
import type { ReactNode } from "react";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";

export interface AdminTabDef {
  key: string;
  label: string;
  /** 查看该标签所需的 RBAC 权限，null 表示对有页面入口权限的用户均可见 */
  permission: string | null;
  content: ReactNode;
}

export function AdminTabbedPage({ tabs, className }: { tabs: AdminTabDef[]; className?: string }) {
  const hasPermission = useAuthStore((s) => s.hasPermission);

  const visible = tabs.filter((t) => t.permission === null || hasPermission(t.permission));
  if (visible.length === 0) return null;

  // 只剩一个可见标签时无需标签栏
  if (visible.length === 1) return <>{visible[0].content}</>;

  return (
    <Tabs defaultValue={visible[0].key} className={cn("w-full", className)}>
      <div className="px-6 pt-6">
        <TabsList>
          {visible.map((t) => (
            <TabsTrigger key={t.key} value={t.key}>
              {t.label}
            </TabsTrigger>
          ))}
        </TabsList>
      </div>
      {visible.map((t) => (
        <TabsContent key={t.key} value={t.key} className="mt-0">
          {t.content}
        </TabsContent>
      ))}
    </Tabs>
  );
}
