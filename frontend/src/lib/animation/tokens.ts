/**
 * ─── 动画令牌（TypeScript） ──────────────────────────────────
 *
 * 与 CSS 变量保持同步的 TS 常量，供 JS 侧使用。
 * 用于：framer-motion-like 内联动画、动态延迟计算等。
 */

export const DURATION = {
  instant: "80ms",
  fast: "150ms",
  normal: "250ms",
  slow: "400ms",
  slower: "600ms",
  epic: "800ms",
} as const;

export const EASING = {
  linear: "linear",
  in: "cubic-bezier(0.4, 0, 1, 1)",
  out: "cubic-bezier(0, 0, 0.2, 1)",
  inOut: "cubic-bezier(0.4, 0, 0.2, 1)",
  spring: "cubic-bezier(0.34, 1.56, 0.64, 1)",
  smooth: "cubic-bezier(0.45, 0, 0.15, 1)",
  precise: "cubic-bezier(0.16, 1, 0.3, 1)",
} as const;

export const SCALE = {
  enter: 0.96,
  exit: 1.04,
  hover: 1.02,
  press: 0.98,
} as const;

export const DELAY = {
  d1: 50,
  d2: 100,
  d3: 150,
  d4: 200,
  d5: 250,
  d6: 300,
} as const;

export type Duration = (typeof DURATION)[keyof typeof DURATION];
export type Easing = (typeof EASING)[keyof typeof EASING];
