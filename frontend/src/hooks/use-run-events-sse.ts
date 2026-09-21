/**
 * useRunEventsSSE — fetch-based SSE hook for governed writing runtime events
 *
 * Connects to GET /api/v2/runs/{runId}/events with `Authorization: Bearer` via
 * fetch + ReadableStream instead of native EventSource, because EventSource
 * cannot set custom headers and the run-events endpoint is JWT-protected.
 *
 * Resumption: the backend emits `id: <sequence>` per event and accepts
 * `?after=<sequence>` (and Last-Event-ID). We track the last sequence and
 * re-establish with `?after=` on reconnect — durable ledger, no event loss.
 */
import { useEffect, useRef } from "react";
import { useWritingRuntimeStore } from "@/stores/writing-runtime-store";
import { useAuthStore } from "@/stores/auth-store";

const MAX_RECONNECT_DELAY = 30_000; // 30s cap
const BASE_RECONNECT_DELAY = 1_000; // 1s start

/**
 * Minimal SSE parser: splits the byte stream into events on blank-line
 * separators and extracts id/event/data fields. Handles \n\n and \r\n\r\n.
 */
function parseSSEBlock(block: string): { id: string; event: string; data: string } | null {
  let id = "";
  let event = "message";
  const dataLines: string[] = [];
  for (const line of block.split("\n")) {
    const field = line.replace(/\r$/, "");
    if (field.startsWith("id:")) {
      id = field.slice(3).trim();
    } else if (field.startsWith("event:")) {
      event = field.slice(6).trim();
    } else if (field.startsWith("data:")) {
      dataLines.push(field.slice(5).trimStart());
    }
    // comments (":" prefix) and unknown fields ignored
  }
  if (dataLines.length === 0) return null;
  return { id, event, data: dataLines.join("\n") };
}

export function useRunEventsSSE(runId: string | null) {
  const abortRef = useRef<AbortController | null>(null);
  const reconnectAttempt = useRef(0);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mountedRef = useRef(true);
  const lastSequence = useRef(0);

  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  useEffect(() => {
    if (!runId) return;

    lastSequence.current = 0;
    reconnectAttempt.current = 0;

    async function connect() {
      while (mountedRef.current) {
        const controller = new AbortController();
        abortRef.current = controller;
        try {
          const token = useAuthStore.getState().token;
          const after = lastSequence.current > 0 ? `?after=${lastSequence.current}` : "";
          const response = await fetch(`/api/v2/runs/${runId}/events${after}`, {
            headers: {
              ...(token ? { Authorization: `Bearer ${token}` } : {}),
              Accept: "text/event-stream",
            },
            signal: controller.signal,
          });
          if (!response.ok || !response.body) {
            throw new Error(`SSE connect failed: ${response.status}`);
          }

          reconnectAttempt.current = 0;
          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";

          // Read until the stream ends (server close / abort) or unmount.
          for (;;) {
            const { done, value } = await reader.read();
            if (done) break;
            buffer += decoder.decode(value, { stream: true });

            let sep: number;
            while ((sep = buffer.search(/\n\n|\r\n\r\n/)) >= 0) {
              const block = buffer.slice(0, sep);
              buffer = buffer.slice(sep).replace(/^\r?\n\r?\n/, "");
              const parsed = parseSSEBlock(block);
              if (!parsed) continue;
              if (parsed.id) {
                const seq = Number(parsed.id);
                if (Number.isFinite(seq) && seq > lastSequence.current) {
                  lastSequence.current = seq;
                }
              }
              try {
                const event = JSON.parse(parsed.data);
                useWritingRuntimeStore.getState().applyEvent(event);
              } catch {
                // Malformed event data — ignore
              }
            }
          }
          // Stream ended normally (server closed) — fall through to reconnect.
        } catch (error) {
          if (!mountedRef.current || (error instanceof DOMException && error.name === "AbortError")) {
            return; // unmounted or superseded — do not reconnect
          }
          // Connection error — exponential backoff and retry with ?after=
          const delay = Math.min(
            BASE_RECONNECT_DELAY * Math.pow(2, reconnectAttempt.current),
            MAX_RECONNECT_DELAY,
          );
          reconnectAttempt.current++;
          await new Promise((resolve) => {
            reconnectTimer.current = setTimeout(resolve, delay);
          });
          reconnectTimer.current = null;
        }
      }
    }

    void connect();

    return () => {
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current);
        reconnectTimer.current = null;
      }
      abortRef.current?.abort();
      abortRef.current = null;
    };
  }, [runId]);

  return abortRef;
}
