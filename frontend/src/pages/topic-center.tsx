/**
 * 选题中心 — 热搜选题 + 自定义选题 + 素材库（Tab 切换）
 *
 * 合并了原「选题中心」和「我的素材库」两个页面，
 * 通过顶部 Tab 切换「选题」和「素材」视图。
 *
 * 职责分层：
 * - useTopics hook      → 选题数据获取与状态管理
 * - TopicSidebar        → 左侧导航（全部 / 热搜汇总[可展开] / 自定义 / 收藏）
 * - RecommendationStrip → AI 推荐横幅（仅"全部"视图）
 * - TopicCard           → 单个选题卡片
 * - MaterialsTab        → 素材库 Tab 内容
 * - AddTopicDialog      → 自定义选题弹窗
 * - TopicDetailDialog   → 详情弹窗（AI 写作角度 / 趋势图 / 相关文章 / 关联素材）
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, Flame, Wifi, WifiOff, Loader2, RefreshCw, Compass, Database } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";

import { useTopics } from "@/hooks/use-topics";
import { TopicSidebar } from "@/components/topic/topic-sidebar";
import { RecommendationStrip } from "@/components/topic/recommendation-strip";
import { TopicCard } from "@/components/topic/topic-card";
import { TopicEditDialog } from "@/components/topic/topic-edit-dialog";
import { TopicDetailDialog } from "@/components/topic/topic-detail-dialog";
import { MaterialsTab } from "@/components/topic/materials-tab";
import { useAgentStore } from "@/stores/agent-store";
import { buildWritingMessage } from "@/stores/topic-draft-store";
import { listTopicMaterials, searchMaterials } from "@/lib/material-api";
import { toast } from "@/stores/toast-store";
import type { Topic, WritingAngle } from "@/lib/types";
import { cn } from "@/lib/utils";

export function TopicCenter() {
  const navigate = useNavigate();
  const createSession = useAgentStore((s) => s.createSession);
  const startWriting = useAgentStore((s) => s.startWriting);
  const [hotExpanded, setHotExpanded] = useState(true);
  const [showAddDialog, setShowAddDialog] = useState(false);
  const [editTopic, setEditTopic] = useState<Topic | null>(null);
  const [activeTab, setActiveTab] = useState<"topics" | "materials">("topics");

  const t = useTopics();

  const handleDeleteTopic = async (topicId: string) => {
    if (!confirm("确认删除这个选题？")) return;
    await t.deleteTopic(topicId);
  };

  // 直接在选题中心完成全部操作：建会话 + 开始写作 + 跳转
  // 不依赖 WritingWorkspace mount 时消费 draft，避免时机问题
  // 同时自动拉取选题关联的素材内容，注入到 user_materials
  const handleStartWriting = async (topic: Topic, angle?: WritingAngle) => {
    // 1. 新建会话
    createSession();

    // 2. 组装写作指令
    const message = buildWritingMessage({
      topic,
      angle,
      recommendationReason: topic.recommendation_reason,
    });

    const angleStyle = angle?.style;
    const wordLimit = angle?.word_count;

    // 3. 拉取选题关联的素材内容
    const userMaterials: string[] = [];
    if (topic.description) {
      userMaterials.push(topic.description);
    }
    if (topic.id) {
      try {
        const { associations } = await listTopicMaterials(topic.id);
        for (const assoc of associations) {
          if (assoc.material?.content_preview) {
            // 用标题 + 内容预览作为素材标签
            userMaterials.push(`📎 ${assoc.material.title}: ${assoc.material.content_preview}`);
          }
        }
        // 如果关联素材不足，自动搜索素材库补充
        if (associations.length === 0) {
          const results = await searchMaterials(topic.title, 3);
          for (const r of results) {
            if (r.score > 0.3) {
              userMaterials.push(`📎 ${r.title}: ${r.content.slice(0, 300)}`);
            }
          }
        }
      } catch {
        // 素材拉取失败不阻塞写作
      }
    }

    // 4. 跳转到写作页
    navigate("/write");

    // 5. 延迟开始写作，确保 navigate 后 WritingWorkspace 已 mount 并准备好 WS
    setTimeout(() => {
      startWriting({
        message,
        style: angleStyle || "yinyue",
        mode: "writing",
        user_materials: userMaterials.length > 0 ? userMaterials : undefined,
        word_limit: wordLimit && wordLimit > 0 ? wordLimit : undefined,
      });
      if (userMaterials.length > 1) {
        toast.success(`已注入 ${userMaterials.length - 1} 条关联素材`, "素材将作为写作参考");
      }
    }, 200);
  };

  return (
    <div className="flex h-screen flex-col">
      {/* ─── Header ─── */}
      <header className="flex items-center justify-between border-b px-6 py-3">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="sm" onClick={() => navigate("/write")}>← 返回</Button>
          <Separator orientation="vertical" className="h-5" />

          {/* Tab 切换 */}
          <div className="flex items-center gap-1">
            <button
              onClick={() => setActiveTab("topics")}
              className={cn(
                "flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-ui",
                activeTab === "topics"
                  ? "bg-accent text-foreground"
                  : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"
              )}
            >
              <Compass className="h-4 w-4" />
              选题
            </button>
            <button
              onClick={() => setActiveTab("materials")}
              className={cn(
                "flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-ui",
                activeTab === "materials"
                  ? "bg-accent text-foreground"
                  : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"
              )}
            >
              <Database className="h-4 w-4" />
              素材库
            </button>
          </div>

          {activeTab === "topics" && (
            <span className={cn(
              "flex items-center gap-1 text-xs ml-1",
              t.sseConnected ? "text-green-600" : "text-muted-foreground"
            )}>
              {t.sseConnected ? <Wifi className="h-3.5 w-3.5" /> : <WifiOff className="h-3.5 w-3.5" />}
              {t.sseConnected ? "实时" : "离线"}
            </span>
          )}
        </div>

        {/* 右侧操作按钮 — 根据 Tab 切换 */}
        <div className="flex items-center gap-2">
          {activeTab === "topics" ? (
            <>
              <Button variant="outline" size="sm" onClick={t.fetchHotTopics} disabled={t.fetchingHot} className="gap-1.5">
                <RefreshCw className={cn("h-4 w-4", t.fetchingHot && "animate-spin")} />
                {t.fetchingHot ? "抓取中..." : "刷新热搜"}
              </Button>
              <Button onClick={() => setShowAddDialog(true)} className="gap-1.5">
                <Plus className="h-4 w-4" />
                自定义选题
              </Button>
            </>
          ) : null}
        </div>
      </header>

      {/* ─── Main ─── */}
      {activeTab === "topics" ? (
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
      ) : (
        <div className="flex-1 overflow-y-auto">
          <MaterialsTab />
        </div>
      )}

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
