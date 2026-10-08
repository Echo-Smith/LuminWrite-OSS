/**
 * AdminTable — 统一表格组件
 *
 * 功能：
 * - 列定义（key, header, render, width, align）
 * - 排序（点击表头切换）
 * - 分页（pageSize, 总数, 当前页）
 * - 空状态自动展示
 * - loading skeleton
 * - 行点击回调
 *
 * 使用示例：
 *   const columns: AdminColumn<ModelConfig>[] = [
 *     { key: "display_name", header: "名称", sortable: true },
 *     { key: "is_active", header: "状态", render: (row) => row.is_active ? "启用" : "禁用" },
 *     { key: "actions", header: "操作", render: (row) => <Button onClick={() => edit(row)}>编辑</Button> },
 *   ];
 *   <AdminTable data={items} columns={columns} loading={loading} />
 */
import { type ReactNode, useState, useMemo } from "react";
import { ChevronUp, ChevronDown, ChevronsUpDown } from "lucide-react";
import { cn } from "@/lib/utils";
import { AdminEmptyState } from "./admin-empty-state";
import { AdminTableSkeleton } from "./admin-loading-skeleton";
import { Inbox } from "lucide-react";

export interface AdminColumn<T> {
  /** 唯一键 */
  key: string;
  /** 表头标题 */
  header: string;
  /** 自定义渲染 */
  render?: (row: T) => ReactNode;
  /** 排序比较函数（提供则该列可排序） */
  sort?: (a: T, b: T) => number;
  /** 列宽 */
  width?: string;
  /** 对齐 */
  align?: "left" | "center" | "right";
}

interface AdminTableProps<T> {
  data: T[];
  columns: AdminColumn<T>[];
  /** 行唯一键 */
  rowKey: (row: T) => string;
  /** 加载中 */
  loading?: boolean;
  /** 空状态标题 */
  emptyTitle?: string;
  /** 空状态描述 */
  emptyDesc?: string;
  /** 行点击 */
  onRowClick?: (row: T) => void;
  /** 分页 */
  pagination?: {
    page: number;
    pageSize: number;
    total: number;
    onPageChange: (page: number) => void;
  };
  className?: string;
}

export function AdminTable<T>({
  data,
  columns,
  rowKey,
  loading,
  emptyTitle = "暂无数据",
  emptyDesc,
  onRowClick,
  pagination,
  className,
}: AdminTableProps<T>) {
  const [sortKey, setSortKey] = useState<string | null>(null);
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");

  const sortedData = useMemo(() => {
    if (!sortKey) return data;
    const col = columns.find((c) => c.key === sortKey);
    if (!col?.sort) return data;
    const sorted = [...data].sort(col.sort);
    return sortDir === "desc" ? sorted.reverse() : sorted;
  }, [data, columns, sortKey, sortDir]);

  const handleSort = (col: AdminColumn<T>) => {
    if (!col.sort) return;
    if (sortKey === col.key) {
      setSortDir((prev) => (prev === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(col.key);
      setSortDir("asc");
    }
  };

  if (loading) {
    return <AdminTableSkeleton rows={5} cols={columns.length} />;
  }

  if (data.length === 0) {
    return <AdminEmptyState icon={Inbox} title={emptyTitle} description={emptyDesc} />;
  }

  const totalPages = pagination ? Math.ceil(pagination.total / pagination.pageSize) : 1;

  return (
    <div className={cn("w-full", className)}>
      <div className="rounded-lg border">
        {/* 表头 */}
        <div className="flex gap-4 border-b bg-muted/30 px-4 py-2.5">
          {columns.map((col) => (
            <div
              key={col.key}
              className={cn(
                "flex items-center gap-1 text-xs font-semibold text-muted-foreground",
                col.align === "center" && "justify-center",
                col.align === "right" && "justify-end",
              )}
              style={col.width ? { width: col.width, flex: col.width === "auto" ? "0 0 auto" : undefined } : { flex: 1 }}
            >
              {col.header}
              {col.sort && (
                <button onClick={() => handleSort(col)} className="hover:text-foreground">
                  {sortKey === col.key ? (
                    sortDir === "asc" ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />
                  ) : (
                    <ChevronsUpDown className="h-3 w-3 opacity-40" />
                  )}
                </button>
              )}
            </div>
          ))}
        </div>
        {/* 行 */}
        {sortedData.map((row) => (
          <div
            key={rowKey(row)}
            onClick={onRowClick ? () => onRowClick(row) : undefined}
            className={cn(
              "flex gap-4 border-b px-4 py-3 text-sm transition-colors last:border-0",
              onRowClick && "cursor-pointer hover:bg-accent/50",
            )}
          >
            {columns.map((col) => (
              <div
                key={col.key}
                className={cn(
                  col.align === "center" && "text-center",
                  col.align === "right" && "text-right",
                )}
                style={col.width ? { width: col.width, flex: col.width === "auto" ? "0 0 auto" : undefined } : { flex: 1 }}
              >
                {col.render ? col.render(row) : (row as Record<string, unknown>)[col.key] as ReactNode}
              </div>
            ))}
          </div>
        ))}
      </div>

      {/* 分页 */}
      {pagination && totalPages > 1 && (
        <div className="flex items-center justify-between px-1 py-3 text-xs text-muted-foreground">
          <span>
            共 {pagination.total} 条，第 {pagination.page}/{totalPages} 页
          </span>
          <div className="flex items-center gap-1">
            <button
              onClick={() => pagination.onPageChange(pagination.page - 1)}
              disabled={pagination.page <= 1}
              className="rounded border px-2 py-1 hover:bg-accent disabled:opacity-40"
            >
              上一页
            </button>
            <button
              onClick={() => pagination.onPageChange(pagination.page + 1)}
              disabled={pagination.page >= totalPages}
              className="rounded border px-2 py-1 hover:bg-accent disabled:opacity-40"
            >
              下一页
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
