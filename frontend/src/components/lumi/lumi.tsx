import * as React from "react";
import {
  blendSamples,
  easeOutExpo,
  profileToPath,
  sampleLumi,
  staticSample,
  type LumiSample,
  type LumiState,
} from "@/lib/lumi/engine";

export type { LumiState } from "@/lib/lumi/engine";
import { cn } from "@/lib/utils";

/**
 * ─── Lumi · 风格助手笔形动画形象 ─────────────────────────────
 *
 * 静置为钢笔剪影，thinking 收拢为呼吸圆点，writing 摆笔书写并点出墨迹，
 * done 时笔尾弹出星形。视觉语言与写作区一致：墨色随 currentColor，
 * 强调用 --desk-brass 黄铜色。动画引擎见 lib/lumi/engine.ts（纯时间函数）。
 */

const VIEW = 48;
const CENTER = VIEW / 2;
const SCALE = 20;
const TRANSITION_MS = 320;
const BRASS = "var(--desk-brass, #9a6b2f)";

export interface LumiProps {
  /** idle 静置 · thinking 思考圆点 · writing 书写循环 · done 完成星星 */
  state?: LumiState;
  /** 像素尺寸（正方形） */
  size?: number;
  className?: string;
  /** 提供时作为 role=img 的可访问名称，否则视为装饰 */
  label?: string;
}

export function Lumi({ state = "idle", size = 40, className, label }: LumiProps) {
  const groupRef = React.useRef<SVGGElement | null>(null);
  const bodyRef = React.useRef<SVGPathElement | null>(null);
  const inkRef = React.useRef<SVGCircleElement | null>(null);
  const trailRef = React.useRef<SVGPathElement | null>(null);
  const ringRef = React.useRef<SVGCircleElement | null>(null);
  const starRef = React.useRef<SVGPathElement | null>(null);

  const sampleRef = React.useRef<LumiSample>(staticSample(state));
  const prevRef = React.useRef<LumiSample | null>(null);
  const switchRef = React.useRef(0);
  const stateRef = React.useRef(state);

  React.useLayoutEffect(() => {
    if (stateRef.current === state) return;
    prevRef.current = sampleRef.current;
    stateRef.current = state;
    switchRef.current = performance.now();
  }, [state]);

  React.useEffect(() => {
    const paint = (sample: LumiSample) => {
      sampleRef.current = sample;
      bodyRef.current?.setAttribute("d", profileToPath(sample.profile, SCALE));
      groupRef.current?.setAttribute(
        "transform",
        `translate(${CENTER} ${CENTER + sample.offsetY * SCALE}) rotate(${sample.rotation.toFixed(2)})`
      );
      if (inkRef.current) {
        inkRef.current.setAttribute("r", Math.max(0.01, sample.ink * SCALE * 0.1).toFixed(2));
        inkRef.current.setAttribute("opacity", Math.min(1, Math.max(0, sample.ink)).toFixed(2));
        inkRef.current.setAttribute("fill", stateRef.current === "error" ? "var(--destructive, #dc2626)" : BRASS);
      }
      if (trailRef.current) {
        trailRef.current.setAttribute("stroke-dashoffset", (1 - Math.min(1, Math.max(0, sample.trail))).toFixed(3));
      }
      if (ringRef.current) {
        if (sample.pulse >= 0) {
          ringRef.current.setAttribute("r", (SCALE * (0.34 + 0.28 * sample.pulse)).toFixed(2));
          ringRef.current.setAttribute("opacity", (0.32 * (1 - sample.pulse)).toFixed(3));
        } else {
          ringRef.current.setAttribute("opacity", "0");
        }
      }
      if (starRef.current) {
        starRef.current.setAttribute("opacity", Math.min(1, Math.max(0, sample.star)).toFixed(2));
        starRef.current.setAttribute(
          "transform",
          `translate(${SCALE * 0.52} ${-SCALE * 0.66}) scale(${(SCALE * 0.3 * sample.star).toFixed(2)})`
        );
      }
    };

    const media = window.matchMedia("(prefers-reduced-motion: reduce)");
    if (media.matches) {
      paint(staticSample(state));
      return;
    }

    let raf = 0;
    const loop = (now: number) => {
      const since = now - switchRef.current;
      const target = sampleLumi(state, now, since);
      const prev = prevRef.current;
      const current = prev ? blendSamples(prev, target, easeOutExpo(since / TRANSITION_MS)) : target;
      if (prev && since >= TRANSITION_MS) prevRef.current = null;
      paint(current);
      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(raf);
  }, [state]);

  return (
    <svg
      viewBox={`0 0 ${VIEW} ${VIEW}`}
      width={size}
      height={size}
      className={cn("shrink-0 overflow-visible", className)}
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    >
      <g ref={groupRef} transform={`translate(${CENTER} ${CENTER})`}>
        <path ref={bodyRef} className="fill-current" />
        <circle ref={inkRef} cx={0} cy={SCALE * 0.95} r={0.01} fill={BRASS} opacity={0} />
        <path
          ref={trailRef}
          d={`M ${-SCALE * 0.3} ${SCALE * 1.02} Q 0 ${SCALE * 1.16} ${SCALE * 0.28} ${SCALE * 1.0}`}
          fill="none"
          stroke={BRASS}
          strokeWidth={SCALE * 0.07}
          strokeLinecap="round"
          pathLength={1}
          strokeDasharray={1}
          strokeDashoffset={1}
        />
      </g>
      <circle ref={ringRef} cx={CENTER} cy={CENTER} r={0} fill="none" stroke={BRASS} strokeWidth={1.5} opacity={0} />
      <path
        ref={starRef}
        d="M 0 -1 L 0.2245 -0.309 L 0.951 -0.309 L 0.363 0.118 L 0.588 0.809 L 0 0.382 L -0.588 0.809 L -0.363 0.118 L -0.951 -0.309 L -0.2245 -0.309 Z"
        fill={BRASS}
        opacity={0}
      />
    </svg>
  );
}
