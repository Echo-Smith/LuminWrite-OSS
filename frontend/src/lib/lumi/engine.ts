/**
 * ─── Lumi 变形引擎 ──────────────────────────────────────────
 *
 * 风格助手的笔形动画核心。参考 bloub（Grok blob 复刻）的思路：
 * - 形状 = 径向轮廓（radial profile），关键帧之间对半径数组插值
 * - sampleLumi 是纯时间函数：同样输入永远得到同样输出，无 DOM、无时钟、可测试
 * - 过渡用指数缓出（ease-out expo），从不过冲
 *
 * 单位空间：以中心为原点、基准半径 1 的极坐标轮廓，由渲染层映射到 viewBox。
 */

export type LumiState = "idle" | "thinking" | "writing" | "done" | "error" | "paused";

export interface LumiSample {
  /** 径向轮廓半径数组（单位空间，基准 1），长度恒为 LUMI_PROFILE_SAMPLES */
  profile: number[];
  /** 笔身旋转角（度） */
  rotation: number;
  /** 笔身竖直下移量（单位空间） */
  offsetY: number;
  /** 笔尖墨点缩放（0 = 不可见） */
  ink: number;
  /** 笔尖墨迹进度（0..1，驱动 dash） */
  trail: number;
  /** 完成星星缩放（0..1） */
  star: number;
  /** 思考脉冲环进度（0..1，<0 表示无脉冲） */
  pulse: number;
}

export const LUMI_PROFILE_SAMPLES = 64;

interface Pt {
  x: number;
  y: number;
}

/** 静置钢笔的多边形顶点（y 向下），矩形笔身 + 底部锥形笔尖 */
const PEN_POLYGON: Pt[] = [
  { x: -0.16, y: -0.66 },
  { x: -0.13, y: -0.76 },
  { x: 0.13, y: -0.76 },
  { x: 0.16, y: -0.66 },
  { x: 0.16, y: 0.5 },
  { x: 0, y: 0.95 },
  { x: -0.16, y: 0.5 },
];

/** 思考态圆点的基准半径 */
export const LUMI_DOT_RADIUS = 0.34;

function polygonCentroid(poly: Pt[]): Pt {
  let a = 0;
  let cx = 0;
  let cy = 0;
  for (let i = 0; i < poly.length; i++) {
    const p = poly[i];
    const q = poly[(i + 1) % poly.length];
    const cross = p.x * q.y - q.x * p.y;
    a += cross;
    cx += (p.x + q.x) * cross;
    cy += (p.y + q.y) * cross;
  }
  a *= 3;
  return { x: cx / a, y: cy / a };
}

/**
 * 从多边形质心向各采样角做射线，取与边界的最近交点距离，
 * 得到星形凸轮廓的径向函数。钢笔轮廓为凸多边形，射线恰交边界一次。
 */
function radialProfile(poly: Pt[]): number[] {
  const c = polygonCentroid(poly);
  const shifted = poly.map((p) => ({ x: p.x - c.x, y: p.y - c.y }));
  const profile: number[] = new Array(LUMI_PROFILE_SAMPLES);
  for (let i = 0; i < LUMI_PROFILE_SAMPLES; i++) {
    const theta = (i / LUMI_PROFILE_SAMPLES) * Math.PI * 2;
    const d = { x: Math.cos(theta), y: Math.sin(theta) };
    let best = Infinity;
    for (let e = 0; e < shifted.length; e++) {
      const p = shifted[e];
      const q = shifted[(e + 1) % shifted.length];
      const ex = q.x - p.x;
      const ey = q.y - p.y;
      const crossDE = d.x * ey - d.y * ex;
      if (Math.abs(crossDE) < 1e-9) continue;
      // 射线 origin + t·d 与线段 p + s·(q-p) 联立（2D 叉积）：
      const t = (p.x * ey - p.y * ex) / crossDE;
      const s = -(d.x * p.y - d.y * p.x) / crossDE;
      if (t > 0 && s >= 0 && s <= 1 && t < best) best = t;
    }
    profile[i] = Number.isFinite(best) ? best : 0;
  }
  return profile;
}

const COS: number[] = new Array(LUMI_PROFILE_SAMPLES);
const SIN: number[] = new Array(LUMI_PROFILE_SAMPLES);
for (let i = 0; i < LUMI_PROFILE_SAMPLES; i++) {
  const theta = (i / LUMI_PROFILE_SAMPLES) * Math.PI * 2;
  COS[i] = Math.cos(theta);
  SIN[i] = Math.sin(theta);
}

/** 静置钢笔轮廓（模块级预计算一次） */
export const LUMI_PEN_PROFILE = radialProfile(PEN_POLYGON);

const PEN_PROFILE_STATIC = LUMI_PEN_PROFILE.slice();

function dotProfile(breath: number): number[] {
  const r = LUMI_DOT_RADIUS * breath;
  return new Array(LUMI_PROFILE_SAMPLES).fill(r);
}

/** 指数缓出，u∈[0,1]；bloub 原则：从不过冲 */
export function easeOutExpo(u: number): number {
  const x = Math.min(1, Math.max(0, u));
  return x >= 1 ? 1 : 1 - Math.pow(2, -10 * x);
}

function easeInOutCubic(u: number): number {
  const x = Math.min(1, Math.max(0, u));
  return x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2;
}

const WRITING_PERIOD_MS = 1600;
const THINKING_BREATH_MS = 1800;
const PULSE_PERIOD_MS = 1400;
const SWAY_PERIOD_MS = 6000;
const STAR_POP_MS = 450;

/** 书写循环关键帧：[phase, rotation, offsetY, ink, trail] */
const WRITING_KEYFRAMES: Array<[number, number, number, number, number]> = [
  [0.0, 10, 0.06, 0.9, 0.15],
  [0.35, -9, 0.1, 0.7, 0.75],
  [0.55, 8, 0.22, 1.5, 0.2],
  [0.75, 11, 0.07, 0.95, 0.1],
  [1.0, 10, 0.06, 0.9, 0.15],
];

function writingSample(tMs: number): LumiSample {
  const phase = (tMs % WRITING_PERIOD_MS) / WRITING_PERIOD_MS;
  let i = 0;
  while (i < WRITING_KEYFRAMES.length - 2 && WRITING_KEYFRAMES[i + 1][0] < phase) i++;
  const a = WRITING_KEYFRAMES[i];
  const b = WRITING_KEYFRAMES[i + 1];
  const span = b[0] - a[0];
  const u = easeInOutCubic(span > 0 ? (phase - a[0]) / span : 0);
  const lerp = (p: number, q: number) => p + (q - p) * u;
  return {
    profile: PEN_PROFILE_STATIC,
    rotation: lerp(a[1], b[1]),
    offsetY: lerp(a[2], b[2]),
    ink: lerp(a[3], b[3]),
    trail: lerp(a[4], b[4]),
    star: 0,
    pulse: -1,
  };
}

/**
 * 采样指定状态在 tMs（绝对时间，驱动循环）与 sinceMs（状态已持续时长，
 * 驱动入场动画）下的画面。纯函数。
 */
export function sampleLumi(state: LumiState, tMs: number, sinceMs: number): LumiSample {
  switch (state) {
    case "idle": {
      const rotation = 1.2 * Math.sin((2 * Math.PI * tMs) / SWAY_PERIOD_MS);
      return { profile: PEN_PROFILE_STATIC, rotation, offsetY: 0, ink: 0, trail: 0, star: 0, pulse: -1 };
    }
    case "writing":
      return writingSample(tMs);
    case "thinking": {
      const breath = 1 + 0.05 * Math.sin((2 * Math.PI * tMs) / THINKING_BREATH_MS);
      const pulse = (tMs % PULSE_PERIOD_MS) / PULSE_PERIOD_MS;
      return { profile: dotProfile(breath), rotation: 0, offsetY: 0, ink: 0, trail: 0, star: 0, pulse };
    }
    case "done": {
      const star = easeOutExpo(sinceMs / STAR_POP_MS);
      return { profile: PEN_PROFILE_STATIC, rotation: 0, offsetY: 0, ink: 0, trail: 0, star, pulse: -1 };
    }
    case "error": {
      // 断墨：笔身歪倒、笔尖触底，墨点涨出又收小，表达一次失败
      const settled = easeOutExpo(sinceMs / 500);
      const ink = 1.3 - 0.6 * settled;
      return { profile: PEN_PROFILE_STATIC, rotation: 72 * settled, offsetY: 0.3 * settled, ink, trail: 0, star: 0, pulse: -1 };
    }
    case "paused": {
      // 平放搁置：笔躺倒在桌面，呼吸放缓到近乎静止
      const settled = easeOutExpo(sinceMs / 450);
      const drift = 0.8 * Math.sin((2 * Math.PI * tMs) / 6000) * settled;
      return { profile: PEN_PROFILE_STATIC, rotation: 90 * settled + drift, offsetY: 0.34 * settled, ink: 0, trail: 0, star: 0, pulse: -1 };
    }
  }
}

/** 在两个采样之间线性混合，u=0 得 a，u=1 得 b。用于状态过渡。 */
export function blendSamples(a: LumiSample, b: LumiSample, u: number): LumiSample {
  const k = Math.min(1, Math.max(0, u));
  if (k <= 0) return { ...a, profile: a.profile.slice() };
  if (k >= 1) return { ...b, profile: b.profile.slice() };
  const lerp = (p: number, q: number) => p + (q - p) * k;
  const profile = a.profile.map((r, i) => lerp(r, b.profile[i]));
  return {
    profile,
    rotation: lerp(a.rotation, b.rotation),
    offsetY: lerp(a.offsetY, b.offsetY),
    ink: lerp(a.ink, b.ink),
    trail: lerp(a.trail, b.trail),
    star: lerp(a.star, b.star),
    pulse: a.pulse >= 0 && b.pulse >= 0 ? lerp(a.pulse, b.pulse) : b.pulse >= 0 ? b.pulse * k : a.pulse * (1 - k) - k,
  };
}

/** 静止代表帧：reduced-motion 与首帧渲染用 */
export function staticSample(state: LumiState): LumiSample {
  switch (state) {
    case "thinking":
      return { profile: dotProfile(1), rotation: 0, offsetY: 0, ink: 0, trail: 0, star: 0, pulse: -1 };
    case "writing":
      return { profile: PEN_PROFILE_STATIC, rotation: 10, offsetY: 0.06, ink: 0.9, trail: 0.5, star: 0, pulse: -1 };
    case "done":
      return { profile: PEN_PROFILE_STATIC, rotation: 0, offsetY: 0, ink: 0, trail: 0, star: 1, pulse: -1 };
    case "error":
      return { profile: PEN_PROFILE_STATIC, rotation: 72, offsetY: 0.3, ink: 0.7, trail: 0, star: 0, pulse: -1 };
    case "paused":
      return { profile: PEN_PROFILE_STATIC, rotation: 90, offsetY: 0.34, ink: 0, trail: 0, star: 0, pulse: -1 };
    default:
      return { profile: PEN_PROFILE_STATIC, rotation: 0, offsetY: 0, ink: 0, trail: 0, star: 0, pulse: -1 };
  }
}

/** 单位空间轮廓 → viewBox 路径（中点二次贝塞尔平滑，闭合） */
export function profileToPath(profile: number[], scale: number): string {
  const n = profile.length;
  const pts: Pt[] = new Array(n);
  for (let i = 0; i < n; i++) {
    pts[i] = { x: profile[i] * COS[i] * scale, y: profile[i] * SIN[i] * scale };
  }
  const mid = (p: Pt, q: Pt): Pt => ({ x: (p.x + q.x) / 2, y: (p.y + q.y) / 2 });
  const m0 = mid(pts[n - 1], pts[0]);
  let d = `M ${m0.x.toFixed(2)} ${m0.y.toFixed(2)}`;
  for (let i = 0; i < n; i++) {
    const nxt = pts[(i + 1) % n];
    const m = mid(pts[i], nxt);
    d += ` Q ${pts[i].x.toFixed(2)} ${pts[i].y.toFixed(2)} ${m.x.toFixed(2)} ${m.y.toFixed(2)}`;
  }
  return d + " Z";
}
