/**
 * BrandIcon — 统一品牌图标组件
 *
 * 简洁的 "笔" 字品牌标记 + bg-brand-gradient 圆角容器。
 * 全项目唯一品牌图标来源，确保视觉一致性。
 *
 * 设计来源：terms 页面 header 中的原始方案
 * - 容器：rounded-xl + bg-brand-gradient + shadow-sm
 * - 内容：text-{size} font-bold text-white 的 "笔" 字
 *
 * 尺寸变体（通过 size prop 控制）：
 *   - sm   : h-8  w-8  (32px)  text-sm   → 导航栏、header、法律页面
 *   - md   : h-9 w-9 (36px)  text-[15px] → 侧边栏（默认）
 *   - lg   : h-12 w-12 (48px)  text-lg   → 登录页
 *   - xl   : h-16 w-16 (64px)  text-2xl  → 个人中心 about 区
 */
import { cn } from "@/lib/utils";

type Size = "sm" | "md" | "lg" | "xl";

interface BrandIconProps {
  /** 图标尺寸 */
  size?: Size;
  /** 是否显示 "笔润智谈" 文字标签（仅展开态） */
  showLabel?: boolean;
  /** 是否显示副标题 */
  subtitle?: string;
  /** 额外类名 */
  className?: string;
  /** 点击事件 */
  onClick?: () => void;
}

const SIZE_CONFIG: Record<Size, { container: string; text: string; label: string; sub: string }> = {
  sm:  { container: "h-8 w-8",        text: "text-sm",          label: "text-sm",    sub: "text-[10px]" },
  md:  { container: "h-9 w-9",        text: "text-[15px]",      label: "text-sm",    sub: "text-[11px]" },
  lg:  { container: "h-12 w-12",      text: "text-lg",          label: "text-base",  sub: "text-xs" },
  xl:  { container: "h-16 w-16 rounded-2xl", text: "text-2xl",  label: "text-xl",   sub: "text-xs" },
};

export function BrandIcon({
  size = "md",
  showLabel = false,
  subtitle,
  className,
  onClick,
}: BrandIconProps) {
  const config = SIZE_CONFIG[size];

  const iconContainer = (
    <div
      className={cn(
        "flex items-center justify-center rounded-xl bg-brand-gradient shadow-sm shrink-0",
        config.container,
        onClick && "cursor-pointer hover:shadow-md transition-shadow",
        className
      )}
      onClick={onClick}
    >
      <span className={cn("font-bold text-white select-none", config.text)}>
        笔
      </span>
    </div>
  );

  if (!showLabel) {
    return iconContainer;
  }

  return (
    <div className="flex items-center gap-2.5">
      {iconContainer}
      <div className="flex flex-col min-w-0">
        <span className={cn("font-bold tracking-tight", config.label)}>
          笔润智谈
        </span>
        {subtitle && (
          <span className={cn("text-muted-foreground font-mono-sm", config.sub)}>
            {subtitle}
          </span>
        )}
      </div>
    </div>
  );
}

/**
 * BrandMark — 极简品牌标记（用于 footer、水印等轻量场景）
 * 仅 "笔" 字无容器背景
 */
export function BrandMark({ className, size = "sm" }: Omit<BrandIconProps, "showLabel" | "subtitle"> & { size?: Size }) {
  const config = SIZE_CONFIG[size];
  return (
    <span className={cn("font-bold select-none", config.text, className)}>
      笔
    </span>
  );
}

export default BrandIcon;
