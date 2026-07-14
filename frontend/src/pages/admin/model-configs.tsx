/**
 * 模型配置 — Admin Dashboard
 */
import { useState, useEffect, useCallback } from "react";
import { Plus, Trash2, Pencil, Cpu, Star, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";

interface ModelConfig {
  id: string;
  provider: string;
  model_name: string;
  display_name: string;
  base_url: string;
  max_tokens: number;
  temperature: number;
  is_default: boolean;
  is_active: boolean;
  capabilities: Record<string, boolean>;
  created_at: string;
  updated_at: string;
}

const PROVIDERS = [
  { value: "deepseek", label: "DeepSeek" },
  { value: "openai", label: "OpenAI" },
  { value: "qwen", label: "通义千问" },
  { value: "claude", label: "Claude" },
];

export function ModelConfigsPage() {
  const [configs, setConfigs] = useState<ModelConfig[]>([]);
  const [loading, setLoading] = useState(false);
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<ModelConfig | null>(null);
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({
    provider: "deepseek",
    model_name: "",
    display_name: "",
    base_url: "",
    max_tokens: 8192,
    temperature: 0.7,
    is_default: false,
    is_active: true,
  });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch("/api/v2/admin/models");
      const json = await res.json();
      if (json.success) setConfigs(json.data?.configs ?? []);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleSave = async () => {
    if (!form.model_name) return;
    setSaving(true);
    try {
      const url = editing ? `/api/v2/admin/models/${editing.id}` : "/api/v2/admin/models";
      const method = editing ? "PUT" : "POST";
      await fetch(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          ...form,
          capabilities: { stream: true, thinking: false, vision: false },
        }),
      });
      setShowAdd(false);
      setEditing(null);
      setForm({ provider: "deepseek", model_name: "", display_name: "", base_url: "", max_tokens: 8192, temperature: 0.7, is_default: false, is_active: true });
      await load();
    } finally {
      setSaving(false);
    }
  };

  const handleEdit = (c: ModelConfig) => {
    setEditing(c);
    setForm({
      provider: c.provider,
      model_name: c.model_name,
      display_name: c.display_name,
      base_url: c.base_url,
      max_tokens: c.max_tokens,
      temperature: c.temperature,
      is_default: c.is_default,
      is_active: c.is_active,
    });
    setShowAdd(true);
  };

  const handleDelete = async (id: string) => {
    await fetch(`/api/v2/admin/models/${id}`, { method: "DELETE" });
    await load();
  };

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">模型配置</h2>
        <Button size="sm" onClick={() => { setShowAdd(!showAdd); setEditing(null); }}>
          <Plus className="h-4 w-4 mr-2" /> 添加模型
        </Button>
      </div>

      {showAdd && (
        <Card>
          <CardHeader><CardTitle className="text-sm">{editing ? "编辑模型" : "添加模型"}</CardTitle></CardHeader>
          <CardContent>
            <div className="grid grid-cols-3 gap-4">
              <div>
                <Label>供应商</Label>
                <Select value={form.provider} onValueChange={(v) => setForm({ ...form, provider: v })}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {PROVIDERS.map((p) => <SelectItem key={p.value} value={p.value}>{p.label}</SelectItem>)}
                  </SelectContent>
                </Select>
              </div>
              <div>
                <Label>模型名称</Label>
                <Input value={form.model_name} onChange={(e) => setForm({ ...form, model_name: e.target.value })} placeholder="deepseek-chat" />
              </div>
              <div>
                <Label>显示名称</Label>
                <Input value={form.display_name} onChange={(e) => setForm({ ...form, display_name: e.target.value })} placeholder="DeepSeek Chat" />
              </div>
              <div>
                <Label>Base URL</Label>
                <Input value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} placeholder="https://api.deepseek.com/v1" />
              </div>
              <div>
                <Label>最大 Tokens</Label>
                <Input type="number" value={form.max_tokens} onChange={(e) => setForm({ ...form, max_tokens: parseInt(e.target.value) || 8192 })} />
              </div>
              <div>
                <Label>Temperature</Label>
                <Input type="number" step="0.1" value={form.temperature} onChange={(e) => setForm({ ...form, temperature: parseFloat(e.target.value) || 0.7 })} />
              </div>
            </div>
            <div className="flex items-center gap-6 mt-4">
              <div className="flex items-center gap-2">
                <Switch checked={form.is_default} onCheckedChange={(v) => setForm({ ...form, is_default: v })} />
                <Label>设为默认</Label>
              </div>
              <div className="flex items-center gap-2">
                <Switch checked={form.is_active} onCheckedChange={(v) => setForm({ ...form, is_active: v })} />
                <Label>启用</Label>
              </div>
            </div>
            <div className="flex justify-end gap-2 mt-4">
              <Button variant="outline" size="sm" onClick={() => { setShowAdd(false); setEditing(null); }}>取消</Button>
              <Button size="sm" onClick={handleSave} disabled={!form.model_name || saving}>
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
              <th className="text-left p-3 text-sm font-medium">模型</th>
              <th className="text-left p-3 text-sm font-medium">供应商</th>
              <th className="text-left p-3 text-sm font-medium">Base URL</th>
              <th className="text-left p-3 text-sm font-medium">Max Tokens</th>
              <th className="text-left p-3 text-sm font-medium">Temp</th>
              <th className="text-left p-3 text-sm font-medium">状态</th>
              <th className="text-right p-3 text-sm font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={7} className="p-8 text-center"><Loader2 className="h-4 w-4 animate-spin mx-auto" /></td></tr>
            ) : configs.length === 0 ? (
              <tr><td colSpan={7} className="p-8 text-center text-muted-foreground text-sm">暂无模型配置</td></tr>
            ) : (
              configs.map((c) => (
                <tr key={c.id} className="border-b last:border-0 hover:bg-accent/30">
                  <td className="p-3 text-sm font-medium flex items-center gap-2">
                    <Cpu className="h-3.5 w-3.5 text-muted-foreground" />
                    {c.display_name || c.model_name}
                    {c.is_default && <Star className="h-3.5 w-3.5 text-yellow-500 fill-yellow-500" />}
                  </td>
                  <td className="p-3 text-sm">{PROVIDERS.find((p) => p.value === c.provider)?.label ?? c.provider}</td>
                  <td className="p-3 text-sm text-muted-foreground truncate max-w-[200px]">{c.base_url || "—"}</td>
                  <td className="p-3 text-sm">{c.max_tokens}</td>
                  <td className="p-3 text-sm">{c.temperature}</td>
                  <td className="p-3">
                    <div className="flex gap-1">
                      {c.is_default && <Badge variant="outline" className="bg-yellow-100 text-yellow-700">默认</Badge>}
                      <Badge variant="outline" className={c.is_active ? "bg-green-100 text-green-700" : "bg-gray-100 text-gray-500"}>
                        {c.is_active ? "启用" : "禁用"}
                      </Badge>
                    </div>
                  </td>
                  <td className="p-3 text-right">
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="sm" onClick={() => handleEdit(c)} title="编辑">
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => handleDelete(c.id)} title="删除">
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
