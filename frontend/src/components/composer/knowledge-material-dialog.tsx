import { useEffect, useMemo, useState } from "react";
import { Database, FileText, FolderSearch, Image, Loader2, Search } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Switch } from "@/components/ui/switch";
import type { UserMaterial } from "@/lib/material-api";
import { cn } from "@/lib/utils";

interface KnowledgeMaterialDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  materials: UserMaterial[];
  loading: boolean;
  kbEnabled: boolean;
  onToggleKB: (checked: boolean) => void;
  attachedMaterialIds: Set<string>;
  onAddMaterials: (materials: UserMaterial[]) => Promise<void>;
}

function MaterialIcon({ material }: { material: UserMaterial }) {
  if (material.source_type === "file" && material.file_name?.match(/\.(png|jpe?g|gif|bmp|webp)$/i)) {
    return <Image className="h-4 w-4" />;
  }
  if (material.source_type === "file") return <FileText className="h-4 w-4" />;
  return <Database className="h-4 w-4" />;
}

export function KnowledgeMaterialDialog({
  open,
  onOpenChange,
  materials,
  loading,
  kbEnabled,
  onToggleKB,
  attachedMaterialIds,
  onAddMaterials,
}: KnowledgeMaterialDialogProps) {
  const [query, setQuery] = useState("");
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!open) return;
    setQuery("");
    setSelectedIds(new Set());
  }, [open]);

  const filteredMaterials = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    if (!normalized) return materials;
    return materials.filter((material) =>
      [material.title, material.file_name, material.content_preview]
        .filter(Boolean)
        .some((value) => value?.toLocaleLowerCase().includes(normalized)),
    );
  }, [materials, query]);

  const selectedMaterials = useMemo(
    () => materials.filter((material) => selectedIds.has(material.id)),
    [materials, selectedIds],
  );

  const toggleMaterial = (materialId: string, checked: boolean) => {
    if (attachedMaterialIds.has(materialId)) return;
    setSelectedIds((current) => {
      const next = new Set(current);
      if (checked) next.add(materialId);
      else next.delete(materialId);
      return next;
    });
  };

  const handleConfirm = async () => {
    if (selectedMaterials.length === 0) return;
    setSubmitting(true);
    try {
      await onAddMaterials(selectedMaterials);
      onOpenChange(false);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="knowledge-material-dialog max-h-[82vh] max-w-[760px] gap-0 overflow-hidden p-0">
        <DialogHeader className="border-b px-6 py-5 pr-14">
          <DialogTitle className="flex items-center gap-2 text-base">
            <FolderSearch className="h-4 w-4 text-muted-foreground" />
            从素材库添加资料
          </DialogTitle>
          <DialogDescription>搜索并多选本次写作需要引用的资料。</DialogDescription>
        </DialogHeader>

        <div className="grid min-h-0 flex-1 md:grid-cols-[190px_minmax(0,1fr)]">
          <aside className="border-b bg-muted/20 p-5 md:border-b-0 md:border-r">
            <p className="text-xs font-medium text-foreground">素材库设置</p>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">自动检索会在写作时补充相关资料；手动勾选的素材始终优先。</p>
            <label className="mt-5 flex cursor-pointer items-center justify-between gap-3 rounded-lg border bg-background px-3 py-3">
              <span>
                <strong className="block text-xs font-medium">自动检索</strong>
                <small className="mt-0.5 block text-[11px] text-muted-foreground">{kbEnabled ? "已开启" : "已关闭"}</small>
              </span>
              <Switch checked={kbEnabled} onCheckedChange={onToggleKB} aria-label="素材库自动检索" />
            </label>
          </aside>

          <section className="flex min-h-0 flex-col">
            <div className="border-b p-4">
              <div className="relative">
                <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="搜索标题、文件名或内容摘要"
                  className="pl-9"
                  aria-label="搜索素材库资料"
                />
              </div>
            </div>

            <ScrollArea className="h-[360px]">
              {loading ? (
                <div className="flex h-48 items-center justify-center gap-2 text-sm text-muted-foreground">
                  <Loader2 className="h-4 w-4 animate-spin" /> 正在读取素材库…
                </div>
              ) : filteredMaterials.length === 0 ? (
                <div className="flex h-48 flex-col items-center justify-center px-6 text-center">
                  <Database className="h-6 w-6 text-muted-foreground" />
                  <p className="mt-3 text-sm font-medium">没有匹配的素材</p>
                  <p className="mt-1 text-xs text-muted-foreground">尝试更短的关键词，或先在个人中心上传资料。</p>
                </div>
              ) : (
                <div className="p-2">
                  {filteredMaterials.map((material) => {
                    const attached = attachedMaterialIds.has(material.id);
                    const checked = attached || selectedIds.has(material.id);
                    const checkboxId = `knowledge-material-${material.id}`;
                    return (
                      <label
                        key={material.id}
                        htmlFor={checkboxId}
                        className={cn(
                          "flex cursor-pointer items-start gap-3 rounded-lg px-3 py-3 transition-ui hover:bg-accent/60",
                          checked && "bg-accent/45",
                          attached && "cursor-default opacity-60",
                        )}
                      >
                        <Checkbox
                          id={checkboxId}
                          checked={checked}
                          disabled={attached}
                          onCheckedChange={(value) => toggleMaterial(material.id, value === true)}
                          className="mt-0.5"
                        />
                        <span className="mt-0.5 text-muted-foreground"><MaterialIcon material={material} /></span>
                        <span className="min-w-0 flex-1">
                          <strong className="block truncate text-sm font-medium text-foreground">{material.title}</strong>
                          <small className="mt-1 block truncate text-xs text-muted-foreground">
                            {material.content_preview || material.file_name || "暂无内容摘要"}
                          </small>
                        </span>
                        {attached && <small className="shrink-0 text-[11px] text-muted-foreground">已添加</small>}
                      </label>
                    );
                  })}
                </div>
              )}
            </ScrollArea>
          </section>
        </div>

        <DialogFooter className="items-center justify-between gap-3 border-t px-6 py-4 sm:space-x-0">
          <span className="text-xs text-muted-foreground">已选择 {selectedMaterials.length} 条</span>
          <div className="flex items-center gap-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>取消</Button>
            <Button onClick={handleConfirm} disabled={submitting || selectedMaterials.length === 0}>
              {submitting && <Loader2 className="h-4 w-4 animate-spin" />}
              添加所选素材
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
