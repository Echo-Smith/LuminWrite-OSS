/**
 * 选题中心 — 热搜选题 + 自定义选题 + SSE 实时推送
 *
 * 重构后职责分层：
 * - useTopics hook      → 全部数据获取与状态管理
 * - TopicSidebar        → 左侧导航（全部 / 热搜汇总[可展开] / 自定义 / 收藏）
 * - RecommendationStrip → AI 推荐横幅（仅“全部”视图）
 * - TopicCard           → 单个选题卡片
 * - AddTopicDialog      → 自定义选题弹窗
 * - TopicDetailDialog   → 详情弹窗（AI 写作角度 / 趋势图 / 相关文章）
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, Flame, Wifi, WifiOff, Loader2, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";

import { useTopics } from "@/hooks/use-topics";
import { TopicSidebar } from "@/components/topic/topic-sidebar";
import { RecommendationStrip } from "@/components/topic/recommendation-strip";
import { TopicCard } from "@/components/topic/topic-card";
import { TopicEditDialog } from "@/components/topic/topic-edit-dialog";
import { TopicDetailDialog } from "@/components/topic/topic-detail-dialog";
import type { Topic, WritingAngle } from "@/lib/types";
import { cn } from "@/lib/utils";

export function TopicCenter() {
  const navigate = useNavigate();
  const [hotExpanded, setHotExpanded] = useState(true);
  const [showAddDialog, setShowAddDialog] = useState(false);
  const [editTopic, setEditTopic] = useState<Topic | null>(null);

  const t = useTopics();

  const handleDeleteTopic = async (topicId: string) => {
    if (!confirm("确认删除这个选题？")) return;
    await t.deleteTopic(topicId);
  };

  const handleStartWriting = (topic: Topic, angle?: WritingAngle) => {
    const params = new URLSearchParams();
    params.set("topic", topic.title);
    if (angle?.style) params.set("style", angle.style);
    navigate(`/write?${params.toString()}`);
  };

  return (
    <div className="flex h-screen flex-col">
      {/* ─── Header ─── */}
      <header className="flex items-center justify-between border-b px-6 py-3">
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={() => navigate("/write")}>← 返回</Button>
          <Separator orientation="vertical" className="h-5" />
          <h1 className="text-lg font-semibold">选题中心</h1>
          <span className={cn(
            "flex items-center gap-1 text-xs",
            t.sseConnected ? "text-green-600" : "text-muted-foreground"
          )}>
            {t.sseConnected ? <Wifi className="h-3.5 w-3.5" /> : <WifiOff className="h-3.5 w-3.5" />}
            {t.sseConnected ? "实时" : "离线"}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={t.fetchHotTopics} disabled={t.fetchingHot} className="gap-1.5">
            <RefreshCw className={cn("h-4 w-4", t.fetchingHot && "animate-spin")} />
            {t.fetchingHot ? "抓取中..." : "刷新热搜"}
          </Button>
          <Button onClick={() => setShowAddDialog(true)} className="gap-1.5">
            <Plus className="h-4 w-4" />
            自定义选题
          </Button>
        </div>
      </header>

      {/* ─── Main: Sidebar + Content ─── */}
      <div className="flex flex-1 overflow-hidden">
        <TopicSidebar
          filter={t.filter}
          setFilter={t.setFilter}
          hotExpanded={hotExpanded}
          setHotExpanded={setHotExpanded}
          platformStats={t.platformStats}
        />

        <ScrollArea className="flex-1">
          <div className="p-6">
            {t.filter === "all" && (
              <RecommendationStrip
                recommendations={t.recommendations}
                loading={t.loadingRecs}
                onOpen={t.openDetail}
                onRefresh={t.refreshRecs}
              />
            )}

            {t.loading ? (
              <div className="flex items-center justify-center py-12">
                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
              </div>
            ) : t.topics.length === 0 ? (
              <div className="py-12 text-center text-muted-foreground">
                <Flame className="mx-auto mb-3 h-12 w-12 opacity-20" />
                <p>暂无选题</p>
                <p className="mt-1 text-xs">点击右上角添加自定义选题</p>
              </div>
            ) : (
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
                {t.topics.map((topic) => (
                  <TopicCard
                    key={topic.id ?? topic.title}
                    topic={topic}
                    favorited={t.favoriteIds.has(topic.id)}
                    onOpen={t.openDetail}
                    onToggleFavorite={t.toggleFavoriteFromCard}
                    onDelete={handleDeleteTopic}
                    onEdit={setEditTopic}
                  />
                ))}
              </div>
            )}
          </div>
        </ScrollArea>
      </div>

      {/* ─── Add/Edit Topic Dialog ─── */}
      <TopicEditDialog
        open={showAddDialog}
        topic={null}
        onClose={() => setShowAddDialog(false)}
        onSubmit={(title, desc) => t.addTopic(title, desc)}
      />
      <TopicEditDialog
        open={!!editTopic}
        topic={editTopic}
        onClose={() => setEditTopic(null)}
        onSubmit={(title, desc) => editTopic && t.updateTopic(editTopic.id, title, desc)}
      />

      {/* ─── Topic Detail Dialog ─── */}
      <TopicDetailDialog
        topic={t.detailTopic}
        loading={t.detailLoading}
        writingAngles={t.writingAngles}
        relatedArticles={t.relatedArticles}
        trendData={t.trendData}
        isFavorited={t.isFavorited}
        onToggleFavorite={() => t.detailTopic && t.toggleFavorite(t.detailTopic.id)}
        onClose={() => t.setDetailTopic(null)}
        onStartWriting={(angle) => t.detailTopic && handleStartWriting(t.detailTopic, angle)}
      />
    </div>
  );
}
