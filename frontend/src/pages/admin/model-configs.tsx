/**
 * 模型配置 — Admin Dashboard
 * 支持：输入 base_url + api_key → 自动发现模型列表 → 选择启用
 * API Key 内聚在模型配置中，不再关联独立密钥
 * 供应商名称可自定义，支持自定义 HTTP 请求头
 */
import { useState, useEffect, useCallback, type ReactElement } from "react";
import { Plus, Trash2, Pencil, Cpu, Star, Loader2, Key, Zap, Brain, Eye, Search, CheckCircle, XCircle, ChevronDown, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription,
} from "@/components/ui/dialog";
import { adminFetch, adminMutate, adminDelete } from "@/lib/admin-api";
import { AdminConfirmDialog, AdminPageHeader, AdminBulkActions } from "@/components/admin";
import { cn } from "@/lib/utils";

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
  reasoning_effort: string;
  is_default: boolean;
  is_active: boolean;
  capabilities: Record<string, boolean | number>;
  custom_headers?: Record<string, string>;
  created_at: string;
  updated_at: string;
}

interface DiscoveredModel {
  id: string;
  owned_by: string;
}

// 预设供应商 — 用于快速填充 base_url，但 provider 字段可自定义
const PROVIDER_PRESETS = [
  { value: "deepseek", label: "DeepSeek", baseUrl: "https://api.deepseek.com" },
  { value: "kimi", label: "Kimi (Moonshot)", baseUrl: "https://api.moonshot.cn/v1" },
  { value: "openai", label: "OpenAI", baseUrl: "https://api.openai.com/v1" },
  { value: "qwen", label: "通义千问", baseUrl: "https://dashscope.aliyuncs.com/compatible-mode/v1" },
  { value: "claude", label: "Claude", baseUrl: "https://api.anthropic.com/v1" },
  { value: "custom", label: "自定义", baseUrl: "" },
];

const STATUS_ICONS: Record<string, ReactElement> = {
  ok: <CheckCircle className="h-3.5 w-3.5 text-green-500" />,
  fail: <XCircle className="h-3.5 w-3.5 text-red-500" />,
  unknown: <span className="text-xs text-muted-foreground">—</span>,
};

interface HeaderEntry {
  key: string;
  value: string;
}

function headersToMap(headers: HeaderEntry[]): Record<string, string> {
  const map: Record<string, string> = {};
  for (const h of headers) {
    if (h.key.trim()) map[h.key.trim()] = h.value;
  }
  return map;
}

function mapToHeaders(map?: Record<string, string>): HeaderEntry[] {
  if (!map) return [];
  return Object.entries(map).map(([key, value]) => ({ key, value }));
}

export function ModelConfigsPage() {
  const [configs, setConfigs] = useState<ModelConfig[]>([]);
  const [loading, setLoading] = useState(false);
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<ModelConfig | null>(null);
  const [saving, setSaving] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [form, setForm] = useState({
    provider: "deepseek",
    providerLabel: "", // 自定义供应商显示名（可选）
    model_name: "",
    display_name: "",
    base_url: "",
    api_key: "",
    max_tokens: 65536,
    temperature: 0.7,
    reasoning_effort: "high",
    context_window: 0, // 0 = 不设置
    max_output: 0,     // 0 = 不设置
    is_default: false,
    is_active: true,
    stream: true,
    thinking: false,
    vision: false,
  });
  const [headerEntries, setHeaderEntries] = useState<HeaderEntry[]>([]);

  // Discover models state
  const [discovering, setDiscovering] = useState(false);
  const [discoveredModels, setDiscoveredModels] = useState<DiscoveredModel[]>([]);
  const [discoverError, setDiscoverError] = useState("");

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);

  const handleBatchAction = async (action: "delete" | "activate" | "deactivate") => {
    const { success, data } = await adminMutate<{affected: number}>("/api/v2/admin/models/batch", { method: "POST", body: JSON.stringify({ ids: selectedIds, action }), successTitle: action === "delete" ? "Deleted" : "Updated" });
    if (success) { setSelectedIds([]); load(); }
  };

  const toggleSelect = (id: string) => setSelectedIds(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id]);

  const load = useCallback(async () => {
    setLoading(true);
    const { success, data } = await adminFetch<{ configs: ModelConfig[] }>("/api/v2/admin/models", { silent: true });
    if (success && data) setConfigs(data.configs ?? []);
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  // ─── Discover models from provider ──────────────────────

  const handleDiscover = async () => {
    if (!form.api_key) {
      setDiscoverError("请先输入 API Key");
      return;
    }
    const baseURL = form.base_url || PROVIDER_PRESETS.find(p => p.value === form.provider)?.baseUrl || "";
    if (!baseURL) {
      setDiscoverError("请先输入 Base URL");
      return;
    }

    setDiscovering(true);
    setDiscoverError("");
    setDiscoveredModels([]);

    // 构建 discover 请求的 headers
    const discoverHeaders: Record<string, string> = {};
    for (const h of headerEntries) {
      if (h.key.trim()) discoverHeaders[h.key.trim()] = h.value;
    }

    const { success, data, error } = await adminFetch<{ models: DiscoveredModel[] }>(
      "/api/v2/admin/models/discover",
      { method: "POST", body: JSON.stringify({ base_url: baseURL, api_key: form.api_key, custom_headers: Object.keys(discoverHeaders).length > 0 ? discoverHeaders : undefined }) },
    );
    if (success && data) {
      setDiscoveredModels(data.models ?? []);
    } else {
      setDiscoverError(error?.message ?? "获取模型列表失败");
    }
    setDiscovering(false);
  };

  // ─── Model Config handlers ──────────────────────────────

  const handleSave = async () => {
    if (!form.model_name) return;
    setSaving(true);
    const url = editing ? `/api/v2/admin/models/${editing.id}` : "/api/v2/admin/models";
    const method = editing ? "PUT" : "POST";
    const customHeaders = headersToMap(headerEntries);
    const body: Record<string, unknown> = {
      provider: form.provider,
      model_name: form.model_name,
      display_name: form.display_name || form.model_name,
      base_url: form.base_url || PROVIDER_PRESETS.find(p => p.value === form.provider)?.baseUrl || "",
      api_key: form.api_key || undefined,
      max_tokens: form.max_tokens,
      temperature: form.temperature,
      reasoning_effort: form.reasoning_effort,
      is_default: form.is_default,
      is_active: form.is_active,
      capabilities: {
        stream: form.stream,
        thinking: form.thinking,
        vision: form.vision,
        ...(form.context_window > 0 ? { context_window: form.context_window } : {}),
        ...(form.max_output > 0 ? { max_output: form.max_output } : {}),
      },
      custom_headers: Object.keys(customHeaders).length > 0 ? customHeaders : {},
    };
    if (editing && !form.api_key) delete body.api_key;

    const { success } = await adminMutate<ModelConfig>(url, {
      method,
      body: JSON.stringify(body),
      successTitle: editing ? "模型已更新" : "模型已添加",
      successDesc: form.display_name || form.model_name,
    });
    if (success) {
      resetForm();
      await load();
    }
    setSaving(false);
  };

  const resetForm = () => {
    setShowAdd(false);
    setEditing(null);
    setDiscoveredModels([]);
    setDiscoverError("");
    setShowAdvanced(false);
    setHeaderEntries([]);
    setForm({
      provider: "deepseek", providerLabel: "", model_name: "", display_name: "", base_url: "",
      api_key: "", max_tokens: 65536, temperature: 0.7,
      reasoning_effort: "high", context_window: 0, max_output: 0,
      is_default: false, is_active: true,
      stream: true, thinking: false, vision: false,
    });
  };

  const openAdd = () => {
    resetForm();
    setShowAdd(true);
  };

  const handleEdit = (c: ModelConfig) => {
    setEditing(c);
    setForm({
      provider: c.provider,
      providerLabel: "",
      model_name: c.model_name,
      display_name: c.display_name,
      base_url: c.base_url,
      api_key: "", // don't prefill — user enters new key to replace
      max_tokens: c.max_tokens,
      temperature: c.temperature,
      reasoning_effort: c.reasoning_effort || "high",
      context_window: (c.capabilities?.context_window as number) || 0,
      max_output: (c.capabilities?.max_output as number) || 0,
      is_default: c.is_default,
      is_active: c.is_active,
      stream: Boolean(c.capabilities?.stream ?? true),
      thinking: Boolean(c.capabilities?.thinking ?? false),
      vision: Boolean(c.capabilities?.vision ?? false),
    });
    setHeaderEntries(mapToHeaders(c.custom_headers));
    setShowAdvanced(Object.keys(c.custom_headers ?? {}).length > 0);
    setDiscoveredModels([]);
    setDiscoverError("");
    setShowAdd(true);
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    const ok = await adminDelete(
      `/api/v2/admin/models/${deleteTarget}`,
      "确认删除此模型配置？",
      "模型已删除",
    );
    if (ok) await load();
    setDeleteTarget(null);
  };

  return (
      <div className="p-6 space-y-6">
        <AdminPageHeader
          title="模型配置"
          description="输入 Base URL 和 API Key，自动发现可用模型。支持自定义供应商名称和 HTTP 请求头。密钥加密存储在模型配置中。"
          action={
            <Button size="sm" onClick={openAdd}>
              <Plus className="h-4 w-4 mr-2" /> 添加模型
            </Button>
          }
        />
        <AdminBulkActions selectedIds={selectedIds} onClear={() => setSelectedIds([])} onBatchAction={handleBatchAction} />

      {/* Add/Edit Model Dialog */}
      <Dialog open={showAdd} onOpenChange={(v) => { if (!v && !saving) resetForm(); }}>
        <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editing ? "编辑模型" : "添加模型"}</DialogTitle>
            <DialogDescription>
              输入 Base URL 和 API Key，自动发现可用模型。支持自定义供应商名称和 HTTP 请求头。密钥加密存储。
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4">
            {/* Step 1: Provider + Base URL + API Key + Discover */}
            <div className="space-y-3">
              <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
                <span className="flex h-5 w-5 items-center justify-center rounded-full bg-primary/10 text-primary">1</span>
                连接供应商并发现模型
              </div>

              <div className="grid grid-cols-3 gap-4">
                <div>
                  <Label>供应商</Label>
                  <div className="flex gap-2">
                    <select
                      className="flex-1 h-9 rounded-md border border-input bg-transparent px-3 text-sm ring-offset-background focus:ring-2 focus:ring-ring focus:ring-offset-2 outline-none"
                      value={PROVIDER_PRESETS.some(p => p.value === form.provider) ? form.provider : "custom"}
                      onChange={(e) => {
                        const preset = PROVIDER_PRESETS.find(p => p.value === e.target.value);
                        if (preset && e.target.value !== "custom") {
                          setForm({ ...form, provider: e.target.value, base_url: form.base_url || preset.baseUrl, model_name: "", display_name: "" });
                        } else {
                          setForm({ ...form, provider: form.provider === "deepseek" ? "" : form.provider, base_url: form.base_url });
                        }
                      }}
                    >
                      {PROVIDER_PRESETS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
                    </select>
                    {/* 当选择"自定义"或非预设值时，显示文本输入框 */}
                    {!PROVIDER_PRESETS.some(p => p.value === form.provider) || form.provider === "custom" ? (
                      <Input
                        className="flex-1"
                        value={form.provider === "custom" ? "" : form.provider}
                        onChange={(e) => setForm({ ...form, provider: e.target.value })}
                        placeholder="自定义供应商名"
                      />
                    ) : null}
                  </div>
                </div>
                <div className="col-span-2">
                  <Label>Base URL</Label>
                  <Input
                    value={form.base_url}
                    onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                    placeholder={PROVIDER_PRESETS.find(p => p.value === form.provider)?.baseUrl || "https://api.example.com/v1"}
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
                <div>
                  <Label>思考深度 (Reasoning Effort)</Label>
                  <select
                    className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm ring-offset-background focus:ring-2 focus:ring-ring focus:ring-offset-2 outline-none"
                    value={form.reasoning_effort}
                    onChange={(e) => setForm({ ...form, reasoning_effort: e.target.value })}
                  >
                    <option value="low">low — 快速响应</option>
                    <option value="medium">medium — 平衡</option>
                    <option value="high">high — 深度思考</option>
                    <option value="max">max — 最大思考</option>
                  </select>
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

              {/* 上下文窗口参数 */}
              <div className="grid grid-cols-2 gap-4 pt-2 border-t">
                <div>
                  <Label>上下文窗口 (Context Window)</Label>
                  <Input
                    type="number"
                    value={form.context_window || ""}
                    onChange={(e) => setForm({ ...form, context_window: parseInt(e.target.value) || 0 })}
                    placeholder="留空 = 不设置（如 1048576 = 1M）"
                  />
                </div>
                <div>
                  <Label>最大输出 (Max Output)</Label>
                  <Input
                    type="number"
                    value={form.max_output || ""}
                    onChange={(e) => setForm({ ...form, max_output: parseInt(e.target.value) || 0 })}
                    placeholder="留空 = 不设置（如 384000 = 384K）"
                  />
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

            {/* Step 3: Advanced — Custom Headers */}
            <div className="space-y-3 pt-2 border-t">
              <button
                type="button"
                onClick={() => setShowAdvanced(!showAdvanced)}
                className="flex items-center gap-2 text-xs font-medium text-muted-foreground hover:text-foreground transition-colors"
              >
                {showAdvanced ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                <span className="flex h-5 w-5 items-center justify-center rounded-full bg-primary/10 text-primary">3</span>
                自定义 HTTP 请求头（高级）
                {headerEntries.some(h => h.key.trim()) && (
                  <Badge variant="secondary" className="text-[10px] py-0">{headerEntries.filter(h => h.key.trim()).length} 个</Badge>
                )}
              </button>

              {showAdvanced && (
                <div className="space-y-2 pl-7">
                  <p className="text-xs text-muted-foreground">
                    部分供应商或中转站需要额外的 HTTP 请求头（如 X-API-Key、X-Request-Source 等）。
                    标准的 Content-Type 和 Authorization 头无需重复添加。
                  </p>
                  {headerEntries.map((entry, i) => (
                    <div key={i} className="flex items-center gap-2">
                      <Input
                        className="flex-1"
                        value={entry.key}
                        onChange={(e) => {
                          const next = [...headerEntries];
                          next[i] = { ...next[i], key: e.target.value };
                          setHeaderEntries(next);
                        }}
                        placeholder="Header 名称（如 X-API-Key）"
                      />
                      <Input
                        className="flex-1"
                        value={entry.value}
                        onChange={(e) => {
                          const next = [...headerEntries];
                          next[i] = { ...next[i], value: e.target.value };
                          setHeaderEntries(next);
                        }}
                        placeholder="Header 值"
                      />
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setHeaderEntries(headerEntries.filter((_, j) => j !== i))}
                        className="h-9 w-9 p-0 text-muted-foreground hover:text-destructive"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  ))}
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => setHeaderEntries([...headerEntries, { key: "", value: "" }])}
                    className="text-xs"
                  >
                    <Plus className="h-3 w-3 mr-1" /> 添加 Header
                  </Button>
                </div>
              )}
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" size="sm" onClick={resetForm} disabled={saving}>取消</Button>
            <Button size="sm" onClick={handleSave} disabled={!form.model_name || saving}>
              {saving ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Plus className="h-4 w-4 mr-2" />}
              {editing ? "保存" : "添加"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AdminConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => { if (!v) setDeleteTarget(null); }}
        title="删除模型"
        description="确认删除此模型配置？此操作不可撤销。"
        confirmText="删除"
        variant="destructive"
        onConfirm={handleDelete}
      />

      <div className="rounded-lg border">
        <table className="w-full">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="text-left p-3 text-sm font-medium">模型</th>
              <th className="text-left p-3 text-sm font-medium">供应商</th>
              <th className="text-left p-3 text-sm font-medium">API Key</th>
              <th className="text-left p-3 text-sm font-medium">能力</th>
              <th className="text-left p-3 text-sm font-medium">Max Tokens</th>
              <th className="text-left p-3 text-sm font-medium">思考深度</th>
              <th className="text-left p-3 text-sm font-medium">状态</th>
              <th className="text-right p-3 text-sm font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={8} className="p-8 text-center"><Loader2 className="h-4 w-4 animate-spin mx-auto" /></td></tr>
            ) : configs.length === 0 ? (
              <tr><td colSpan={8} className="p-8 text-center text-muted-foreground text-sm">暂无模型配置，点击「添加模型」创建</td></tr>
            ) : (
              configs.map((c) => (
                <tr key={c.id} className="border-b last:border-0 hover:bg-accent/30">
                  <td className="p-3"><input type="checkbox" checked={selectedIds.includes(c.id)} onChange={() => toggleSelect(c.id)} className="h-4 w-4 rounded border-border" /></td>
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
                  <td className="p-3 text-sm">{c.provider}</td>
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
                    <div className="flex flex-wrap gap-1">
                      {c.capabilities?.stream && <Badge variant="outline" className="text-[10px] py-0"><Zap className="h-2.5 w-2.5 mr-0.5" />流式</Badge>}
                      {c.capabilities?.thinking && <Badge variant="outline" className="text-[10px] py-0"><Brain className="h-2.5 w-2.5 mr-0.5" />思考</Badge>}
                      {c.capabilities?.vision && <Badge variant="outline" className="text-[10px] py-0"><Eye className="h-2.5 w-2.5 mr-0.5" />视觉</Badge>}
                      {c.custom_headers && Object.keys(c.custom_headers).length > 0 && (
                        <Badge variant="outline" className="text-[10px] py-0 text-purple-600">
                          {Object.keys(c.custom_headers).length} 个自定义Header
                        </Badge>
                      )}
                    </div>
                  </td>
                  <td className="p-3 text-sm">{c.max_tokens}</td>
                  <td className="p-3 text-sm">{c.reasoning_effort || "—"}</td>
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
                      <Button variant="ghost" size="sm" onClick={() => setDeleteTarget(c.id)} title="删除">
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
