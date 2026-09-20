/**
 * useRunEventsSSE — SSE hook for governed writing runtime events
 *
 * Connects to GET /api/v2/runs/{runId}/events via EventSource API.
 * Supports Last-Event-ID for resumption, exponential backoff on reconnect,
 * and dispatches events to the writing-runtime-store.
 */
import { useEffect, useRef } from "react";
import { useWritingRuntimeStore } from "@/stores/writing-runtime-store";

const MAX_RECONNECT_DELAY = 30_000; // 30s cap
const BASE_RECONNECT_DELAY = 1_000; // 1s start

/**
 * SSE event types the governed backend emits on the writing event stream.
 * Each type maps to a named EventSource event (not the default "message").
 */
const GOVERNED_EVENT_TYPES = [
  "writing.run.status",
  "writing.node.status",
  "writing.content.delta",
  "writing.content.done",
  "writing.reasoning.delta",
  "writing.node.progress",
  "writing.artifact.created",
  "writing.document.committed",
  "writing.quality.updated",
  // Document delta events (provisional streaming)
  "writing.document.delta",
  // Ledger events (research progress, gates)
  "writing.ledger.event",
] as const;

export function useRunEventsSSE(runId: string | null) {
  const esRef = useRef<EventSource | null>(null);
  const reconnectAttempt = useRef(0);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  useEffect(() => {
    if (!runId) return;

    const applyEvent = useWritingRuntimeStore.getState().applyEvent;
    const url = `/api/v2/runs/${runId}/events`;

    function connect() {
      if (!mountedRef.current) return;

      // EventSource automatically sends Accept: text/event-stream
      // and handles Last-Event-ID for resumption on reconnect.
      const es = new EventSource(url);
      esRef.current = es;

      es.onopen = () => {
        reconnectAttempt.current = 0;
      };

      // Default message handler (events without a specific type)
      es.onmessage = (e) => {
        try {
          const event = JSON.parse(e.data);
          applyEvent(event);
        } catch {
          // Malformed event data — ignore
        }
      };

      // Listen for specific named event types
      for (const type of GOVERNED_EVENT_TYPES) {
        es.addEventListener(type, (e: MessageEvent) => {
          try {
            const event = JSON.parse(e.data);
            applyEvent(event);
          } catch {
            // Malformed event data — ignore
          }
        });
      }

      es.onerror = () {
        // EventSource auto-reconnects on transient errors.
        // On permanent failure (e.g. 204 No Content), the browser
        // closes the connection and does not reopen it.
        if (es.readyState === EventSource.CLOSED) {
          esRef.current = null;
          // Exponential backoff reconnect
          if (mountedRef.current) {
            const delay = Math.min(
              BASE_RECONNECT_DELAY * Math.pow(2, reconnectAttempt.current),
              MAX_RECONNECT_DELAY,
            );
            reconnectAttempt.current++;
            reconnectTimer.current = setTimeout(() => {
              reconnectTimer.current = null;
              if (mountedRef.current) connect();
            }, delay);
          }
        }
      };
    }

    connect();

    return () => {
      // Cleanup on unmount or runId change
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current);
        reconnectTimer.current = null;
      }
      if (esRef.current) {
        esRef.current.close();
        esRef.current = null;
      }
    };
  }, [runId]);

  return esRef;
}
