/**
 * useWorkflowSSE — 全局 Workflow/Editorial SSE 监听 Hook
 *
 * Editorial Transport Migration：workflow.* / node.* 编排事件原先经 WebSocket
 * 广播（writing-runtime-store 的临时 WS 连接 + handleServerMessage 转发），
 * 现改由 /api/v2/sse/topics 上的 workflow:* / node:* 事件定向推送（task owner 隔离），
 * 其余编辑部事件保持 "editorial:event" 信封转发给 editorial-store。
 *
 * 在 App 根组件挂载（与 useSSENotifications 同模式）：编辑部工作流既可从
 * 工作台页触发，也可从写作页 Composer（agent_mode=editorial）触发，
 * 全局监听保证任意页面都能收到事件。
 *
 * 命令走 REST（lib/workflow-api.ts）；本 hook 只消费事件。
 * 复用通用 topics SSE 端点，不新建连接类型。
 */
import { useEffect, useRef, useSyncExternalStore } from "react";
import { useAuthStore } from "@/stores/auth-store";
import { useWorkflowStore, type AgentConfig as WFAgentConfig, type WorkflowSpec as WFWorkflowSpec } from "@/stores/workflow-store";
import { useEditorialStore } from "@/stores/editorial-store";

const MAX_RECONNECT_DELAY = 30_000;
const BASE_RECONNECT_DELAY = 3_000;

interface WorkflowEventPayload {
  task_id?: string;
  node_id?: string;
  error?: string;
  message?: string;
  total_tokens?: number;
  tokens_used?: number;
  duration_ms?: number;
  artifact_id?: string;
  artifact_type?: string;
  delta?: string;
  agent_name?: string;
  [key: string]: unknown;
}

export function useWorkflowSSE() {
  const eventSourceRef = useRef<EventSource | null>(null);
  const reconnectAttempt = useRef(0);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const tokenRef = useRef<string | null>(null);

  // 与 useSSENotifications 相同：useSyncExternalStore 订阅 token，
  // 登录/登出时重连，且避免 StrictMode 下的 hooks 链问题。
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

      const wf = () => useWorkflowStore.getState();
      const parse = <T,>(e: MessageEvent): T | null => {
        try {
          return JSON.parse((e as MessageEvent).data) as T;
        } catch {
          return null;
        }
      };

      // ── Workflow 生命周期 ──
      es.addEventListener("workflow:created", (e) => {
        const p = parse<WorkflowEventPayload & { agents?: WFAgentConfig[]; workflow?: WFWorkflowSpec; rationale?: string }>(e);
        if (!p) return;
        wf().setPlan({
          agents: p.agents ?? [],
          workflow: p.workflow ?? ({} as WFWorkflowSpec),
          rationale: p.rationale ?? "",
        });
        if (p.task_id) wf().setTaskId(p.task_id);
      });

      es.addEventListener("workflow:started", (e) => {
        if (parse<WorkflowEventPayload>(e)) wf().setRunStatus("running");
      });

      es.addEventListener("workflow:completed", (e) => {
        const p = parse<WorkflowEventPayload>(e);
        if (!p) return;
        wf().setWorkflowCompleted(p.total_tokens ?? 0);
      });

      es.addEventListener("workflow:failed", (e) => {
        const p = parse<WorkflowEventPayload>(e);
        if (!p) return;
        wf().setWorkflowFailed(p.error ?? p.message ?? "工作流执行失败");
      });

      es.addEventListener("workflow:cancelled", (e) => {
        // 服务端取消收敛为失败终态（DAGExecutor.finalize 发 workflow.failed）；
        // 此事件仅在兼容旧客户端语义时出现，前端直接回到 idle。
        if (parse<WorkflowEventPayload>(e)) wf().reset();
      });

      // ── 节点执行状态 ──
      es.addEventListener("node:started", (e) => {
        const p = parse<WorkflowEventPayload>(e);
        if (!p?.node_id) return;
        wf().setNodeStarted(p.node_id, (p.agent_name as string) ?? "");
      });

      es.addEventListener("node:stream:delta", (e) => {
        const p = parse<WorkflowEventPayload>(e);
        if (!p?.node_id || !p.delta) return;
        wf().appendNodeStream(p.node_id, p.delta);
      });

      es.addEventListener("node:stream:reset", (e) => {
        const p = parse<WorkflowEventPayload>(e);
        if (!p?.node_id) return;
        wf().resetNodeStream(p.node_id);
      });

      es.addEventListener("node:completed", (e) => {
        const p = parse<WorkflowEventPayload>(e);
        if (!p?.node_id) return;
        wf().setNodeCompleted(
          p.node_id,
          p.artifact_id ?? "",
          p.artifact_type ?? "",
          p.tokens_used ?? 0,
          p.duration_ms ?? 0,
        );
      });

      es.addEventListener("node:failed", (e) => {
        const p = parse<WorkflowEventPayload>(e);
        if (!p?.node_id) return;
        wf().setNodeFailed(p.node_id, p.error ?? "节点执行失败", p.duration_ms ?? 0);
      });

      es.addEventListener("node:error", (e) => {
        const p = parse<WorkflowEventPayload>(e);
        if (p) console.warn(`[DAG] Node error: ${p.node_id}`, p.message);
      });

      // ── 其余编辑部事件（task.* / artifact.* / decision.* / experiment.*）
      //    —— 信封与旧 WS "editorial.event" 一致，editorial-store 原样消费 ──
      es.addEventListener("editorial:event", (e) => {
        const evt = parse<{ type: string; task_id: string; payload: Record<string, unknown>; timestamp: string }>(e);
        if (evt?.type) useEditorialStore.getState().pushEvent(evt);
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
