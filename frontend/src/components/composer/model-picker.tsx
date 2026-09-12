/**
 * 模型选择器 — Popover 下拉选择
 * 从后端 /api/v2/models 动态加载可用模型列表
 * 显示约X积分/千字和费用档位（经济/标准/高消耗）
 */
import { useState, useEffect, useRef } from "react";
import { ChevronDown, Star } from "lucide-react";
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
  compact?: boolean;
}

// 费用档位配置
const COST_LEVELS: Record<string, { label: string; color: string }> = {
  economy: { label: "经济", color: "text-green-500" },
  standard: { label: "标准", color: "text-blue-500" },
  premium: { label: "高消耗", color: "text-orange-500" },
};

function modelTierLabel(name: string, costLevel?: string): string {
  if (costLevel === "premium" || /\bpro\b/i.test(name)) return "Pro";
  if (/flash|lite|turbo|fast/i.test(name)) return "快速";
  return name;
}

function fallbackCostLevel(name: string): keyof typeof COST_LEVELS | null {
  if (/flash|lite|turbo|fast/i.test(name)) return "economy";
  if (/\bpro\b/i.test(name)) return "premium";
  return null;
}

export function ModelPicker({ value, onChange, compact = false }: ModelPickerProps) {
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
  const fallbackLevel = fallbackCostLevel(value);

  // If no models loaded yet, show a simple label
  if (loading && models.length === 0) {
    return (
      <div className={cn("flex h-8 items-center rounded-xl px-2.5 text-sm text-muted-foreground", compact ? "max-w-[116px]" : "max-w-[45vw] sm:max-w-none")}>
        <span className="truncate">{value || "加载模型..."}</span>
      </div>
    );
  }

  // If models are empty (fetch failed or no config), show fallback
  if (models.length === 0) {
    const level = fallbackLevel ? COST_LEVELS[fallbackLevel] : null;
    return (
      <div className={cn("composer-model-trigger flex h-8 items-center gap-1 rounded-xl px-2.5 text-sm text-muted-foreground", compact && "max-w-[116px]")}>
        <span className="composer-model-speed truncate">{modelTierLabel(value || "默认模型", fallbackLevel ?? undefined)}</span>
        {level && <><span aria-hidden="true">·</span><span className={cn("composer-model-cost text-[11px]", level.color)}>{level.label}</span></>}
      </div>
    );
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button className={cn("composer-model-trigger flex h-8 items-center gap-1 rounded-xl px-2.5 text-sm text-muted-foreground transition-ui hover:bg-accent hover:text-foreground", compact && "max-w-[116px]")}>
          <span className="composer-model-speed truncate">{modelTierLabel(selected?.display_name ?? selected?.model_name ?? "选择模型", selected?.cost_level)}</span>
          {selected && selected.cost_level && COST_LEVELS[selected.cost_level] && (
            <><span aria-hidden="true">·</span><span className={cn("composer-model-cost text-[11px]", COST_LEVELS[selected.cost_level].color)}>
              {COST_LEVELS[selected.cost_level].label}
            </span></>
          )}
          <ChevronDown className={cn("composer-control-chevron h-4 w-4 opacity-50", compact && "hidden")} />
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
                "flex w-full items-start rounded-md px-3 py-2 text-left transition-colors hover:bg-accent",
                m.model_name === value && "bg-accent/50"
              )}
            >
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
