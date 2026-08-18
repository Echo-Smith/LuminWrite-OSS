import { Trash2, Power, PowerOff, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useAdminPermission } from "@/hooks/use-admin-permission";

interface AdminBulkActionsProps {
  selectedIds: string[];
  onClear: () => void;
  onBatchAction: (action: "delete" | "activate" | "deactivate") => void;
  resource?: string;
}

export function AdminBulkActions({ selectedIds, onClear, onBatchAction, resource }: AdminBulkActionsProps) {
  const { canDo } = useAdminPermission();
  if (selectedIds.length === 0) return null;
  return (
    <div className="flex items-center gap-2 rounded-lg border bg-muted/50 px-4 py-2 animate-in fade-in-0 slide-in-from-bottom-2 duration-200">
      <span className="text-sm font-medium">{selectedIds.length} selected</span>
      <div className="h-4 w-px bg-border" />
      {canDo("batch_delete") && (
        <Button size="sm" variant="destructive" onClick={() => onBatchAction("delete")}>
          <Trash2 className="h-3.5 w-3.5 mr-1.5" /> Delete
        </Button>
      )}
      {canDo("batch_toggle") && (
        <>
          <Button size="sm" variant="outline" onClick={() => onBatchAction("activate")}>
            <Power className="h-3.5 w-3.5 mr-1.5" /> Activate
          </Button>
          <Button size="sm" variant="outline" onClick={() => onBatchAction("deactivate")}>
            <PowerOff className="h-3.5 w-3.5 mr-1.5" /> Deactivate
          </Button>
        </>
      )}
      <div className="flex-1" />
      <Button size="sm" variant="ghost" onClick={onClear}>
        <X className="h-3.5 w-3.5 mr-1.5" /> Clear
      </Button>
    </div>
  );
}