/**
 * ─── 动画预设 ───────────────────────────────────────────────
 *
 * 预定义的动画组合，直接返回 CSS 类名字符串。
 * 可与 cn() 工具函数组合使用。
 *
 * @example
 * import { presets } from "@/lib/animation/presets";
 * <div className={cn("card", presets.fadeUp)}>...</div>
 */
import { cn } from "@/lib/utils";

export const presets = {
  /** 淡入上移（最常用的入场动画） */
  fadeUp: "anim-fade-up",
  /** 淡入 */
  fadeIn: "anim-fade-in",
  /** 淡入 + 缩放 */
  fadeScale: "anim-fade-scale",
  /** 淡入 + 模糊清除（高级感） */
  fadeBlur: "anim-fade-blur",
  /** 从左滑入 */
  slideRight: "anim-slide-right",
  /** 从右滑入 */
  slideLeft: "anim-slide-left",
  /** 弹入 */
  popIn: "anim-pop-in",

  /** Stagger 序列 — 第二项 */
  stagger1: cn("anim-fade-up", "anim-delay-1"),
  /** Stagger 序列 — 第三项 */
  stagger2: cn("anim-fade-up", "anim-delay-2"),
  /** Stagger 序列 — 第四项 */
  stagger3: cn("anim-fade-up", "anim-delay-3"),
  /** Stagger 序列 — 第五项 */
  stagger4: cn("anim-fade-up", "anim-delay-4"),
  /** Stagger 序列 — 第六项 */
  stagger5: cn("anim-fade-up", "anim-delay-5"),

  /** 卡片悬浮 */
  cardHover: "",
  /** 平滑过渡 */
  smooth: "transition-ui",
  /** 精准过渡 */
  precise: "transition-precise",
  /** 弹性过渡 */
  spring: "transition-transform-precise",
} as const;

/**
 * 根据 index 生成 stagger 动画类
 */
export function staggerClass(index: number, base = "anim-fade-up"): string {
  const delays = ["", "anim-delay-1", "anim-delay-2", "anim-delay-3", "anim-delay-4", "anim-delay-5", "anim-delay-6"];
  const delay = delays[Math.min(index, delays.length - 1)];
  return delay ? cn(base, delay) : base;
}

/**
 * 根据 index 生成 stagger 内联样式（用于超过 6 项的情况）
 */
export function staggerStyle(index: number, interval = 50): React.CSSProperties {
  return { animationDelay: `${index * interval}ms` };
}
