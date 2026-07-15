/**
 * AnimatedBar — 动画进度条
 *
 * 支持确定进度和不确定进度（indeterminate）两种模式。
 *
 * @example
 * <AnimatedBar value={0.65} />
 * <AnimatedBar indeterminate />
 */
import { cn } from "@/lib/utils";

interface AnimatedBarProps {
  /** 进度值 0-1（确定模式） */
  value?: number;
  /** 不确定模式 */
  indeterminate?: boolean;
  /** 高度 */
  height?: "thin" | "normal" | "thick";
  /** 颜色 */
  color?: "primary" | "emerald" | "amber" | "red";
  className?: string;
}

const HEIGHT = {
  thin: "h-1",
  normal: "h-1.5",
  thick: "h-2.5",
};

const COLOR = {
  primary: "bg-primary",
  emerald: "bg-emerald-500",
  amber: "bg-amber-500",
  red: "bg-red-500",
};

export function AnimatedBar({
  value,
  indeterminate = false,
  height = "normal",
  color = "primary",
  className,
}: AnimatedBarProps) {
  return (
    <div
      className={cn(
        "relative w-full overflow-hidden rounded-full bg-muted",
        HEIGHT[height],
        className
      )}
    >
      {indeterminate ? (
        <div
          className={cn(
            "absolute inset-y-0 left-0 w-1/4 rounded-full anim-progress-indeterminate",
            COLOR[color]
          )}
        />
      ) : (
        <div
          className={cn(
            "h-full rounded-full transition-ui",
            COLOR[color]
          )}
          style={{
            width: `${Math.min(Math.max((value ?? 0) * 100, 0), 100)}%`,
            transitionProperty: "width",
          }}
        />
      )}
    </div>
  );
}
