/**
 * AdminConfirmDialog — 统一确认对话框
 *
 * 用于删除确认、操作确认等场景
 *
 * 使用示例：
 *   <AdminConfirmDialog
 *     open={showDelete}
 *     onOpenChange={setShowDelete}
 *     title="删除密钥"
 *     description="确认删除此 API 密钥？此操作不可撤销。"
 *     confirmText="删除"
 *     variant="destructive"
 *     onConfirm={handleDelete}
 *   />
 */
import { useState, type ReactNode } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Loader2 } from "lucide-react";

interface AdminConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  /** 确认按钮文字 */
  confirmText?: string;
  /** 取消按钮文字 */
  cancelText?: string;
  /** 危险操作样式 */
  variant?: "destructive" | "default";
  /** 子内容（在 description 下方） */
  children?: ReactNode;
  /** 确认回调 */
  onConfirm: () => Promise<void> | void;
  /** 确认中不可关闭 */
  loading?: boolean;
}

export function AdminConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmText = "确认",
  cancelText = "取消",
  variant = "default",
  children,
  onConfirm,
  loading: externalLoading,
}: AdminConfirmDialogProps) {
  const [internalLoading, setInternalLoading] = useState(false);
  const loading = externalLoading ?? internalLoading;

  const handleConfirm = async () => {
    setInternalLoading(true);
    try {
      await onConfirm();
      onOpenChange(false);
    } finally {
      setInternalLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!loading) onOpenChange(v); }}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        {children}
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={loading}
          >
            {cancelText}
          </Button>
          <Button
            variant={variant === "destructive" ? "destructive" : "default"}
            onClick={handleConfirm}
            disabled={loading}
          >
            {loading && <Loader2 className="h-4 w-4 mr-1.5 animate-spin" />}
            {confirmText}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
