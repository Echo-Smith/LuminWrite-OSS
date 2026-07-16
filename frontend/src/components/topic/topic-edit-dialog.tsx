/**
 * TopicEditDialog — 自定义选题添加/编辑弹窗
 *
 * 当 topic 为 null 时为"添加"模式，否则为"编辑"模式。
 */
import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import type { Topic } from "@/lib/types";

interface TopicEditDialogProps {
  open: boolean;
  topic?: Topic | null;
  onClose: () => void;
  onSubmit: (title: string, description: string) => void;
}

export function TopicEditDialog({ open, topic, onClose, onSubmit }: TopicEditDialogProps) {
  const [title, setTitle] = useState("");
  const [desc, setDesc] = useState("");

  const isEdit = !!topic;

  // 打开弹窗时同步初始值
  useEffect(() => {
    if (open) {
      setTitle(topic?.title ?? "");
      setDesc(topic?.description ?? "");
    }
  }, [open, topic]);

  if (!open) return null;

  const handleSubmit = () => {
    if (!title.trim()) return;
    onSubmit(title, desc);
    onClose();
  };

  return (
    <div className="fixed inset-0 flex items-center justify-center bg-black/50">
      <div className="w-96 rounded-lg border bg-background p-6 shadow-lg">
        <h2 className="mb-4 text-lg font-semibold">{isEdit ? "编辑选题" : "自定义选题"}</h2>
        <Input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="选题标题"
          className="mb-3"
          autoFocus
        />
        <Textarea
          value={desc}
          onChange={(e) => setDesc(e.target.value)}
          placeholder="选题描述（可选）"
          className="mb-4 h-24"
        />
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={handleSubmit}>{isEdit ? "保存" : "添加"}</Button>
        </div>
      </div>
    </div>
  );
}
