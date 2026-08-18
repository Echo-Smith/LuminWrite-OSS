/**
 * AdminEmptyState — 统一的空状态展示
 *
 * 用于列表无数据时展示
 */
import { type LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

interface AdminEmptyStateProps {
  icon: LucideIcon;
  title: string;
  description?: string;
  action?: {
    label: string;
    onClick: () => void;
  };
  className?: string;
}

export function AdminEmptyState({ icon: Icon, title, description, action, className }: AdminEmptyStateProps) {
  return (
    <div className={cn("flex flex-col items-center justify-center py-12 text-center", className)}>
      <Icon className="h-10 w-10 text-muted-foreground/40 mb-3" />
      <p className="text-sm font-medium text-muted-foreground">{title}</p>
      {description && (
        <p className="text-xs text-muted-foreground/70 mt-1 max-w-sm">{description}</p>
      )}
      {action && (
        <button
          onClick={action.onClick}
          className="mt-4 text-sm text-primary hover:underline"
        >
          {action.label}
        </button>
      )}
    </div>
  );
}

/**
 * AdminErrorState — 统一的错误状态展示
 */
interface AdminErrorStateProps {
  message?: string;
  onRetry?: () => void;
  className?: string;
}

export function AdminErrorState({ message = "加载失败", onRetry, className }: AdminErrorStateProps) {
  return (
    <div className={cn("flex flex-col items-center justify-center py-12 text-center", className)}>
      <p className="text-sm text-destructive">{message}</p>
      {onRetry && (
        <button
          onClick={onRetry}
          className="mt-3 text-sm text-primary hover:underline"
        >
          重试
        </button>
      )}
    </div>
  );
}
