/**
 * 定时任务 — Admin Dashboard
 */
import { useState, useEffect, useCallback } from "react";
import { Plus, Trash2, Pencil, Play, Clock, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";

interface CronJob {
  id: string;
  name: string;
  description: string;
  schedule: string;
  task_type: string;
  task_config: Record<string, unknown>;
  is_active: boolean;
  last_run_at?: string | null;
  next_run_at?: string | null;
  last_status: string;
  last_error?: string;
  run_count: number;
  fail_count: number;
  created_at: string;
}

const TASK_TYPES = [
  { value: "topic_fetch", label: "热点选题拉取" },
  { value: "feedback_aggregate", label: "反馈聚合计算" },
  { value: "eval_run", label: "评测运行" },
  { value: "cleanup", label: "数据清理" },
  { value: "custom", label: "自定义" },
];

const STATUS_COLORS: Record<string, string> = {
  success: "bg-green-100 text-green-700",
  failed: "bg-red-100 text-red-700",
  running: "bg-blue-100 text-blue-700",
  pending: "bg-gray-100 text-gray-500",
};

export function CronJobsPage() {
  const [jobs, setJobs] = useState<CronJob[]>([]);
  const [loading, setLoading] = useState(false);
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<CronJob | null>(null);
  const [saving, setSaving] = useState(false);
  const [running, setRunning] = useState<string | null>(null);
  const [form, setForm] = useState({
    name: "",
    description: "",
    schedule: "",
    task_type: "topic_fetch",
    is_active: true,
  });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch("/api/v2/admin/cron-jobs");
      const json = await res.json();
      if (json.success) setJobs(json.data?.jobs ?? []);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleSave = async () => {
    if (!form.name || !form.schedule || !form.task_type) return;
    setSaving(true);
    try {
      const url = editing ? `/api/v2/admin/cron-jobs/${editing.id}` : "/api/v2/admin/cron-jobs";
      const method = editing ? "PUT" : "POST";
      await fetch(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...form, task_config: {} }),
      });
      setShowAdd(false);
      setEditing(null);
      setForm({ name: "", description: "", schedule: "", task_type: "topic_fetch", is_active: true });
      await load();
    } finally {
      setSaving(false);
    }
  };

  const handleEdit = (j: CronJob) => {
    setEditing(j);
    setForm({ name: j.name, description: j.description, schedule: j.schedule, task_type: j.task_type, is_active: j.is_active });
    setShowAdd(true);
  };

  const handleDelete = async (id: string) => {
    await fetch(`/api/v2/admin/cron-jobs/${id}`, { method: "DELETE" });
    await load();
  };

  const handleRun = async (id: string) => {
    setRunning(id);
    try {
      await fetch(`/api/v2/admin/cron-jobs/${id}/run`, { method: "POST" });
      setTimeout(() => load(), 2000);
    } finally {
      setRunning(null);
    }
  };

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">定时任务</h2>
        <Button size="sm" onClick={() => { setShowAdd(!showAdd); setEditing(null); }}>
          <Plus className="h-4 w-4 mr-2" /> 添加任务
        </Button>
      </div>

      {showAdd && (
        <Card>
          <CardHeader><CardTitle className="text-sm">{editing ? "编辑任务" : "添加任务"}</CardTitle></CardHeader>
          <CardContent>
            <div className="grid grid-cols-2 gap-4">
              <div>
                <Label>任务名称</Label>
                <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="热点选题拉取" />
              </div>
              <div>
                <Label>任务类型</Label>
                <Select value={form.task_type} onValueChange={(v) => setForm({ ...form, task_type: v })}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {TASK_TYPES.map((t) => <SelectItem key={t.value} value={t.value}>{t.label}</SelectItem>)}
                  </SelectContent>
                </Select>
              </div>
              <div>
                <Label>定时表达式</Label>
                <Input value={form.schedule} onChange={(e) => setForm({ ...form, schedule: e.target.value })} placeholder="0 * * * *" />
              </div>
              <div>
                <Label>描述</Label>
                <Input value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} placeholder="每小时从微博/知乎抓取热榜" />
              </div>
            </div>
            <div className="flex items-center gap-2 mt-4">
              <Switch checked={form.is_active} onCheckedChange={(v) => setForm({ ...form, is_active: v })} />
              <Label>启用</Label>
            </div>
            <div className="flex justify-end gap-2 mt-4">
              <Button variant="outline" size="sm" onClick={() => { setShowAdd(false); setEditing(null); }}>取消</Button>
              <Button size="sm" onClick={handleSave} disabled={!form.name || !form.schedule || saving}>
                {saving ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Plus className="h-4 w-4 mr-2" />}
                {editing ? "保存" : "添加"}
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      <div className="rounded-lg border">
        <table className="w-full">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="text-left p-3 text-sm font-medium">任务名称</th>
              <th className="text-left p-3 text-sm font-medium">类型</th>
              <th className="text-left p-3 text-sm font-medium">定时</th>
              <th className="text-left p-3 text-sm font-medium">状态</th>
              <th className="text-left p-3 text-sm font-medium">运行次数</th>
              <th className="text-left p-3 text-sm font-medium">最后运行</th>
              <th className="text-right p-3 text-sm font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={7} className="p-8 text-center"><Loader2 className="h-4 w-4 animate-spin mx-auto" /></td></tr>
            ) : jobs.length === 0 ? (
              <tr><td colSpan={7} className="p-8 text-center text-muted-foreground text-sm">暂无定时任务</td></tr>
            ) : (
              jobs.map((j) => (
                <tr key={j.id} className="border-b last:border-0 hover:bg-accent/30">
                  <td className="p-3 text-sm font-medium flex items-center gap-2">
                    <Clock className="h-3.5 w-3.5 text-muted-foreground" />
                    {j.name}
                  </td>
                  <td className="p-3 text-sm">{TASK_TYPES.find((t) => t.value === j.task_type)?.label ?? j.task_type}</td>
                  <td className="p-3 text-sm font-mono">{j.schedule}</td>
                  <td className="p-3">
                    <div className="flex items-center gap-1">
                      <Badge variant="outline" className={STATUS_COLORS[j.last_status] ?? ""}>{j.last_status}</Badge>
                      {!j.is_active && <Badge variant="outline" className="bg-gray-100 text-gray-500">已禁用</Badge>}
                      {j.last_error && <span className="text-xs text-red-500 truncate max-w-[120px]" title={j.last_error}>{j.last_error}</span>}
                    </div>
                  </td>
                  <td className="p-3 text-sm">
                    <span className="font-mono">{j.run_count}</span>
                    {j.fail_count > 0 && <span className="text-red-500 text-xs ml-1">(失败 {j.fail_count})</span>}
                  </td>
                  <td className="p-3 text-sm text-muted-foreground">
                    {j.last_run_at ? new Date(j.last_run_at).toLocaleString("zh-CN") : "—"}
                  </td>
                  <td className="p-3 text-right">
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="sm" onClick={() => handleRun(j.id)} disabled={running === j.id} title="手动运行">
                        {running === j.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => handleEdit(j)} title="编辑">
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => handleDelete(j.id)} title="删除">
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
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
