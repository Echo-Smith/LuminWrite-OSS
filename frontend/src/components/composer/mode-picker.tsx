/**
 * 模式选择器 — auto / writing / guided / polish
 * 使用 Popover 替代 Select，无 ✅ 选中标记，仅 hover 高亮
 */
import { useState } from "react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Zap, PenLine, ListTree, Sparkles, ChevronDown } from "lucide-react";
import type { WriteMode } from "@/lib/types";
import type { ApprovalMode, AssuranceLevel, OrchestrationMode } from "@/lib/writing-runtime-types";
import { cn } from "@/lib/utils";

interface ModePickerProps {
  value: WriteMode;
  onChange: (mode: WriteMode) => void;
  orchestrationValue?: OrchestrationMode;
  onOrchestrationChange?: (mode: OrchestrationMode) => void;
  assuranceValue?: AssuranceLevel;
  onAssuranceChange?: (level: AssuranceLevel) => void;
  approvalValue?: ApprovalMode;
  onApprovalChange?: (mode: ApprovalMode) => void;
}

const MODE_OPTIONS: { value: WriteMode; label: string; icon: typeof Zap; description: string }[] = [
  { value: "auto", label: "自动模式", icon: Zap, description: "AI 自动判断最佳写作方式" },
  { value: "writing", label: "写作模式", icon: PenLine, description: "直接生成完整文章" },
  { value: "guided", label: "引导模式", icon: ListTree, description: "先确认提纲再生成" },
  { value: "polish", label: "润色模式", icon: Sparkles, description: "对已有文本进行润色" },
];

const ORCHESTRATION_OPTIONS: Array<{ value: OrchestrationMode; label: string; description: string }> = [
  { value: "auto", label: "自动安排", description: "按任务、成本与风险选择策略" },
  { value: "fast", label: "快速成稿", description: "用最短路径形成可编辑初稿" },
  { value: "outline_first", label: "大纲优先", description: "先稳定结构，再分段写作" },
  { value: "sourced", label: "材料综合", description: "围绕来源组织观点与证据" },
  { value: "strict_research", label: "严格研究", description: "高保障检索与逐项验证" },
];

const ASSURANCE_OPTIONS: Array<{ value: AssuranceLevel; label: string }> = [
  { value: "flexible", label: "灵活" }, { value: "standard", label: "标准" },
  { value: "sourced", label: "有来源" }, { value: "strict", label: "严格" },
];

const APPROVAL_OPTIONS: Array<{ value: ApprovalMode; label: string }> = [
  { value: "conditional", label: "风险时确认" }, { value: "always", label: "执行前总是确认" }, { value: "auto", label: "允许自动执行" },
];

export function ModePicker({
  value, onChange,
  orchestrationValue = "auto", onOrchestrationChange,
  assuranceValue = "standard", onAssuranceChange,
  approvalValue = "conditional", onApprovalChange,
}: ModePickerProps) {
  const [open, setOpen] = useState(false);
  const selected = MODE_OPTIONS.find((opt) => opt.value === value);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
          <button
            aria-label={`写作与执行模式：${selected?.label ?? "未选择"}`}
            className="flex items-center gap-1.5 h-8 rounded-xl px-2.5 text-sm text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
          >
            {selected && (
              <span key={`mode-icon-${selected.value}`} className="flex items-center anim-fade-scale">
                <selected.icon className="h-[18px] w-[18px]" />
              </span>
            )}
            <span key={`mode-label-${selected?.value ?? 'none'}`} className="hidden sm:inline anim-fade-scale">
              {selected?.label ?? "选择模式"}
            </span>
            <ChevronDown className="h-4 w-4 opacity-50 hidden sm:block" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="max-h-[80vh] w-72 overflow-y-auto p-1">
        <p className="px-3 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">任务方式</p>
        {MODE_OPTIONS.map((opt) => (
          <button
            key={opt.value}
            onClick={() => {
              onChange(opt.value);
              setOpen(false);
            }}
            className={cn(
              "flex w-full items-start gap-2.5 rounded-md px-3 py-2 text-left transition-colors hover:bg-accent",
              opt.value === value && "bg-accent/50"
            )}
          >
            <opt.icon className="h-4 w-4 mt-0.5 text-muted-foreground shrink-0" />
            <div className="flex-1 min-w-0">
              <div className="text-sm font-medium">{opt.label}</div>
              <div className="text-xs text-muted-foreground">{opt.description}</div>
            </div>
          </button>
        ))}
        {onOrchestrationChange && (
          <>
            <div className="mx-2 my-1 border-t" />
            <p className="px-3 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">执行策略</p>
            {ORCHESTRATION_OPTIONS.map((opt) => (
              <button
                key={opt.value}
                onClick={() => { onOrchestrationChange(opt.value); setOpen(false); }}
                className={cn("flex w-full items-start gap-2.5 rounded-md px-3 py-2 text-left transition-colors hover:bg-accent", opt.value === orchestrationValue && "bg-accent/50")}
              >
                <Zap className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1"><div className="text-sm font-medium">{opt.label}</div><div className="text-xs text-muted-foreground">{opt.description}</div></div>
              </button>
            ))}
          </>
        )}
        {onAssuranceChange && (
          <>
            <div className="mx-2 my-1 border-t" />
            <p className="px-3 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">验收强度</p>
            <div className="grid grid-cols-2 gap-1 px-2 pb-1">
              {ASSURANCE_OPTIONS.map((opt) => <button key={opt.value} onClick={() => onAssuranceChange(opt.value)} className={cn("rounded-md px-2 py-1.5 text-xs hover:bg-accent", opt.value === assuranceValue && "bg-accent text-foreground")}>{opt.label}</button>)}
            </div>
          </>
        )}
        {onApprovalChange && (
          <>
            <div className="mx-2 my-1 border-t" />
            <p className="px-3 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">执行确认</p>
            {APPROVAL_OPTIONS.map((opt) => <button key={opt.value} onClick={() => { onApprovalChange(opt.value); setOpen(false); }} className={cn("w-full rounded-md px-3 py-2 text-left text-xs hover:bg-accent", opt.value === approvalValue && "bg-accent/50 font-medium")}>{opt.label}</button>)}
          </>
        )}
      </PopoverContent>
    </Popover>
  );
}
