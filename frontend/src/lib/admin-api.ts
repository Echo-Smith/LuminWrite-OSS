/**
 * Admin 统一 API 调用层
 *
 * 封装所有 Admin 页面的 fetch 调用：
 * - 自动附加 JWT Authorization header（由全局 fetch 拦截器处理）
 * - 统一解析后端响应格式 { success, data?, error? }
 * - 统一 toast 错误提示（可通过 silent 选项关闭）
 * - 统一网络错误处理
 *
 * 使用方式：
 *   const { success, data } = await adminFetch<ModelConfig[]>("/api/v2/admin/models");
 *   const result = await adminMutate<ModelConfig>("/api/v2/admin/models", { method: "POST", body: JSON.stringify(payload) });
 */
import { toast } from "@/stores/toast-store";
import type { AdminApiResponse } from "./admin-types";

// ─── 核心封装函数 ──────────────────────────────────────────

interface AdminFetchOptions extends RequestInit {
  /** 静默模式：不弹 toast 错误提示（用于列表轮询、后台预加载等场景） */
  silent?: boolean;
}

/**
 * GET 类请求：自动解析响应，错误时弹 toast
 *
 * @returns { success: boolean; data?: T; error?: { code, message } }
 */
export async function adminFetch<T>(
  endpoint: string,
  options?: AdminFetchOptions,
): Promise<AdminApiResponse<T>> {
  const { silent, ...fetchOptions } = options ?? {};

  try {
    const isFormData = typeof FormData !== "undefined" && fetchOptions.body instanceof FormData;
    const res = await fetch(endpoint, {
      ...fetchOptions,
      headers: isFormData
        ? fetchOptions?.headers
        : {
            "Content-Type": "application/json",
            ...fetchOptions?.headers,
          },
    });

    const json: AdminApiResponse<T> = await res.json();

    if (!res.ok || !json.success) {
      if (!silent) {
        const msg = json.error?.message ?? `请求失败 (${res.status})`;
        toast.error("操作失败", msg);
      }
      return {
        success: false,
        data: json.data,
        error: json.error ?? { code: "http_error", message: `HTTP ${res.status}` },
      };
    }

    return { success: true, data: json.data };
  } catch (err) {
    if (!silent) {
      const msg = err instanceof Error ? err.message : "网络错误，请检查连接";
      toast.error("网络错误", msg);
    }
    return { success: false, error: { code: "network_error", message: err instanceof Error ? err.message : "网络错误" } };
  }
}

export async function adminUpload<T>(
  endpoint: string,
  file: File,
  options: { silent?: boolean; fieldName?: string } = {},
): Promise<AdminApiResponse<T>> {
  const form = new FormData();
  form.append(options.fieldName ?? "file", file);
  return adminMutate<T>(endpoint, {
    method: "POST",
    body: form,
    silent: options.silent,
  });
}

export async function adminDownload(endpoint: string, fallbackFileName: string): Promise<boolean> {
  try {
    const res = await fetch(endpoint);
    if (!res.ok) {
      let message = `下载失败 (${res.status})`;
      try {
        const payload = await res.json() as AdminApiResponse<never>;
        message = payload.error?.message ?? message;
      } catch {
        // The response may not be JSON (for example, an upstream proxy error page).
      }
      toast.error("下载失败", message);
      return false;
    }
    const blob = await res.blob();
    const disposition = res.headers.get("Content-Disposition") ?? "";
    const match = disposition.match(/filename\*?=(?:UTF-8''|")?([^";]+)/i);
    const fileName = match?.[1] ? decodeURIComponent(match[1]) : fallbackFileName;
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = fileName;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
    return true;
  } catch (err) {
    toast.error("下载失败", err instanceof Error ? err.message : "网络错误，请稍后重试");
    return false;
  }
}

/**
 * 写操作（POST/PUT/DELETE）：自动解析响应，成功时弹 toast，错误时弹 toast
 *
 * @param successTitle 成功时的 toast 标题
 * @param successDesc  成功时的 toast 描述（可选）
 */
export async function adminMutate<T>(
  endpoint: string,
  options: AdminFetchOptions & { successTitle?: string; successDesc?: string } = {},
): Promise<AdminApiResponse<T>> {
  const { successTitle, successDesc, silent, ...fetchOptions } = options;

  const result = await adminFetch<T>(endpoint, { ...fetchOptions, silent: true });

  if (result.success) {
    if (successTitle && !silent) {
      toast.success(successTitle, successDesc);
    }
  } else {
    if (!silent) {
      toast.error("操作失败", result.error?.message ?? "请检查权限或重试");
    }
  }

  return result;
}

/**
 * 删除操作：带确认提示
 *
 * @param endpoint  删除接口 URL
 * @param confirmMsg 确认提示文案
 * @param successTitle 成功时的 toast 标题
 */
export async function adminDelete(
  endpoint: string,
  confirmMsg = "确认删除？此操作不可撤销。",
  successTitle = "删除成功",
): Promise<boolean> {
  if (!window.confirm(confirmMsg)) return false;

  const { success } = await adminMutate(endpoint, {
    method: "DELETE",
    successTitle,
  });

  return success;
}

// ─── 便捷方法 ──────────────────────────────────────────────

/**
 * 批量并行 GET 请求（用于页面初始化时同时加载多个数据源）
 */
export async function adminBatch<T extends Record<string, unknown>>(
  requests: { [K in keyof T]: string },
): Promise<{ success: boolean; data: Partial<T> }> {
  const entries = Object.entries(requests) as [string, string][];
  const results = await Promise.all(
    entries.map(async ([key, url]) => {
      const { success, data } = await adminFetch<unknown>(url, { silent: true });
      return [key, { success, data }] as const;
    }),
  );

  const merged: Partial<T> = {};
  let allSuccess = true;

  for (const [key, { success, data }] of results) {
    if (success && data !== undefined) {
      (merged as Record<string, unknown>)[key] = data;
    } else {
      allSuccess = false;
    }
  }

  return { success: allSuccess, data: merged };
}
