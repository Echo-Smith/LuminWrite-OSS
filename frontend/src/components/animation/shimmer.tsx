/**
 * Shimmer — 骨架屏闪光效果
 *
 * @example
 * <Shimmer className="h-4 w-32 rounded" />
 * <Shimmer lines={3} />
 */
import { type ReactNode } from "react";
import { cn } from "@/lib/utils";

interface ShimmerProps {
  /** 多行骨架（自动生成等宽块） */
  lines?: number;
  className?: string;
  children?: ReactNode;
  /** 宽度比例（用于 lines 模式） */
  width?: string;
}

export function Shimmer({ lines, className, children, width = "100%" }: ShimmerProps) {
  if (lines && lines > 0) {
    return (
      <div className="space-y-2">
        {Array.from({ length: lines }).map((_, i) => (
          <div
            key={i}
            className={cn("motion-skeleton h-4 rounded", className)}
            style={{
              width: i === lines - 1 ? "60%" : width,
            }}
          />
        ))}
      </div>
    );
  }

  return (
    <div className={cn("motion-skeleton rounded", className)}>
      {children}
    </div>
  );
}
