/**
 * useAdminResource — 通用 Admin CRUD Hook
 *
 * 封装列表加载、创建、更新、删除的通用逻辑：
 * - 自动加载列表 + loading/error 状态管理
 * - create/update/delete 后自动 refresh
 * - 统一 toast 反馈
 * - 支持依赖加载（waitFor 条件满足后才加载）
 *
 * 使用示例：
 *   const { items, loading, error, create, update, remove, refresh } =
 *     useAdminResource<ModelConfig>("/api/v2/admin/models");
 *
 *   // 创建
 *   await create({ name: "GPT-4", ... }, { successTitle: "模型已添加" });
 *
 *   // 更新
 *   await update("model-id", { name: "GPT-4o" }, { successTitle: "模型已更新" });
 *
 *   // 删除
 *   await remove("model-id", { confirmMsg: "确认删除此模型？", successTitle: "模型已删除" });
 */
import { useState, useEffect, useCallback, useRef } from "react";
import { adminFetch, adminMutate, adminDelete } from "@/lib/admin-api";

interface UseAdminResourceOptions {
  /** 是否自动加载（默认 true） */
  autoLoad?: boolean;
  /** 依赖条件：满足后才加载（如 configured === true） */
  waitFor?: boolean;
  /** 静默模式：加载失败不弹 toast */
  silentLoad?: boolean;
}

interface MutateOptions {
  successTitle?: string;
  successDesc?: string;
  confirmMsg?: string;
  silent?: boolean;
}

export function useAdminResource<T extends { id?: string }>(
  endpoint: string,
  options: UseAdminResourceOptions = {},
) {
  const { autoLoad = true, waitFor = true, silentLoad = true } = options;

  const [items, setItems] = useState<T[]>([]);
  const [loading, setLoading] = useState(autoLoad && waitFor);
  const [error, setError] = useState<string | null>(null);
  const mountedRef = useRef(true);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    const { success, data, error: err } = await adminFetch<T[]>(endpoint, { silent: silentLoad });
    if (!mountedRef.current) return;
    if (success) {
      setItems(data ?? []);
    } else {
      setError(err?.message ?? "加载失败");
    }
    setLoading(false);
  }, [endpoint, silentLoad]);

  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  useEffect(() => {
    if (autoLoad && waitFor) {
      refresh();
    }
  }, [autoLoad, waitFor, refresh]);

  /** 创建新资源 */
  const create = useCallback(async (
    body: Record<string, unknown>,
    opts: MutateOptions = {},
  ): Promise<boolean> => {
    const { success } = await adminMutate<T>(endpoint, {
      method: "POST",
      body: JSON.stringify(body),
      successTitle: opts.successTitle,
      successDesc: opts.successDesc,
      silent: opts.silent,
    });
    if (success) {
      await refresh();
    }
    return success;
  }, [endpoint, refresh]);

  /** 更新资源 */
  const update = useCallback(async (
    id: string,
    body: Record<string, unknown>,
    opts: MutateOptions = {},
  ): Promise<boolean> => {
    const { success } = await adminMutate<T>(`${endpoint}/${id}`, {
      method: "PUT",
      body: JSON.stringify(body),
      successTitle: opts.successTitle,
      successDesc: opts.successDesc,
      silent: opts.silent,
    });
    if (success) {
      await refresh();
    }
    return success;
  }, [endpoint, refresh]);

  /** 删除资源（带确认） */
  const remove = useCallback(async (
    id: string,
    opts: MutateOptions = {},
  ): Promise<boolean> => {
    const ok = await adminDelete(
      `${endpoint}/${id}`,
      opts.confirmMsg ?? "确认删除？此操作不可撤销。",
      opts.successTitle ?? "删除成功",
    );
    if (ok) {
      await refresh();
    }
    return ok;
  }, [endpoint, refresh]);

  /** 自定义操作（如 test、reconnect、run 等） */
  const action = useCallback(async <R = unknown>(
    id: string,
    actionPath: string,
    opts: MutateOptions = {},
  ): Promise<R | null> => {
    const { success, data } = await adminMutate<R>(`${endpoint}/${id}/${actionPath}`, {
      method: "POST",
      successTitle: opts.successTitle,
      successDesc: opts.successDesc,
      silent: opts.silent,
    });
    if (success && data !== undefined) {
      await refresh();
      return data;
    }
    return null;
  }, [endpoint, refresh]);

  return {
    items,
    loading,
    error,
    create,
    update,
    remove,
    action,
    refresh,
    setItems,
  };
}
