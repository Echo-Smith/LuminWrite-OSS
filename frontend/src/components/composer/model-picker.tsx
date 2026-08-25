/**
 * 模型选择器 — Popover 下拉选择
 * 从后端 /api/v2/models 动态加载可用模型列表
 * 显示约X积分/千字和费用档位（经济/标准/高消耗）
 */
import { useState, useEffect, useRef } from "react";
import { Cpu, ChevronDown, Star, Loader2 } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

interface ModelOption {
  id: string;
  model_name: string;
  display_name: string;
  provider: string;
  is_default: boolean;
  has_api_key: boolean;
  points_per_k_token: number;
  cost_level: "economy" | "standard" | "premium";
}

interface ModelPickerProps {
  value: string;
  onChange: (v: string) => void;
}

// 费用档位配置
const COST_LEVELS: Record<string, { label: string; color: string }> = {
  economy: { label: "经济", color: "text-green-500" },
  standard: { label: "标准", color: "text-blue-500" },
  premium: { label: "高消耗", color: "text-orange-500" },
};

export function ModelPicker({ value, onChange }: ModelPickerProps) {
  const [open, setOpen] = useState(false);
  const [models, setModels] = useState<ModelOption[]>([]);
  const [loading, setLoading] = useState(false);
  const fetchedRef = useRef(false);

  useEffect(() => {
    if (fetchedRef.current) return;
    fetchedRef.current = true;
    setLoading(true);
    fetch("/api/v2/models")
      .then((res) => res.json())
      .then((json) => {
        if (json.success) {
          setModels(json.data?.models ?? []);
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  const selected = models.find((m) => m.model_name === value);

  // If no models loaded yet, show a simple label
  if (loading && models.length === 0) {
    return (
      <div className="flex items-center gap-1.5 h-8 rounded-xl px-2.5 text-sm text-muted-foreground">
        <Loader2 className="h-[18px] w-[18px] animate-spin text-blue-500" />
        <span className="hidden sm:inline">加载模型...</span>
      </div>
    );
  }

  // If models are empty (fetch failed or no config), show fallback
  if (models.length === 0) {
    return (
      <div className="flex items-center gap-1.5 h-8 rounded-xl px-2.5 text-sm text-muted-foreground">
        <Cpu className="h-[18px] w-[18px] text-blue-500" />
        <span className="hidden sm:inline">{value || "默认模型"}</span>
      </div>
    );
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button className="flex items-center gap-1.5 h-8 rounded-xl px-2.5 text-sm text-muted-foreground transition-ui hover:bg-accent hover:text-foreground">
          <Cpu className="h-[18px] w-[18px] text-blue-500" />
          <span className="hidden sm:inline">{selected?.display_name ?? selected?.model_name ?? "选择模型"}</span>
          {selected && selected.cost_level && COST_LEVELS[selected.cost_level] && (
            <span className={cn("ml-0.5 text-[11px] hidden sm:inline", COST_LEVELS[selected.cost_level].color)}>
              {COST_LEVELS[selected.cost_level].label}
            </span>
          )}
          <ChevronDown className="h-4 w-4 opacity-50 hidden sm:block" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-64 p-1">
        {models.map((m) => {
          const level = m.cost_level ? COST_LEVELS[m.cost_level] : null;
          return (
            <button
              key={m.id}
              onClick={() => {
                onChange(m.model_name);
                setOpen(false);
              }}
              className={cn(
                "flex w-full items-start gap-2.5 rounded-md px-3 py-2 text-left transition-colors hover:bg-accent",
                m.model_name === value && "bg-accent/50"
              )}
            >
              <Cpu className="h-4 w-4 mt-0.5 text-muted-foreground shrink-0" />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-1">
                  <span className="text-sm font-medium">{m.display_name || m.model_name}</span>
                  {m.is_default && <Star className="h-3 w-3 text-yellow-500 fill-yellow-500" />}
                </div>
                <div className="flex items-center gap-2 mt-0.5">
                  <span className="text-xs text-muted-foreground">{m.provider}</span>
                  {level && (
                    <span className={cn("text-[10px]", level.color)}>
                      {level.label}
                    </span>
                  )}
                  {m.points_per_k_token > 0 && (
                    <span className="text-[10px] text-muted-foreground/70">
                      ~{m.points_per_k_token.toFixed(1)}积分/千字
                    </span>
                  )}
                </div>
              </div>
            </button>
          );
        })}
      </PopoverContent>
    </Popover>
  );
}
