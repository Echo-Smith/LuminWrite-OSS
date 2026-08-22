/**
 * 记忆管理子页面
 */
import { useState, useEffect } from "react";
import {
  Brain, Trash2, Plus, TrendingUp, Shield, AlertCircle,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { useMemoryStore, type UserMemory } from "@/stores/memory-store";
import { cn } from "@/lib/utils";
import { SimpleModal, formatDate } from "./shared";

// ─── 记忆层配置 ──────────────────────────────────────────

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

export function MemorySection() {
  const { memories, loading, fetchMemories, createMemory, deleteMemory } = useMemoryStore();
  const [showCreate, setShowCreate] = useState(false);
  const [newCategory, setNewCategory] = useState("");
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");

  useEffect(() => {
    fetchMemories();
  }, [fetchMemories]);

  // 监听右下角 + 按钮事件
  useEffect(() => {
    const handler = () => setShowCreate(true);
    window.addEventListener("personal-center-add", handler);
    return () => window.removeEventListener("personal-center-add", handler);
  }, []);

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

  const grouped = memories.reduce((acc, m) => {
    if (!acc[m.tier]) acc[m.tier] = [];
    acc[m.tier].push(m);
    return acc;
  }, {} as Record<string, UserMemory[]>);

  return (
    <div className="px-6 pt-6 pb-12 space-y-5">

      {loading ? (
        <div className="py-12 text-center text-muted-foreground text-sm">加载中...</div>
      ) : memories.length === 0 ? (
        <div className="py-12 text-center">
          <Brain className="mx-auto h-12 w-12 text-muted-foreground/30" />
          <p className="mt-3 text-sm text-muted-foreground">
            暂无记忆数据。写作几次后，AI 会自动学习你的偏好。
          </p>
        </div>
      ) : (
        <div className="space-y-5">
          {(["hard", "pattern", "feedback"] as const).map((tier) => {
            const tierMemories = grouped[tier] ?? [];
            if (tierMemories.length === 0) return null;
            const config = TIER_CONFIG[tier];
            const Icon = config.icon;

            return (
              <div key={tier}>
                <div className="mb-2.5 flex items-center gap-2">
                  <Icon className={cn("h-4 w-4", config.color)} />
                  <h3 className="text-sm font-semibold">{config.label}</h3>
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
                            <span>置信度 {Math.round(mem.confidence * 100)}%</span>
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

      {/* 记忆创建弹窗 — 由右下角 + 按钮触发 */}
      <SimpleModal open={showCreate} onClose={() => setShowCreate(false)} title="添加硬偏好">
          <div className="space-y-4">
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
      </SimpleModal>
    </div>
  );
}
