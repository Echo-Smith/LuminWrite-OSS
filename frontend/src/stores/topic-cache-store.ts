/**
 * TopicCacheStore — 选题缓存 Store
 *
 * 将 AI 推荐和平台统计缓存到组件生命周期之外，避免每次进入选题中心
 * 都重新请求（尤其是 AI 推荐，后端需要调用 LLM，开销大）。
 *
 * 策略：
 * - AI 推荐：首次进入时自动拉取一次（如果缓存为空），之后仅用户手动刷新时重新拉取
 * - 平台统计：首次进入时自动拉取一次，手动刷新热搜后同步刷新
 * - 退出登录时清空缓存
 */
import { create } from "zustand";
import type { Topic, PlatformStat } from "@/lib/types";

interface TopicCacheState {
  // ── 缓存数据 ──
  recommendations: Topic[];
  platformStats: PlatformStat[];
  recsLoaded: boolean;
  statsLoaded: boolean;

  // ── 加载状态 ──
  recsLoading: boolean;

  // ── Actions ──
  /** 仅在缓存为空时拉取 AI 推荐（首次进入） */
  ensureRecommendations: (fetcher: () => Promise<Topic[]>) => Promise<void>;
  /** 强制刷新 AI 推荐（用户手动点击） */
  refreshRecommendations: (fetcher: () => Promise<Topic[]>) => Promise<void>;
  /** 仅在缓存为空时拉取平台统计 */
  ensurePlatformStats: (fetcher: () => Promise<PlatformStat[]>) => Promise<void>;
  /** 强制刷新平台统计 */
  refreshPlatformStats: (fetcher: () => Promise<PlatformStat[]>) => Promise<void>;
  /** 清空缓存（退出登录时调用） */
  clearCache: () => void;
}

export const useTopicCacheStore = create<TopicCacheState>((set, get) => ({
  recommendations: [],
  platformStats: [],
  recsLoaded: false,
  statsLoaded: false,
  recsLoading: false,

  ensureRecommendations: async (fetcher) => {
    if (get().recsLoaded || get().recsLoading) return;
    set({ recsLoading: true });
    try {
      const recs = await fetcher();
      set({ recommendations: recs, recsLoaded: true });
    } catch {
      set({ recommendations: [], recsLoaded: true });
    } finally {
      set({ recsLoading: false });
    }
  },

  refreshRecommendations: async (fetcher) => {
    set({ recsLoading: true });
    try {
      const recs = await fetcher();
      set({ recommendations: recs, recsLoaded: true });
    } catch {
      /* keep previous data on refresh failure */
    } finally {
      set({ recsLoading: false });
    }
  },

  ensurePlatformStats: async (fetcher) => {
    if (get().statsLoaded) return;
    try {
      const stats = await fetcher();
      set({ platformStats: stats, statsLoaded: true });
    } catch {
      set({ platformStats: [], statsLoaded: true });
    }
  },

  refreshPlatformStats: async (fetcher) => {
    try {
      const stats = await fetcher();
      set({ platformStats: stats, statsLoaded: true });
    } catch {
      /* keep previous data on refresh failure */
    }
  },

  clearCache: () => {
    set({
      recommendations: [],
      platformStats: [],
      recsLoaded: false,
      statsLoaded: false,
      recsLoading: false,
    });
  },
}));
