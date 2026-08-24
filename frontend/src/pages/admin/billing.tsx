/**
 * Admin Billing Page — 计费管理
 *
 * 功能：
 * 1. 概览面板：总收入、总积分、活跃订阅数
 * 2. 费率配置：查看/编辑各模型的积分费率
 * 3. 全局倍率配置
 * 4. 套餐管理
 * 5. 用户计费列表
 * 6. 手动充值
 */
import { useState, useEffect, useCallback } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Wallet, Users, TrendingUp, Settings2, Loader2, RefreshCw, Plus, Save, Ticket } from "lucide-react";
import { useAuthStore } from "@/stores/auth-store";
import { cn } from "@/lib/utils";

// ─── 类型 ──────────────────────────────────────────────

interface BillingOverview {
  total_revenue: number;
  total_points_sold: number;
  total_points_consumed: number;
  active_subscriptions: number;
  total_recharge_orders: number;
}

interface PointRate {
  id: string;
  model_name: string;
  task_type: string;
  input_rate: number;
  output_rate: number;
  is_active: boolean;
  updated_at: string;
}

interface PlanInfo {
  id: string;
  name: string;
  display_name: string;
  price_monthly: number;
  point_quota: number;
  features: Record<string, unknown>;
  is_active: boolean;
  is_popular: boolean;
  sort_order: number;
}

interface UserBilling {
  user_id: string;
  username: string;
  plan_name: string;
  plan_display_name: string;
  balance: number;
  total_consumed: number;
  last_active: string | null;
}

// ─── 辅助 ──────────────────────────────────────────────

function adminHeaders(): Record<string, string> {
  const token = useAuthStore.getState().token;
  return {
    "Content-Type": "application/json",
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

async function adminApi<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...options,
    headers: { ...adminHeaders(), ...(options?.headers || {}) },
  });
  const json = await res.json();
  if (!json.success) throw new Error(json.error?.message || "API error");
  return json.data as T;
}

const fmt = (n: number) => (n || 0).toFixed(1);

// ─── 主组件 ─────────────────────────────────────────────

export function BillingPage() {
  const [tab, setTab] = useState<"overview" | "rates" | "plans" | "users" | "redeem">("overview");

  return (
    <div className="p-6 space-y-6">
      {/* 页面标题 */}
      <div>
        <h2 className="text-lg font-semibold">计费管理</h2>
        <p className="text-xs text-muted-foreground mt-0.5">管理积分费率、套餐、兑换码及用户余额</p>
      </div>

      {/* Tab Bar */}
      <div className="flex items-center gap-1 border-b">
        {[
          { key: "overview", label: "概览" },
          { key: "rates", label: "费率配置" },
          { key: "plans", label: "套餐管理" },
          { key: "redeem", label: "兑换码" },
          { key: "users", label: "用户计费" },
        ].map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key as typeof tab)}
            className={cn(
              "px-3 py-1.5 text-sm transition-colors border-b-2 -mb-px",
              tab === t.key
                ? "border-foreground text-foreground font-medium"
                : "border-transparent text-muted-foreground hover:text-foreground"
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === "overview" && <OverviewTab />}
      {tab === "rates" && <RatesTab />}
      {tab === "plans" && <PlansTab />}
      {tab === "redeem" && <RedeemCodesTab />}
      {tab === "users" && <UsersTab />}
    </div>
  );
}

// ─── 概览 ──────────────────────────────────────────────

function OverviewTab() {
  const [overview, setOverview] = useState<BillingOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [consumption, setConsumption] = useState<{ total_consumed: number; by_category: Record<string, number> } | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [ov, cs] = await Promise.all([
        adminApi<BillingOverview>("/api/v2/admin/billing/overview"),
        adminApi<{ total_consumed: number; by_category: Record<string, number> }>("/api/v2/admin/billing/consumption?days=30"),
      ]);
      setOverview(ov);
      setConsumption(cs);
    } catch {
      // ignore
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  if (loading) {
    return <div className="flex items-center justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>;
  }

  const stats = [
    { label: "总收入", value: `¥${fmt(overview?.total_revenue || 0)}`, icon: Wallet, color: "text-green-600" },
    { label: "总售出积分", value: Math.floor(overview?.total_points_sold || 0).toLocaleString(), icon: Wallet, color: "text-amber-500" },
    { label: "总消耗积分", value: Math.floor(overview?.total_points_consumed || 0).toLocaleString(), icon: TrendingUp, color: "text-orange-500" },
    { label: "活跃订阅", value: overview?.active_subscriptions || 0, icon: Users, color: "text-blue-500" },
  ];

  return (
    <div className="space-y-6">
      {/* 统计卡片 */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        {stats.map((s) => {
          const Icon = s.icon;
          return (
            <Card key={s.label}>
              <CardContent className="p-4">
                <div className="flex items-center justify-between mb-2">
                  <span className="text-xs text-muted-foreground">{s.label}</span>
                  <Icon className={cn("h-4 w-4", s.color)} />
                </div>
                <div className="text-2xl font-bold tracking-tight">{s.value}</div>
              </CardContent>
            </Card>
          );
        })}
      </div>

      {/* 消费分类 */}
      {consumption && Object.keys(consumption.by_category).length > 0 && (
        <Card>
          <CardHeader><CardTitle className="text-sm">近30天消费分布</CardTitle></CardHeader>
          <CardContent>
            <div className="space-y-2">
              {Object.entries(consumption.by_category)
                .sort((a, b) => b[1] - a[1])
                .map(([cat, pts]) => {
                  const total = consumption.total_consumed || 1;
                  const pct = (pts / total) * 100;
                  return (
                    <div key={cat} className="flex items-center gap-3">
                      <span className="text-xs w-20 shrink-0">{cat}</span>
                      <div className="flex-1 h-2 rounded-full bg-muted overflow-hidden">
                        <div className="h-full bg-amber-400" style={{ width: `${pct}%` }} />
                      </div>
                      <span className="text-xs text-muted-foreground w-16 text-right">{fmt(pts)} 积分</span>
                      <span className="text-[10px] text-muted-foreground/60 w-10 text-right">{pct.toFixed(0)}%</span>
                    </div>
                  );
                })}
            </div>
          </CardContent>
        </Card>
      )}

      <Button variant="outline" size="sm" onClick={load} className="gap-1.5">
        <RefreshCw className="h-3.5 w-3.5" />
        刷新
      </Button>
    </div>
  );
}

// ─── 费率配置 ──────────────────────────────────────────

function RatesTab() {
  const [rates, setRates] = useState<PointRate[]>([]);
  const [multiplier, setMultiplier] = useState(1.0);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<Record<string, { input: number; output: number }>>({});
  const [saving, setSaving] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [newRate, setNewRate] = useState({ model_name: "*", task_type: "writing", input_rate: 0.001, output_rate: 0.003 });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await adminApi<{ rates: PointRate[]; global_multiplier: number }>("/api/v2/admin/billing/point-rates");
      setRates(data.rates || []);
      setMultiplier(data.global_multiplier || 1.0);
    } catch {
      // ignore
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleSave = async (rateId: string) => {
    const edit = editing[rateId];
    if (!edit) return;
    setSaving(rateId);
    try {
      await adminApi(`/api/v2/admin/billing/point-rates/${rateId}`, {
        method: "PUT",
        body: JSON.stringify({ input_rate: edit.input, output_rate: edit.output }),
      });
      setEditing((prev) => {
        const next = { ...prev };
        delete next[rateId];
        return next;
      });
      load();
    } catch {
      // ignore
    }
    setSaving(null);
  };

  const handleSetMultiplier = async () => {
    try {
      await adminApi("/api/v2/admin/billing/multiplier", {
        method: "PUT",
        body: JSON.stringify({ multiplier }),
      });
      load();
    } catch {
      // ignore
    }
  };

  const handleAddRate = async () => {
    try {
      await adminApi("/api/v2/admin/billing/point-rates", {
        method: "POST",
        body: JSON.stringify(newRate),
      });
      setShowAdd(false);
      setNewRate({ model_name: "*", task_type: "writing", input_rate: 0.001, output_rate: 0.003 });
      load();
    } catch {
      // ignore
    }
  };

  if (loading) {
    return <div className="flex items-center justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>;
  }

  return (
    <div className="space-y-6">
      {/* 全局倍率 */}
      <Card>
        <CardHeader><CardTitle className="text-sm flex items-center gap-2"><Settings2 className="h-4 w-4" />全局倍率</CardTitle></CardHeader>
        <CardContent>
          <div className="flex items-center gap-3">
            <Input
              type="number"
              step="0.1"
              min="0.1"
              max="10"
              value={multiplier}
              onChange={(e) => setMultiplier(parseFloat(e.target.value) || 1)}
              className="w-24"
            />
            <span className="text-xs text-muted-foreground">当前费率 × 倍率 = 实际扣减积分</span>
            <Button size="sm" onClick={handleSetMultiplier} className="ml-auto">保存</Button>
          </div>
          <p className="text-[10px] text-muted-foreground/60 mt-2">设为 1.5 为促销加价，0.8 为降价。默认 1.0。</p>
        </CardContent>
      </Card>

      {/* 费率列表 */}
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle className="text-sm">积分费率表</CardTitle>
            <Button size="sm" variant="outline" onClick={() => setShowAdd(!showAdd)} className="gap-1">
              <Plus className="h-3.5 w-3.5" />
              新增
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {showAdd && (
            <div className="mb-3 p-3 rounded-lg border border-border bg-muted/30 space-y-2">
              <div className="flex items-center gap-2 flex-wrap">
                <Input placeholder="模型名（* = 通配）" value={newRate.model_name} onChange={(e) => setNewRate({ ...newRate, model_name: e.target.value })} className="w-40 text-xs" />
                <select value={newRate.task_type} onChange={(e) => setNewRate({ ...newRate, task_type: e.target.value })} className="text-xs border rounded-md px-2 py-1.5">
                  <option value="writing">writing</option>
                  <option value="editorial">editorial</option>
                  <option value="memory">memory</option>
                  <option value="fact_check">fact_check</option>
                  <option value="search">search</option>
                </select>
                <Input type="number" step="0.0001" placeholder="输入费率" value={newRate.input_rate} onChange={(e) => setNewRate({ ...newRate, input_rate: parseFloat(e.target.value) || 0 })} className="w-28 text-xs" />
                <Input type="number" step="0.0001" placeholder="输出费率" value={newRate.output_rate} onChange={(e) => setNewRate({ ...newRate, output_rate: parseFloat(e.target.value) || 0 })} className="w-28 text-xs" />
                <Button size="sm" onClick={handleAddRate}>添加</Button>
              </div>
            </div>
          )}

          <div className="space-y-1">
            {rates.map((rate) => {
              const edit = editing[rate.id];
              const isEditing = !!edit;
              return (
                <div key={rate.id} className="flex items-center gap-2 rounded-md border border-border/40 px-3 py-2 text-xs">
                  <Badge variant={rate.model_name === "*" ? "outline" : "secondary"} className="text-[10px]">{rate.model_name}</Badge>
                  <span className="text-muted-foreground">{rate.task_type}</span>
                  <div className="ml-auto flex items-center gap-2">
                    {isEditing ? (
                      <>
                        <Input type="number" step="0.0001" value={edit.input} onChange={(e) => setEditing({ ...editing, [rate.id]: { ...edit, input: parseFloat(e.target.value) || 0 } })} className="w-24 h-7 text-xs" />
                        <Input type="number" step="0.0001" value={edit.output} onChange={(e) => setEditing({ ...editing, [rate.id]: { ...edit, output: parseFloat(e.target.value) || 0 } })} className="w-24 h-7 text-xs" />
                        <Button size="sm" variant="ghost" onClick={() => handleSave(rate.id)} disabled={saving === rate.id} className="h-7 px-2">
                          {saving === rate.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <Save className="h-3 w-3" />}
                        </Button>
                      </>
                    ) : (
                      <>
                        <span className="text-muted-foreground">输入 {rate.input_rate}</span>
                        <span className="text-muted-foreground">输出 {rate.output_rate}</span>
                        <Button size="sm" variant="ghost" onClick={() => setEditing({ ...editing, [rate.id]: { input: rate.input_rate, output: rate.output_rate } })} className="h-7 px-2 text-[10px]">
                          编辑
                        </Button>
                      </>
                    )}
                  </div>
                </div>
              );
            })}
          </div>

          <p className="text-[10px] text-muted-foreground/60 mt-3">
费率 = 每 Token 消耗的积分。系统优先匹配精确模型名，回退到通配符 *。
综合千字积分 ≈ (输入费率×0.67 + 输出费率×0.33) × 1000 × 全局倍率。
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

// ─── 套餐管理 ──────────────────────────────────────────

function PlansTab() {
  const [plans, setPlans] = useState<PlanInfo[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await adminApi<{ plans: PlanInfo[] }>("/api/v2/admin/billing/plans");
      setPlans(data.plans || []);
    } catch {
      // ignore
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  if (loading) {
    return <div className="flex items-center justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>;
  }

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        {plans.map((plan) => (
          <Card key={plan.id}>
            <CardContent className="p-4 space-y-2">
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium">{plan.display_name}</span>
                {plan.is_popular && <Badge variant="default" className="text-[9px]">推荐</Badge>}
              </div>
              <div className="flex items-baseline gap-0.5">
                <span className="text-xl font-bold">¥{plan.price_monthly}</span>
                <span className="text-[10px] text-muted-foreground">/月</span>
              </div>
              <div className="text-xs text-muted-foreground">{Math.floor(plan.point_quota).toLocaleString()} 积分/月</div>
              <div className="flex flex-wrap gap-1 pt-1">
                {Object.entries(plan.features).slice(0, 4).map(([k, v]) => (
                  <Badge key={k} variant="outline" className="text-[9px]">{k}: {String(v)}</Badge>
                ))}
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      <p className="text-[10px] text-muted-foreground/60">套餐配置暂不支持在线编辑，如需修改请联系开发团队。</p>
    </div>
  );
}

// ─── 兑换码管理 ──────────────────────────────────────────

interface RedeemCode {
  id: string;
  code: string;
  point_amount: number;
  batch_label?: string;
  status: "unused" | "used" | "disabled" | "expired";
  redeemed_by?: string;
  redeemed_at?: string;
  expires_at?: string;
  created_at: string;
}

const REDEEM_STATUS_META: Record<string, { label: string; color: string }> = {
  unused: { label: "未使用", color: "text-green-600" },
  used: { label: "已使用", color: "text-muted-foreground" },
  disabled: { label: "已作废", color: "text-red-500" },
  expired: { label: "已过期", color: "text-amber-600" },
};

function RedeemCodesTab() {
  const [codes, setCodes] = useState<RedeemCode[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [statusFilter, setStatusFilter] = useState("");
  const [page, setPage] = useState(1);

  // 生成兑换码表单
  const [showCreate, setShowCreate] = useState(false);
  const [createForm, setCreateForm] = useState({ count: 10, point_amount: 500, batch_label: "", expires_in_days: 0 });
  const [creating, setCreating] = useState(false);
  const [createdCodes, setCreatedCodes] = useState<RedeemCode[] | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const params = new URLSearchParams({ page: String(page), limit: "20" });
      if (statusFilter) params.set("status", statusFilter);
      const data = await adminApi<{ codes: RedeemCode[]; total: number }>(`/api/v2/admin/billing/redeem-codes?${params}`);
      setCodes(data.codes || []);
      setTotal(data.total || 0);
    } catch {
      // ignore
    }
    setLoading(false);
  }, [page, statusFilter]);

  useEffect(() => { load(); }, [load]);

  const handleCreate = async () => {
    setCreating(true);
    try {
      const data = await adminApi<{ codes: RedeemCode[]; count: number }>("/api/v2/admin/billing/redeem-codes", {
        method: "POST",
        body: JSON.stringify(createForm),
      });
      setCreatedCodes(data.codes || []);
      setShowCreate(false);
      load();
    } catch {
      // ignore
    }
    setCreating(false);
  };

  const handleDisable = async (codeId: string) => {
    if (!confirm("确认作废此兑换码？")) return;
    try {
      await adminApi(`/api/v2/admin/billing/redeem-codes/${codeId}`, { method: "DELETE" });
      load();
    } catch {
      // ignore
    }
  };

  const handleCopyCodes = () => {
    if (!createdCodes) return;
    const text = createdCodes.map((c) => c.code).join("\n");
    navigator.clipboard.writeText(text);
  };

  if (loading) {
    return <div className="flex items-center justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>;
  }

  return (
    <div className="space-y-6">
      {/* 操作栏 */}
      <div className="flex items-center gap-2">
        <Button size="sm" onClick={() => setShowCreate(!showCreate)} className="gap-1.5">
          <Plus className="h-3.5 w-3.5" />
          生成兑换码
        </Button>
        <div className="flex items-center gap-1 ml-auto">
          <select
            value={statusFilter}
            onChange={(e) => { setStatusFilter(e.target.value); setPage(1); }}
            className="text-xs border rounded-md px-2 py-1.5"
          >
            <option value="">全部</option>
            <option value="unused">未使用</option>
            <option value="used">已使用</option>
            <option value="disabled">已作废</option>
            <option value="expired">已过期</option>
          </select>
          <Button size="sm" variant="outline" onClick={load} className="gap-1">
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      {/* 生成兑换码表单 */}
      {showCreate && (
        <Card>
          <CardContent className="p-4 space-y-3">
            <div className="text-sm font-medium flex items-center gap-2">
              <Ticket className="h-4 w-4" />
              生成兑换码
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <Label className="text-xs">生成数量（1-500）</Label>
                <Input type="number" min="1" max="500" value={createForm.count} onChange={(e) => setCreateForm({ ...createForm, count: parseInt(e.target.value) || 1 })} className="mt-1" />
              </div>
              <div>
                <Label className="text-xs">每张积分</Label>
                <Input type="number" min="1" value={createForm.point_amount} onChange={(e) => setCreateForm({ ...createForm, point_amount: parseInt(e.target.value) || 100 })} className="mt-1" />
              </div>
              <div>
                <Label className="text-xs">批次标签（可选）</Label>
                <Input value={createForm.batch_label} onChange={(e) => setCreateForm({ ...createForm, batch_label: e.target.value })} placeholder="如：8月活动赠送" className="mt-1" />
              </div>
              <div>
                <Label className="text-xs">有效期（天，0=永久）</Label>
                <Input type="number" min="0" value={createForm.expires_in_days} onChange={(e) => setCreateForm({ ...createForm, expires_in_days: parseInt(e.target.value) || 0 })} className="mt-1" />
              </div>
            </div>
            <div className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => setShowCreate(false)}>取消</Button>
              <Button size="sm" onClick={handleCreate} disabled={creating}>
                {creating ? <Loader2 className="h-3.5 w-3.5 animate-spin mr-1" /> : null}
                确认生成
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      {/* 生成结果弹窗 */}
      {createdCodes && (
        <Card className="border-green-300 dark:border-green-800">
          <CardContent className="p-4 space-y-3">
            <div className="flex items-center justify-between">
              <div className="text-sm font-medium text-green-700 dark:text-green-400">
                成功生成 {createdCodes.length} 个兑换码（每个 {createdCodes[0]?.point_amount || 0} 积分）
              </div>
              <div className="flex gap-2">
                <Button size="sm" variant="outline" onClick={handleCopyCodes} className="text-xs">复制全部</Button>
                <Button size="sm" variant="ghost" onClick={() => setCreatedCodes(null)} className="text-xs">关闭</Button>
              </div>
            </div>
            <div className="grid grid-cols-2 sm:grid-cols-3 gap-1.5 max-h-60 overflow-y-auto">
              {createdCodes.map((c) => (
                <div key={c.id} className="rounded-md border border-border/40 px-2 py-1.5 text-xs font-mono text-center">
                  {c.code}
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* 兑换码列表 */}
      <Card>
        <CardContent className="p-0">
          <div className="space-y-1">
            {codes.length === 0 ? (
              <div className="py-12 text-center text-sm text-muted-foreground">暂无兑换码</div>
            ) : (
              codes.map((code) => {
                const meta = REDEEM_STATUS_META[code.status] || { label: code.status, color: "" };
                return (
                  <div key={code.id} className="flex items-center gap-3 border-b border-border/30 px-3 py-2 text-xs last:border-0">
                    <span className="font-mono font-medium min-w-[140px]">{code.code}</span>
                    <span className="text-amber-600 dark:text-amber-400">{Math.floor(code.point_amount)} 积分</span>
                    {code.batch_label && <Badge variant="outline" className="text-[9px]">{code.batch_label}</Badge>}
                    <Badge variant="outline" className={cn("text-[9px]", meta.color)}>{meta.label}</Badge>
                    {code.redeemed_at && (
                      <span className="text-[10px] text-muted-foreground/60">
                        {new Date(code.redeemed_at).toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })}
                      </span>
                    )}
                    {code.status === "unused" && (
                      <Button size="sm" variant="ghost" onClick={() => handleDisable(code.id)} className="ml-auto h-6 px-2 text-[10px] text-muted-foreground hover:text-destructive">
                        作废
                      </Button>
                    )}
                  </div>
                );
              })
            )}
          </div>
        </CardContent>
      </Card>

      {total > 20 && (
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>共 {total} 条</span>
          <div className="flex gap-2">
            <Button size="sm" variant="outline" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</Button>
            <span className="py-1">第 {page} 页</span>
            <Button size="sm" variant="outline" disabled={page * 20 >= total} onClick={() => setPage(page + 1)}>下一页</Button>
          </div>
        </div>
      )}
    </div>
  );
}

function UsersTab() {
  const [users, setUsers] = useState<UserBilling[]>([]);
  const [loading, setLoading] = useState(true);
  const [rechargeUser, setRechargeUser] = useState<string | null>(null);
  const [rechargePoints, setRechargePoints] = useState(1000);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await adminApi<{ users: UserBilling[] }>("/api/v2/admin/billing/users?page=1&limit=20");
      setUsers(data.users || []);
    } catch {
      // ignore
    }
    setLoading(false);
  }, []);

  useEffect(() => { load(); }, [load]);

  const handleRecharge = async () => {
    if (!rechargeUser || rechargePoints <= 0) return;
    try {
      await adminApi("/api/v2/admin/billing/recharge", {
        method: "POST",
        body: JSON.stringify({ user_id: rechargeUser, points: rechargePoints }),
      });
      setRechargeUser(null);
      load();
    } catch {
      // ignore
    }
  };

  if (loading) {
    return <div className="flex items-center justify-center py-12"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>;
  }

  return (
    <div className="space-y-6">
      <div className="space-y-1">
        {users.map((u) => (
          <div key={u.user_id} className="flex items-center gap-3 rounded-md border border-border/40 px-3 py-2 text-xs">
            <div className="min-w-0 flex-1">
              <span className="font-medium">{u.username || u.user_id.slice(0, 8)}</span>
              <span className="text-muted-foreground ml-2">{u.plan_display_name}</span>
            </div>
            <div className="flex items-center gap-3 shrink-0">
              <div className="text-right">
                <div className="text-muted-foreground">余额</div>
                <div className="font-mono">{Math.floor(u.balance)}</div>
              </div>
              <div className="text-right">
                <div className="text-muted-foreground">累计消费</div>
                <div className="font-mono text-orange-600 dark:text-orange-400">{fmt(u.total_consumed)}</div>
              </div>
              <Button size="sm" variant="outline" onClick={() => { setRechargeUser(u.user_id); setRechargePoints(1000); }} className="h-7 text-[10px]">
                充值
              </Button>
            </div>
          </div>
        ))}
      </div>

      {/* 手动充值弹窗 */}
      {rechargeUser && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={() => setRechargeUser(null)}>
          <div className="rounded-xl border border-border bg-background p-4 w-80 space-y-3" onClick={(e) => e.stopPropagation()}>
            <div className="text-sm font-medium">手动充值</div>
            <p className="text-xs text-muted-foreground">用户 ID: {rechargeUser.slice(0, 12)}...</p>
            <div className="flex items-center gap-2">
              <Input type="number" value={rechargePoints} onChange={(e) => setRechargePoints(parseInt(e.target.value) || 0)} className="flex-1" />
              <span className="text-xs text-muted-foreground">积分</span>
            </div>
            <div className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => setRechargeUser(null)} className="flex-1">取消</Button>
              <Button size="sm" onClick={handleRecharge} className="flex-1">确认充值</Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
