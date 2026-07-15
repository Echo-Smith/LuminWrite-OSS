/**
 * PulseIndicator — 状态指示点（带涟漪扩散）
 *
 * @example
 * <PulseIndicator status="running" />
 * <PulseIndicator status="complete" />
 * <PulseIndicator status="error" />
 */
import { cn } from "@/lib/utils";

type Status = "running" | "complete" | "error" | "idle" | "paused";

interface PulseIndicatorProps {
  status: Status;
  size?: "sm" | "md" | "lg";
  className?: string;
  /** 是否显示涟漪扩散（默认 running 时显示） */
  ring?: boolean;
}

const STATUS_COLOR: Record<Status, string> = {
  running: "bg-blue-500",
  complete: "bg-emerald-500",
  error: "bg-red-500",
  idle: "bg-gray-400",
  paused: "bg-amber-500",
};

const STATUS_RING_COLOR: Record<Status, string> = {
  running: "rgba(59, 130, 246, 0.4)",
  complete: "rgba(16, 185, 129, 0.4)",
  error: "rgba(239, 68, 68, 0.4)",
  idle: "rgba(156, 163, 175, 0.4)",
  paused: "rgba(245, 158, 11, 0.4)",
};

const SIZE: Record<NonNullable<PulseIndicatorProps["size"]>, string> = {
  sm: "h-2 w-2",
  md: "h-2.5 w-2.5",
  lg: "h-3 w-3",
};

export function PulseIndicator({
  status,
  size = "md",
  className,
  ring = true,
}: PulseIndicatorProps) {
  const showRing = ring && status === "running";

  return (
    <span
      className={cn(
        "relative inline-flex shrink-0 rounded-full",
        SIZE[size],
        STATUS_COLOR[status],
        status === "running" && "anim-pulse",
        className
      )}
      style={
        showRing
          ? ({ ["--anim-ring-color" as string]: STATUS_RING_COLOR[status] } as React.CSSProperties)
          : undefined
      }
    >
      {showRing && (
        <span
          className="absolute inset-0 rounded-full anim-pulse-ring"
          style={
            { ["--anim-ring-color" as string]: STATUS_RING_COLOR[status] } as React.CSSProperties
          }
        />
      )}
    </span>
  );
}
