/**
 * AdminLoadingSkeleton — 统一的加载骨架屏
 *
 * 用于 Admin 列表页面的加载状态展示
 */
import { Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";

interface AdminLoadingProps {
  /** 显示文字 */
  text?: string;
  /** 全屏模式 */
  fullPage?: boolean;
  className?: string;
}

export function AdminLoading({ text = "加载中...", fullPage, className }: AdminLoadingProps) {
  const content = (
    <div className={cn("flex items-center justify-center gap-2 text-muted-foreground", className)}>
      <Loader2 className="h-4 w-4 animate-spin" />
      <span className="text-sm">{text}</span>
    </div>
  );

  if (fullPage) {
    return <div className="flex h-full items-center justify-center py-16">{content}</div>;
  }
  return content;
}

/**
 * AdminTableSkeleton — 表格加载骨架屏
 * 显示行数可调的骨架行，模拟表格加载
 */
interface SkeletonProps {
  rows?: number;
  cols?: number;
}

export function AdminTableSkeleton({ rows = 5, cols = 4 }: SkeletonProps) {
  return (
    <div className="space-y-2">
      {/* 表头骨架 */}
      <div className="flex gap-4 px-4 py-2">
        {Array.from({ length: cols }).map((_, i) => (
          <div key={i} className="h-4 flex-1 rounded bg-muted animate-pulse" style={{ maxWidth: `${100 / cols}%` }} />
        ))}
      </div>
      {/* 行骨架 */}
      {Array.from({ length: rows }).map((_, r) => (
        <div key={r} className="flex gap-4 px-4 py-3 border-t">
          {Array.from({ length: cols }).map((_, c) => (
            <div
              key={c}
              className="h-4 flex-1 rounded bg-muted/50 animate-pulse"
              style={{ maxWidth: `${100 / cols}%`, animationDelay: `${r * 50}ms` }}
            />
          ))}
        </div>
      ))}
    </div>
  );
}
