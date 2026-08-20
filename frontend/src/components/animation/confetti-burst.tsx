/**
 * ConfettiBurst — 纯 CSS 粒子爆发组件
 *
 * 在挂载时播放一次性粒子爆发动画，0.8s 后自动卸载。
 * 用于质量评分通过、操作成功等庆祝场景。
 *
 * @example
 * {passed && <ConfettiBurst />}
 */
import { useMemo } from "react";

interface ConfettiBurstProps {
  /** 粒子数量，默认 12 */
  count?: number;
  /** 粒子颜色列表 */
  colors?: string[];
  className?: string;
}

const DEFAULT_COLORS = [
  "#10b981", // emerald-500
  "#f59e0b", // amber-500
  "#3b82f6", // blue-500
  "#8b5cf6", // violet-500
  "#ec4899", // pink-500
];

export function ConfettiBurst({
  count = 12,
  colors = DEFAULT_COLORS,
  className,
}: ConfettiBurstProps) {
  // 在挂载时生成一次随机参数，之后不变
  const pieces = useMemo(
    () =>
      Array.from({ length: count }, (_, i) => {
        // 均匀分布角度 + 小随机偏移
        const angle = (i / count) * Math.PI * 2 + (Math.random() - 0.5) * 0.4;
        const distance = 28 + Math.random() * 20; // 28-48px
        const x = Math.cos(angle) * distance;
        const y = Math.sin(angle) * distance - 12; // 向上偏移
        const rot = (Math.random() - 0.5) * 360;
        const color = colors[i % colors.length];
        const delay = Math.random() * 100; // 0-100ms 随机延迟
        return { x, y, rot, color, delay, key: i };
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    []
  );

  return (
    <div
      className={className}
      style={{ position: "relative", pointerEvents: "none" }}
    >
      {pieces.map((p) => (
        <span
          key={p.key}
          className="anim-confetti-piece"
          style={
            {
              "--confetti-x": `${p.x}px`,
              "--confetti-y": `${p.y}px`,
              "--confetti-rot": `${p.rot}deg`,
              background: p.color,
              animationDelay: `${p.delay}ms`,
            } as React.CSSProperties
          }
        />
      ))}
    </div>
  );
}
