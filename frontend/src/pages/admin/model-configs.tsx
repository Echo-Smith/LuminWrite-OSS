/**
 * 模型配置 — Admin Dashboard
 * 支持：输入 base_url + api_key → 自动发现模型列表 → 选择启用
 * API Key 内聚在模型配置中，不再关联独立密钥
 */
import { useState, useEffect, useCallback, type ReactElement } from "react";
import { Plus, Trash2, Pencil, Cpu, Star, Loader2, Key, Zap, Brain, Eye, Search, CheckCircle, XCircle } from "lucide-react";
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
  api_key_id?: string | null;
  api_key?: string;
  has_api_key?: boolean;
  max_tokens: number;
  temperature: number;
  is_default: boolean;
  is_active: boolean;
  capabilities: Record<string, boolean>;
  created_at: string;
  updated_at: string;
}

interface DiscoveredModel {
  id: string;
  owned_by: string;
}

const PROVIDERS = [
  { value: "deepseek", label: "DeepSeek" },
  { value: "kimi", label: "Kimi (Moonshot)" },
  { value: "openai", label: "OpenAI" },
  { value: "qwen", label: "通义千问" },
  { value: "claude", label: "Claude" },
];

const DEFAULT_BASE_URLS: Record<string, string> = {
  deepseek: "https://api.deepseek.com/v1",
  kimi: "https://api.moonshot.cn/v1",
  openai: "https://api.openai.com/v1",
  qwen: "https://dashscope.aliyuncs.com/compatible-mode/v1",
  claude: "https://api.anthropic.com/v1",
};

const STATUS_ICONS: Record<string, ReactElement> = {
  ok: <CheckCircle className="h-3.5 w-3.5 text-green-500" />,
  fail: <XCircle className="h-3.5 w-3.5 text-red-500" />,
  unknown: <span className="text-xs text-muted-foreground">—</span>,
};

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
    api_key: "",
    max_tokens: 8192,
    temperature: 0.7,
    is_default: false,
    is_active: true,
    stream: true,
    thinking: false,
    vision: false,
  });

  // Discover models state
  const [discovering, setDiscovering] = useState(false);
  const [discoveredModels, setDiscoveredModels] = useState<DiscoveredModel[]>([]);
  const [discoverError, setDiscoverError] = useState("");

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

  // ─── Discover models from provider ──────────────────────

  const handleDiscover = async () => {
    if (!form.api_key) {
      setDiscoverError("请先输入 API Key");
      return;
    }
    const baseURL = form.base_url || DEFAULT_BASE_URLS[form.provider] || "";
    if (!baseURL) {
      setDiscoverError("请先输入 Base URL");
      return;
    }

    setDiscovering(true);
    setDiscoverError("");
    setDiscoveredModels([]);

    try {
      const res = await fetch("/api/v2/admin/models/discover", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ base_url: baseURL, api_key: form.api_key }),
      });
      const json = await res.json();
      if (json.success) {
        setDiscoveredModels(json.data?.models ?? []);
      } else {
        setDiscoverError(json.error?.message || "获取模型列表失败");
      }
    } catch (err) {
      setDiscoverError(err instanceof Error ? err.message : "网络错误");
    } finally {
      setDiscovering(false);
    }
  };

  // ─── Model Config handlers ──────────────────────────────

  const handleSave = async () => {
    if (!form.model_name) return;
    setSaving(true);
    try {
      const url = editing ? `/api/v2/admin/models/${editing.id}` : "/api/v2/admin/models";
      const method = editing ? "PUT" : "POST";
      const body: Record<string, unknown> = {
        provider: form.provider,
        model_name: form.model_name,
        display_name: form.display_name || form.model_name,
        base_url: form.base_url || DEFAULT_BASE_URLS[form.provider] || "",
        api_key: form.api_key || undefined,
        max_tokens: form.max_tokens,
        temperature: form.temperature,
        is_default: form.is_default,
        is_active: form.is_active,
        capabilities: {
          stream: form.stream,
          thinking: form.thinking,
          vision: form.vision,
        },
      };
      // Don't send api_key if empty (keep existing on update)
      if (editing && !form.api_key) delete body.api_key;

      await fetch(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      resetForm();
      await load();
    } finally {
      setSaving(false);
    }
  };

  const resetForm = () => {
    setShowAdd(false);
    setEditing(null);
    setDiscoveredModels([]);
    setDiscoverError("");
    setForm({
      provider: "deepseek", model_name: "", display_name: "", base_url: "",
      api_key: "", max_tokens: 8192, temperature: 0.7,
      is_default: false, is_active: true,
      stream: true, thinking: false, vision: false,
    });
  };

  const handleEdit = (c: ModelConfig) => {
    setEditing(c);
    setForm({
      provider: c.provider,
      model_name: c.model_name,
      display_name: c.display_name,
      base_url: c.base_url,
      api_key: "", // don't prefill — user enters new key to replace
      max_tokens: c.max_tokens,
      temperature: c.temperature,
      is_default: c.is_default,
      is_active: c.is_active,
      stream: c.capabilities?.stream ?? true,
      thinking: c.capabilities?.thinking ?? false,
      vision: c.capabilities?.vision ?? false,
    });
    setDiscoveredModels([]);
    setDiscoverError("");
    setShowAdd(true);
  };

  const handleDelete = async (id: string) => {
    await fetch(`/api/v2/admin/models/${id}`, { method: "DELETE" });
    await load();
  };

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-xl font-semibold">模型配置</h2>
          <p className="text-sm text-muted-foreground mt-1">
            输入 Base URL 和 API Key，自动发现可用模型。密钥加密存储在模型配置中。
          </p>
        </div>
        <Button size="sm" onClick={resetForm}>
          <Plus className="h-4 w-4 mr-2" /> 添加模型
        </Button>
      </div>

      {showAdd && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">{editing ? "编辑模型" : "添加模型"}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            {/* Step 1: Provider + Base URL + API Key + Discover */}
            <div className="space-y-3">
              <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
                <span className="flex h-5 w-5 items-center justify-center rounded-full bg-primary/10 text-primary">1</span>
                连接供应商并发现模型
              </div>

              <div className="grid grid-cols-3 gap-4">
                <div>
                  <Label>供应商</Label>
                  <Select
                    value={form.provider}
                    onValueChange={(v) => setForm({ ...form, provider: v, base_url: form.base_url || DEFAULT_BASE_URLS[v] || "", model_name: "", display_name: "" })}
                  >
                    <SelectTrigger><SelectValue /></SelectTrigger>
                    <SelectContent>
                      {PROVIDERS.map((p) => <SelectItem key={p.value} value={p.value}>{p.label}</SelectItem>)}
                    </SelectContent>
                  </Select>
                </div>
                <div className="col-span-2">
                  <Label>Base URL</Label>
                  <Input
                    value={form.base_url}
                    onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                    placeholder={DEFAULT_BASE_URLS[form.provider] || "https://api.example.com/v1"}
                  />
                </div>
                <div className="col-span-2">
                  <Label>API Key {editing && !form.api_key && "(留空保持不变)"}</Label>
                  <Input
                    type="password"
                    value={form.api_key}
                    onChange={(e) => setForm({ ...form, api_key: e.target.value })}
                    placeholder="sk-..."
                  />
                </div>
                <div className="flex items-end">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={handleDiscover}
                    disabled={!form.api_key || discovering}
                    className="w-full"
                  >
                    {discovering ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Search className="h-4 w-4 mr-2" />}
                    获取模型列表
                  </Button>
                </div>
              </div>

              {discoverError && (
                <p className="text-xs text-red-500">{discoverError}</p>
              )}

              {discoveredModels.length > 0 && (
                <div className="rounded-md border bg-muted/30 p-3 max-h-48 overflow-y-auto">
                  <p className="text-xs text-muted-foreground mb-2">发现 {discoveredModels.length} 个可用模型，点击选择：</p>
                  <div className="flex flex-wrap gap-1.5">
                    {discoveredModels.map((m) => (
                      <button
                        key={m.id}
                        onClick={() => setForm({ ...form, model_name: m.id, display_name: m.id })}
                        className={`text-xs px-2.5 py-1 rounded-md border transition-colors ${
                          form.model_name === m.id
                            ? "bg-primary text-primary-foreground border-primary"
                            : "bg-background hover:bg-accent border-border"
                        }`}
                      >
                        {m.id}
                      </button>
                    ))}
                  </div>
                </div>
              )}
            </div>

            {/* Step 2: Model details */}
            <div className="space-y-3 pt-2 border-t">
              <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
                <span className="flex h-5 w-5 items-center justify-center rounded-full bg-primary/10 text-primary">2</span>
                配置模型参数
              </div>

              <div className="grid grid-cols-3 gap-4">
                <div>
                  <Label>模型名称</Label>
                  <Input
                    value={form.model_name}
                    onChange={(e) => setForm({ ...form, model_name: e.target.value })}
                    placeholder="deepseek-v4-flash"
                  />
                </div>
                <div>
                  <Label>显示名称</Label>
                  <Input
                    value={form.display_name}
                    onChange={(e) => setForm({ ...form, display_name: e.target.value })}
                    placeholder="DeepSeek V4 Flash"
                  />
                </div>
                <div>
                  <Label>最大 Tokens</Label>
                  <Input
                    type="number"
                    value={form.max_tokens}
                    onChange={(e) => setForm({ ...form, max_tokens: parseInt(e.target.value) || 8192 })}
                  />
                </div>
                <div>
                  <Label>Temperature</Label>
                  <Input
                    type="number"
                    step="0.1"
                    value={form.temperature}
                    onChange={(e) => setForm({ ...form, temperature: parseFloat(e.target.value) || 0.7 })}
                  />
                </div>
              </div>

              <div className="flex items-center gap-6 pt-2 border-t">
                <span className="text-xs font-medium text-muted-foreground">模型能力</span>
                <div className="flex items-center gap-2">
                  <Switch checked={form.stream} onCheckedChange={(v) => setForm({ ...form, stream: v })} />
                  <Label className="flex items-center gap-1 cursor-pointer" onClick={() => setForm({ ...form, stream: !form.stream })}>
                    <Zap className="h-3 w-3" /> 流式输出
                  </Label>
                </div>
                <div className="flex items-center gap-2">
                  <Switch checked={form.thinking} onCheckedChange={(v) => setForm({ ...form, thinking: v })} />
                  <Label className="flex items-center gap-1 cursor-pointer" onClick={() => setForm({ ...form, thinking: !form.thinking })}>
                    <Brain className="h-3 w-3" /> 深度思考
                  </Label>
                </div>
                <div className="flex items-center gap-2">
                  <Switch checked={form.vision} onCheckedChange={(v) => setForm({ ...form, vision: v })} />
                  <Label className="flex items-center gap-1 cursor-pointer" onClick={() => setForm({ ...form, vision: !form.vision })}>
                    <Eye className="h-3 w-3" /> 视觉理解
                  </Label>
                </div>
              </div>

              <div className="flex items-center gap-6">
                <div className="flex items-center gap-2">
                  <Switch checked={form.is_default} onCheckedChange={(v) => setForm({ ...form, is_default: v })} />
                  <Label>设为默认</Label>
                </div>
                <div className="flex items-center gap-2">
                  <Switch checked={form.is_active} onCheckedChange={(v) => setForm({ ...form, is_active: v })} />
                  <Label>启用</Label>
                </div>
              </div>
            </div>

            <div className="flex justify-end gap-2 pt-2 border-t">
              <Button variant="outline" size="sm" onClick={resetForm}>取消</Button>
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
              <th className="text-left p-3 text-sm font-medium">API Key</th>
              <th className="text-left p-3 text-sm font-medium">能力</th>
              <th className="text-left p-3 text-sm font-medium">Max Tokens</th>
              <th className="text-left p-3 text-sm font-medium">状态</th>
              <th className="text-right p-3 text-sm font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={7} className="p-8 text-center"><Loader2 className="h-4 w-4 animate-spin mx-auto" /></td></tr>
            ) : configs.length === 0 ? (
              <tr><td colSpan={7} className="p-8 text-center text-muted-foreground text-sm">暂无模型配置，点击「添加模型」创建</td></tr>
            ) : (
              configs.map((c) => (
                <tr key={c.id} className="border-b last:border-0 hover:bg-accent/30">
                  <td className="p-3 text-sm font-medium">
                    <div className="flex items-center gap-2">
                      <Cpu className="h-3.5 w-3.5 text-muted-foreground" />
                      <div>
                        <div className="flex items-center gap-1.5">
                          {c.display_name || c.model_name}
                          {c.is_default && <Star className="h-3.5 w-3.5 text-yellow-500 fill-yellow-500" />}
                        </div>
                        <div className="text-xs text-muted-foreground">{c.model_name}</div>
                      </div>
                    </div>
                  </td>
                  <td className="p-3 text-sm">{PROVIDERS.find((p) => p.value === c.provider)?.label ?? c.provider}</td>
                  <td className="p-3 text-sm">
                    {c.has_api_key ? (
                      <span className="flex items-center gap-1 text-xs text-green-600">
                        <Key className="h-3 w-3" /> 已配置
                      </span>
                    ) : (
                      <span className="text-xs text-amber-600">未配置</span>
                    )}
                  </td>
                  <td className="p-3">
                    <div className="flex gap-1">
                      {c.capabilities?.stream && <Badge variant="outline" className="text-[10px] py-0"><Zap className="h-2.5 w-2.5 mr-0.5" />流式</Badge>}
                      {c.capabilities?.thinking && <Badge variant="outline" className="text-[10px] py-0"><Brain className="h-2.5 w-2.5 mr-0.5" />思考</Badge>}
                      {c.capabilities?.vision && <Badge variant="outline" className="text-[10px] py-0"><Eye className="h-2.5 w-2.5 mr-0.5" />视觉</Badge>}
                    </div>
                  </td>
                  <td className="p-3 text-sm">{c.max_tokens}</td>
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
