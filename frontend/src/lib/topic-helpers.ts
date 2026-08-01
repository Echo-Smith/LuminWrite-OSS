/**
 * 选题中心 — 平台展示常量与工具函数
 */
import type { Topic } from "@/lib/types";

export const PLATFORM_LABELS: Record<string, string> = {
  tencent: "腾讯新闻",
  weibo: "微博热搜",
  zhihu: "知乎热榜",
  baidu: "百度热搜",
  douyin: "抖音热搜",
  bilibili: "B站热搜",
  toutiao: "头条热搜",
  kuaishou: "快手热搜",
  qq_news: "腾讯热搜",
  user: "自定义",
  system: "系统",
  unknown: "未分类",
};

export const PLATFORM_COLORS: Record<string, string> = {
  tencent: "bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-950/50 dark:text-blue-300 dark:hover:bg-blue-900/50",
  weibo: "bg-red-100 text-red-700 hover:bg-red-200 dark:bg-red-950/50 dark:text-red-300 dark:hover:bg-red-900/50",
  zhihu: "bg-purple-100 text-purple-700 hover:bg-purple-200 dark:bg-purple-950/50 dark:text-purple-300 dark:hover:bg-purple-900/50",
  baidu: "bg-green-100 text-green-700 hover:bg-green-200 dark:bg-green-950/50 dark:text-green-300 dark:hover:bg-green-900/50",
  douyin: "bg-pink-100 text-pink-700 hover:bg-pink-200 dark:bg-pink-950/50 dark:text-pink-300 dark:hover:bg-pink-900/50",
  bilibili: "bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-950/50 dark:text-blue-300 dark:hover:bg-blue-900/50",
  toutiao: "bg-orange-100 text-orange-700 hover:bg-orange-200 dark:bg-orange-950/50 dark:text-orange-300 dark:hover:bg-orange-900/50",
  kuaishou: "bg-yellow-100 text-yellow-700 hover:bg-yellow-200 dark:bg-yellow-950/50 dark:text-yellow-300 dark:hover:bg-yellow-900/50",
  qq_news: "bg-blue-100 text-blue-700 hover:bg-blue-200 dark:bg-blue-950/50 dark:text-blue-300 dark:hover:bg-blue-900/50",
  user: "bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-gray-800/50 dark:text-gray-300 dark:hover:bg-gray-700/50",
  system: "bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-gray-800/50 dark:text-gray-300 dark:hover:bg-gray-700/50",
  unknown: "bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-gray-800/50 dark:text-gray-300 dark:hover:bg-gray-700/50",
};

export const PLATFORM_DOT_COLORS: Record<string, string> = {
  tencent: "bg-blue-500",
  weibo: "bg-red-500",
  zhihu: "bg-purple-500",
  baidu: "bg-green-500",
  douyin: "bg-pink-500",
  bilibili: "bg-blue-400",
  toutiao: "bg-orange-500",
  kuaishou: "bg-yellow-500",
  qq_news: "bg-blue-500",
  user: "bg-gray-400",
  system: "bg-gray-400",
  unknown: "bg-gray-400",
};

export function platformLabel(p?: string) {
  if (!p) return "未分类";
  return PLATFORM_LABELS[p] || p;
}

export function platformColor(p?: string) {
  if (!p) return PLATFORM_COLORS.unknown;
  return PLATFORM_COLORS[p] || PLATFORM_COLORS.unknown;
}

export function platformDotColor(p?: string) {
  if (!p) return PLATFORM_DOT_COLORS.unknown;
  return PLATFORM_DOT_COLORS[p] || PLATFORM_DOT_COLORS.unknown;
}

export function styleLabel(style?: string) {
  switch (style) {
    case "yinyue": return "预设评论风格";
    case "shenlun": return "申论";
    case "xiaohongshu": return "小红书";
    default: return style ?? "";
  }
}

/**
 * 判断一个选题是否属于当前筛选视图。
 *
 * 这是前端唯一的过滤判断逻辑——fetchTopics 通过不同的 API 端点实现服务端过滤，
 * SSE 推送则通过此函数实现客户端过滤，确保两者一致。
 *
 * 规则：
 * - "all" / "user"  → 仅 source === "user" 的选题
 * - "hot"           → 仅 source !== "user" 的选题（各平台热搜）
 * - "platform:xxx"  → 仅 platform === "xxx" 的选题
 * - "favorites"     → 不接受 SSE 推送（收藏列表由专用 API 管理）
 * - 其他             → 按 source 字段匹配
 */
export function topicMatchesFilter(topic: Topic, filter: string): boolean {
  if (filter === "favorites") return false;
  if (filter === "all" || filter === "user") return topic.source === "user";
  if (filter === "hot") return topic.source !== "user";
  if (filter.startsWith("platform:")) {
    return topic.platform === filter.slice("platform:".length);
  }
  return topic.source === filter;
}

/**
 * 判断两个选题是否为同一条（用于去重）。
 * 优先用 id 比较；id 缺失时回退到 (title, platform) 组合。
 */
export function isSameTopic(a: Topic, b: Topic): boolean {
  if (a.id && b.id && a.id === b.id) return true;
  return a.title === b.title && (a.platform ?? "") === (b.platform ?? "");
}
