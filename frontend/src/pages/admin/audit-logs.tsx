import { useState, useEffect, useCallback } from "react";
import { Shield, Filter } from "lucide-react";
import { adminFetch } from "@/lib/admin-api";
import { AdminPageHeader, AdminLoading, AdminEmptyState } from "@/components/admin";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const RESOURCE_LABELS: Record<string, string> = {
  model_config: "Model Config", api_key: "API Key", cron_job: "Cron Job",
  style: "Style", sensitive_word: "Sensitive Word", pending_style: "Pending Style",
  evaluation: "Evaluation", kb: "Knowledge Base", mcp_server: "MCP Server",
};
const ACTION_COLORS: Record<string, string> = {
  create: "bg-green-100 text-green-700", update: "bg-blue-100 text-blue-700",
  delete: "bg-red-100 text-red-700", batch_delete: "bg-red-100 text-red-700",
  batch_activate: "bg-green-100 text-green-700", batch_deactivate: "bg-yellow-100 text-yellow-700",
  publish: "bg-purple-100 text-purple-700", archive: "bg-gray-100 text-gray-700",
  approve: "bg-green-100 text-green-700", reject: "bg-red-100 text-red-700",
};

interface AuditLogEntry {
  id: number; actor_id: string; actor_role: string; action: string;
  resource: string; resource_id: string; detail: string;
  changes: Record<string, unknown>; ip_address: string; user_agent: string;
  created_at: string;
}

export function AuditLogsPage() {
  const [logs, setLogs] = useState<AuditLogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [filterResource, setFilterResource] = useState("");
  const [filterAction, setFilterAction] = useState("");
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const pageSize = 50;
  const loadLogs = useCallback(async () => {
    setLoading(true);
    const params = new URLSearchParams();
    if (filterResource) params.set("resource", filterResource);
    if (filterAction) params.set("action", filterAction);
    params.set("page", String(page));
    params.set("page_size", String(pageSize));
    const { success, data } = await adminFetch<{logs: AuditLogEntry[]; total: number}>(`/api/v2/admin/audit-logs?${params}`, { silent: true });
    if (success && data) { setLogs(data.logs ?? []); setTotal(data.total); } else { setLogs([]); setTotal(0); }
    setLoading(false);
  }, [filterResource, filterAction, page]);
  useEffect(() => { loadLogs(); }, [loadLogs]);
  const totalPages = Math.ceil(total / pageSize);
  return (
    <div className="p-6 space-y-6">
      <AdminPageHeader title="Audit Logs" description="Append-only log of all admin write operations" />
      <div className="flex items-center gap-3">
        <Filter className="h-4 w-4 text-muted-foreground" />
        <Select value={filterResource} onValueChange={(v) => { setFilterResource(v === "all" ? "" : v); setPage(1); }}>
          <SelectTrigger className="w-40"><SelectValue placeholder="All Resources" /></SelectTrigger>
          <SelectContent><SelectItem value="all">All Resources</SelectItem>
            {Object.entries(RESOURCE_LABELS).map(([k,v]) => <SelectItem key={k} value={k}>{v}</SelectItem>)}</SelectContent>
        </Select>
        <Select value={filterAction} onValueChange={(v) => { setFilterAction(v === "all" ? "" : v); setPage(1); }}>
          <SelectTrigger className="w-40"><SelectValue placeholder="All Actions" /></SelectTrigger>
          <SelectContent><SelectItem value="all">All Actions</SelectItem>
            <SelectItem value="create">Create</SelectItem><SelectItem value="update">Update</SelectItem>
            <SelectItem value="delete">Delete</SelectItem><SelectItem value="batch_delete">Batch Delete</SelectItem>
            <SelectItem value="batch_activate">Batch Activate</SelectItem><SelectItem value="batch_deactivate">Batch Deactivate</SelectItem>
          </SelectContent>
        </Select>
      </div>
      {loading ? <AdminLoading /> : logs.length === 0 ? <AdminEmptyState icon={Shield} title="No audit logs" description="No operations logged yet." /> : (
        <div className="space-y-2">
          {logs.map((log) => (
            <Card key={log.id}><CardContent className="p-3 flex items-start gap-3">
              <Badge className={ACTION_COLORS[log.action] ?? "bg-gray-100 text-gray-700"} variant="secondary">{log.action}</Badge>
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2 text-sm"><span className="font-medium">{RESOURCE_LABELS[log.resource] ?? log.resource}</span>{log.resource_id && <span className="text-muted-foreground">#{log.resource_id.slice(0,8)}</span>}</div>
                <p className="text-xs text-muted-foreground mt-0.5">{log.detail}</p>
                <div className="flex items-center gap-3 mt-1 text-xs text-muted-foreground"><span>by {log.actor_id}</span><span>{new Date(log.created_at).toLocaleString()}</span>{log.ip_address && <span>{log.ip_address}</span>}</div>
              </div>
            </CardContent></Card>
          ))}
        </div>
      )}
      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <span className="text-sm text-muted-foreground">Page {page} of {totalPages} ({total} total)</span>
          <div className="flex gap-2"><Button size="sm" variant="outline" disabled={page <= 1} onClick={() => setPage(p => p-1)}>Prev</Button><Button size="sm" variant="outline" disabled={page >= totalPages} onClick={() => setPage(p => p+1)}>Next</Button></div>
        </div>
      )}
    </div>
  );
}