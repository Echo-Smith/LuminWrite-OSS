/**
 * 模型配置 — Admin Dashboard
 * 支持模型 CRUD + API Key 管理 + 能力配置
 * 模型与 API Key 关联，实现 OpenAI 兼容的动态调用
 */
import { useState, useEffect, useCallback, type ReactElement } from "react";
import { Plus, Trash2, Pencil, Cpu, Star, Loader2, Key, Zap, Brain, Eye, CheckCircle, XCircle } from "lucide-react";
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
  max_tokens: number;
  temperature: number;
  is_default: boolean;
  is_active: boolean;
  capabilities: Record<string, boolean>;
  created_at: string;
  updated_at: string;
}

interface APIKey {
  id: string;
  name: string;
  provider: string;
  base_url: string;
  is_active: boolean;
  key_value?: string;
  last_status?: string;
  last_error?: string;
  last_check?: string | null;
}

const PROVIDERS = [
  { value: "deepseek", label: "DeepSeek" },
  { value: "openai", label: "OpenAI" },
  { value: "qwen", label: "通义千问" },
  { value: "claude", label: "Claude" },
];

const KEY_PROVIDERS = [
  { value: "deepseek", label: "DeepSeek" },
  { value: "openai", label: "OpenAI" },
  { value: "qwen", label: "通义千问" },
  { value: "claude", label: "Claude" },
  { value: "tavily", label: "Tavily Search" },
  { value: "dashscope", label: "DashScope" },
];

const DEFAULT_BASE_URLS: Record<string, string> = {
  deepseek: "https://api.deepseek.com",
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
  const [apiKeys, setApiKeys] = useState<APIKey[]>([]);
  const [loading, setLoading] = useState(false);
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<ModelConfig | null>(null);
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({
    provider: "deepseek",
    model_name: "",
    display_name: "",
    base_url: "",
    api_key_id: "" as string,
    max_tokens: 8192,
    temperature: 0.7,
    is_default: false,
    is_active: true,
    stream: true,
    thinking: false,
    vision: false,
  });

  // API Key form state
  const [showKeyAdd, setShowKeyAdd] = useState(false);
  const [editingKey, setEditingKey] = useState<APIKey | null>(null);
  const [savingKey, setSavingKey] = useState(false);
  const [testingKey, setTestingKey] = useState<string | null>(null);
  const [keyForm, setKeyForm] = useState({
    name: "",
    provider: "deepseek",
    key_value: "",
    base_url: "",
    is_active: true,
  });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [modelsRes, keysRes] = await Promise.all([
        fetch("/api/v2/admin/models"),
        fetch("/api/v2/admin/api-keys"),
      ]);
      const modelsJson = await modelsRes.json();
      const keysJson = await keysRes.json();
      if (modelsJson.success) setConfigs(modelsJson.data?.configs ?? []);
      if (keysJson.success) setApiKeys(keysJson.data?.keys ?? []);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  // ─── Model Config handlers ──────────────────────────────

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
          provider: form.provider,
          model_name: form.model_name,
          display_name: form.display_name || form.model_name,
          base_url: form.base_url || DEFAULT_BASE_URLS[form.provider] || "",
          api_key_id: form.api_key_id || null,
          max_tokens: form.max_tokens,
          temperature: form.temperature,
          is_default: form.is_default,
          is_active: form.is_active,
          capabilities: {
            stream: form.stream,
            thinking: form.thinking,
            vision: form.vision,
          },
        }),
      });
      setShowAdd(false);
      setEditing(null);
      setForm({
        provider: "deepseek", model_name: "", display_name: "", base_url: "",
        api_key_id: "", max_tokens: 8192, temperature: 0.7,
        is_default: false, is_active: true,
        stream: true, thinking: false, vision: false,
      });
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
      api_key_id: c.api_key_id ?? "",
      max_tokens: c.max_tokens,
      temperature: c.temperature,
      is_default: c.is_default,
      is_active: c.is_active,
      stream: c.capabilities?.stream ?? true,
      thinking: c.capabilities?.thinking ?? false,
      vision: c.capabilities?.vision ?? false,
    });
    setShowAdd(true);
  };

  const handleDelete = async (id: string) => {
    await fetch(`/api/v2/admin/models/${id}`, { method: "DELETE" });
    await load();
  };

  // ─── API Key handlers ───────────────────────────────────

  const handleKeySave = async () => {
    if (!keyForm.name || !keyForm.provider || (!editingKey && !keyForm.key_value)) return;
    setSavingKey(true);
    try {
      const url = editingKey ? `/api/v2/admin/api-keys/${editingKey.id}` : "/api/v2/admin/api-keys";
      const method = editingKey ? "PUT" : "POST";
      const body: Record<string, unknown> = { ...keyForm };
      if (editingKey && !keyForm.key_value) delete body.key_value;
      await fetch(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      setShowKeyAdd(false);
      setEditingKey(null);
      setKeyForm({ name: "", provider: "deepseek", key_value: "", base_url: "", is_active: true });
      await load();
    } finally {
      setSavingKey(false);
    }
  };

  const handleKeyEdit = (k: APIKey) => {
    setEditingKey(k);
    setKeyForm({ name: k.name, provider: k.provider, key_value: "", base_url: k.base_url, is_active: k.is_active });
    setShowKeyAdd(true);
  };

  const handleKeyDelete = async (id: string) => {
    await fetch(`/api/v2/admin/api-keys/${id}`, { method: "DELETE" });
    await load();
  };

  const handleKeyTest = async (id: string) => {
    setTestingKey(id);
    try {
      await fetch(`/api/v2/admin/api-keys/${id}/test`, { method: "POST" });
      await load();
    } finally {
      setTestingKey(null);
    }
  };

  const filteredKeys = apiKeys.filter((k) => k.provider === form.provider);
  const linkedModelCount = (keyId: string) => configs.filter((c) => c.api_key_id === keyId).length;

  return (
    <div className="p-6 space-y-8">
      {/* ── 模型配置区 ── */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-xl font-semibold">模型配置</h2>
            <p className="text-sm text-muted-foreground mt-1">
              配置 OpenAI 兼容的模型端点，关联 API Key 实现动态调用
            </p>
          </div>
          <Button size="sm" onClick={() => {
            setShowAdd(!showAdd);
            setEditing(null);
            setForm({
              provider: "deepseek", model_name: "", display_name: "", base_url: "",
              api_key_id: "", max_tokens: 8192, temperature: 0.7,
              is_default: false, is_active: true,
              stream: true, thinking: false, vision: false,
            });
          }}>
            <Plus className="h-4 w-4 mr-2" /> 添加模型
          </Button>
        </div>

        {showAdd && (
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">{editing ? "编辑模型" : "添加模型"}</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="grid grid-cols-3 gap-4">
                <div>
                  <Label>供应商</Label>
                  <Select
                    value={form.provider}
                    onValueChange={(v) => setForm({ ...form, provider: v, api_key_id: "", base_url: form.base_url || DEFAULT_BASE_URLS[v] || "" })}
                  >
                    <SelectTrigger><SelectValue /></SelectTrigger>
                    <SelectContent>
                      {PROVIDERS.map((p) => <SelectItem key={p.value} value={p.value}>{p.label}</SelectItem>)}
                    </SelectContent>
                  </Select>
                </div>
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
                <div className="col-span-2">
                  <Label>Base URL</Label>
                  <Input
                    value={form.base_url}
                    onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                    placeholder={DEFAULT_BASE_URLS[form.provider] || "https://api.example.com/v1"}
                  />
                </div>
                <div>
                  <Label>关联 API Key</Label>
                  <Select
                    value={form.api_key_id || "none"}
                    onValueChange={(v) => setForm({ ...form, api_key_id: v === "none" ? "" : v })}
                  >
                    <SelectTrigger><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="none">不关联（按供应商匹配）</SelectItem>
                      {filteredKeys.map((k) => (
                        <SelectItem key={k.id} value={k.id}>
                          {k.name} {k.key_value ? `(${k.key_value})` : ""}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {filteredKeys.length === 0 && (
                    <p className="text-xs text-amber-600 mt-1">
                      该供应商下暂无 API Key，请在下方添加
                    </p>
                  )}
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

              <div className="flex justify-end gap-2 pt-2 border-t">
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
                <tr><td colSpan={7} className="p-8 text-center text-muted-foreground text-sm">暂无模型配置</td></tr>
              ) : (
                configs.map((c) => {
                  const linkedKey = apiKeys.find((k) => k.id === c.api_key_id);
                  return (
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
                        {linkedKey ? (
                          <span className="flex items-center gap-1 text-xs">
                            <Key className="h-3 w-3 text-green-500" />
                            {linkedKey.name}
                          </span>
                        ) : (
                          <span className="text-xs text-muted-foreground">按供应商匹配</span>
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
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* ── API Key 管理区 ── */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-xl font-semibold flex items-center gap-2">
              <Key className="h-5 w-5 text-muted-foreground" />
              API 密钥
            </h2>
            <p className="text-sm text-muted-foreground mt-1">
              管理各供应商的 API 密钥，密钥在数据库中加密存储
            </p>
          </div>
          <Button size="sm" onClick={() => {
            setShowKeyAdd(!showKeyAdd);
            setEditingKey(null);
            setKeyForm({ name: "", provider: "deepseek", key_value: "", base_url: "", is_active: true });
          }}>
            <Plus className="h-4 w-4 mr-2" /> 添加密钥
          </Button>
        </div>

        {showKeyAdd && (
          <Card>
            <CardHeader>
              <CardTitle className="text-sm">{editingKey ? "编辑密钥" : "添加密钥"}</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <Label>名称</Label>
                  <Input
                    value={keyForm.name}
                    onChange={(e) => setKeyForm({ ...keyForm, name: e.target.value })}
                    placeholder="DeepSeek 主密钥"
                  />
                </div>
                <div>
                  <Label>供应商</Label>
                  <Select
                    value={keyForm.provider}
                    onValueChange={(v) => setKeyForm({ ...keyForm, provider: v })}
                  >
                    <SelectTrigger><SelectValue /></SelectTrigger>
                    <SelectContent>
                      {KEY_PROVIDERS.map((p) => <SelectItem key={p.value} value={p.value}>{p.label}</SelectItem>)}
                    </SelectContent>
                  </Select>
                </div>
                <div>
                  <Label>密钥值{editingKey && " (留空保持不变)"}</Label>
                  <Input
                    type="password"
                    value={keyForm.key_value}
                    onChange={(e) => setKeyForm({ ...keyForm, key_value: e.target.value })}
                    placeholder="sk-..."
                  />
                </div>
                <div>
                  <Label>Base URL (可选)</Label>
                  <Input
                    value={keyForm.base_url}
                    onChange={(e) => setKeyForm({ ...keyForm, base_url: e.target.value })}
                    placeholder={DEFAULT_BASE_URLS[keyForm.provider] || "https://api.example.com/v1"}
                  />
                </div>
              </div>
              <div className="flex items-center gap-2">
                <Switch checked={keyForm.is_active} onCheckedChange={(v) => setKeyForm({ ...keyForm, is_active: v })} />
                <Label>启用</Label>
              </div>
              <div className="flex justify-end gap-2 pt-2 border-t">
                <Button variant="outline" size="sm" onClick={() => { setShowKeyAdd(false); setEditingKey(null); }}>取消</Button>
                <Button size="sm" onClick={handleKeySave} disabled={!keyForm.name || (!editingKey && !keyForm.key_value) || savingKey}>
                  {savingKey ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Plus className="h-4 w-4 mr-2" />}
                  {editingKey ? "保存" : "添加"}
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
                <th className="text-left p-3 text-sm font-medium">供应商</th>
                <th className="text-left p-3 text-sm font-medium">密钥</th>
                <th className="text-left p-3 text-sm font-medium">关联模型</th>
                <th className="text-left p-3 text-sm font-medium">连通状态</th>
                <th className="text-right p-3 text-sm font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr><td colSpan={6} className="p-8 text-center"><Loader2 className="h-4 w-4 animate-spin mx-auto" /></td></tr>
              ) : apiKeys.length === 0 ? (
                <tr><td colSpan={6} className="p-8 text-center text-muted-foreground text-sm">暂无 API 密钥，点击「添加密钥」创建</td></tr>
              ) : (
                apiKeys.map((k) => {
                  const count = linkedModelCount(k.id);
                  return (
                    <tr key={k.id} className="border-b last:border-0 hover:bg-accent/30">
                      <td className="p-3 text-sm font-medium">
                        <div className="flex items-center gap-2">
                          <Key className="h-3.5 w-3.5 text-muted-foreground" />
                          {k.name}
                        </div>
                      </td>
                      <td className="p-3 text-sm">{KEY_PROVIDERS.find((p) => p.value === k.provider)?.label ?? k.provider}</td>
                      <td className="p-3 text-sm font-mono text-muted-foreground">{k.key_value || "****"}</td>
                      <td className="p-3 text-sm">
                        {count > 0 ? (
                          <Badge variant="outline" className="bg-blue-50 text-blue-700">{count} 个模型</Badge>
                        ) : (
                          <span className="text-xs text-muted-foreground">未关联</span>
                        )}
                      </td>
                      <td className="p-3">
                        <div className="flex items-center gap-2">
                          {STATUS_ICONS[k.last_status ?? "unknown"] ?? STATUS_ICONS.unknown}
                          {k.last_error && (
                            <span className="text-xs text-red-500 truncate max-w-[120px]" title={k.last_error}>{k.last_error}</span>
                          )}
                        </div>
                      </td>
                      <td className="p-3 text-right">
                        <div className="flex justify-end gap-1">
                          <Button variant="ghost" size="sm" onClick={() => handleKeyTest(k.id)} disabled={testingKey === k.id} title="测试连通性">
                            {testingKey === k.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Zap className="h-3.5 w-3.5" />}
                          </Button>
                          <Button variant="ghost" size="sm" onClick={() => handleKeyEdit(k)} title="编辑">
                            <Pencil className="h-3.5 w-3.5" />
                          </Button>
                          <Button variant="ghost" size="sm" onClick={() => handleKeyDelete(k.id)} title="删除">
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
