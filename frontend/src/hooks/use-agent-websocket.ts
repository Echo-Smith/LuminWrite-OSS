/**
 * useAgentWebSocket — 封装 Agent WebSocket 连接管理
 *
 * 职责：
 *  - 认证就绪后自动连接 WS
 *  - 组件卸载时断开（但保留 store 中的 WS 供其他组件使用）
 *  - 暴露连接状态与写作控制方法（暂停/恢复/取消/确认提纲）
 *  - 断线自动重连（指数退避）
 */
import { useEffect, useRef, useCallback } from "react";
import { useAgentStore } from "@/stores/agent-store";
import { useAuthStore } from "@/stores/auth-store";
import type { AgentStartPayload, OutlineData } from "@/lib/types";

const MAX_RECONNECT_DELAY = 30_000; // 30s 上限
const BASE_RECONNECT_DELAY = 1_000; // 1s 起步

export function useAgentWebSocket() {
  const wsConnected = useAgentStore((s) => s.wsConnected);
  const connectWS = useAgentStore((s) => s.connectWS);
  const sendWS = useAgentStore((s) => s.sendWS);
  const startWriting = useAgentStore((s) => s.startWriting);
  const pauseWriting = useAgentStore((s) => s.pauseWriting);
  const resumeWriting = useAgentStore((s) => s.resumeWriting);
  const cancelWriting = useAgentStore((s) => s.cancelWriting);
  const confirmOutline = useAgentStore((s) => s.confirmOutline);
  const regenerateOutline = useAgentStore((s) => s.regenerateOutline);

  const isAuthenticated = useAuthStore((s) => s.isAuthenticated());
  const token = useAuthStore((s) => s.token);

  const reconnectAttempt = useRef(0);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // ─── 自动连接 ──────────────────────────────────────────
  // 依赖 token：当 token 变化（如游客注册升级）时，上层 login() 会先
  // close 旧 WS 并置 null，此处随之触发用新 token 重连。
  useEffect(() => {
    if (!isAuthenticated) return;

    // 如果已连接或正在连接，不重复
    const { ws } = useAgentStore.getState();
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
      return;
    }

    connectWS();
  }, [isAuthenticated, token, connectWS]);

  // ─── 监听断线 → 指数退避重连 ────────────────────────────
  useEffect(() => {
    if (wsConnected) {
      reconnectAttempt.current = 0;
      return;
    }

    // 未认证不重连
    if (!isAuthenticated) return;

    // 已有定时器在等待
    if (reconnectTimer.current) return;

    const delay = Math.min(
      BASE_RECONNECT_DELAY * Math.pow(2, reconnectAttempt.current),
      MAX_RECONNECT_DELAY
    );

    reconnectTimer.current = setTimeout(() => {
      reconnectTimer.current = null;
      reconnectAttempt.current++;
      connectWS();
    }, delay);

    return () => {
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current);
        reconnectTimer.current = null;
      }
    };
  }, [wsConnected, isAuthenticated, connectWS]);

  // ─── 组件卸载时清理 ────────────────────────────────────
  useEffect(() => {
    return () => {
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current);
        reconnectTimer.current = null;
      }
    };
  }, []);

  // ─── 封装的控制方法 ────────────────────────────────────
  const start = useCallback(
    (payload: AgentStartPayload) => {
      startWriting(payload);
    },
    [startWriting]
  );

  const pause = useCallback(() => pauseWriting(), [pauseWriting]);
  const resume = useCallback(() => resumeWriting(), [resumeWriting]);
  const cancel = useCallback(() => cancelWriting(), [cancelWriting]);
  const confirm = useCallback(
    (data: OutlineData | null) => confirmOutline(data),
    [confirmOutline]
  );
  const regenerate = useCallback(() => regenerateOutline(), [regenerateOutline]);

  return {
    connected: wsConnected,
    reconnectAttempt: reconnectAttempt.current,
    start,
    pause,
    resume,
    cancel,
    confirm,
    regenerate,
    send: sendWS,
  };
}
