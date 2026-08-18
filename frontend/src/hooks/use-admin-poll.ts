/**
 * useAdminPoll — Admin 实时轮询 Hook
 *
 * 用于需要实时刷新的 Admin 页面（如 cron-jobs 执行状态、evaluation 运行进度）
 *
 * 使用示例：
 *   const { data, loading, refresh } = useAdminPoll<CronJob[]>(
 *     "/api/v2/admin/cron-jobs",
 *     { interval: 5000, enabled: isRunning }
 *   );
 */
import { useState, useEffect, useCallback, useRef } from "react";
import { adminFetch } from "@/lib/admin-api";

interface UseAdminPollOptions {
  /** 轮询间隔（ms），默认 5000 */
  interval?: number;
  /** 是否启用轮询，默认 true */
  enabled?: boolean;
  /** 静默模式（不弹错误 toast），默认 true */
  silent?: boolean;
}

export function useAdminPoll<T>(
  endpoint: string,
  options: UseAdminPollOptions = {},
) {
  const { interval = 5000, enabled = true, silent = true } = options;

  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const mountedRef = useRef(true);

  const poll = useCallback(async () => {
    const { success, data: result, error: err } = await adminFetch<T>(endpoint, { silent });
    if (!mountedRef.current) return;
    if (success) {
      setData(result ?? null);
      setError(null);
    } else {
      setError(err?.message ?? "加载失败");
    }
    setLoading(false);
  }, [endpoint, silent]);

  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  useEffect(() => {
    if (!enabled) {
      if (timerRef.current) {
        clearInterval(timerRef.current);
        timerRef.current = null;
      }
      return;
    }

    // 立即加载一次
    poll();

    // 启动定时轮询
    timerRef.current = setInterval(poll, interval);

    return () => {
      if (timerRef.current) {
        clearInterval(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [poll, interval, enabled]);

  const refresh = useCallback(async () => {
    setLoading(true);
    await poll();
  }, [poll]);

  return { data, loading, error, refresh, setData };
}
