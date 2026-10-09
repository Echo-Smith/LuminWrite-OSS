/**
 * 素材（悬浮窗，/topics）
 *
 * 与个人中心/审计中心/风格和技能同款的 FloatingShell 骨架：左侧菜单
 * 「选题 / 订阅 / 知识库」三个 tab——选题（热搜/自定义/收藏 + AI 推荐）、
 * 订阅（RSS 源管理，由弹窗升级为页签）、知识库（上传/导入的素材库）。
 *
 * 路由：/topics（含 ?tab=materials 直达知识库、?tab=rss 直达订阅；
 * 旧 /materials 重定向至此）。
 */
import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Plus, Flame, Wifi, WifiOff, Loader2, RefreshCw, Compass, Database, Rss } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useTopics } from "@/hooks/use-topics";
import { TopicSidebar } from "@/components/topic/topic-sidebar";
import { RecommendationStrip } from "@/components/topic/recommendation-strip";
import { TopicCard } from "@/components/topic/topic-card";
import { TopicEditDialog } from "@/components/topic/topic-edit-dialog";
import { TopicDetailDialog } from "@/components/topic/topic-detail-dialog";
import { MaterialsTab } from "@/components/topic/materials-tab";
import { RSSSubscriptionsPanel } from "@/components/materials/rss-subscriptions-panel";
import { useWritingRuntimeStore } from "@/stores/writing-runtime-store";
import { buildWritingMessage } from "@/stores/topic-draft-store";
import { listTopicMaterials, searchMaterials } from "@/lib/material-api";
import { toast } from "@/stores/toast-store";
import type { Topic, WritingAngle } from "@/lib/types";
import { cn } from "@/lib/utils";
import { closeOverlayAndBack } from "@/lib/close-overlay";
import { FloatingShell, type FloatingShellEntry } from "@/components/shell/floating-shell";

type TabKey = "topics" | "rss" | "materials";

const ITEMS: FloatingShellEntry[] = [
  { key: "topics", label: "选题", icon: Compass },
  { key: "rss", label: "订阅", icon: Rss },
  { key: "materials", label: "知识库", icon: Database },
];

const META: Record<TabKey, { title: string; subtitle: string }> = {
  topics: { title: "选题", subtitle: "热搜选题 · AI 写作角度 · 自定义选题" },
  rss: { title: "订阅", subtitle: "RSS 源管理：新文章自动抓取入库为素材" },
  materials: { title: "知识库", subtitle: "上传/导入素材，写作时自动检索引用" },
};

export function TopicCenter() {
  const navigate = useNavigate();
  const createSession = useWritingRuntimeStore((s) => s.createSession);
  const startWriting = useWritingRuntimeStore((s) => s.startWriting);
  const [hotExpanded, setHotExpanded] = useState(true);
  const [showAddDialog, setShowAddDialog] = useState(false);
  const [editTopic, setEditTopic] = useState<Topic | null>(null);

  // Tab 状态与 URL 同步（?tab=materials / ?tab=rss）：侧边栏入口可直达对应页签
  const [searchParams, setSearchParams] = useSearchParams();
  const initialTab = (): TabKey => {
    const tab = searchParams.get("tab");
    return tab === "materials" || tab === "rss" ? tab : "topics";
  };
  const [activeTab, setActiveTab] = useState<TabKey>(initialTab);
  const switchTab = (tab: TabKey) => {
    setActiveTab(tab);
    setSearchParams(tab === "topics" ? {} : { tab }, { replace: true });
  };
  const [favoritedAngles, setFavoritedAngles] = useState<Set<string>>(new Set());

  const t = useTopics();

  const handleDeleteTopic = async (topicId: string) => {
    if (!confirm("确认删除这个选题？")) return;
    await t.deleteTopic(topicId);
  };

  // 收藏单个写作角度 — 将角度创建为独立自定义选题并收藏
  const handleFavoriteAngle = async (angle: WritingAngle) => {
    const angleKey = `${angle.angle}|${angle.style}`;
    if (favoritedAngles.has(angleKey)) {
      // 已收藏 — 取消收藏
      setFavoritedAngles((prev) => { const n = new Set(prev); n.delete(angleKey); return n; });
      return;
    }
    // 未收藏 — 创建自定义选题并自动收藏
    const desc = [`风格: ${angle.style}`, angle.word_count > 0 ? `建议字数: ${angle.word_count}` : "", `理由: ${angle.rationale}`].filter(Boolean).join("\n");
    try {
      await fetch("/api/v2/topics", {
        method: "POST",
        headers: { "Content-Type": "application/json", ...({} as Record<string, string>) },
        body: JSON.stringify({ title: angle.angle, description: desc }),
      });
      setFavoritedAngles((prev) => new Set(prev).add(angleKey));
      toast.success("已收藏写作角度", angle.angle);
    } catch {
      toast.error("收藏失败", "请稍后重试");
    }
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
        // 如果关联素材不足，自动搜索知识库补充
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
        topic_url: topic.url || undefined,
      });
      // 素材仅在右侧详情面板的"素材"页签中展示，不在对话框中提示
    }, 200);
  };

  const header = (
    <div className="flex items-center gap-2.5 w-full">
      <div className="flex h-9 w-9 items-center justify-center rounded-full bg-primary/10">
        <Compass className="h-4 w-4 text-primary" />
      </div>
      <div>
        <p className="text-sm font-medium">素材</p>
        <p className="text-[11px] text-muted-foreground">topics &amp; materials</p>
      </div>
    </div>
  );

  return (
    <FloatingShell
      header={header}
      items={ITEMS}
      active={activeTab}
      onItemChange={(k) => switchTab(k as TabKey)}
      meta={META}
      onClose={() => closeOverlayAndBack(navigate)}
    >
      {activeTab === "rss" ? (
        <RSSSubscriptionsPanel />
      ) : activeTab === "topics" ? (
        <div className="flex h-full min-h-0">
          {/* ── 二级侧栏（热搜/自定义/收藏过滤） ── */}
          <TopicSidebar
            filter={t.filter}
            setFilter={t.setFilter}
            hotExpanded={hotExpanded}
            setHotExpanded={setHotExpanded}
            platformStats={t.platformStats}
          />

          <div className="flex-1 min-w-0 overflow-y-auto scrollbar-hide">
            {/* 操作条：实时状态 + 刷新热搜 + 自定义选题 */}
            <div className="flex items-center justify-between gap-2 border-b px-5 py-2.5">
              <span className={cn(
                "flex items-center gap-1 text-xs",
                t.sseConnected ? "text-green-600" : "text-muted-foreground"
              )}>
                {t.sseConnected ? <Wifi className="h-3.5 w-3.5" /> : <WifiOff className="h-3.5 w-3.5" />}
                {t.sseConnected ? "实时" : "离线"}
              </span>
              <div className="flex items-center gap-2">
                {(t.filter === "hot" || t.filter.startsWith("platform:")) && (
                  <Button variant="outline" size="sm" onClick={t.fetchHotTopics} disabled={t.fetchingHot} className="gap-1.5">
                    <RefreshCw className={cn("h-4 w-4", t.fetchingHot && "animate-spin")} />
                    {t.fetchingHot ? "抓取中..." : "刷新热搜"}
                  </Button>
                )}
                {t.filter === "user" && (
                  <Button size="sm" onClick={() => setShowAddDialog(true)} className="gap-1.5">
                    <Plus className="h-4 w-4" />
                    自定义选题
                  </Button>
                )}
              </div>
            </div>

            <div className="p-5">
              {(t.filter === "all" || t.filter === "hot") && (
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
                <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
                  {t.topics.map((topic) => (
                    <TopicCard
                      key={topic.id ?? topic.title}
                      topic={topic}
                      favorited={t.favoriteIds.has(topic.id)}
                      onOpen={t.openDetail}
                      onDelete={handleDeleteTopic}
                      onEdit={setEditTopic}
                    />
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>
      ) : (
        <MaterialsTab />
      )}

      {/* ── Add/Edit Topic Dialog ── */}
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

      {/* ── Topic Detail Dialog ── */}
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
        onRefreshAngles={t.refreshAngles}
        onFavoriteAngle={handleFavoriteAngle}
        favoritedAngles={favoritedAngles}
      />
    </FloatingShell>
  );
}
