/**
 * FadeIn — 包裹子元素，在进入视口或挂载时淡入
 *
 * @example
 * <FadeIn direction="up">
 *   <Card>...</Card>
 * </FadeIn>
 *
 * <FadeIn direction="scale" delay={200} className="z-10">
 *   <LoginCard />
 * </FadeIn>
 */
import { type ReactNode, type CSSProperties, type ElementType } from "react";
import { cn } from "@/lib/utils";
import { useInView, useMounted } from "@/lib/animation/hooks";

interface FadeInProps {
  children: ReactNode;
  /** 动画方向 */
  direction?: "up" | "down" | "none" | "scale" | "blur";
  /** 延迟（ms） */
  delay?: number;
  /** 是否在视口内才触发（默认 true） */
  triggerInView?: boolean;
  /** 只触发一次 */
  once?: boolean;
  className?: string;
  /** 渲染的 HTML 标签 */
  as?: ElementType;
  /** 透传给底层标签的属性 */
  [key: string]: unknown;
}

const DIRECTION_CLASS: Record<NonNullable<FadeInProps["direction"]>, string> = {
  up: "anim-fade-up",
  down: "anim-fade-down",
  none: "anim-fade-in",
  scale: "anim-fade-scale",
  blur: "anim-fade-blur",
};

export function FadeIn({
  children,
  direction = "up",
  delay = 0,
  triggerInView = true,
  once = true,
  className,
  as,
  ...rest
}: FadeInProps) {
  const Tag = (as ?? "div") as ElementType;
  const mounted = useMounted();
  const { ref, inView } = useInView<HTMLElement>({ once });

  const shouldShow = triggerInView ? inView && mounted : mounted;
  const animClass = DIRECTION_CLASS[direction];

  const style: CSSProperties | undefined =
    delay > 0 ? { animationDelay: `${delay}ms` } : undefined;

  return (
    <Tag
      ref={ref}
      className={cn(shouldShow ? animClass : "opacity-0", className)}
      style={style}
      {...rest}
    >
      {children}
    </Tag>
  );
}
