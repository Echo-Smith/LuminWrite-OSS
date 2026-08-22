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

const AGENT_MODE_OPTIONS: { value: AgentMode; label: string; description: string }[] = [
  { value: "harness", label: "智能会话模式", description: "LLM 持续会话 + Harness 路由，支持多轮修改、对话、搜索，适合实际写作场景" },
  { value: "pipeline", label: "流水线模式", description: "固定步骤流水线，稳定可预测，适合标准化写作" },
  { value: "editorial", label: "编辑部模式 (Beta)", description: "模拟编辑部协作流程：选题→研究→写作→审校，多 Agent 角色分工，适合高质量长文创作" },
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
              </div>
            </button>
          ))}
        </div>
      </div>

      

      {/* 提示信息 */}
      <div className="rounded-lg bg-muted/50 p-4">
        <p className="text-xs text-muted-foreground">
          <strong className="text-foreground">提示：</strong>
          智能会话模式（Harness）让 AI 在持续会话中自主搜索、写作、评审和修改，支持多轮对话和定向修改；
          流水线模式（Pipeline）使用固定步骤，更稳定但灵活性较低。
          编辑部模式（Editorial）模拟编辑部协作流程，多 Agent 角色分工，适合高质量长文。
          如果对生成质量不满意，可以尝试切换模式体验差异。
        </p>
      </div>
    </div>
  );
}
