/**
 * 风格选择器 — Popover 下拉选择
 * 支持全局风格 + 用户自定义风格 + AI 创建入口
 */
import { useState, useEffect, useCallback } from "react";
import { Palette, ChevronDown, Sparkles } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Badge } from "@/components/ui/badge";
import { StyleBuilderDialog } from "@/components/composer/style-builder-dialog";
import { useAuthStore } from "@/stores/auth-store";
import type { StyleOption } from "@/lib/types";
import { cn } from "@/lib/utils";

interface StylePickerProps {
  value: string;
  onChange: (slug: string) => void;
}

export function StylePicker({ value, onChange }: StylePickerProps) {
  const [styles, setStyles] = useState<StyleOption[]>([]);
  const [open, setOpen] = useState(false);
  const [builderOpen, setBuilderOpen] = useState(false);
  const token = useAuthStore((s) => s.token);

  const loadStyles = useCallback(() => {
    const headers: Record<string, string> = {};
    if (token) headers.Authorization = `Bearer ${token}`;
    fetch("/api/v2/styles", { headers })
      .then((res) => res.json())
      .then((data) => {
        const styles = data?.data?.styles ?? data?.styles ?? [];
        setStyles(styles);
      })
      .catch(() => {
        setStyles([
          { slug: "yinyue", name: "预设评论风格", description: "[style description removed]", version: 3, word_range: [1800, 2800], tags: ["政论", "民生", "深度评论"] },
          { slug: "shenlun", name: "应用文风格", description: "[style description removed]", version: 1, word_range: [800, 1200], tags: ["申论", "公考"] },
          { slug: "xiaohongshu", name: "小红书风格", description: "轻松种草风格", version: 1, word_range: [300, 800], tags: ["社交媒体", "种草"] },
        ]);
      });
  }, [token]);

  useEffect(() => {
    loadStyles();
  }, [loadStyles]);

  const selected = styles.find((s) => s.slug === value);

  const globalStyles = styles.filter((s) => !s.tags?.includes("自定义"));
  const myStyles = styles.filter((s) => s.tags?.includes("自定义"));

  return (
    <>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <button
            className="flex items-center gap-1.5 rounded-full border border-border/60 px-3 py-1 text-xs text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
          >
            <span key={`style-icon-${value}`} className="flex items-center anim-fade-scale">
              <Palette className="h-3.5 w-3.5 text-purple-500" />
            </span>
            <span key={`style-label-${selected?.slug ?? 'none'}`} className="anim-fade-scale">
              {selected?.name ?? "选择风格"}
            </span>
            <ChevronDown className="h-3 w-3 opacity-50" />
          </button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-80 max-h-[400px] overflow-y-auto">
          <div className="space-y-1">
            <p className="text-xs font-medium text-muted-foreground px-1 pb-2">全局风格</p>
            {styles.length === 0 && (
              <p className="text-center text-xs text-muted-foreground py-4">加载中...</p>
            )}
            {globalStyles.map((style) => (
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

            {myStyles.length > 0 && (
              <>
                <div className="border-t pt-2 mt-2">
                  <p className="text-xs font-medium text-muted-foreground px-1 pb-1">我的风格</p>
                </div>
                {myStyles.map((style) => (
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
              </>
            )}

            <div className="border-t pt-2 mt-2">
              <button
                onClick={() => {
                  setOpen(false);
                  setBuilderOpen(true);
                }}
                className="flex w-full items-center justify-center gap-1.5 rounded-lg px-3 py-2.5 text-sm font-medium text-purple-600 transition-colors hover:bg-purple-50 dark:hover:bg-purple-950/30"
              >
                <Sparkles className="h-4 w-4" />
                AI 创建自定义风格
              </button>
            </div>
          </div>
        </PopoverContent>
      </Popover>

      <StyleBuilderDialog
        open={builderOpen}
        onOpenChange={setBuilderOpen}
        onCreated={() => {
          loadStyles();
        }}
      />
    </>
  );
}
