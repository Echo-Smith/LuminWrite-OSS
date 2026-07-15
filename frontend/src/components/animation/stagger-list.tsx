/**
 * StaggerList — 错开入场列表动画
 *
 * 子元素按顺序依次淡入上移，形成流畅的瀑布效果。
 *
 * @example
 * <StaggerList interval={60}>
 *   {items.map(item => <StaggerItem key={item.id}>...</StaggerItem>)}
 * </StaggerList>
 */
import { type ReactNode, type CSSProperties, type ElementType } from "react";
import { cn } from "@/lib/utils";

interface StaggerListProps {
  children: ReactNode;
  /** 错开间隔（ms），默认 50 */
  interval?: number;
  /** 起始延迟（ms） */
  startDelay?: number;
  className?: string;
  /** 渲染标签 */
  as?: ElementType;
  [key: string]: unknown;
}

export function StaggerList({
  children,
  interval = 50,
  startDelay = 0,
  className,
  as,
  ...rest
}: StaggerListProps) {
  const Tag = (as ?? "div") as ElementType;
  return (
    <Tag className={cn(className)} {...rest}>{children}</Tag>
  );
}

interface StaggerItemProps {
  children: ReactNode;
  /** 在列表中的索引 */
  index: number;
  /** 与父级 StaggerList 的 interval 一致 */
  interval?: number;
  /** 与父级 StaggerList 的 startDelay 一致 */
  startDelay?: number;
  /** 动画类型 */
  animation?: "fade-up" | "fade-in" | "slide-right" | "scale";
  className?: string;
  /** 渲染标签 */
  as?: ElementType;
  /** 透传样式 */
  style?: CSSProperties;
  [key: string]: unknown;
}

const ANIM_CLASS = {
  "fade-up": "anim-fade-up",
  "fade-in": "anim-fade-in",
  "slide-right": "anim-slide-right",
  "scale": "anim-fade-scale",
};

export function StaggerItem({
  children,
  index,
  interval = 50,
  startDelay = 0,
  animation = "fade-up",
  className,
  as,
  style: passedStyle,
  ...rest
}: StaggerItemProps) {
  const Tag = (as ?? "div") as ElementType;
  const style: CSSProperties = {
    animationDelay: `${startDelay + index * interval}ms`,
    ...passedStyle,
  };

  return (
    <Tag className={cn(ANIM_CLASS[animation], className)} style={style} {...rest}>
      {children}
    </Tag>
  );
}
