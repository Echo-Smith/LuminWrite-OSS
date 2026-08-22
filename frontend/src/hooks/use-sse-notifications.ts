/**
 * useSSENotifications — 全局 SSE 通知监听 Hook
 *
 * 在 App 根组件挂载，监听 article:completed 和 notification 事件，
 * 自动弹出 toast 通知。
 *
 * 与 useSSETopics 分离：useSSETopics 在 TopicCenter 页面使用，
 * 负责选题推送 + 通知；本 hook 在 App 根使用，
 * 确保所有页面都能收到 article:completed 通知。
 *
 * 注意：两个 hook 会各自创建独立的 EventSource 连接，
 * 但 SSE 是轻量级的，服务器端 SSEHub 支持多客户端。
 *
 * JWT token 通过 URL query param 传递（EventSource 不支持自定义 header），
 * 当用户登录/登出时自动重新连接。
 *
 * 设计说明：
 * 不直接使用 useAuthStore((s) => s.token) 的 hook 形式，
 * 而是通过 useSyncExternalStore 订阅 store 变化，避免在 App 根组件
 * 渲染路径中通过 zustand 的 useStore 内部注册额外的 hooks，
 * 防止 StrictMode 下 "Rendered more hooks than during the previous render" 错误。
 */
import { useEffect, useRef, useSyncExternalStore } from "react";
import { toast } from "@/stores/toast-store";
import { useAuthStore } from "@/stores/auth-store";

const MAX_RECONNECT_DELAY = 30_000;
const BASE_RECONNECT_DELAY = 3_000;

export function useSSENotifications() {
  const eventSourceRef = useRef<EventSource | null>(null);
  const reconnectAttempt = useRef(0);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const tokenRef = useRef<string | null>(null);

  // 使用 useSyncExternalStore 直接订阅 useAuthStore 的 token 字段，
  // 避免通过 zustand 的 useStore hook 间接注册 useCallback hooks。
  // zustand 的 useStore 内部使用 useSyncExternalStore + 2× useCallback，
  // 在 StrictMode 下可能因 selector 每次渲染新引用导致 hooks 链表不一致。
  const token = useSyncExternalStore(
    useAuthStore.subscribe,
    () => useAuthStore.getState().token ?? null,
  );

  useEffect(() => {
    tokenRef.current = token;

    const connect = () => {
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }

      const isDev = import.meta.env.DEV;
      const currentToken = tokenRef.current;
      const tokenParam = currentToken ? `?token=${encodeURIComponent(currentToken)}` : "";
      const sseUrl = isDev
        ? `http://localhost:8080/api/v2/sse/topics${tokenParam}`
        : `/api/v2/sse/topics${tokenParam}`;

      const es = new EventSource(sseUrl);
      eventSourceRef.current = es;

      es.onopen = () => {
        reconnectAttempt.current = 0;
      };

      es.onerror = () => {
        es.close();
        eventSourceRef.current = null;

        if (reconnectTimer.current) return;

        const delay = Math.min(
          BASE_RECONNECT_DELAY * Math.pow(2, reconnectAttempt.current),
          MAX_RECONNECT_DELAY,
        );

        reconnectTimer.current = setTimeout(() => {
          reconnectTimer.current = null;
          reconnectAttempt.current++;
          connect();
        }, delay);
      };

      // ── 文章完成通知 — 仅当后端确认生成了文章时才弹通知 ──
      // 后端已通过 SSEEvent.UserID 做了服务端过滤（只有发起写作的用户会收到），
      // 此处不再需要前端过滤，但仍保留 article_title/topic 非空检查。
      es.addEventListener("article:completed", (e) => {
        try {
          const data = JSON.parse((e as MessageEvent).data);
          // 防御性检查：只有 article_title 或 topic 非空时才显示
          // 避免 chat 等非写作意图也触发通知
          const title = data.article_title || data.topic;
          if (!title) return;
          toast.success("✍️ 写作完成", title, 6000);
        } catch { /* ignore */ }
      });

      // ── 通用通知 ──
      es.addEventListener("notification", (e) => {
        try {
          const data = JSON.parse((e as MessageEvent).data);
          toast.info(data.title || "通知", data.body, 5000);
        } catch { /* ignore */ }
      });
    };

    connect();

    return () => {
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current);
        reconnectTimer.current = null;
      }
    };
  }, [token]);
}
