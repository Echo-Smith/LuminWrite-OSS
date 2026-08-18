/**
 * AdminPageHeader — Admin 页面统一头部
 *
 * 功能：
 * - 标题 + 描述（可选）
 * - 右侧操作区（按钮等）
 * - 统一 padding 和间距
 *
 * 使用示例：
 *   <AdminPageHeader
 *     title="模型配置"
 *     description="输入 Base URL 和 API Key，自动发现可用模型。"
 *     action={<Button>添加模型</Button>}
 *   />
 */
import { type ReactNode } from "react";
import { cn } from "@/lib/utils";

interface AdminPageHeaderProps {
  title: string;
  description?: string;
  /** 右侧操作区 */
  action?: ReactNode;
  className?: string;
}

export function AdminPageHeader({ title, description, action, className }: AdminPageHeaderProps) {
  return (
    <div className={cn("flex items-start justify-between gap-4", className)}>
      <div className="min-w-0">
        <h2 className="text-xl font-semibold tracking-tight">{title}</h2>
        {description && (
          <p className="text-sm text-muted-foreground mt-1">{description}</p>
        )}
      </div>
      {action && (
        <div className="flex items-center gap-2 shrink-0">
          {action}
        </div>
      )}
    </div>
  );
}
