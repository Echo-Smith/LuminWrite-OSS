/**
 * 风格选择器 — Popover 下拉选择
 * 无 ✅ 选中标记，仅 hover 高亮
 */
import { useState, useEffect } from "react";
import { Palette, ChevronDown } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Badge } from "@/components/ui/badge";
import type { StyleOption } from "@/lib/types";
import { cn } from "@/lib/utils";

interface StylePickerProps {
  value: string;
  onChange: (slug: string) => void;
}

export function StylePicker({ value, onChange }: StylePickerProps) {
  const [styles, setStyles] = useState<StyleOption[]>([]);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    // 从后端获取风格列表
    fetch("/api/v2/styles")
      .then((res) => res.json())
      .then((data) => {
        const styles = data?.data?.styles ?? data?.styles ?? [];
        setStyles(styles);
      })
      .catch(() => {
        // 后端未就绪时使用默认数据
        setStyles([
          { slug: "yinyue", name: "预设评论风格", description: "植根于杭州时评专栏的深度评论风格", version: 3, word_range: [1000, 1500], tags: ["政论", "民生", "深度评论"] },
          { slug: "shenlun", name: "应用文风格", description: "[style description removed]", version: 1, word_range: [800, 1200], tags: ["申论", "公考"] },
          { slug: "xiaohongshu", name: "小红书风格", description: "轻松种草风格", version: 1, word_range: [300, 800], tags: ["社交媒体", "种草"] },
        ]);
      });
  }, []);

  const selected = styles.find((s) => s.slug === value);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          className="flex items-center gap-1.5 rounded-full border border-border/60 px-3 py-1 text-xs text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
        >
          <Palette className="h-3.5 w-3.5 text-purple-500" />
          <span>{selected?.name ?? "选择风格"}</span>
          <ChevronDown className="h-3 w-3 opacity-50" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80">
        <div className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground px-1 pb-2">选择写作风格</p>
          {styles.length === 0 && (
            <p className="text-center text-xs text-muted-foreground py-4">加载中...</p>
          )}
          {styles.map((style) => (
            <button
              key={style.slug}
              onClick={() => {
                onChange(style.slug);
                setOpen(false);
              }}
              className={cn(
                "flex w-full flex-col items-start gap-1 rounded-lg px-3 py-2.5 text-left transition-colors hover:bg-accent",
                style.slug === value && "bg-accent/50"
              )}
            >
              <div className="flex w-full items-center justify-between">
                <span className="text-sm font-medium">{style.name}</span>
                <span className="text-xs text-muted-foreground">
                  {style.word_range[0]}-{style.word_range[1]}字
                </span>
              </div>
              <span className="text-xs text-muted-foreground">{style.description}</span>
              <div className="flex items-center gap-1.5">
                {style.tags.map((tag) => (
                  <Badge key={tag} variant="secondary" className="text-xs px-1.5 py-0">
                    {tag}
                  </Badge>
                ))}
              </div>
            </button>
          ))}
          <div className="border-t pt-2 mt-2">
            <p className="text-center text-xs text-muted-foreground">
              自定义风格 — 敬请期待
            </p>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}
