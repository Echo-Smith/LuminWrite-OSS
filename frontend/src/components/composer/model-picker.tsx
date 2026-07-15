/**
 * 模型选择器 — Popover 下拉选择
 * 从后端 /api/v2/models 动态加载可用模型列表
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
}

interface ModelPickerProps {
  value: string;
  onChange: (v: string) => void;
}

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
      <div className="flex items-center gap-1.5 rounded-full border border-border/60 px-3 py-1 text-xs text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-500" />
        <span>加载模型...</span>
      </div>
    );
  }

  // If models are empty (fetch failed or no config), show fallback
  if (models.length === 0) {
    return (
      <div className="flex items-center gap-1.5 rounded-full border border-border/60 px-3 py-1 text-xs text-muted-foreground">
        <Cpu className="h-3.5 w-3.5 text-blue-500" />
        <span>{value || "默认模型"}</span>
      </div>
    );
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button className="flex items-center gap-1.5 rounded-full border border-border/60 px-3 py-1 text-xs text-muted-foreground transition-ui hover:bg-accent hover:text-foreground">
          <Cpu className="h-3.5 w-3.5 text-blue-500" />
          <span>{selected?.display_name ?? selected?.model_name ?? "选择模型"}</span>
          <ChevronDown className="h-3 w-3 opacity-50" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-56 p-1">
        {models.map((m) => (
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
              <div className="text-xs text-muted-foreground">{m.provider}</div>
            </div>
          </button>
        ))}
      </PopoverContent>
    </Popover>
  );
}
