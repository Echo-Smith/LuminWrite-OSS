/**
 * PageTransition — 路由过渡包装组件
 *
 * 在路由切换时触发 fade+up 入场动画。
 * 利用 useLocation pathname 作为 key 强制 remount，
 * 新页面挂载时播放 anim-fade-up。
 *
 * @example
 * <Route path="/write" element={
 *   <PageTransition>
 *     <WritingWorkspace />
 *   </PageTransition>
 * } />
 */
import { type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { cn } from "@/lib/utils";

interface PageTransitionProps {
  children: ReactNode;
  className?: string;
}

export function PageTransition({ children, className }: PageTransitionProps) {
  const location = useLocation();
  return (
    <div
      key={location.pathname}
      className={cn("anim-fade-up", className)}
    >
      {children}
    </div>
  );
}
