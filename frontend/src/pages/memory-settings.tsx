/**
 * 记忆管理页面 — 设置页独立入口
 *
 * 展示用户三层记忆，支持查看/创建/删除
 */
import { useEffect, useState } from "react";
import { Brain, Trash2, Plus, TrendingUp, Shield, AlertCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { useMemoryStore, type UserMemory } from "@/stores/memory-store";
import { cn } from "@/lib/utils";

const TIER_CONFIG = {
  hard: { label: "硬偏好", icon: Shield, color: "text-blue-600", bg: "bg-blue-50" },
  pattern: { label: "行为模式", icon: TrendingUp, color: "text-green-600", bg: "bg-green-50" },
  feedback: { label: "反馈改进", icon: AlertCircle, color: "text-amber-600", bg: "bg-amber-50" },
};

const CATEGORY_LABELS: Record<string, string> = {
  word_count: "篇幅偏好",
  style: "风格偏好",
  structure: "结构偏好",
  tone: "语气偏好",
  title: "标题偏好",
  topic: "话题偏好",
  argument: "论证模式",
  sentence: "句式偏好",
  mode: "写作模式",
};

function formatConfidence(conf: number): string {
  return `${Math.round(conf * 100)}%`;
}

function formatDate(dateStr: string): string {
  const d = new Date(dateStr);
  const now = new Date();
  const diffDays = Math.floor((now.getTime() - d.getTime()) / (1000 * 60 * 60 * 24));
  if (diffDays === 0) return "今天";
  if (diffDays === 1) return "昨天";
  if (diffDays < 7) return `${diffDays} 天前`;
  return d.toLocaleDateString("zh-CN");
}

export function MemorySettings() {
  const { memories, loading, fetchMemories, createMemory, deleteMemory } = useMemoryStore();
  const [showCreate, setShowCreate] = useState(false);
  const [newCategory, setNewCategory] = useState("");
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");

  useEffect(() => {
    fetchMemories();
  }, [fetchMemories]);

  const handleCreate = async () => {
    if (!newCategory || !newKey || !newValue) return;
    const ok = await createMemory(newCategory, newKey, newValue);
    if (ok) {
      setShowCreate(false);
      setNewCategory("");
      setNewKey("");
      setNewValue("");
    }
  };

  // Group by tier
  const grouped = memories.reduce((acc, m) => {
    if (!acc[m.tier]) acc[m.tier] = [];
    acc[m.tier].push(m);
    return acc;
  }, {} as Record<string, UserMemory[]>);

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-primary/10">
            <Brain className="h-5 w-5 text-primary" />
          </div>
          <div>
            <h1 className="text-xl font-bold">记忆管理</h1>
            <p className="text-sm text-muted-foreground">
              AI 根据你的写作习惯和反馈自动学习，你也可以手动管理
            </p>
          </div>
        </div>
        <Dialog open={showCreate} onOpenChange={setShowCreate}>
          <DialogTrigger asChild>
            <Button size="sm" className="gap-2">
              <Plus className="h-4 w-4" />
              添加偏好
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>添加硬偏好</DialogTitle>
            </DialogHeader>
            <div className="space-y-4 pt-2">
              <div>
                <Label>类别</Label>
                <Input
                  className="mt-1.5"
                  placeholder="如：word_count, style, tone"
                  value={newCategory}
                  onChange={(e) => setNewCategory(e.target.value)}
                />
              </div>
              <div>
                <Label>标识</Label>
                <Input
                  className="mt-1.5"
                  placeholder="如：preferred_length"
                  value={newKey}
                  onChange={(e) => setNewKey(e.target.value)}
                />
              </div>
              <div>
                <Label>内容</Label>
                <Input
                  className="mt-1.5"
                  placeholder="如：偏好 800-1000 字"
                  value={newValue}
                  onChange={(e) => setNewValue(e.target.value)}
                />
              </div>
              <Button className="w-full" onClick={handleCreate} disabled={!newCategory || !newKey || !newValue}>
                创建
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      </div>

      <Separator />

      {/* Memory tiers */}
      {loading ? (
        <div className="py-12 text-center text-muted-foreground">加载中...</div>
      ) : memories.length === 0 ? (
        <Card>
          <CardContent className="py-12 text-center">
            <Brain className="mx-auto h-12 w-12 text-muted-foreground/30" />
            <p className="mt-3 text-sm text-muted-foreground">
              暂无记忆数据。写作几次后，AI 会自动学习你的偏好。
            </p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-6">
          {(["hard", "pattern", "feedback"] as const).map((tier) => {
            const tierMemories = grouped[tier] ?? [];
            if (tierMemories.length === 0) return null;
            const config = TIER_CONFIG[tier];
            const Icon = config.icon;

            return (
              <div key={tier}>
                <div className="mb-3 flex items-center gap-2">
                  <Icon className={cn("h-4 w-4", config.color)} />
                  <h2 className="text-sm font-semibold">{config.label}</h2>
                  <Badge variant="secondary" className="text-xs">{tierMemories.length}</Badge>
                </div>
                <div className="space-y-2">
                  {tierMemories.map((mem) => (
                    <Card key={mem.id} className="overflow-hidden">
                      <CardContent className="flex items-start gap-3 py-3">
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="text-xs text-muted-foreground">
                              {CATEGORY_LABELS[mem.category] ?? mem.category}
                            </span>
                            {mem.status === "candidate" && (
                              <Badge variant="outline" className="text-xs text-amber-600">
                                待确认
                              </Badge>
                            )}
                            {mem.quality_source === "workbuddy_adopt" && (
                              <Badge variant="outline" className="text-xs text-blue-600">
                                已录用
                              </Badge>
                            )}
                          </div>
                          <p className="mt-1 text-sm">{mem.value}</p>
                          <div className="mt-1 flex items-center gap-3 text-xs text-muted-foreground">
                            <span>置信度 {formatConfidence(mem.confidence)}</span>
                            {mem.occurrences > 1 && <span>出现 {mem.occurrences} 次</span>}
                            <span>更新于 {formatDate(mem.updated_at)}</span>
                          </div>
                        </div>
                        <button
                          onClick={() => deleteMemory(mem.id)}
                          className="text-muted-foreground hover:text-destructive transition-colors shrink-0"
                          title="删除"
                        >
                          <Trash2 className="h-4 w-4" />
                        </button>
                      </CardContent>
                    </Card>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
