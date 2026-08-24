/**
 * 模式选择器 — auto / writing / guided / polish
 * 使用 Popover 替代 Select，无 ✅ 选中标记，仅 hover 高亮
 */
import { useState } from "react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Zap, PenLine, ListTree, Sparkles, ChevronDown } from "lucide-react";
import type { WriteMode } from "@/lib/types";
import { cn } from "@/lib/utils";

interface ModePickerProps {
  value: WriteMode;
  onChange: (mode: WriteMode) => void;
}

const MODE_OPTIONS: { value: WriteMode; label: string; icon: typeof Zap; description: string }[] = [
  { value: "auto", label: "自动模式", icon: Zap, description: "AI 自动判断最佳写作方式" },
  { value: "writing", label: "写作模式", icon: PenLine, description: "直接生成完整文章" },
  { value: "guided", label: "引导模式", icon: ListTree, description: "先确认提纲再生成" },
  { value: "polish", label: "润色模式", icon: Sparkles, description: "对已有文本进行润色" },
];

export function ModePicker({ value, onChange }: ModePickerProps) {
  const [open, setOpen] = useState(false);
  const selected = MODE_OPTIONS.find((opt) => opt.value === value);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
          <button
            className="flex items-center gap-1.5 rounded-md border border-border/60 px-2.5 py-1 text-xs text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
          >
            {selected && (
              <span key={`mode-icon-${selected.value}`} className="flex items-center anim-fade-scale">
                <selected.icon className="h-3.5 w-3.5" />
              </span>
            )}
            <span key={`mode-label-${selected?.value ?? 'none'}`} className="hidden sm:inline anim-fade-scale">
              {selected?.label ?? "选择模式"}
            </span>
            <ChevronDown className="h-3 w-3 opacity-50 hidden sm:block" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-56 p-1">
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
      </PopoverContent>
    </Popover>
  );
}
