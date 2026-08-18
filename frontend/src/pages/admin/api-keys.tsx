/**
 * MCP 服务密钥管理 — Admin Dashboard
 * 管理 MCP / 第三方服务（如搜索）的 API 密钥
 * LLM 密钥已内聚到「模型配置」页面，此处不再管理
 */
import { useState, useEffect, useCallback, type ReactElement } from "react";
import { Plus, Trash2, Pencil, Key, Zap, Loader2, CheckCircle, XCircle, Server, Wrench, Download } from "lucide-react";
import { toast } from "@/stores/toast-store";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { adminMutate } from "@/lib/admin-api";
import { AdminPageHeader, AdminBulkActions } from "@/components/admin";

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
  { value: "dashscope", label: "DashScope (Embedding)" },
  { value: "anysearch", label: "AnySearch" },
  { value: "tencent", label: "腾讯新闻" },
  { value: "tencent_news", label: "腾讯新闻 CLI (事实核查)" },
  { value: "jiaozhen", label: "较真事实核查" },
  { value: "weibo", label: "微博" },
  { value: "bing", label: "Bing" },
];

const STATUS_ICONS: Record<string, ReactElement> = {
  ok: <CheckCircle className="h-3.5 w-3.5 text-green-500" />,
  fail: <XCircle className="h-3.5 w-3.5 text-red-500" />,
  unknown: <span className="text-xs text-muted-foreground">—</span>,
};

interface MCPStatus {
  in_process: {
    enabled: boolean;
    tool_count?: number;
    config?: { enabled: boolean; http_addr: string; stdio: boolean };
  };
  external_servers: { name: string; status: string }[];
  external_count?: number;
}

interface MCPTool {
  name: string;
  description: string;
  source: string;
  schema?: Record<string, unknown>;
}

export function APIKeysPage() {
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [loading, setLoading] = useState(false);
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<APIKey | null>(null);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState<string | null>(null);
  const [mcpStatus, setMcpStatus] = useState<MCPStatus | null>(null);
  const [mcpTools, setMcpTools] = useState<MCPTool[]>([]);
  const [mcpLoading, setMcpLoading] = useState(false);
  const [form, setForm] = useState({
    name: "",
    provider: "tavily",
    key_value: "",
    base_url: "",
    is_active: true,
  });

  const handleBatchAction = async (action: "delete" | "activate" | "deactivate") => {
    const { success } = await adminMutate("/api/v2/admin/api-keys/batch", { method: "POST", body: JSON.stringify({ ids: selectedIds, action }), successTitle: action === "delete" ? "Deleted" : "Updated" });
    if (success) { setSelectedIds([]); load(); }
  };
  const toggleSelect = (id: string) => setSelectedIds(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id]);

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

  // Load MCP status and tools
  const loadMCPInfo = useCallback(async () => {
    setMcpLoading(true);
    try {
      const [statusRes, toolsRes] = await Promise.all([
        fetch("/api/v2/admin/mcp/status"),
        fetch("/api/v2/admin/mcp/tools"),
      ]);
      const statusJson = await statusRes.json();
      if (statusJson.success) setMcpStatus(statusJson.data);
      const toolsJson = await toolsRes.json();
      if (toolsJson.success) setMcpTools(toolsJson.data?.tools ?? []);
    } catch {
      // ignore
    } finally {
      setMcpLoading(false);
    }
  }, []);

  useEffect(() => { loadMCPInfo(); }, [loadMCPInfo]);

  const handleSave = async () => {
    if (!form.name || !form.provider || (!editing && !form.key_value)) return;
    setSaving(true);
    try {
      const url = editing ? `/api/v2/admin/api-keys/${editing.id}` : "/api/v2/admin/api-keys";
      const method = editing ? "PUT" : "POST";
      const body: Record<string, unknown> = { ...form, category: "mcp" };
      if (editing && !form.key_value) delete body.key_value;
      const res = await fetch(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const json = await res.json();
      if (!res.ok || !json.success) {
        toast.error("保存失败", json.error?.message ?? "请检查权限或重试");
        return;
      }
      toast.success(editing ? "密钥已更新" : "密钥已添加", form.name);
      setShowAdd(false);
      setEditing(null);
      setForm({ name: "", provider: "tavily", key_value: "", base_url: "", is_active: true });
      await load();
    } catch {
      toast.error("网络错误", "请检查网络连接后重试");
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
    if (!confirm("确认删除此密钥？")) return;
    try {
      const res = await fetch(`/api/v2/admin/api-keys/${id}`, { method: "DELETE" });
      const json = await res.json();
      if (!res.ok || !json.success) {
        toast.error("删除失败", json.error?.message ?? "请检查权限或重试");
        return;
      }
      toast.success("密钥已删除");
      await load();
    } catch {
      toast.error("网络错误", "请检查网络连接后重试");
    }
  };

  const handleTest = async (id: string) => {
    setTesting(id);
    try {
      const res = await fetch(`/api/v2/admin/api-keys/${id}/test`, { method: "POST" });
      const json = await res.json();
      if (!res.ok || !json.success) {
        toast.error("测试失败", json.error?.message ?? "请检查权限或重试");
        await load();
        return;
      }
      const status = json.data?.status as string;
      if (status === "ok") {
        toast.success("连通性测试通过", "服务可正常访问");
      } else {
        toast.error("连通性测试失败", json.data?.error ?? "请检查配置");
      }
      await load();
    } catch {
      toast.error("网络错误", "请检查网络连接后重试");
    } finally {
      setTesting(null);
    }
  };

  return (
    <div className="p-6 space-y-6">
      <AdminBulkActions selectedIds={selectedIds} onClear={() => setSelectedIds([])} onBatchAction={handleBatchAction} />
      <AdminPageHeader
        title="MCP 服务密钥"
        description="管理搜索、知识库等 MCP 服务的 API 密钥。LLM 密钥请在「模型配置」中管理。"
        action={
          <Button size="sm" onClick={() => { setShowAdd(true); setEditing(null); setForm({ name: "", provider: "tavily", key_value: "", base_url: "", is_active: true }); }}>
            <Plus className="h-4 w-4 mr-2" /> 添加密钥
          </Button>
        }
      />

      {/* Add/Edit Key Dialog */}
      <Dialog open={showAdd} onOpenChange={(v) => { if (!v && !saving) { setShowAdd(false); setEditing(null); } }}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing ? "编辑密钥" : "添加密钥"}</DialogTitle>
            <DialogDescription>
              管理搜索、知识库等 MCP 服务的 API 密钥。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
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
            <div className="flex items-center gap-2">
              <Switch checked={form.is_active} onCheckedChange={(v) => setForm({ ...form, is_active: v })} />
              <Label>启用</Label>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => { setShowAdd(false); setEditing(null); }} disabled={saving}>取消</Button>
            <Button size="sm" onClick={handleSave} disabled={!form.name || (!editing && !form.key_value) || saving}>
              {saving ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Plus className="h-4 w-4 mr-2" />}
              {editing ? "保存" : "添加"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

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

      {/* MCP Server Status & Tools */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold flex items-center gap-2">
            <Server className="h-4 w-4" /> MCP 服务器状态
          </h3>
          <Button variant="ghost" size="sm" onClick={loadMCPInfo} disabled={mcpLoading}>
            {mcpLoading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Zap className="h-3.5 w-3.5" />}
            刷新
          </Button>
        </div>

        {mcpStatus && (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {/* In-Process MCP Server */}
            <Card>
              <CardContent className="p-4">
                <div className="flex items-center justify-between mb-2">
                  <span className="text-sm font-medium">内置 MCP 服务</span>
                  <Badge variant="outline" className={mcpStatus.in_process.enabled ? "bg-green-100 text-green-700" : "bg-gray-100 text-gray-500"}>
                    {mcpStatus.in_process.enabled ? "已启用" : "未启用"}
                  </Badge>
                </div>
                {mcpStatus.in_process.enabled && (
                  <div className="text-xs text-muted-foreground space-y-1">
                    <div>工具数: {mcpStatus.in_process.tool_count ?? 0}</div>
                    {mcpStatus.in_process.config && (
                      <div>HTTP 地址: {mcpStatus.in_process.config.http_addr}</div>
                    )}
                  </div>
                )}
              </CardContent>
            </Card>

            {/* External MCP Servers */}
            <Card>
              <CardContent className="p-4">
                <div className="flex items-center justify-between mb-2">
                  <span className="text-sm font-medium">外部 MCP 服务器</span>
                  <Badge variant="outline">{mcpStatus.external_count ?? 0} 个</Badge>
                </div>
                {mcpStatus.external_servers.length > 0 ? (
                  <div className="space-y-1">
                    {mcpStatus.external_servers.map((s) => (
                      <div key={s.name} className="flex items-center justify-between text-xs">
                        <span>{s.name}</span>
                        <Badge variant="outline" className="bg-green-100 text-green-700">{s.status}</Badge>
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="text-xs text-muted-foreground">无外部 MCP 服务器</p>
                )}
              </CardContent>
            </Card>
          </div>
        )}

        {/* MCP Tools List */}
        {mcpTools.length > 0 && (
          <Card>
            <CardHeader>
              <CardTitle className="text-sm flex items-center gap-2">
                <Wrench className="h-4 w-4" /> MCP 工具列表 ({mcpTools.length})
              </CardTitle>
            </CardHeader>
            <CardContent>
              <div className="space-y-2 max-h-[300px] overflow-y-auto">
                {mcpTools.map((tool) => (
                  <div key={tool.name} className="flex items-start gap-2 p-2 rounded-md hover:bg-accent/30">
                    <Badge variant="outline" className="text-xs shrink-0 mt-0.5" title={tool.source}>
                      {tool.source === "in-process" ? "内置" : "外部"}
                    </Badge>
                    <div className="min-w-0 flex-1">
                      <p className="text-xs font-medium font-mono">{tool.name}</p>
                      <p className="text-xs text-muted-foreground line-clamp-2">{tool.description}</p>
                    </div>
                  </div>
                ))}
              </div>
              <div className="mt-3 flex justify-end">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    window.open("/api/v2/admin/mcp/export", "_blank");
                  }}
                >
                  <Download className="h-3.5 w-3.5 mr-1.5" /> 导出工具定义
                </Button>
              </div>
            </CardContent>
          </Card>
        )}
      </div>

      {/* MCP Server CRUD Management */}
      <MCPServerManager />
    </div>
  );
}

// ─── MCP Server CRUD Manager ───────────────────────────

interface MCPServer {
  id: string;
  name: string;
  transport: string;
  command?: string;
  args?: string[];
  env?: string[];
  url?: string;
  is_active: boolean;
  description: string;
  last_status: string;
  last_error?: string;
  last_connected_at?: string | null;
  created_at: string;
  updated_at: string;
}

function MCPServerManager() {
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [loading, setLoading] = useState(false);
  const [showDialog, setShowDialog] = useState(false);
  const [editing, setEditing] = useState<MCPServer | null>(null);
  const [saving, setSaving] = useState(false);
  const [reconnecting, setReconnecting] = useState<string | null>(null);

  // Form state
  const [form, setForm] = useState({
    name: "",
    transport: "stdio",
    command: "",
    argsText: "",
    envText: "",
    url: "",
    is_active: true,
    description: "",
  });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch("/api/v2/admin/mcp/servers");
      const json = await res.json();
      if (json.success) setServers(json.data?.servers ?? []);
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const resetForm = () => {
    setForm({
      name: "", transport: "stdio", command: "", argsText: "", envText: "",
      url: "", is_active: true, description: "",
    });
    setEditing(null);
  };

  const openAdd = () => {
    resetForm();
    setShowDialog(true);
  };

  const openEdit = (srv: MCPServer) => {
    setEditing(srv);
    setForm({
      name: srv.name,
      transport: srv.transport,
      command: srv.command ?? "",
      argsText: (srv.args ?? []).join("\n"),
      envText: (srv.env ?? []).join("\n"),
      url: srv.url ?? "",
      is_active: srv.is_active,
      description: srv.description,
    });
    setShowDialog(true);
  };

  const handleSave = async () => {
    if (!form.name.trim() || !form.transport) return;
    if (form.transport === "stdio" && !form.command.trim()) return;
    if (form.transport === "sse" && !form.url.trim()) return;
    setSaving(true);
    try {
      const payload = {
        name: form.name.trim(),
        transport: form.transport,
        command: form.command.trim(),
        args: form.argsText.split("\n").map(s => s.trim()).filter(Boolean),
        env: form.envText.split("\n").map(s => s.trim()).filter(Boolean),
        url: form.url.trim(),
        is_active: form.is_active,
        description: form.description.trim(),
      };
      const url = editing
        ? `/api/v2/admin/mcp/servers/${editing.id}`
        : "/api/v2/admin/mcp/servers";
      const method = editing ? "PUT" : "POST";
      const res = await fetch(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const json = await res.json();
      if (!res.ok || !json.success) {
        toast.error("保存失败", json.error?.message ?? "请检查配置或重试");
        return;
      }
      toast.success(editing ? "服务器已更新" : "服务器已添加", form.name);
      setShowDialog(false);
      resetForm();
      await load();
    } catch {
      toast.error("网络错误", "请检查网络连接后重试");
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("确定删除此 MCP 服务器配置？")) return;
    try {
      const res = await fetch(`/api/v2/admin/mcp/servers/${id}`, { method: "DELETE" });
      const json = await res.json();
      if (!res.ok || !json.success) {
        toast.error("删除失败", json.error?.message ?? "请检查权限或重试");
        return;
      }
      toast.success("MCP 服务器已删除");
      await load();
    } catch {
      toast.error("网络错误", "请检查网络连接后重试");
    }
  };

  const handleReconnect = async (id: string) => {
    setReconnecting(id);
    try {
      const res = await fetch(`/api/v2/admin/mcp/servers/${id}/reconnect`, { method: "POST" });
      const json = await res.json();
      if (!res.ok || !json.success) {
        toast.error("重连失败", json.error?.message ?? "请检查配置或重试");
        await load();
        return;
      }
      const status = json.data?.status as string;
      if (status === "connected") {
        toast.success("MCP 服务器已连接");
      } else {
        toast.error("连接失败", json.data?.message ?? "请检查服务器配置");
      }
      await load();
    } catch {
      toast.error("网络错误", "请检查网络连接后重试");
    } finally {
      setReconnecting(null);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold flex items-center gap-2">
          <Server className="h-4 w-4" /> 外部 MCP 服务器管理
        </h3>
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={load} disabled={loading}>
            {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Zap className="h-3.5 w-3.5" />}
            刷新
          </Button>
          <Button variant="outline" size="sm" onClick={openAdd}>
            <Plus className="h-3.5 w-3.5 mr-1.5" /> 添加服务器
          </Button>
        </div>
      </div>

      {servers.length === 0 ? (
        <Card>
          <CardContent className="p-8 text-center">
            <Server className="h-8 w-8 mx-auto text-muted-foreground mb-2" />
            <p className="text-sm text-muted-foreground">暂无 MCP 服务器配置</p>
            <p className="text-xs text-muted-foreground mt-1">点击「添加服务器」配置外部 MCP 工具</p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-2">
          {servers.map((srv) => (
            <Card key={srv.id}>
              <CardContent className="p-4">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex-1 min-w-0 space-y-1">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-sm font-medium">{srv.name}</span>
                      <Badge variant="outline" className="text-xs">{srv.transport}</Badge>
                      <Badge
                        variant="outline"
                        className={
                          srv.last_status === "connected"
                            ? "bg-green-100 text-green-700"
                            : srv.last_status === "failed"
                            ? "bg-red-100 text-red-700"
                            : "bg-gray-100 text-gray-500"
                        }
                      >
                        {srv.last_status === "connected" ? "已连接" : srv.last_status === "failed" ? "连接失败" : "未知"}
                      </Badge>
                      {!srv.is_active && (
                        <Badge variant="outline" className="bg-gray-100 text-gray-500">已禁用</Badge>
                      )}
                    </div>
                    {srv.description && (
                      <p className="text-xs text-muted-foreground">{srv.description}</p>
                    )}
                    <div className="text-xs text-muted-foreground space-y-0.5">
                      {srv.transport === "stdio" && srv.command && (
                        <div className="font-mono">
                          {srv.command} {(srv.args ?? []).join(" ")}
                        </div>
                      )}
                      {srv.transport === "sse" && srv.url && (
                        <div className="font-mono truncate">{srv.url}</div>
                      )}
                      {srv.last_error && (
                        <div className="text-red-500">错误: {srv.last_error}</div>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => handleReconnect(srv.id)}
                      disabled={reconnecting === srv.id}
                      title="重连"
                    >
                      {reconnecting === srv.id ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      ) : (
                        <Zap className="h-3.5 w-3.5" />
                      )}
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => openEdit(srv)}
                      title="编辑"
                    >
                      <Pencil className="h-3.5 w-3.5" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => handleDelete(srv.id)}
                      title="删除"
                      className="text-destructive hover:text-destructive"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* Add/Edit Dialog */}
      <Dialog open={showDialog} onOpenChange={(v) => { if (!v && !saving) { resetForm(); } setShowDialog(v); }}>
        <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editing ? "编辑 MCP 服务器" : "添加 MCP 服务器"}</DialogTitle>
            <DialogDescription>
              配置外部 MCP 服务器连接，支持 stdio 和 SSE 两种传输方式
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4">
            {/* Name */}
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label>名称 *</Label>
                <Input
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder="例如: filesystem"
                  disabled={!!editing}
                />
              </div>
              <div className="space-y-2">
                <Label>传输方式 *</Label>
                <Select
                  value={form.transport}
                  onValueChange={(v) => setForm({ ...form, transport: v })}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="stdio">stdio (本地进程)</SelectItem>
                    <SelectItem value="sse">sse (HTTP SSE)</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>

            {/* Description */}
            <div className="space-y-2">
              <Label>描述</Label>
              <Input
                value={form.description}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                placeholder="服务器用途说明"
              />
            </div>

            {/* stdio fields */}
            {form.transport === "stdio" && (
              <>
                <div className="space-y-2">
                  <Label>命令 *</Label>
                  <Input
                    value={form.command}
                    onChange={(e) => setForm({ ...form, command: e.target.value })}
                    placeholder="例如: npx -y @anthropic/mcp-filesystem"
                  />
                </div>
                <div className="grid grid-cols-2 gap-4">
                  <div className="space-y-2">
                    <Label>参数（每行一个）</Label>
                    <Textarea
                      value={form.argsText}
                      onChange={(e) => setForm({ ...form, argsText: e.target.value })}
                      placeholder={"/tmp/data\n--readonly"}
                      rows={3}
                      className="font-mono text-xs"
                    />
                  </div>
                  <div className="space-y-2">
                    <Label>环境变量（每行一个，KEY=VALUE）</Label>
                    <Textarea
                      value={form.envText}
                      onChange={(e) => setForm({ ...form, envText: e.target.value })}
                      placeholder={"API_KEY=xxx\nNODE_ENV=production"}
                      rows={3}
                      className="font-mono text-xs"
                    />
                  </div>
                </div>
              </>
            )}

            {/* sse fields */}
            {form.transport === "sse" && (
              <div className="space-y-2">
                <Label>URL *</Label>
                <Input
                  value={form.url}
                  onChange={(e) => setForm({ ...form, url: e.target.value })}
                  placeholder="https://mcp-server.example.com/sse"
                />
              </div>
            )}

            {/* Active toggle */}
            <div className="flex items-center gap-2">
              <Switch
                checked={form.is_active}
                onCheckedChange={(v) => setForm({ ...form, is_active: v })}
              />
              <Label className="cursor-pointer">启用（保存后自动连接）</Label>
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => { resetForm(); setShowDialog(false); }} disabled={saving}>
              取消
            </Button>
            <Button onClick={handleSave} disabled={saving || !form.name.trim()}>
              {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
