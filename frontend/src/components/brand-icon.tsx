/**
 * BrandIcon — 统一品牌图标组件
 *
 * 使用品牌 SVG 的深浅模式双版本，圆角由图像资产统一控制。
 * 全项目唯一品牌图标来源，确保视觉一致性。
 *
 * 主题映射：
 * - 浅色模式：深色版图标（黑底白字）
 * - 深色模式：浅色版图标（白底黑字）
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
  xl:  { container: "h-16 w-16",      text: "text-2xl",         label: "text-xl",   sub: "text-xs" },
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
        "relative flex shrink-0 overflow-hidden rounded-[18.75%]",
        config.container,
        onClick && "cursor-pointer hover:shadow-card-float transition-shadow",
        className
      )}
      onClick={onClick}
    >
      <img
        src="/favicon.svg"
        alt=""
        aria-hidden="true"
        draggable={false}
        className="block h-full w-full object-cover dark:hidden"
      />
      <img
        src="/favicon-dark.svg"
        alt=""
        aria-hidden="true"
        draggable={false}
        className="hidden h-full w-full object-cover dark:block"
      />
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
          笔润智谈 | LuminWrite
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
