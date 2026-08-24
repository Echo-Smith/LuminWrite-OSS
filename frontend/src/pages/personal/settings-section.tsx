/**
 * 偏好设置子页面
 */
import { useState, useEffect } from "react";
import {
  Settings, Loader2, Check,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { useAuthStore } from "@/stores/auth-store";
import { useSettingsStore, type AgentMode } from "@/stores/settings-store";
import { cn } from "@/lib/utils";

const AGENT_MODE_OPTIONS: { value: AgentMode; label: string; description: string; scenario: string }[] = [
  {
    value: "harness",
    label: "智能会话模式",
    description: "LLM 在持续会话中自主决策，支持多轮对话、定向修改、实时搜索",
    scenario: "日常写作 · 多轮修改 · 对话式创作",
  },
  {
    value: "pipeline",
    label: "流水线模式",
    description: "固定步骤编排，检索→提纲→写作→审校→修正，流程稳定可预测",
    scenario: "标准化写作 · 快速出稿 · 流程可控",
  },
  {
    value: "editorial",
    label: "编辑部模式 (Beta)",
    description: "多 Agent 角色协作：研究→写作→审校，上下文隔离，风格统一注入",
    scenario: "长文创作 · 深度报告 · 高质量产出",
  },
];

export function SettingsSection({ onClosePanel }: { onClosePanel?: () => void }) {
  const agentMode = useSettingsStore((s) => s.agentMode);
  const setAgentMode = useSettingsStore((s) => s.setAgentMode);
  const token = useAuthStore((s) => s.token);

  // 默认写作风格
  const [defaultStyle, setDefaultStyle] = useState<string>("");
  const [styles, setStyles] = useState<Array<{ slug: string; name: string }>>([]);
  const [loadingStyles, setLoadingStyles] = useState(true);

  // 加载可用风格列表
  useEffect(() => {
    fetch("/api/v2/styles", {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
      .then((res) => res.json())
      .then((json) => {
        if (json.success && json.data?.styles) {
          setStyles(json.data.styles.map((s: { slug: string; name: string }) => ({ slug: s.slug, name: s.name })));
        }
      })
      .catch(() => {})
      .finally(() => setLoadingStyles(false));
  }, [token]);

  // 加载当前默认风格
  useEffect(() => {
    fetch("/api/v2/preferences", {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    })
      .then((res) => res.json())
      .then((json) => {
        if (json.success && json.data?.default_style) {
          setDefaultStyle(json.data.default_style as string);
        }
      })
      .catch(() => {});
  }, [token]);

  const handleSetDefaultStyle = (slug: string) => {
    setDefaultStyle(slug);
    // 同步到服务器
    fetch("/api/v2/preferences", {
      method: "PUT",
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({ default_style: slug }),
    }).catch(() => {});
  };

  return (
    <div className="px-6 pt-6 pb-12 space-y-6">

      {/* 默认写作风格 */}
      <div>
        <Label className="text-base font-semibold">默认写作风格</Label>
        <p className="mt-1 text-sm text-muted-foreground">
          选择新建写作任务时的默认风格。可在写作时随时切换。
        </p>
        <div className="mt-3">
          {loadingStyles ? (
            <p className="text-sm text-muted-foreground">加载风格列表...</p>
          ) : (
            <Select value={defaultStyle} onValueChange={handleSetDefaultStyle}>
              <SelectTrigger className="w-full max-w-xs">
                <SelectValue placeholder="选择默认写作风格" />
              </SelectTrigger>
              <SelectContent>
                {styles.map((s) => (
                  <SelectItem key={s.slug} value={s.slug}>
                    {s.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>
      </div>

      

      {/* 编排模式 */}
      <div>
        <Label className="text-base font-semibold">默认写作模式</Label>
        <p className="mt-1 text-sm text-muted-foreground">
          选择 AI 执行写作任务的编排方式。设置跟随你的账号，换设备登录也会保持。
        </p>
        <div className="mt-4 space-y-2">
          {AGENT_MODE_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              onClick={() => {
                setAgentMode(opt.value);
                // 选择编辑部模式后关闭面板并回到首页
                if (opt.value === "editorial" && onClosePanel) {
                  onClosePanel();
                }
              }}
              className={cn(
                "flex w-full items-start gap-3 rounded-lg border p-4 text-left transition-ui",
                agentMode === opt.value
                  ? "border-primary bg-primary/5"
                  : "border-border hover:bg-accent/50"
              )}
            >
              <div className={cn(
                "flex h-5 w-5 shrink-0 items-center justify-center rounded-full border-2 mt-0.5",
                agentMode === opt.value ? "border-primary" : "border-muted-foreground/30"
              )}>
                {agentMode === opt.value && (
                  <div className="h-2.5 w-2.5 rounded-full bg-primary" />
                )}
              </div>
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{opt.label}</span>
                </div>
                <div className="text-xs text-muted-foreground mt-0.5">{opt.description}</div>
                <div className="text-[11px] text-muted-foreground/70 mt-1 flex items-center gap-1">
                  <span className="inline-block w-1 h-1 rounded-full bg-muted-foreground/40" />
                  {opt.scenario}
                </div>
              </div>
            </button>
          ))}
        </div>
      </div>

      

      {/* 提示信息 */}
      <div className="rounded-lg bg-muted/50 p-4 space-y-2">
        <p className="text-xs text-muted-foreground">
          <strong className="text-foreground">三种模式怎么选？</strong>
        </p>
        <div className="space-y-1.5">
          <p className="text-xs text-muted-foreground">
            <strong className="text-foreground/80">智能会话</strong> — 最灵活，AI 自主决定何时搜索、写作、修改，支持多轮对话和定向调整。适合大多数日常写作场景。
          </p>
          <p className="text-xs text-muted-foreground">
            <strong className="text-foreground/80">流水线</strong> — 最稳定，按固定步骤执行（检索→提纲→写作→审校→修正），流程透明、结果可预测。适合格式固定的标准化写作。
          </p>
          <p className="text-xs text-muted-foreground">
            <strong className="text-foreground/80">编辑部</strong> — 最高质量，多 Agent 角色分工协作（研究 Agent 检索素材、写作 Agent 生成正文、审校 Agent 多维度评审），上下文隔离避免风格漂移。适合万字长文、深度报告等高要求场景。
          </p>
        </div>
        <p className="text-[11px] text-muted-foreground/60 pt-1 border-t">
          三种模式均统一注入风格约束（RenderWritingConstraints），切换模式不会影响风格一致性。如果对当前模式生成质量不满意，可尝试切换体验差异。
        </p>
      </div>
    </div>
  );
}
