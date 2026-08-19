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
 */
import { useEffect, useRef } from "react";
import { toast } from "@/stores/toast-store";

const MAX_RECONNECT_DELAY = 30_000;
const BASE_RECONNECT_DELAY = 3_000;

export function useSSENotifications() {
  const eventSourceRef = useRef<EventSource | null>(null);
  const reconnectAttempt = useRef(0);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    const connect = () => {
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }

      const isDev = import.meta.env.DEV;
      const sseUrl = isDev
        ? "http://localhost:8080/api/v2/sse/topics"
        : "/api/v2/sse/topics";

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

      // ── 文章完成通知 ──
      es.addEventListener("article:completed", (e) => {
        try {
          const data = JSON.parse((e as MessageEvent).data);
          const title = data.article_title || data.topic || "写作完成";
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
  }, []);
}
