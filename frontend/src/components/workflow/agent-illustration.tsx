/**
 * AgentIllustration — 极简黑白人形插画
 *
 * 三个角色各一幅：研究员（人+放大镜）、撰稿人（人+笔）、审校（人+盾牌）。
 * 线条风格：1.5px stroke，无填充，黑白色系，与项目整体设计一致。
 *
 * 莫兰迪色卡：用于节点卡片左侧色块背景（浅色低饱和）。
 *   - researcher: 莫兰迪蓝灰 (#a8b8c4)
 *   - writer:     莫兰迪灰绿 (#a8c0b4)
 *   - reviewer:   莫兰迪暖灰 (#c4b8a8)
 *   - default:    莫兰迪紫灰 (#b8aec4)
 */
import { cn } from "@/lib/utils";

// ─── 莫兰迪浅色色卡 ─────────────────────────────────────

export interface RoleTheme {
  /** 莫兰迪浅色背景（色块/卡片头部底色） */
  morandiBg: string;
  /** 莫兰迪深色（边框/强调） */
  morandiBorder: string;
  /** 莫兰迪文字色 */
  morandiText: string;
  /** 原有角色色（保持兼容，用于 handle 等） */
  accentColor: string;
}

export const ROLE_THEMES: Record<string, RoleTheme> = {
  researcher: {
    morandiBg: "#e8edf0",      // 莫兰迪蓝灰浅
    morandiBorder: "#a8b8c4",  // 莫兰迪蓝灰深
    morandiText: "#5a6b78",    // 莫兰迪蓝灰文字
    accentColor: "#3b82f6",
  },
  writer: {
    morandiBg: "#e8f0ea",      // 莫兰迪灰绿浅
    morandiBorder: "#a8c0b4",  // 莫兰迪灰绿深
    morandiText: "#5a6b5e",    // 莫兰迪灰绿文字
    accentColor: "#10b981",
  },
  reviewer: {
    morandiBg: "#f0ebe5",      // 莫兰迪暖灰浅
    morandiBorder: "#c4b8a8",  // 莫兰迪暖灰深
    morandiText: "#6b5e4e",    // 莫兰迪暖灰文字
    accentColor: "#f59e0b",
  },
  default: {
    morandiBg: "#ede8f0",      // 莫兰迪紫灰浅
    morandiBorder: "#b8aec4",  // 莫兰迪紫灰深
    morandiText: "#5e526b",    // 莫兰迪紫灰文字
    accentColor: "#8b5cf6",
  },
};

export function getRoleTheme(role: string): RoleTheme {
  return ROLE_THEMES[role] || ROLE_THEMES.default;
}

// ─── 极简黑白 SVG 人形插画 ───────────────────────────────

interface IllustrationProps {
  className?: string;
  size?: number;
}

/** 研究员：人形 + 放大镜 */
export function ResearcherIllustration({ className, size = 36 }: IllustrationProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 48 48"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={cn("text-zinc-800 dark:text-zinc-200", className)}
    >
      {/* 头 */}
      <circle cx="18" cy="12" r="5" />
      {/* 身体 */}
      <path d="M10 38c0-6 4-10 8-10s8 4 8 10" />
      {/* 右手举放大镜 */}
      <line x1="24" y1="20" x2="32" y2="28" />
      {/* 放大镜 */}
      <circle cx="34" cy="30" r="5" />
      <line x1="38" y1="34" x2="42" y2="38" />
    </svg>
  );
}

/** 撰稿人：人形 + 笔 */
export function WriterIllustration({ className, size = 36 }: IllustrationProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 48 48"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={cn("text-zinc-800 dark:text-zinc-200", className)}
    >
      {/* 头 */}
      <circle cx="18" cy="12" r="5" />
      {/* 身体 */}
      <path d="M10 38c0-6 4-10 8-10s8 4 8 10" />
      {/* 右手执笔 */}
      <line x1="24" y1="20" x2="36" y2="8" />
      {/* 笔尖 */}
      <path d="M34 6l4 4-2 2-4-4z" />
      {/* 写字线 */}
      <line x1="28" y1="28" x2="40" y2="28" />
    </svg>
  );
}

/** 审校：人形 + 盾牌 */
export function ReviewerIllustration({ className, size = 36 }: IllustrationProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 48 48"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={cn("text-zinc-800 dark:text-zinc-200", className)}
    >
      {/* 头 */}
      <circle cx="18" cy="12" r="5" />
      {/* 身体 */}
      <path d="M10 38c0-6 4-10 8-10s8 4 8 10" />
      {/* 右手持盾 */}
      <line x1="24" y1="22" x2="30" y2="22" />
      {/* 盾牌 */}
      <path d="M30 18h8v8c0 4-2 6-4 7-2-1-4-3-4-7z" />
      {/* 盾牌勾 */}
      <path d="M33 24l2 2 3-3" />
    </svg>
  );
}

/** 根据角色名获取对应插画组件 */
export function getRoleIllustration(role: string) {
  switch (role) {
    case "researcher":
      return ResearcherIllustration;
    case "writer":
      return WriterIllustration;
    case "reviewer":
      return ReviewerIllustration;
    default:
      return WriterIllustration;
  }
}
