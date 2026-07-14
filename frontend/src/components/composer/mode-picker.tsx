/**
 * 模式选择器 — auto / writing / guided / polish
 */
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Zap, PenLine, ListTree, Sparkles } from "lucide-react";
import type { WriteMode } from "@/lib/types";

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
  return (
    <Select value={value} onValueChange={(v) => onChange(v as WriteMode)}>
      <SelectTrigger className="h-8 w-[130px] text-sm">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {MODE_OPTIONS.map((opt) => (
          <SelectItem key={opt.value} value={opt.value}>
            <div className="flex items-center gap-2">
              <opt.icon className="h-3.5 w-3.5 text-muted-foreground" />
              <span>{opt.label}</span>
            </div>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
