/**
 * TypingDots — 打字指示器（三点跳动）
 *
 * @example
 * <TypingDots />
 * <TypingDots label="正在思考" />
 */
import { cn } from "@/lib/utils";

interface TypingDotsProps {
  /** 是否显示文字标签 */
  label?: string;
  /** 点的颜色类（默认使用 muted-foreground） */
  color?: string;
  className?: string;
}

export function TypingDots({
  label,
  color,
  className,
}: TypingDotsProps) {
  return (
    <span className={cn("inline-flex items-center gap-2", className)}>
      <span className="motion-typing-dots">
        <span style={color ? { background: color } : undefined} />
        <span style={color ? { background: color } : undefined} />
        <span style={color ? { background: color } : undefined} />
      </span>
      {label && (
        <span className="text-sm text-muted-foreground">{label}</span>
      )}
    </span>
  );
}
