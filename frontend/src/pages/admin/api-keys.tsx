/**
 * MCP 服务密钥管理 — Admin Dashboard
 * 管理 MCP / 第三方服务（如搜索）的 API 密钥
 * LLM 密钥已内聚到「模型配置」页面，此处不再管理
 */
import { useState, useEffect, useCallback, type ReactElement } from "react";
import { Plus, Trash2, Pencil, Key, Zap, Loader2, CheckCircle, XCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";

interface APIKey {
  id: string;
  name: string;
  provider: string;
  category: string;
  key_value?: string;
  base_url: string;
  is_active: boolean;
  last_used_at?: string | null;
  last_check?: string | null;
  last_status: string;
  last_error?: string;
  created_at: string;
}

// Only MCP/service providers — LLM keys are managed in model configs
const MCP_PROVIDERS = [
  { value: "tavily", label: "Tavily Search" },
  { value: "zhihu", label: "知乎" },
  { value: "weknora", label: "知识库" },
  { value: "dashscope", label: "DashScope (Embedding)" },
  { value: "anysearch", label: "AnySearch" },
  { value: "tencent", label: "腾讯新闻" },
  { value: "weibo", label: "微博" },
  { value: "bing", label: "Bing" },
];

const STATUS_ICONS: Record<string, ReactElement> = {
  ok: <CheckCircle className="h-3.5 w-3.5 text-green-500" />,
  fail: <XCircle className="h-3.5 w-3.5 text-red-500" />,
  unknown: <span className="text-xs text-muted-foreground">—</span>,
};

export function APIKeysPage() {
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [loading, setLoading] = useState(false);
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<APIKey | null>(null);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState<string | null>(null);
  const [form, setForm] = useState({
    name: "",
    provider: "tavily",
    key_value: "",
    base_url: "",
    is_active: true,
  });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch("/api/v2/admin/api-keys?category=mcp");
      const json = await res.json();
      if (json.success) setKeys(json.data?.keys ?? []);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleSave = async () => {
    if (!form.name || !form.provider || (!editing && !form.key_value)) return;
    setSaving(true);
    try {
      const url = editing ? `/api/v2/admin/api-keys/${editing.id}` : "/api/v2/admin/api-keys";
      const method = editing ? "PUT" : "POST";
      const body: Record<string, unknown> = { ...form, category: "mcp" };
      if (editing && !form.key_value) delete body.key_value;
      await fetch(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      setShowAdd(false);
      setEditing(null);
      setForm({ name: "", provider: "tavily", key_value: "", base_url: "", is_active: true });
      await load();
    } finally {
      setSaving(false);
    }
  };

  const handleEdit = (k: APIKey) => {
    setEditing(k);
    setForm({ name: k.name, provider: k.provider, key_value: "", base_url: k.base_url, is_active: k.is_active });
    setShowAdd(true);
  };

  const handleDelete = async (id: string) => {
    await fetch(`/api/v2/admin/api-keys/${id}`, { method: "DELETE" });
    await load();
  };

  const handleTest = async (id: string) => {
    setTesting(id);
    try {
      await fetch(`/api/v2/admin/api-keys/${id}/test`, { method: "POST" });
      await load();
    } finally {
      setTesting(null);
    }
  };

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-xl font-semibold">MCP 服务密钥</h2>
          <p className="text-sm text-muted-foreground mt-1">
            管理搜索、知识库等 MCP 服务的 API 密钥。LLM 密钥请在「模型配置」中管理。
          </p>
        </div>
        <Button size="sm" onClick={() => { setShowAdd(!showAdd); setEditing(null); }}>
          <Plus className="h-4 w-4 mr-2" /> 添加密钥
        </Button>
      </div>

      {showAdd && (
        <Card>
          <CardHeader><CardTitle className="text-sm">{editing ? "编辑密钥" : "添加密钥"}</CardTitle></CardHeader>
          <CardContent>
            <div className="grid grid-cols-2 gap-4">
              <div>
                <Label>名称</Label>
                <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="Tavily 搜索密钥" />
              </div>
              <div>
                <Label>服务</Label>
                <Select value={form.provider} onValueChange={(v) => setForm({ ...form, provider: v })}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {MCP_PROVIDERS.map((p) => <SelectItem key={p.value} value={p.value}>{p.label}</SelectItem>)}
                  </SelectContent>
                </Select>
              </div>
              <div>
                <Label>密钥值{editing && " (留空保持不变)"}</Label>
                <Input type="password" value={form.key_value} onChange={(e) => setForm({ ...form, key_value: e.target.value })} placeholder="sk-..." />
              </div>
              <div>
                <Label>Base URL (可选)</Label>
                <Input value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} placeholder="https://api.tavily.com" />
              </div>
            </div>
            <div className="flex items-center gap-2 mt-4">
              <Switch checked={form.is_active} onCheckedChange={(v) => setForm({ ...form, is_active: v })} />
              <Label>启用</Label>
            </div>
            <div className="flex justify-end gap-2 mt-4">
              <Button variant="outline" size="sm" onClick={() => { setShowAdd(false); setEditing(null); }}>取消</Button>
              <Button size="sm" onClick={handleSave} disabled={!form.name || (!editing && !form.key_value) || saving}>
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
              <th className="text-left p-3 text-sm font-medium">名称</th>
              <th className="text-left p-3 text-sm font-medium">服务</th>
              <th className="text-left p-3 text-sm font-medium">密钥</th>
              <th className="text-left p-3 text-sm font-medium">状态</th>
              <th className="text-left p-3 text-sm font-medium">最后检查</th>
              <th className="text-right p-3 text-sm font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={6} className="p-8 text-center"><Loader2 className="h-4 w-4 animate-spin mx-auto" /></td></tr>
            ) : keys.length === 0 ? (
              <tr><td colSpan={6} className="p-8 text-center text-muted-foreground text-sm">暂无 MCP 服务密钥</td></tr>
            ) : (
              keys.map((k) => (
                <tr key={k.id} className="border-b last:border-0 hover:bg-accent/30">
                  <td className="p-3 text-sm font-medium flex items-center gap-2">
                    <Key className="h-3.5 w-3.5 text-muted-foreground" />
                    {k.name}
                  </td>
                  <td className="p-3 text-sm">{MCP_PROVIDERS.find((p) => p.value === k.provider)?.label ?? k.provider}</td>
                  <td className="p-3 text-sm font-mono text-muted-foreground">{k.key_value || "****"}</td>
                  <td className="p-3">
                    <div className="flex items-center gap-2">
                      {STATUS_ICONS[k.last_status] ?? STATUS_ICONS.unknown}
                      <Badge variant="outline" className={k.is_active ? "bg-green-100 text-green-700" : "bg-gray-100 text-gray-500"}>
                        {k.is_active ? "启用" : "禁用"}
                      </Badge>
                      {k.last_error && (
                        <span className="text-xs text-red-500 truncate max-w-[150px]" title={k.last_error}>{k.last_error}</span>
                      )}
                    </div>
                  </td>
                  <td className="p-3 text-sm text-muted-foreground">
                    {k.last_check ? new Date(k.last_check).toLocaleString("zh-CN") : "—"}
                  </td>
                  <td className="p-3 text-right">
                    <div className="flex justify-end gap-1">
                      <Button variant="ghost" size="sm" onClick={() => handleTest(k.id)} disabled={testing === k.id} title="测试连通性">
                        {testing === k.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Zap className="h-3.5 w-3.5" />}
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => handleEdit(k)} title="编辑">
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => handleDelete(k.id)} title="删除">
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
