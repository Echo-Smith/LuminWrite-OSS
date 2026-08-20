/**
 * PageTransition — 路由过渡包装组件（已简化）
 *
 * 不再使用 key 强制 remount，避免关闭弹窗/路由切换时的异常重渲染。
 * 仅提供透传容器，由 Radix UI / 组件自身控制动画。
 */
import { type ReactNode } from "react";
import { cn } from "@/lib/utils";

interface PageTransitionProps {
  children: ReactNode;
  className?: string;
}

export function PageTransition({ children, className }: PageTransitionProps) {
  return (
    <div className={cn(className)}>
      {children}
    </div>
  );
}
