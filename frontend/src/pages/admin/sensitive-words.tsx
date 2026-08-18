/**
 * 敏感词库 — Admin Dashboard
 * 占位页面，接口已预留，后续实现具体功能
 */
import { useState, useEffect, useCallback } from "react";
import { Plus, Trash2, Shield, Info, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription,
} from "@/components/ui/dialog";
import { adminFetch, adminMutate, adminDelete } from "@/lib/admin-api";
import { AdminPageHeader } from "@/components/admin";
import { AdminConfirmDialog } from "@/components/admin";

interface SensitiveWord {
  id: string;
  word: string;
  category: string;
  severity: string;
  action: string;
  replacement?: string | null;
  is_active: boolean;
  created_at: string;
}

const CATEGORIES = [
  { value: "political", label: "政治敏感" },
  { value: "violence", label: "暴力" },
  { value: "privacy", label: "隐私" },
  { value: "clickbait", label: "标题党" },
];

const SEVERITIES = [
  { value: "low", label: "低" },
  { value: "medium", label: "中" },
  { value: "high", label: "高" },
];

const ACTIONS = [
  { value: "warn", label: "警告" },
  { value: "block", label: "拦截" },
  { value: "replace", label: "替换" },
];

const SEVERITY_COLORS: Record<string, string> = {
  low: "bg-blue-100 text-blue-700",
  medium: "bg-yellow-100 text-yellow-700",
  high: "bg-red-100 text-red-700",
};

const ACTION_COLORS: Record<string, string> = {
  warn: "bg-yellow-100 text-yellow-700",
  block: "bg-red-100 text-red-700",
  replace: "bg-green-100 text-green-700",
};

export function SensitiveWordsPage() {
  const [words, setWords] = useState<SensitiveWord[]>([]);
  const [loading, setLoading] = useState(false);
  const [strictness, setStrictness] = useState("standard");
  const [showAdd, setShowAdd] = useState(false);
  const [newWord, setNewWord] = useState({
    word: "",
    category: "clickbait",
    severity: "medium",
    action: "warn",
    replacement: "",
  });
  const [adding, setAdding] = useState(false);

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  const loadWords = useCallback(async () => {
    setLoading(true);
    const { success, data } = await adminFetch<{ words: SensitiveWord[] }>("/api/v2/admin/sensitive-words", { silent: true });
    if (success && data) setWords(data.words ?? []);
    setLoading(false);
  }, []);

  const loadConfig = useCallback(async () => {
    const { success, data } = await adminFetch<{ strictness: string }>("/api/v2/admin/sensitive-words/config", { silent: true });
    if (success && data) setStrictness(data.strictness ?? "standard");
  }, []);

  const handleAdd = async () => {
    if (!newWord.word) return;
    setAdding(true);
    const { success } = await adminMutate("/api/v2/admin/sensitive-words", {
      method: "POST",
      body: JSON.stringify({
        word: newWord.word,
        category: newWord.category,
        severity: newWord.severity,
        action: newWord.action,
        replacement: newWord.replacement || null,
      }),
      successTitle: "敏感词已添加",
      successDesc: newWord.word,
    });
    if (success) {
      setNewWord({ word: "", category: "clickbait", severity: "medium", action: "warn", replacement: "" });
      setShowAdd(false);
      await loadWords();
    }
    setAdding(false);
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    const ok = await adminDelete(
      `/api/v2/admin/sensitive-words/${deleteTarget}`,
      "确认删除此敏感词？",
      "敏感词已删除",
    );
    if (ok) await loadWords();
    setDeleteTarget(null);
  };

  const handleStrictnessChange = async (value: string) => {
    setStrictness(value);
    await adminMutate("/api/v2/admin/sensitive-words/config", {
      method: "PUT",
      body: JSON.stringify({ strictness: value }),
      successTitle: "严格程度已更新",
    });
  };

  useEffect(() => {
    loadWords();
    loadConfig();
  }, [loadWords, loadConfig]);

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <AdminPageHeader
        title="敏感词库"
        action={
          <Button size="sm" onClick={() => setShowAdd(true)}>
            <Plus className="h-4 w-4 mr-2" />
            添加敏感词
          </Button>
        }
      />

      {/* Placeholder Notice */}
      <div className="flex items-start gap-3 rounded-lg border border-blue-200 bg-blue-50 p-4">
        <Info className="h-5 w-5 text-blue-500 flex-shrink-0 mt-0.5" />
        <div className="text-sm text-blue-700">
          <p className="font-medium mb-1">功能预留说明</p>
          <p>
            敏感词库的 API 接口已预留（CRUD + 全局严格程度配置），数据库表 `sensitive_words` 已在迁移中创建。
            后续将接入 V1 的 `sensitiveCheckService.js` 词库，实现完整的敏感词检测和过滤功能。
          </p>
        </div>
      </div>

      {/* Strictness Config */}
      <Card>
        <CardHeader>
          <CardTitle className="text-sm flex items-center gap-2">
            <Shield className="h-4 w-4" />
            全局严格程度
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex items-center gap-4">
            <Select value={strictness} onValueChange={handleStrictnessChange}>
              <SelectTrigger className="w-40"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="loose">宽松</SelectItem>
                <SelectItem value="standard">标准</SelectItem>
                <SelectItem value="strict">严格</SelectItem>
              </SelectContent>
            </Select>
            <span className="text-sm text-muted-foreground">
              {strictness === "loose" && "仅拦截高危词，允许大部分内容通过"}
              {strictness === "standard" && "标准模式，平衡内容质量与安全性"}
              {strictness === "strict" && "严格模式，拦截所有中高危词"}
            </span>
          </div>
        </CardContent>
      </Card>

      {/* Add Sensitive Word Dialog */}
      <Dialog open={showAdd} onOpenChange={(v) => { if (!v && !adding) setShowAdd(false); }}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>添加敏感词</DialogTitle>
            <DialogDescription>
              添加需要检测和过滤的敏感词。
            </DialogDescription>
          </DialogHeader>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <Label>敏感词</Label>
              <Input
                value={newWord.word}
                onChange={(e) => setNewWord({ ...newWord, word: e.target.value })}
                placeholder="如 震惊"
              />
            </div>
            <div>
              <Label>分类</Label>
              <Select value={newWord.category} onValueChange={(v) => setNewWord({ ...newWord, category: v })}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {CATEGORIES.map((c) => <SelectItem key={c.value} value={c.value}>{c.label}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
            <div>
              <Label>严重程度</Label>
              <Select value={newWord.severity} onValueChange={(v) => setNewWord({ ...newWord, severity: v })}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {SEVERITIES.map((s) => <SelectItem key={s.value} value={s.value}>{s.label}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
            <div>
              <Label>动作</Label>
              <Select value={newWord.action} onValueChange={(v) => setNewWord({ ...newWord, action: v })}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {ACTIONS.map((a) => <SelectItem key={a.value} value={a.value}>{a.label}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
            <div className="col-span-2">
              <Label>替换词 (可选)</Label>
              <Input
                value={newWord.replacement}
                onChange={(e) => setNewWord({ ...newWord, replacement: e.target.value })}
                placeholder="如 惊讶"
                disabled={newWord.action !== "replace"}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setShowAdd(false)} disabled={adding}>取消</Button>
            <Button size="sm" onClick={handleAdd} disabled={!newWord.word || adding}>
              {adding ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Plus className="h-4 w-4 mr-2" />}
              添加
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AdminConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => { if (!v) setDeleteTarget(null); }}
        title="删除敏感词"
        description="确认删除此敏感词？此操作不可撤销。"
        confirmText="删除"
        variant="destructive"
        onConfirm={handleDelete}
      />

      {/* Words Table */}
      <div className="rounded-lg border">
        <table className="w-full">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="text-left p-3 text-sm font-medium">敏感词</th>
              <th className="text-left p-3 text-sm font-medium">分类</th>
              <th className="text-left p-3 text-sm font-medium">严重程度</th>
              <th className="text-left p-3 text-sm font-medium">动作</th>
              <th className="text-left p-3 text-sm font-medium">替换词</th>
              <th className="text-right p-3 text-sm font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={6} className="p-8 text-center">
                  <Loader2 className="h-4 w-4 animate-spin mx-auto" />
                </td>
              </tr>
            ) : words.length === 0 ? (
              <tr>
                <td colSpan={6} className="p-8 text-center text-muted-foreground text-sm">
                  暂无敏感词记录。数据库表可能尚未初始化，后续接入 V1 词库后将有数据。
                </td>
              </tr>
            ) : (
              words.map((word) => (
                <tr key={word.id} className="border-b last:border-0 hover:bg-accent/30">
                  <td className="p-3 text-sm font-medium">{word.word}</td>
                  <td className="p-3 text-sm">
                    {CATEGORIES.find((c) => c.value === word.category)?.label ?? word.category}
                  </td>
                  <td className="p-3">
                    <Badge variant="outline" className={SEVERITY_COLORS[word.severity] ?? ""}>
                      {SEVERITIES.find((s) => s.value === word.severity)?.label ?? word.severity}
                    </Badge>
                  </td>
                  <td className="p-3">
                    <Badge variant="outline" className={ACTION_COLORS[word.action] ?? ""}>
                      {ACTIONS.find((a) => a.value === word.action)?.label ?? word.action}
                    </Badge>
                  </td>
                  <td className="p-3 text-sm text-muted-foreground">
                    {word.replacement || "—"}
                  </td>
                  <td className="p-3 text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setDeleteTarget(word.id)}
                      title="删除"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
