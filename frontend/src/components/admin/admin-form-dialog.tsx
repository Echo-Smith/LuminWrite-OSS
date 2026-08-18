/**
 * AdminFormDialog — 统一表单对话框
 *
 * 封装 Dialog + Form 提交的通用模式：
 * - 标题 + 描述
 * - 子内容（表单字段）
 * - 底部 取消/提交 按钮
 * - 提交 loading 状态
 * - 提交时不可关闭
 *
 * 使用示例：
 *   <AdminFormDialog
 *     open={showAdd}
 *     onOpenChange={setShowAdd}
 *     title="添加密钥"
 *     onSubmit={async (e) => { await handleSave(); }}
 *     submitText="保存"
 *   >
 *     <Input ... />
 *   </AdminFormDialog>
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

interface AdminFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  /** 子内容（表单字段） */
  children?: ReactNode;
  /** 提交回调 */
  onSubmit: () => Promise<void> | void;
  /** 提交按钮文字 */
  submitText?: string;
  /** 取消按钮文字 */
  cancelText?: string;
  /** 提交按钮是否禁用 */
  submitDisabled?: boolean;
  /** 提交按钮宽占整行（flex-1） */
  submitFullWidth?: boolean;
  /** 对话框宽度 */
  maxWidth?: string;
}

export function AdminFormDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  onSubmit,
  submitText = "保存",
  cancelText = "取消",
  submitDisabled = false,
  submitFullWidth = false,
  maxWidth = "max-w-lg",
}: AdminFormDialogProps) {
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitDisabled || loading) return;
    setLoading(true);
    try {
      await onSubmit();
      // 成功后由父组件控制关闭（父组件 onSubmit 成功后调用 onOpenChange(false)）
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!loading) onOpenChange(v); }}>
      <DialogContent className={maxWidth}>
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            {description && <DialogDescription>{description}</DialogDescription>}
          </DialogHeader>
          <div className="py-4 space-y-4">
            {children}
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={loading}
            >
              {cancelText}
            </Button>
            <Button
              type="submit"
              disabled={loading || submitDisabled}
              className={submitFullWidth ? "flex-1" : undefined}
            >
              {loading && <Loader2 className="h-4 w-4 mr-1.5 animate-spin" />}
              {submitText}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
