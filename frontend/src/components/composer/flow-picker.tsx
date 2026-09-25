/**
 * 写作流程选择器 — WP4 四大写作流程入口（docs/28-wp4-pilot-scenarios.md +
 * WP4 扩展的第四流程「深度研究」）。样式与 ModePicker 对齐：图标 + 标签 +
 * 下拉箭头触发器，弹层列出流程与一句话说明。
 *
 * 选中「深度研究」时由调用方（writing-composer）接管启动链：打开
 * research-settings 悬浮表单 → 走 research-contract-draft 封存合同的真实
 * 研究链路；其余三个流程贯穿 startWritingRun 的 contract/plan 构建。
 */
import { useState } from "react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { ChevronDown, Feather, FileText, Layers, Telescope } from "lucide-react";
import { DEFAULT_WRITING_FLOW, WRITING_FLOW_SPECS, WRITING_FLOW_TYPES, type WritingFlowType } from "@/lib/writing-flows";
import { cn } from "@/lib/utils";

interface FlowPickerProps {
  value: WritingFlowType;
  onChange: (flow: WritingFlowType) => void;
  compact?: boolean;
}

const FLOW_ICONS: Record<WritingFlowType, typeof FileText> = {
  long_form: FileText,
  multi_material: Layers,
  faithful_rewrite: Feather,
  research_review: Telescope,
};

export function FlowPicker({ value, onChange, compact = false }: FlowPickerProps) {
  const [open, setOpen] = useState(false);
  const spec = WRITING_FLOW_SPECS[value] ?? WRITING_FLOW_SPECS[DEFAULT_WRITING_FLOW];
  const SelectedIcon = FLOW_ICONS[spec.type];

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button aria-label={`写作流程：${spec.label}`} className="composer-mode-trigger flex h-8 items-center gap-1.5 rounded-xl px-2.5 text-sm text-muted-foreground transition-ui hover:bg-accent hover:text-foreground">
          <span key={`flow-icon-${spec.type}`} className="flex items-center anim-fade-scale"><SelectedIcon className="h-[18px] w-[18px]" /></span>
          <span key={`flow-label-${spec.type}`} className={cn("composer-control-label anim-fade-scale", compact && "sr-only")}>{spec.label}</span>
          <ChevronDown className={cn("composer-control-chevron h-4 w-4 opacity-50", compact && "hidden")} />
        </button>
      </PopoverTrigger>

      <PopoverContent align="start" className="w-72 p-1">
        <p className="px-3 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">写作流程</p>
        {WRITING_FLOW_TYPES.map((flowType) => {
          const option = WRITING_FLOW_SPECS[flowType];
          const Icon = FLOW_ICONS[flowType];
          return (
            <button key={flowType} onClick={() => { onChange(flowType); setOpen(false); }} className={cn("flex w-full items-start gap-2.5 rounded-md px-3 py-2 text-left transition-colors hover:bg-accent", flowType === spec.type && "bg-accent/50")}>
              <Icon className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1"><span className="block text-sm font-medium">{option.label}</span><span className="block text-xs text-muted-foreground">{option.description}</span></span>
            </button>
          );
        })}
      </PopoverContent>
    </Popover>
  );
}
