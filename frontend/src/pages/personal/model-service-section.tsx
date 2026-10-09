/**
 * 模型服务子页面 — BYOK（自带密钥）+ 实例默认（回退）
 *
 * 两组结构：
 *   1. 我的模型：用户自带的模型端点与密钥，写作时优先使用；
 *   2. 实例默认：部署级全局模型配置（原「风格和技能」窗的全局默认模型
 *      页迁入），仅当用户未配置对应模型时作为回退。
 * 密钥只显示掩码，明文仅写入时提交。
 */
import { useEffect, useState } from "react";
import {
  Cpu, Trash2, Plus, KeyRound, PlugZap, Star, RefreshCw, Pencil, CheckCircle2, XCircle,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { useAuthStore } from "@/stores/auth-store";
import { useToastStore } from "@/stores/toast-store";
import { cn } from "@/lib/utils";
import { SimpleModal, formatDate } from "./shared";
import {
  type UserModelKey, type UserModelKeyInput, type ProbeResult,
  PURPOSE_OPTIONS,
  listModelKeys, createModelKey, updateModelKey, deleteModelKey,
  setDefaultModelKey, testModelKey, discoverModels, PROBE_ERROR_LABELS,
} from "@/lib/model-keys-api";
import { ModelConfigsPage } from "@/pages/plugins/global-models-page";

const PROVIDERS = [
  { value: "deepseek", label: "DeepSeek" },
  { value: "openai", label: "OpenAI" },
  { value: "qwen", label: "通义千问" },
  { value: "kimi", label: "Kimi" },
  { value: "claude", label: "Claude" },
  { value: "custom", label: "自定义（OpenAI 兼容）" },
];

const EMPTY_FORM = {
  name: "",
  provider: "deepseek",
  base_url: "",
  api_key: "",
  model_name: "",
  max_tokens: "8192",
  temperature: "0.7",
  purpose: "generation" as "generation" | "verification",
  is_default: false,
};

type FormState = typeof EMPTY_FORM;

export function ModelServiceSection() {
  const isGuest = useAuthStore((s) => s.user?.role === "guest");

  // toast.add 的 duration 是必填字段（运行时有默认值但类型未标注），本地统一补齐
  const notify = (
    type: "success" | "error" | "warning" | "info",
    title: string,
    description?: string,
  ) => useToastStore.getState().add({ type, title, description, duration: 3000 });

  const [keys, setKeys] = useState<UserModelKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [editing, setEditing] = useState<UserModelKey | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [saving, setSaving] = useState(false);
  const [discovered, setDiscovered] = useState<string[]>([]);
  const [probing, setProbing] = useState<string | null>(null); // key id 或 "form"
  const [probeResult, setProbeResult] = useState<Record<string, ProbeResult>>({});
  const [deleting, setDeleting] = useState<UserModelKey | null>(null);

  const reload = async () => {
    setLoading(true);
    try {
      setKeys(await listModelKeys());
    } catch (e) {
      notify("error", "模型服务列表加载失败", (e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!isGuest) reload();
  }, [isGuest]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const handler = () => openCreate();
    window.addEventListener("personal-center-add", handler);
    return () => window.removeEventListener("personal-center-add", handler);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const openCreate = () => {
    setEditing(null);
    setForm(EMPTY_FORM);
    setDiscovered([]);
    setShowForm(true);
  };

  const openEdit = (k: UserModelKey) => {
    setEditing(k);
    setForm({
      name: k.name,
      provider: k.provider,
      base_url: k.base_url,
      api_key: "", // 编辑时留空 = 保留已存密钥
      model_name: k.model_name,
      max_tokens: String(k.max_tokens || 8192),
      temperature: String(k.temperature ?? 0.7),
      purpose: (k.purpose === "verification" ? "verification" : "generation"),
      is_default: k.is_default,
    });
    setDiscovered([]);
    setShowForm(true);
  };

  const toastProbe = (r: ProbeResult) => {
    if (r.ok) {
      notify("success", `连通成功（${r.latency_ms ?? "?"}ms）`, `发现 ${r.total ?? 0} 个模型`);
    } else {
      notify("error", PROBE_ERROR_LABELS[r.error_code ?? ""] ?? "连通失败", r.message);
    }
  };

  const handleDiscover = async () => {
    if (!form.api_key && !editing?.has_api_key) {
      notify("warning", "请先填写 API 密钥");
      return;
    }
    // discover 只接受明文密钥（不落库）；编辑态密钥不可回传明文，
    // 提示保存后用列表上的「测试连通」验证已存密钥。
    if (!form.api_key && editing) {
      notify("info", "编辑时请直接保存后使用「测试连通」", "密钥不会回传明文");
      return;
    }
    setProbing("form");
    try {
      const r = await discoverModels({
        provider: form.provider,
        base_url: form.base_url || undefined,
        api_key: form.api_key,
        custom_headers: undefined,
      });
      setDiscovered(r.models ?? []);
      toastProbe(r);
    } catch (e) {
      notify("error", "模型发现失败", (e as Error).message);
    } finally {
      setProbing(null);
    }
  };

  const handleSave = async () => {
    if (!form.provider || !form.model_name) return;
    setSaving(true);
    const input: UserModelKeyInput = {
      name: form.name,
      provider: form.provider,
      model_name: form.model_name,
      base_url: form.base_url,
      max_tokens: Number(form.max_tokens) || 8192,
      temperature: Number(form.temperature) || 0.7,
      purpose: form.purpose,
      is_default: form.is_default,
    };
    if (form.api_key) input.api_key = form.api_key;
    try {
      if (editing) {
        await updateModelKey(editing.id, input);
        notify("success", "模型配置已更新");
      } else {
        if (!form.api_key) {
          notify("warning", "请填写 API 密钥");
          setSaving(false);
          return;
        }
        await createModelKey(input);
        notify("success", "模型配置已添加");
      }
      setShowForm(false);
      await reload();
    } catch (e) {
      notify("error", "保存失败", (e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const handleTest = async (k: UserModelKey) => {
    setProbing(k.id);
    try {
      const r = await testModelKey(k.id);
      setProbeResult((prev) => ({ ...prev, [k.id]: r }));
      toastProbe(r);
    } catch (e) {
      notify("error", "测试失败", (e as Error).message);
    } finally {
      setProbing(null);
    }
  };

  const handleSetDefault = async (k: UserModelKey) => {
    try {
      await setDefaultModelKey(k.id);
      notify("success", `已将 ${k.model_name} 设为默认`);
      await reload();
    } catch (e) {
      notify("error", "设置默认失败", (e as Error).message);
    }
  };

  const handleDelete = async () => {
    if (!deleting) return;
    try {
      await deleteModelKey(deleting.id);
      notify("success", "已删除模型配置");
      setDeleting(null);
      await reload();
    } catch (e) {
      notify("error", "删除失败", (e as Error).message);
    }
  };

  if (isGuest) {
    return (
      <div className="px-6 pt-6 pb-12 space-y-6">
        <Card className="border-amber-200/60 bg-amber-50/50 dark:bg-amber-950/20">
          <CardContent className="py-6 text-center">
            <Cpu className="mx-auto h-10 w-10 text-amber-500/50" />
            <p className="mt-3 text-sm text-amber-900 dark:text-amber-200 font-medium">
              游客模式无法配置模型服务
            </p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">
              注册后可自带 API 密钥，使用自己的模型额度
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="px-6 pt-6 pb-12 space-y-4">
      {/* 回退语义说明 */}
      <Card className="border-blue-200/60 bg-blue-50/50 dark:bg-blue-950/20">
        <CardContent className="py-3 flex items-start gap-2.5">
          <KeyRound className="h-4 w-4 text-blue-600 mt-0.5 shrink-0" />
          <p className="text-xs text-blue-900 dark:text-blue-200 leading-relaxed">
            在这里配置你自己的模型 API 密钥（BYOK）。写作时<b>优先使用你的配置</b>；
            未配置的模型会自动回退到本实例的默认模型。密钥加密存储，仅显示掩码。
          </p>
        </CardContent>
      </Card>

      {loading ? (
        <div className="py-12 text-center text-muted-foreground text-sm">加载中...</div>
      ) : keys.length === 0 ? (
        <div className="py-10 text-center">
          <Cpu className="mx-auto h-12 w-12 text-muted-foreground/30" />
          <p className="mt-3 text-sm text-muted-foreground">
            还没有自己的模型配置。添加后写作将使用你自己的密钥与额度。
          </p>
          <Button className="mt-4" onClick={openCreate}>
            <Plus className="mr-1.5 h-4 w-4" /> 添加模型配置
          </Button>
        </div>
      ) : (
        <div className="space-y-2">
          {keys.map((k) => {
            const probe = probeResult[k.id];
            return (
              <Card key={k.id} className="overflow-hidden">
                <CardContent className="flex items-start gap-3 py-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-sm font-medium">{k.model_name}</span>
                      {k.is_default && (
                        <Badge variant="outline" className="text-xs text-amber-600 border-amber-300">
                          <Star className="mr-0.5 h-3 w-3" /> 默认
                        </Badge>
                      )}
                      <Badge variant="secondary" className="text-xs">{k.provider}</Badge>
                      {k.purpose === "verification" && (
                        <Badge variant="outline" className="text-[10px] text-purple-600 border-purple-300">评审专用</Badge>
                      )}
                      {probe && (
                        probe.ok
                          ? <Badge variant="outline" className="text-xs text-green-600 border-green-300"><CheckCircle2 className="mr-0.5 h-3 w-3" /> {probe.latency_ms}ms</Badge>
                          : <Badge variant="outline" className="text-xs text-red-600 border-red-300"><XCircle className="mr-0.5 h-3 w-3" /> {PROBE_ERROR_LABELS[probe.error_code ?? ""] ?? "不可用"}</Badge>
                      )}
                    </div>
                    <div className="mt-1 flex items-center gap-3 text-xs text-muted-foreground">
                      {k.name && <span>{k.name}</span>}
                      <span className="font-mono">{k.api_key_masked || "未配置密钥"}</span>
                      <span>更新于 {formatDate(k.updated_at)}</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <button
                      onClick={() => handleTest(k)}
                      disabled={probing === k.id}
                      className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
                      title="测试连通"
                    >
                      {probing === k.id ? <RefreshCw className="h-4 w-4 animate-spin" /> : <PlugZap className="h-4 w-4" />}
                    </button>
                    {!k.is_default && (
                      <button
                        onClick={() => handleSetDefault(k)}
                        className="p-1.5 rounded-md text-muted-foreground hover:text-amber-600 hover:bg-accent transition-colors"
                        title="设为默认"
                      >
                        <Star className="h-4 w-4" />
                      </button>
                    )}
                    <button
                      onClick={() => openEdit(k)}
                      className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                      title="编辑"
                    >
                      <Pencil className="h-4 w-4" />
                    </button>
                    <button
                      onClick={() => setDeleting(k)}
                      className="p-1.5 rounded-md text-muted-foreground hover:text-destructive transition-colors shrink-0"
                      title="删除"
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </div>
                </CardContent>
              </Card>
            );
          })}
          <Button variant="outline" className="w-full" onClick={openCreate}>
            <Plus className="mr-1.5 h-4 w-4" /> 添加模型配置
          </Button>
        </div>
      )}

      {/* ─── 实例默认（回退）─── */}
      <div className="space-y-3 pt-2">
        <div className="flex items-center gap-2">
          <Cpu className="h-4 w-4 text-muted-foreground" />
          <h3 className="text-sm font-semibold">实例默认（回退）</h3>
        </div>
        <p className="text-xs text-muted-foreground leading-relaxed">
          部署级模型配置：仅当用户未在「我的模型」配置对应模型时作为回退使用。
          支持任意 OpenAI 兼容端点、模型发现、思考深度与自定义请求头；密钥加密存储。
        </p>
        <ModelConfigsPage />
      </div>

      {/* 新增/编辑弹窗 */}
      <SimpleModal open={showForm} onClose={() => setShowForm(false)} title={editing ? "编辑模型配置" : "添加模型配置"} maxWidth="max-w-xl">
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label>服务商</Label>
              <Select value={form.provider} onValueChange={(v) => setForm((f) => ({ ...f, provider: v }))}>
                <SelectTrigger className="mt-1.5"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {PROVIDERS.map((p) => (
                    <SelectItem key={p.value} value={p.value}>{p.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div>
              <Label>备注名（可选）</Label>
              <Input
                className="mt-1.5"
                placeholder="如：我的 DeepSeek"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              />
            </div>
          </div>

          <div>
            <div className="flex items-center justify-between">
              <Label>API 密钥 {editing && <span className="text-xs text-muted-foreground">（留空保留 {editing.api_key_masked}）</span>}</Label>
            </div>
            <div className="flex gap-2 mt-1.5">
              <Input
                type="password"
                placeholder="sk-..."
                value={form.api_key}
                onChange={(e) => setForm((f) => ({ ...f, api_key: e.target.value }))}
              />
              <Button
                type="button" variant="outline" className="shrink-0"
                disabled={probing === "form" || (!form.api_key && !editing?.has_api_key)}
                onClick={handleDiscover}
              >
                {probing === "form" ? <RefreshCw className="h-4 w-4 animate-spin" /> : "获取模型列表"}
              </Button>
            </div>
          </div>

          <div>
            <Label>模型名称</Label>
            {discovered.length > 0 ? (
              <Select value={form.model_name} onValueChange={(v) => setForm((f) => ({ ...f, model_name: v }))}>
                <SelectTrigger className="mt-1.5"><SelectValue placeholder="选择模型" /></SelectTrigger>
                <SelectContent className="max-h-56">
                  {discovered.map((m) => (
                    <SelectItem key={m} value={m}>{m}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <Input
                className="mt-1.5"
                placeholder="如：deepseek-chat"
                value={form.model_name}
                onChange={(e) => setForm((f) => ({ ...f, model_name: e.target.value }))}
              />
            )}
          </div>

          <div>
            <Label>Base URL（可选，留空用服务商默认）</Label>
            <Input
              className="mt-1.5 font-mono text-xs"
              placeholder="https://api.deepseek.com"
              value={form.base_url}
              onChange={(e) => setForm((f) => ({ ...f, base_url: e.target.value }))}
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label>最大 Tokens</Label>
              <Input
                className="mt-1.5"
                type="number"
                value={form.max_tokens}
                onChange={(e) => setForm((f) => ({ ...f, max_tokens: e.target.value }))}
              />
            </div>
            <div>
              <Label>温度</Label>
              <Input
                className="mt-1.5"
                type="number" step="0.1" min="0" max="2"
                value={form.temperature}
                onChange={(e) => setForm((f) => ({ ...f, temperature: e.target.value }))}
              />
            </div>
          </div>

          <div>
            <Label>用途</Label>
            <Select value={form.purpose} onValueChange={(v) => setForm((f) => ({ ...f, purpose: v as "generation" | "verification", is_default: v === "generation" ? f.is_default : false }))}>
              <SelectTrigger className="mt-1.5"><SelectValue /></SelectTrigger>
              <SelectContent>
                {PURPOSE_OPTIONS.map((o) => (
                  <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex items-center justify-between rounded-lg border px-3 py-2.5">
            <div>
              <p className="text-sm font-medium">设为我的默认模型</p>
              <p className="text-xs text-muted-foreground">
                {form.purpose === "verification" ? "仅写作模型可设默认" : "写作未指定模型时使用"}
              </p>
            </div>
            <Switch
              checked={form.is_default}
              disabled={form.purpose === "verification"}
              onCheckedChange={(v) => setForm((f) => ({ ...f, is_default: v }))}
            />
          </div>

          <Button
            className="w-full"
            onClick={handleSave}
            disabled={saving || !form.provider || !form.model_name || (!editing && !form.api_key)}
          >
            {saving ? "保存中..." : editing ? "保存修改" : "添加"}
          </Button>
        </div>
      </SimpleModal>

      {/* 删除确认 */}
      <SimpleModal open={!!deleting} onClose={() => setDeleting(null)} title="删除模型配置">
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            确定删除 <span className="font-medium text-foreground">{deleting?.model_name}</span>？
            删除后写作将回退到实例默认模型。
          </p>
          <div className="flex gap-2">
            <Button variant="outline" className="flex-1" onClick={() => setDeleting(null)}>取消</Button>
            <Button variant="destructive" className="flex-1" onClick={handleDelete}>删除</Button>
          </div>
        </div>
      </SimpleModal>
    </div>
  );
}
