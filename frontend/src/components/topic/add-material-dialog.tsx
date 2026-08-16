/**
 * AddMaterialDialog — 添加素材弹窗
 *
 * 从 my-materials.tsx 和 materials-tab.tsx 中提取的共享组件。
 * 支持「文本/Markdown」和「上传文件」两种模式。
 */
import { useState, useRef } from "react";
import { FileText, Upload, Loader2, File } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  DialogDescription,
} from "@/components/ui/dialog";
import { createMaterial, uploadMaterial } from "@/lib/material-api";

function formatSize(bytes?: number): string {
  if (!bytes) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

interface AddMaterialDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded?: () => void;
  onError?: (message: string) => void;
}

export function AddMaterialDialog({
  open,
  onOpenChange,
  onAdded,
  onError,
}: AddMaterialDialogProps) {
  const [addMode, setAddMode] = useState<"text" | "file">("text");
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [uploading, setUploading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const reset = () => {
    setTitle("");
    setContent("");
    setFile(null);
    setAddMode("text");
    if (fileInputRef.current) fileInputRef.current.value = "";
  };

  const handleClose = (nextOpen: boolean) => {
    if (!nextOpen && !uploading) {
      reset();
    }
    onOpenChange(nextOpen);
  };

  const handleSubmit = async () => {
    if (addMode === "text") {
      if (!title.trim() || !content.trim()) return;
      setUploading(true);
      try {
        await createMaterial(title, content);
        reset();
        onOpenChange(false);
        onAdded?.();
      } catch (e) {
        onError?.(e instanceof Error ? e.message : "创建失败");
      } finally {
        setUploading(false);
      }
    } else if (addMode === "file" && file) {
      setUploading(true);
      try {
        await uploadMaterial(file, title || undefined);
        reset();
        onOpenChange(false);
        onAdded?.();
      } catch (e) {
        onError?.(e instanceof Error ? e.message : "上传失败");
      } finally {
        setUploading(false);
      }
    }
  };

  const canSubmit =
    addMode === "text"
      ? title.trim().length > 0 && content.trim().length > 0
      : file !== null;

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>添加素材</DialogTitle>
          <DialogDescription>
            支持文本/Markdown 或上传文件，素材将存入个人素材库供写作时检索使用。
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          {/* Mode switcher */}
          <div className="flex gap-2">
            <Button
              size="sm"
              variant={addMode === "text" ? "default" : "outline"}
              onClick={() => setAddMode("text")}
            >
              <FileText className="h-4 w-4 mr-1" /> 文本/Markdown
            </Button>
            <Button
              size="sm"
              variant={addMode === "file" ? "default" : "outline"}
              onClick={() => setAddMode("file")}
            >
              <Upload className="h-4 w-4 mr-1" /> 上传文件
            </Button>
          </div>

          {/* Title */}
          <div>
            <Label>标题</Label>
            <Input
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="给素材起个标题..."
              className="mt-1"
            />
          </div>

          {/* Content / File upload */}
          {addMode === "text" ? (
            <div>
              <Label>内容</Label>
              <Textarea
                value={content}
                onChange={(e) => setContent(e.target.value)}
                placeholder="输入文本或 Markdown 内容..."
                className="mt-1 min-h-[200px] font-mono text-sm"
              />
            </div>
          ) : (
            <div>
              <Label>选择文件</Label>
              <div
                className="mt-1 border-2 border-dashed rounded-lg p-8 text-center cursor-pointer hover:bg-accent/50 transition-colors"
                onClick={() => fileInputRef.current?.click()}
              >
                <input
                  ref={fileInputRef}
                  type="file"
                  className="hidden"
                  onChange={(e) => setFile(e.target.files?.[0] || null)}
                />
                {file ? (
                  <div className="space-y-1">
                    <File className="h-8 w-8 mx-auto text-muted-foreground" />
                    <p className="text-sm font-medium">{file.name}</p>
                    <p className="text-xs text-muted-foreground">
                      {formatSize(file.size)}
                    </p>
                  </div>
                ) : (
                  <div className="space-y-1">
                    <Upload className="h-8 w-8 mx-auto text-muted-foreground" />
                    <p className="text-sm text-muted-foreground">
                      点击选择文件（PDF、Word、图片等）
                    </p>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            size="sm"
            onClick={() => handleClose(false)}
            disabled={uploading}
          >
            取消
          </Button>
          <Button
            size="sm"
            onClick={handleSubmit}
            disabled={uploading || !canSubmit}
          >
            {uploading ? (
              <Loader2 className="h-4 w-4 animate-spin mr-1" />
            ) : null}
            {uploading ? "上传中..." : "保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
