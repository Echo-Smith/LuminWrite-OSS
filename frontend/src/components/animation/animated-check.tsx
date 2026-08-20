/**
 * AnimatedCheck — SVG path 描画 check 动画
 *
 * 在复制成功、保存完成等场景使用，替代静态 Check 图标。
 * path 从 stroke-dashoffset=24 描画到 0，配合 pop-in 容器动画。
 *
 * @example
 * {copied ? <AnimatedCheck className="h-3.5 w-3.5 text-emerald-600" /> : <Copy ... />}
 */
import { cn } from "@/lib/utils";

interface AnimatedCheckProps {
  className?: string;
}

export function AnimatedCheck({ className }: AnimatedCheckProps) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={3}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={cn("anim-pop-in", className)}
    >
      <path
        d="M5 13l4 4L19 7"
        className="anim-draw-check"
      />
    </svg>
  );
}
