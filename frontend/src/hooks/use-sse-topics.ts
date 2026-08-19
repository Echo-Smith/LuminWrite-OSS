/**
 * useSSETopics — SSE 选题实时推送 Hook
 *
 * 连接 GET /api/v2/sse/topics，接收新选题推送，
 * 支持断线自动重连（指数退避）。
 *
 * 设计要点：
 * - 回调通过 ref 持有，connect 函数永远稳定（空依赖），
 *   回调变化不会导致 SSE 重连。
 * - 支持 onNewTopic（新增）和 onUpdateTopic（更新）两个回调。
 * - 通知事件（article:completed, notification）由全局 useSSENotifications hook 处理，
 *   此处不再重复监听，避免双重 toast。
 *
 * 文档来源: docs/03-api-specification.md — SSE API
 */
import { useEffect, useRef, useCallback, useState } from "react";
import type { Topic } from "@/lib/types";

const MAX_RECONNECT_DELAY = 30_000;
const BASE_RECONNECT_DELAY = 2_000;

export function useSSETopics(
  onNewTopic?: (topic: Topic) => void,
  onUpdateTopic?: (topic: Topic) => void,
) {
  const [connected, setConnected] = useState(false);
  const [lastEvent, setLastEvent] = useState<{ event: string; data: unknown } | null>(null);
  const eventSourceRef = useRef<EventSource | null>(null);
  const reconnectAttempt = useRef(0);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // ── 用 ref 持有最新回调，使 connect 永不重建 ──
  const cbRef = useRef({ onNewTopic, onUpdateTopic });
  useEffect(() => {
    cbRef.current = { onNewTopic, onUpdateTopic };
  }, [onNewTopic, onUpdateTopic]);

  const connect = useCallback(() => {
    // 清理旧连接
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
      setConnected(true);
      reconnectAttempt.current = 0;
    };

    es.onerror = () => {
      setConnected(false);
      es.close();
      eventSourceRef.current = null;

      // 指数退避重连
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

    // 监听连接确认事件
    es.addEventListener("connected", (e) => {
      try {
        const data = JSON.parse((e as MessageEvent).data);
        setLastEvent({ event: "connected", data });
      } catch { /* ignore */ }
    });

    // 监听初始选题批量推送
    // 注意：初始数据由 fetchTopics 负责，这里只记录事件不推送，
    // 避免与 fetchTopics 竞态导致重复或错位。
    es.addEventListener("topics:initial", (e) => {
      try {
        const data = JSON.parse((e as MessageEvent).data);
        setLastEvent({ event: "topics:initial", data });
      } catch { /* ignore */ }
    });

    // 监听新选题推送
    es.addEventListener("topic:new", (e) => {
      try {
        const data = JSON.parse((e as MessageEvent).data);
        setLastEvent({ event: "topic:new", data });
        cbRef.current.onNewTopic?.(data as Topic);
      } catch { /* ignore */ }
    });

    // 监听选题更新（如 hot_rank 变化）
    es.addEventListener("topic:update", (e) => {
      try {
        const data = JSON.parse((e as MessageEvent).data);
        setLastEvent({ event: "topic:update", data });
        cbRef.current.onUpdateTopic?.(data as Topic);
      } catch { /* ignore */ }
    });

    // 监听心跳
    es.addEventListener("heartbeat", (e) => {
      try {
        const data = JSON.parse((e as MessageEvent).data);
        setLastEvent({ event: "heartbeat", data });
      } catch { /* ignore */ }
    });
  }, []); // ← 空依赖：connect 永远稳定

  // 自动连接 & 清理
  useEffect(() => {
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
  }, [connect]);

  const disconnect = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close();
      eventSourceRef.current = null;
    }
    if (reconnectTimer.current) {
      clearTimeout(reconnectTimer.current);
      reconnectTimer.current = null;
    }
    setConnected(false);
  }, []);

  return { connected, lastEvent, reconnect: connect, disconnect };
}
