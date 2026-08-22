/**
 * MCP 安全沙箱管理 — Admin Dashboard
 *
 * 完整的安全沙箱管理 UI：
 * 1. 统计概览（策略数、24h 违规数、违规类型分布）
 * 2. 策略列表（allow/deny/conditional + 域名黑白名单 + 资源限制）
 * 3. 创建/编辑策略（模态框）
 * 4. 违规日志（拦截记录列表 + 过滤）
 * 5. 沙箱测试面板（模拟工具调用检查策略匹配）
 */
import { useState, useEffect, useCallback } from "react";
import {
  ShieldCheck, RefreshCw, Plus, Trash2, Edit3, FlaskConical,
  AlertTriangle, CheckCircle, XCircle, Shield, Globe,
  Clock, Zap, Gauge, type LucideIcon,
} from "lucide-react";
import { adminFetch, adminMutate } from "@/lib/admin-api";
import { AdminPageHeader, AdminLoading, AdminEmptyState } from "@/components/admin";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter,
  DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

// ─── Types ──────────────────────────────────────────────

interface ToolPolicy {
  id: string;
  server_name: string;
  tool_name: string;
  mode: string;
  allowed_domains: string[];
  blocked_domains: string[];
  max_arg_length: number;
  max_result_length: number;
  timeout_ms: number;
  rate_limit_per_min: number;
  description: string;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

interface ViolationRecord {
  id: string;
  policy_id?: string;
  server_name: string;
  tool_name: string;
  violation_type: string;
  detail: string;
  args_summary: string;
  trace_id: string;
  user_id: string;
  created_at: string;
}

interface SandboxStats {
  policy_count: number;
  violation_count_24h: number;
  violations_by_type: Record<string, number>;
}

// ─── Constants ──────────────────────────────────────────

const MODE_STYLES: Record<string, string> = {
  allow: "bg-emerald-50 text-emerald-700 border-emerald-200",
  deny: "bg-red-50 text-red-700 border-red-200",
  conditional: "bg-amber-50 text-amber-700 border-amber-200",
};

const MODE_LABELS: Record<string, string> = {
  allow: "允许",
  deny: "拒绝",
  conditional: "条件",
};

const VIOLATION_LABELS: Record<string, string> = {
  denied: "策略拒绝",
  blocked_domain: "域名拦截",
  arg_too_large: "参数过大",
  timeout: "执行超时",
  rate_limit: "频率限制",
};

const VIOLATION_COLORS: Record<string, string> = {
  denied: "text-red-600",
  blocked_domain: "text-orange-600",
  arg_too_large: "text-amber-600",
  timeout: "text-purple-600",
  rate_limit: "text-blue-600",
};

// ─── Component ──────────────────────────────────────────

export function MCPSandboxPage() {
  const [policies, setPolicies] = useState<ToolPolicy[]>([]);
  const [violations, setViolations] = useState<ViolationRecord[]>([]);
  const [stats, setStats] = useState<SandboxStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [editingPolicy, setEditingPolicy] = useState<ToolPolicy | null>(null);
  const [creating, setCreating] = useState(false);

  const loadData = useCallback(async () => {
    setLoading(true);
    const [pRes, vRes, sRes] = await Promise.all([
      adminFetch<{ policies: ToolPolicy[]; total: number }>("/api/v2/admin/mcp/sandbox/policies", { silent: true }),
      adminFetch<{ violations: ViolationRecord[]; total: number }>("/api/v2/admin/mcp/sandbox/violations?limit=50", { silent: true }),
      adminFetch<SandboxStats>("/api/v2/admin/mcp/sandbox/stats", { silent: true }),
    ]);
    if (pRes.success && pRes.data) setPolicies(pRes.data.policies ?? []);
    if (vRes.success && vRes.data) setViolations(vRes.data.violations ?? []);
    if (sRes.success && sRes.data) setStats(sRes.data);
    setLoading(false);
  }, []);

  useEffect(() => { loadData(); }, [loadData]);

  const handleDelete = async (id: string) => {
    if (!confirm("确定删除此安全策略？")) return;
    const { success } = await adminMutate(`/api/v2/admin/mcp/sandbox/policies/${id}`, { method: "DELETE" });
    if (success) { await loadData(); }
  };

  if (loading) return <AdminLoading />;

  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader
        title="MCP 安全沙箱"
        description="MCP 外部工具安全策略 — 网络域名限制、资源配额、频率控制、违规审计"
        action={
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={loadData} disabled={loading}>
              <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} /> 刷新
            </Button>
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus className="h-4 w-4 mr-2" /> 新建策略
            </Button>
          </div>
        }
      />

      {/* Stats Overview */}
      {stats && (
        <div className="grid grid-cols-4 gap-3">
          <StatCard icon={ShieldCheck} label="活跃策略" value={stats.policy_count} color="text-emerald-600" />
          <StatCard icon={AlertTriangle} label="24h 违规" value={stats.violation_count_24h} color="text-red-600" />
          <StatCard icon={Globe} label="域名拦截" value={stats.violations_by_type?.blocked_domain ?? 0} color="text-orange-600" />
          <StatCard icon={Zap} label="频率限制" value={stats.violations_by_type?.rate_limit ?? 0} color="text-blue-600" />
        </div>
      )}

      {/* Policies List */}
      <div className="space-y-3">
        <h3 className="text-sm font-semibold text-muted-foreground">安全策略 ({policies.length})</h3>
        {policies.length === 0 ? (
          <AdminEmptyState icon={ShieldCheck} title="暂无安全策略" description="点击「新建策略」创建 MCP 工具安全策略" />
        ) : (
          policies.map((p) => (
            <PolicyCard
              key={p.id}
              policy={p}
              onEdit={() => setEditingPolicy(p)}
              onDelete={() => handleDelete(p.id)}
            />
          ))
        )}
      </div>

      {/* Violations */}
      <div className="space-y-3">
        <h3 className="text-sm font-semibold text-muted-foreground">最近违规记录 ({violations.length})</h3>
        {violations.length === 0 ? (
          <Card><CardContent className="p-4 text-center text-sm text-muted-foreground">
            <CheckCircle className="h-5 w-5 mx-auto mb-1 text-emerald-500" />
            最近无违规记录
          </CardContent></Card>
        ) : (
          <div className="space-y-2">
            {violations.slice(0, 20).map((v) => (
              <ViolationRow key={v.id} v={v} />
            ))}
          </div>
        )}
      </div>

      {/* Policy Editor Dialog */}
      {(creating || editingPolicy) && (
        <PolicyEditorDialog
          policy={editingPolicy}
          onClose={() => { setCreating(false); setEditingPolicy(null); }}
          onSaved={() => { setCreating(false); setEditingPolicy(null); loadData(); }}
        />
      )}
    </div>
  );
}

// ─── Sub-components ─────────────────────────────────────

function StatCard({ icon: Icon, label, value, color }: { icon: LucideIcon; label: string; value: number; color: string }) {
  return (
    <Card>
      <CardContent className="p-3 flex items-center gap-3">
        <Icon className={`h-5 w-5 ${color}`} />
        <div>
          <div className="text-2xl font-bold">{value}</div>
          <div className="text-xs text-muted-foreground">{label}</div>
        </div>
      </CardContent>
    </Card>
  );
}

function PolicyCard({ policy: p, onEdit, onDelete }: {
  policy: ToolPolicy;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <Card>
      <CardContent className="p-4">
        <div className="flex items-start justify-between gap-4">
          <div className="flex-1 space-y-2">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="font-mono text-sm font-medium">{p.server_name}</span>
              <span className="text-muted-foreground">/</span>
              <span className="font-mono text-sm">{p.tool_name}</span>
              <Badge className={MODE_STYLES[p.mode] ?? "bg-gray-100 text-gray-700"}>
                {MODE_LABELS[p.mode] ?? p.mode}
              </Badge>
              {!p.is_active && (
                <Badge variant="outline" className="text-gray-500">已禁用</Badge>
              )}
            </div>

            {p.description && (
              <p className="text-xs text-muted-foreground">{p.description}</p>
            )}

            <div className="flex items-center gap-4 text-xs text-muted-foreground flex-wrap">
              <span className="flex items-center gap-1">
                <Gauge className="h-3 w-3" /> 参数上限: {p.max_arg_length.toLocaleString()}
              </span>
              <span className="flex items-center gap-1">
                <ShieldCheck className="h-3 w-3" /> 输出上限: {p.max_result_length.toLocaleString()}
              </span>
              <span className="flex items-center gap-1">
                <Clock className="h-3 w-3" /> 超时: {p.timeout_ms}ms
              </span>
              <span className="flex items-center gap-1">
                <Zap className="h-3 w-3" /> 频率: {p.rate_limit_per_min}/min
              </span>
            </div>

            {(p.blocked_domains.length > 0 || p.allowed_domains.length > 0) && (
              <div className="flex gap-3 text-xs flex-wrap">
                {p.blocked_domains.length > 0 && (
                  <div className="flex items-center gap-1">
                    <XCircle className="h-3 w-3 text-red-500" />
                    <span className="text-red-600">黑名单: {p.blocked_domains.join(", ")}</span>
                  </div>
                )}
                {p.allowed_domains.length > 0 && (
                  <div className="flex items-center gap-1">
                    <CheckCircle className="h-3 w-3 text-emerald-500" />
                    <span className="text-emerald-600">白名单: {p.allowed_domains.join(", ")}</span>
                  </div>
                )}
              </div>
            )}
          </div>

          <div className="flex flex-col gap-1">
            <Button size="sm" variant="ghost" className="h-7" onClick={onEdit}>
              <Edit3 className="h-3.5 w-3.5" />
            </Button>
            <Button size="sm" variant="ghost" className="h-7 text-red-600" onClick={onDelete}>
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function ViolationRow({ v }: { v: ViolationRecord }) {
  return (
    <div className="flex items-center gap-3 p-2 bg-muted/20 rounded text-xs">
      <Badge variant="outline" className={`text-xs ${VIOLATION_COLORS[v.violation_type] ?? "text-gray-600"}`}>
        {VIOLATION_LABELS[v.violation_type] ?? v.violation_type}
      </Badge>
      <span className="font-mono">{v.server_name}/{v.tool_name}</span>
      <span className="text-muted-foreground flex-1 truncate">{v.detail}</span>
      <span className="text-muted-foreground whitespace-nowrap">
        {new Date(v.created_at).toLocaleString("zh-CN")}
      </span>
    </div>
  );
}

// ─── Policy Editor Dialog ───────────────────────────────

function PolicyEditorDialog({ policy, onClose, onSaved }: {
  policy: ToolPolicy | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState({
    server_name: policy?.server_name ?? "",
    tool_name: policy?.tool_name ?? "*",
    mode: policy?.mode ?? "allow",
    allowed_domains: policy?.allowed_domains?.join(", ") ?? "",
    blocked_domains: policy?.blocked_domains?.join(", ") ?? "",
    max_arg_length: policy?.max_arg_length ?? 10000,
    max_result_length: policy?.max_result_length ?? 2000,
    timeout_ms: policy?.timeout_ms ?? 30000,
    rate_limit_per_min: policy?.rate_limit_per_min ?? 60,
    description: policy?.description ?? "",
    is_active: policy?.is_active ?? true,
  });
  const [saving, setSaving] = useState(false);
  const [testResult, setTestResult] = useState<{ allowed: boolean; detail: string } | null>(null);

  const handleSave = async () => {
    setSaving(true);
    const body = {
      server_name: form.server_name || "*",
      tool_name: form.tool_name || "*",
      mode: form.mode,
      allowed_domains: form.allowed_domains.split(",").map(s => s.trim()).filter(Boolean),
      blocked_domains: form.blocked_domains.split(",").map(s => s.trim()).filter(Boolean),
      max_arg_length: Number(form.max_arg_length) || 10000,
      max_result_length: Number(form.max_result_length) || 2000,
      timeout_ms: Number(form.timeout_ms) || 30000,
      rate_limit_per_min: Number(form.rate_limit_per_min) || 60,
      description: form.description,
      is_active: form.is_active,
    };

    const url = policy
      ? `/api/v2/admin/mcp/sandbox/policies/${policy.id}`
      : "/api/v2/admin/mcp/sandbox/policies";
    const method = policy ? "PUT" : "POST";

    const { success } = await adminMutate(url, { method, body: JSON.stringify(body) });
    if (success) { onSaved(); }
    setSaving(false);
  };

  const handleTest = async () => {
    setTestResult(null);
    const body = {
      server_name: form.server_name || "*",
      tool_name: form.tool_name || "*",
      args: { test: "sandbox-test" },
    };
    const { success, data } = await adminMutate<{ allowed: boolean; detail: string }>(
      "/api/v2/admin/mcp/sandbox/test",
      { method: "POST", body: JSON.stringify(body) }
    );
    if (success && data) { setTestResult(data); }
  };

  const update = (key: string, value: any) => setForm(prev => ({ ...prev, [key]: value }));

  return (
    <Dialog open={true} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="max-w-lg max-h-[80vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Shield className="h-5 w-5" />
            {policy ? "编辑安全策略" : "新建安全策略"}
          </DialogTitle>
          <DialogDescription>
            配置 MCP 工具的安全策略 — 模式、域名限制、资源配额、频率控制
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          {/* Server + Tool */}
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label className="text-xs text-muted-foreground">服务器名 (或 *)</Label>
              <Input value={form.server_name} onChange={(e) => update("server_name", e.target.value)}
                placeholder="*" />
            </div>
            <div>
              <Label className="text-xs text-muted-foreground">工具名 (或 *)</Label>
              <Input value={form.tool_name} onChange={(e) => update("tool_name", e.target.value)}
                placeholder="*" />
            </div>
          </div>

          {/* Mode */}
          <div>
            <Label className="text-xs text-muted-foreground">模式</Label>
            <div className="flex gap-2 mt-1">
              {["allow", "deny", "conditional"].map((m) => (
                <button
                  key={m}
                  onClick={() => update("mode", m)}
                  className={`px-3 py-1 rounded text-xs border ${
                    form.mode === m
                      ? MODE_STYLES[m] + " border-current"
                      : "bg-gray-50 text-gray-600 border-gray-200"
                  }`}
                >
                  {MODE_LABELS[m]}
                </button>
              ))}
            </div>
          </div>

          {/* Domain Lists */}
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label className="text-xs text-muted-foreground">允许域名 (逗号分隔)</Label>
              <Input value={form.allowed_domains} onChange={(e) => update("allowed_domains", e.target.value)}
                placeholder="example.com, api.example.com" />
            </div>
            <div>
              <Label className="text-xs text-muted-foreground">阻止域名 (逗号分隔)</Label>
              <Input value={form.blocked_domains} onChange={(e) => update("blocked_domains", e.target.value)}
                placeholder="evil.com, malicious.io" />
            </div>
          </div>

          {/* Resource Limits */}
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label className="text-xs text-muted-foreground">最大参数长度 (字符)</Label>
              <Input type="number" value={form.max_arg_length}
                onChange={(e) => update("max_arg_length", e.target.value)} />
            </div>
            <div>
              <Label className="text-xs text-muted-foreground">最大输出长度 (字符)</Label>
              <Input type="number" value={form.max_result_length}
                onChange={(e) => update("max_result_length", e.target.value)} />
            </div>
            <div>
              <Label className="text-xs text-muted-foreground">超时 (毫秒)</Label>
              <Input type="number" value={form.timeout_ms}
                onChange={(e) => update("timeout_ms", e.target.value)} />
            </div>
            <div>
              <Label className="text-xs text-muted-foreground">频率限制 (次/分钟)</Label>
              <Input type="number" value={form.rate_limit_per_min}
                onChange={(e) => update("rate_limit_per_min", e.target.value)} />
            </div>
          </div>

          {/* Description */}
          <div>
            <Label className="text-xs text-muted-foreground">描述</Label>
            <Textarea value={form.description} onChange={(e) => update("description", e.target.value)}
              placeholder="策略描述..." rows={2} />
          </div>

          {/* Active toggle */}
          <div>
            <label className="flex items-center gap-2 text-xs cursor-pointer">
              <input type="checkbox" checked={form.is_active}
                onChange={(e) => update("is_active", e.target.checked)}
                className="rounded border-gray-300" />
              策略启用
            </label>
          </div>

          {/* Test result */}
          {testResult && (
            <div className={`p-2 rounded text-xs flex items-center gap-2 ${
              testResult.allowed ? "bg-emerald-50 text-emerald-700" : "bg-red-50 text-red-700"
            }`}>
              {testResult.allowed ? <CheckCircle className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
              {testResult.detail || (testResult.allowed ? "允许通过" : "被拦截")}
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={handleTest}>
            <FlaskConical className="h-4 w-4 mr-2" /> 测试
          </Button>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? <RefreshCw className="h-4 w-4 mr-2 animate-spin" /> : null}
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
