import { useRef, useState } from "react";
import { Download, FileSpreadsheet, Loader2, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { adminDownload, adminUpload } from "@/lib/admin-api";
import type { WABenchReviewImportResult, WABenchWorkbookValidationError } from "@/lib/admin-types";

const maxFileSize = 8 * 1024 * 1024;

export function ReviewImportPanel({ onImported, disabled = false }: { onImported: () => Promise<void>; disabled?: boolean }) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [uploading, setUploading] = useState(false);
  const [message, setMessage] = useState("");
  const [errors, setErrors] = useState<WABenchWorkbookValidationError[]>([]);

  const selectFile = (selected?: File) => {
    setMessage(""); setErrors([]);
    if (!selected) { setFile(null); return; }
    if (!selected.name.toLowerCase().endsWith(".xlsx")) { setFile(null); setMessage("仅支持 .xlsx 评审表。"); return; }
    if (selected.size <= 0 || selected.size > maxFileSize) { setFile(null); setMessage("Excel 文件必须小于 8 MB。"); return; }
    setFile(selected);
  };

  const upload = async () => {
    if (!file) return;
    setUploading(true); setMessage(""); setErrors([]);
    try {
      const result = await adminUpload<WABenchReviewImportResult>("/api/v2/admin/evaluation/wabench/reviews/import", file, { silent: true });
      if (!result.success) {
        setMessage(result.error?.message ?? "导入失败，请检查评审表。");
        setErrors(result.data?.errors ?? []);
        return;
      }
      setMessage(`已导入 ${result.data?.importedRows ?? 0} 条评审，保留完整评审溯源。`);
      setFile(null);
      if (inputRef.current) inputRef.current.value = "";
      await onImported();
    } finally { setUploading(false); }
  };

  return (
    <section className="border-y border-[#161917]/20 bg-[#ebe9e1]/60 py-5 dark:border-border dark:bg-muted/20 sm:px-5">
      <div className="grid gap-5 xl:grid-cols-[1fr_auto] xl:items-end">
        <div>
          <div className="flex items-center gap-2"><FileSpreadsheet className="h-4 w-4" /><h4 className="text-sm font-semibold">导入中文 Excel 评审表</h4></div>
          <p className="mt-2 max-w-3xl text-xs leading-5 text-muted-foreground">单次最多 500 条、8 MB。必须包含评审人、角色、方式、时间、标签来源和五项 1—5 分。整份表校验通过后才会一次性写入。</p>
          <div className="mt-4"><Label htmlFor="wabench-review-file" className="sr-only">选择 .xlsx 评审文件</Label><input ref={inputRef} id="wabench-review-file" type="file" disabled={disabled} accept=".xlsx,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" onChange={(event) => selectFile(event.target.files?.[0])} className="block min-h-11 w-full max-w-xl cursor-pointer rounded-md border border-border bg-background px-3 py-2 text-sm file:mr-4 file:border-0 file:bg-transparent file:text-sm file:font-semibold focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#1e6b52] disabled:cursor-not-allowed disabled:opacity-60" /></div>
          {file && <p className="mt-2 text-xs text-muted-foreground">{file.name} · {(file.size / 1024).toFixed(1)} KB</p>}
        </div>
        <div className="flex flex-col gap-2 sm:flex-row xl:flex-col">
          <Button variant="outline" className="min-h-11 gap-2" onClick={() => void adminDownload("/api/v2/admin/evaluation/wabench/reviews/template.xlsx", "wabench-review-template-zh.xlsx")}><Download className="h-4 w-4" />下载中文模板</Button>
          <Button className="min-h-11 gap-2 bg-[#161917] text-[#f4f3ee] hover:bg-[#161917]/85 dark:bg-foreground dark:text-background" onClick={upload} disabled={!file || uploading || disabled}>{uploading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Upload className="h-4 w-4" />}{disabled ? "只读权限" : uploading ? "正在校验并导入…" : "校验并导入"}</Button>
        </div>
      </div>
      {message && <p className={`mt-4 text-sm ${errors.length > 0 ? "text-red-800 dark:text-red-300" : "text-emerald-800 dark:text-emerald-300"}`} role="status">{message}</p>}
      {errors.length > 0 && <div className="mt-3 max-h-44 overflow-y-auto border-l-2 border-red-700/50 pl-4"><ul className="space-y-1.5 text-xs text-red-800 dark:text-red-300">{errors.map((error, index) => <li key={`${error.row}-${error.column}-${index}`}>第 {error.row} 行{error.column ? ` · ${error.column}` : ""}：{error.message}</li>)}</ul></div>}
    </section>
  );
}
