/**
 * 选题中心 — 热搜选题 + 自定义选题 + SSE 实时推送
 *
 * 连接 SSE 端点 /api/v2/sse/topics 实时接收新选题，
 * 新选题到达时顶部弹出提示并可一键开始写作。
 *
 * 文档来源: docs/03-api-specification.md — SSE API
 */
import { useState, useEffect, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, Flame, Clock, Wifi, WifiOff, Bell } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { useSSETopics } from "@/hooks/use-sse-topics";
import type { Topic } from "@/lib/types";
import { cn } from "@/lib/utils";

export function TopicCenter() {
  const navigate = useNavigate();
  const [topics, setTopics] = useState<Topic[]>([]);
  const [loading, setLoading] = useState(false);
  const [filter, setFilter] = useState<"all" | "hotlist" | "user">("all");
  const [showAddDialog, setShowAddDialog] = useState(false);
  const [newTopicTitle, setNewTopicTitle] = useState("");
  const [newTopicDesc, setNewTopicDesc] = useState("");
  const [newTopicNotification, setNewTopicNotification] = useState<Topic | null>(null);

  // SSE 连接：新选题到达时添加到列表并弹出通知
  const handleNewTopic = useCallback((topic: Topic) => {
    setTopics((prev) => {
      // 去重：如果已有同标题的选题则不重复添加
      if (prev.some((t) => t.title === topic.title)) return prev;
      return [topic, ...prev];
    });

    // 弹出通知（3 秒后消失）
    setNewTopicNotification(topic);
    setTimeout(() => setNewTopicNotification(null), 3000);
  }, []);

  const { connected: sseConnected } = useSSETopics(handleNewTopic);

  useEffect(() => {
    fetchTopics();
  }, [filter]);

  const fetchTopics = async () => {
    setLoading(true);
    try {
      const res = await fetch(`/api/v2/topics?source=${filter === "all" ? "" : filter}`);
      const data = await res.json();
      setTopics(data.topics ?? []);
    } catch {
      setTopics([]);
    } finally {
      setLoading(false);
    }
  };

  const handleAddTopic = async () => {
    if (!newTopicTitle.trim()) return;
    try {
      await fetch("/api/v2/topics", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title: newTopicTitle, description: newTopicDesc }),
      });
    } catch {
      // ignore
    }
    setNewTopicTitle("");
    setNewTopicDesc("");
    setShowAddDialog(false);
    fetchTopics();
  };

  const handleStartWriting = (topic: Topic) => {
    navigate(`/write?topic=${encodeURIComponent(topic.title)}`);
  };

  return (
    <div className="flex h-screen flex-col">
      {/* 头部 */}
      <header className="flex items-center justify-between border-b px-6 py-3">
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={() => navigate("/write")}>
            ← 返回
          </Button>
          <Separator orientation="vertical" className="h-5" />
          <h1 className="text-lg font-semibold">选题中心</h1>
          {/* SSE 连接状态 */}
          <span className={cn(
            "flex items-center gap-1 text-xs",
            sseConnected ? "text-green-600" : "text-muted-foreground"
          )}>
            {sseConnected ? <Wifi className="h-3.5 w-3.5" /> : <WifiOff className="h-3.5 w-3.5" />}
            {sseConnected ? "实时" : "离线"}
          </span>
        </div>
        <Button onClick={() => setShowAddDialog(true)} className="gap-1.5">
          <Plus className="h-4 w-4" />
          自定义选题
        </Button>
      </header>

      {/* 新选题通知 */}
      {newTopicNotification && (
        <div className="flex items-center gap-2 border-b bg-blue-50 px-6 py-2">
          <Bell className="h-4 w-4 text-blue-600" />
          <span className="text-sm text-blue-700">
            新选题：{newTopicNotification.title}
          </span>
          <Button
            size="sm"
            variant="outline"
            className="ml-auto h-7 gap-1"
            onClick={() => {
              handleStartWriting(newTopicNotification);
              setNewTopicNotification(null);
            }}
          >
            立即写作 →
          </Button>
        </div>
      )}

      {/* 筛选 */}
      <div className="flex gap-2 border-b px-6 py-2">
        {(["all", "hotlist", "user"] as const).map((f) => (
          <Button
            key={f}
            variant={filter === f ? "secondary" : "ghost"}
            size="sm"
            onClick={() => setFilter(f)}
          >
            {f === "all" ? "全部" : f === "hotlist" ? "🔥 热搜" : "✏️ 自定义"}
          </Button>
        ))}
      </div>

      {/* 选题列表 */}
      <ScrollArea className="flex-1">
        <div className="p-6">
          {loading ? (
            <div className="text-center text-muted-foreground">加载中...</div>
          ) : topics.length === 0 ? (
            <div className="text-center text-muted-foreground py-12">
              <Flame className="h-12 w-12 mx-auto mb-3 opacity-20" />
              <p>暂无选题</p>
              <p className="text-xs mt-1">点击右上角添加自定义选题</p>
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
              {topics.map((topic) => (
                <div
                  key={topic.id ?? topic.title}
                  className="rounded-lg border p-4 hover:shadow-md transition-shadow cursor-pointer"
                  onClick={() => handleStartWriting(topic)}
                >
                  <div className="mb-2 flex items-center gap-2">
                    {topic.hot_rank && (
                      <Badge className="bg-orange-100 text-orange-700 hover:bg-orange-200">
                        #{topic.hot_rank}
                      </Badge>
                    )}
                    <Badge variant="secondary">
                      {topic.platform || topic.source}
                    </Badge>
                  </div>
                  <h3 className="mb-2 font-medium">{topic.title}</h3>
                  {topic.description && (
                    <p className="mb-3 line-clamp-2 text-sm text-muted-foreground">
                      {topic.description}
                    </p>
                  )}
                  <div className="flex items-center justify-between">
                    {topic.fetched_at && (
                      <span className="flex items-center gap-1 text-xs text-muted-foreground">
                        <Clock className="h-3 w-3" />
                        {new Date(topic.fetched_at).toLocaleDateString()}
                      </span>
                    )}
                    <span className="text-sm text-primary hover:underline">
                      开始写作 →
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </ScrollArea>

      {/* 添加选题弹窗 */}
      {showAddDialog && (
        <div className="fixed inset-0 flex items-center justify-center bg-black/50">
          <div className="w-96 rounded-lg bg-background p-6 shadow-lg">
            <h2 className="mb-4 text-lg font-semibold">自定义选题</h2>
            <Input
              value={newTopicTitle}
              onChange={(e) => setNewTopicTitle(e.target.value)}
              placeholder="选题标题"
              className="mb-3"
            />
            <Textarea
              value={newTopicDesc}
              onChange={(e) => setNewTopicDesc(e.target.value)}
              placeholder="选题描述（可选）"
              className="mb-4 h-24"
            />
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setShowAddDialog(false)}>
                取消
              </Button>
              <Button onClick={handleAddTopic}>添加</Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
