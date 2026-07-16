/**
 * useTopics — 选题中心数据管理 Hook
 *
 * 统一管理选题列表、平台统计、推荐、收藏、详情等数据获取逻辑。
 *
 * 缓存策略：
 * - AI 推荐和平台统计通过 topic-cache-store 缓存到组件生命周期之外，
 *   首次进入自动拉取一次，之后仅手动刷新。
 * - 选题列表随 filter 变化实时拉取（轻量查询）。
 * - 收藏 ID 列表每次进入拉取（需要准确状态）。
 * - SSE 实时推送按当前 filter 过滤后增量更新列表。
 */
import { useState, useEffect, useCallback, useRef } from "react";
import { useAuthStore } from "@/stores/auth-store";
import { useTopicCacheStore } from "@/stores/topic-cache-store";
import { useSSETopics } from "@/hooks/use-sse-topics";
import { topicMatchesFilter, isSameTopic } from "@/lib/topic-helpers";
import type { Topic, WritingAngle, RelatedArticle, PlatformStat, TrendPoint } from "@/lib/types";

function unwrap<T>(data: unknown, key: string): T[] {
  const obj = data as Record<string, unknown>;
  return (obj[key] ?? (obj.data as Record<string, unknown>)?.[key] ?? []) as T[];
}

export function useTopics() {
  const { getAuthHeaders } = useAuthStore();

  // ── 缓存 store（跨 mount 保持） ──
  const {
    recommendations, platformStats, recsLoading,
    ensureRecommendations, refreshRecommendations,
    ensurePlatformStats, refreshPlatformStats,
  } = useTopicCacheStore();

  // ── 本地状态（每次 mount 初始化） ──
  const [topics, setTopics] = useState<Topic[]>([]);
  const [loading, setLoading] = useState(false);
  const [filter, setFilter] = useState<string>("user");

  const [favoriteIds, setFavoriteIds] = useState<Set<string>>(new Set());
  const [fetchingHot, setFetchingHot] = useState(false);

  // Detail dialog state
  const [detailTopic, setDetailTopic] = useState<Topic | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [writingAngles, setWritingAngles] = useState<WritingAngle[]>([]);
  const [relatedArticles, setRelatedArticles] = useState<RelatedArticle[]>([]);
  const [isFavorited, setIsFavorited] = useState(false);
  const [trendData, setTrendData] = useState<TrendPoint[]>([]);

  // SSE — 用 ref 跟踪当前 filter，回调保持稳定（空依赖）
  const filterRef = useRef(filter);
  useEffect(() => { filterRef.current = filter; }, [filter]);

  const handleNewTopic = useCallback((topic: Topic) => {
    if (!topicMatchesFilter(topic, filterRef.current)) return;
    setTopics((prev) => {
      if (prev.some((t) => isSameTopic(t, topic))) return prev;
      return [topic, ...prev];
    });
  }, []);

  const handleUpdateTopic = useCallback((topic: Topic) => {
    setTopics((prev) => prev.map((t) =>
      isSameTopic(t, topic) ? { ...t, ...topic } : t,
    ));
  }, []);

  const { connected: sseConnected } = useSSETopics(handleNewTopic, handleUpdateTopic);

  // ── Fetchers（纯函数，传给 store） ──
  const fetchRecommendationsFn = useCallback(async (force = false): Promise<Topic[]> => {
    const url = force ? "/api/v2/topics/recommend?force=1" : "/api/v2/topics/recommend";
    const res = await fetch(url, { headers: getAuthHeaders() });
    const data = await res.json();
    return unwrap<Topic>(data, "recommendations");
  }, [getAuthHeaders]);

  const fetchPlatformStatsFn = useCallback(async (): Promise<PlatformStat[]> => {
    const res = await fetch("/api/v2/topics/platforms", { headers: getAuthHeaders() });
    const data = await res.json();
    return unwrap<PlatformStat>(data, "platforms");
  }, [getAuthHeaders]);

  // ── 选题列表（随 filter 变化） ──
  const fetchTopics = useCallback(async () => {
    setLoading(true);
    try {
      let url = "/api/v2/topics";
      if (filter === "favorites") {
        url = "/api/v2/topics/favorites";
      } else if (filter === "all") {
        url = "/api/v2/topics?source=user";
      } else if (filter.startsWith("platform:")) {
        url = `/api/v2/topics/platforms/${encodeURIComponent(filter.slice("platform:".length))}`;
      } else if (filter === "hot") {
        url = "/api/v2/topics?source=hot";
      } else {
        url = `/api/v2/topics?source=${filter}`;
      }
      const res = await fetch(url, { headers: getAuthHeaders() });
      const data = await res.json();
      setTopics(unwrap<Topic>(data, "topics"));
    } catch {
      setTopics([]);
    } finally {
      setLoading(false);
    }
  }, [filter, getAuthHeaders]);

  const fetchFavoriteIds = useCallback(async () => {
    try {
      const res = await fetch("/api/v2/topics/favorites?page_size=100", { headers: getAuthHeaders() });
      const data = await res.json();
      const list = unwrap<Topic>(data, "topics");
      setFavoriteIds(new Set(list.map((t) => t.id)));
    } catch { /* ignore */ }
  }, [getAuthHeaders]);

  // ── 初始化：首次进入拉取缓存数据，每次进入拉取列表和收藏 ──
  useEffect(() => { fetchTopics(); }, [fetchTopics]);
  useEffect(() => {
    fetchFavoriteIds();
    ensureRecommendations(fetchRecommendationsFn);
    ensurePlatformStats(fetchPlatformStatsFn);
  }, [fetchFavoriteIds, ensureRecommendations, fetchRecommendationsFn, ensurePlatformStats, fetchPlatformStatsFn]);

  // ── 手动刷新热搜 ──
  const fetchHotTopics = useCallback(async () => {
    setFetchingHot(true);
    try {
      await fetch("/api/v2/topics/hot", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...getAuthHeaders() },
      });
      await Promise.all([
        fetchTopics(),
        refreshPlatformStats(fetchPlatformStatsFn),
      ]);
    } catch { /* ignore */ }
    finally { setFetchingHot(false); }
  }, [fetchTopics, refreshPlatformStats, fetchPlatformStatsFn, getAuthHeaders]);

  // ── 手动刷新 AI 推荐（强制重新生成，绕过 DB 缓存） ──
  const refreshRecs = useCallback(async () => {
    await refreshRecommendations(() => fetchRecommendationsFn(true));
  }, [refreshRecommendations, fetchRecommendationsFn]);

  // ── 详情 ──
  const openDetail = useCallback(async (topic: Topic) => {
    setDetailTopic(topic);
    setDetailLoading(true);
    setWritingAngles([]);
    setRelatedArticles([]);
    setTrendData([]);
    setIsFavorited(favoriteIds.has(topic.id));
    try {
      const [detailRes, trendRes] = await Promise.all([
        fetch(`/api/v2/topics/${topic.id}/detail`, { headers: getAuthHeaders() }),
        fetch(`/api/v2/topics/${topic.id}/trend?hours=48`, { headers: getAuthHeaders() }),
      ]);
      const detailJson = await detailRes.json();
      const d = (detailJson.data ?? detailJson) as Record<string, unknown>;
      setWritingAngles((d.writing_angles as WritingAngle[]) ?? []);
      setRelatedArticles((d.related_articles as RelatedArticle[]) ?? []);
      setIsFavorited((d.favorited as boolean) ?? false);
      const trendJson = await trendRes.json();
      setTrendData(((trendJson.data ?? trendJson) as Record<string, unknown>).trend as TrendPoint[] ?? []);
    } catch { /* ignore */ }
    finally { setDetailLoading(false); }
  }, [favoriteIds, getAuthHeaders]);

  // ── 收藏 ──
  const toggleFavorite = useCallback(async (topicId: string) => {
    const headers = { "Content-Type": "application/json", ...getAuthHeaders() };
    if (isFavorited) {
      await fetch(`/api/v2/topics/${topicId}/favorite`, { method: "DELETE", headers });
      setIsFavorited(false);
      setFavoriteIds((prev) => { const n = new Set(prev); n.delete(topicId); return n; });
    } else {
      await fetch(`/api/v2/topics/${topicId}/favorite`, { method: "POST", headers });
      setIsFavorited(true);
      setFavoriteIds((prev) => new Set(prev).add(topicId));
    }
  }, [isFavorited, getAuthHeaders]);

  const toggleFavoriteFromCard = useCallback(async (topic: Topic) => {
    const headers = { "Content-Type": "application/json", ...getAuthHeaders() };
    if (favoriteIds.has(topic.id)) {
      await fetch(`/api/v2/topics/${topic.id}/favorite`, { method: "DELETE", headers });
      setFavoriteIds((prev) => { const n = new Set(prev); n.delete(topic.id); return n; });
    } else {
      await fetch(`/api/v2/topics/${topic.id}/favorite`, { method: "POST", headers });
      setFavoriteIds((prev) => new Set(prev).add(topic.id));
    }
  }, [favoriteIds, getAuthHeaders]);

  // ── CRUD ──
  const addTopic = useCallback(async (title: string, description: string) => {
    try {
      await fetch("/api/v2/topics", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...getAuthHeaders() },
        body: JSON.stringify({ title, description }),
      });
    } catch { /* ignore */ }
    fetchTopics();
  }, [fetchTopics, getAuthHeaders]);

  const deleteTopic = useCallback(async (topicId: string) => {
    try {
      await fetch(`/api/v2/topics/${topicId}`, { method: "DELETE", headers: getAuthHeaders() });
      setTopics((prev) => prev.filter((t) => t.id !== topicId));
      setFavoriteIds((prev) => { const n = new Set(prev); n.delete(topicId); return n; });
    } catch { /* ignore */ }
  }, [getAuthHeaders]);

  const updateTopic = useCallback(async (topicId: string, title: string, description: string) => {
    try {
      const res = await fetch(`/api/v2/topics/${topicId}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json", ...getAuthHeaders() },
        body: JSON.stringify({ title, description }),
      });
      const data = await res.json();
      const updated = (data.data ?? data) as Record<string, unknown>;
      setTopics((prev) => prev.map((t) =>
        t.id === topicId ? { ...t, title: updated.title as string, description: updated.description as string } : t,
      ));
    } catch { /* ignore */ }
  }, [getAuthHeaders]);

  return {
    // state
    topics, loading, filter, setFilter,
    platformStats, recommendations, loadingRecs: recsLoading,
    favoriteIds, fetchingHot, sseConnected,
    detailTopic, detailLoading, writingAngles, relatedArticles,
    isFavorited, trendData,
    // actions
    fetchHotTopics, refreshRecs, openDetail, setDetailTopic,
    toggleFavorite, toggleFavoriteFromCard,
    addTopic, deleteTopic, updateTopic,
  };
}
