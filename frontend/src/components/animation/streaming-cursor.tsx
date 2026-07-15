/**
 * StreamingCursor — 流式文本光标（用于 AI 输出时）
 *
 * @example
 * <p>{streamingText}<StreamingCursor active={isStreaming} /></p>
 */
import { cn } from "@/lib/utils";

interface StreamingCursorProps {
  active: boolean;
  className?: string;
  color?: string;
}

export function StreamingCursor({ active, className, color }: StreamingCursorProps) {
  if (!active) return null;

  return (
    <span
      className={cn(
        "inline-block h-4 w-[2px] align-text-bottom anim-cursor-blink rounded-sm",
        className
      )}
      style={color ? { background: color } : { background: "hsl(var(--primary))" }}
    />
  );
}
