import assert from "node:assert/strict";
import test from "node:test";

import { useWritingRuntimeStore } from "../src/stores/writing-runtime-store.ts";
import type { WritingSession } from "../src/lib/writing-runtime-types.ts";

/**
 * History source-of-truth: governed runs (GET /api/v2/runs) are the primary
 * sidebar history, legacy agent_traces sessions stay appended below, and an
 * in-memory session (the live one startWriting linked) wins over both.
 */

const GOVERNED_RUNS = {
  success: true,
  data: {
    total: 2,
    page: 1,
    page_size: 50,
    runs: [
      {
        run_id: "run_abc123",
        document_id: "doc_1",
        title: "governed 最新一次",
        status: "running",
        style_slug: "yinyue",
        created_at: "2026-09-20T10:00:00Z",
        updated_at: "2026-09-20T10:05:00Z",
        completed_at: null,
      },
      {
        run_id: "run_old456",
        document_id: "doc_2",
        title: "完成的历史运行",
        status: "completed",
        style_slug: "yinyue",
        created_at: "2026-09-18T09:00:00Z",
        updated_at: "2026-09-18T09:30:00Z",
        completed_at: "2026-09-18T09:30:00Z",
      },
    ],
  },
};

const LEGACY_SESSIONS = {
  success: true,
  data: {
    total: 2,
    sessions: [
      {
        trace_id: "trace_legacy1",
        status: "completed",
        current_step: "",
        user_input: "旧版选题一",
        mode: "auto",
        created_at: "2026-09-19T08:00:00Z",
      },
      {
        // Same trace id as a governed run: must be dropped in favor of it.
        trace_id: "run_abc123",
        status: "completed",
        current_step: "",
        user_input: "重复的历史行",
        mode: "auto",
        created_at: "2026-09-19T08:00:00Z",
      },
    ],
  },
};

function jsonResponse(body: unknown) {
  return Promise.resolve(new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  }));
}

test("loadSessions merges governed runs first with legacy history below", async () => {
  const store = useWritingRuntimeStore.getState();
  useWritingRuntimeStore.setState({ sessions: [], sessionsLoaded: false });
  const requested: string[] = [];
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const path = typeof input === "string" ? input : input.toString();
    requested.push(path);
    if (path.startsWith("/api/v2/runs")) return jsonResponse(GOVERNED_RUNS);
    return jsonResponse(LEGACY_SESSIONS);
  }) as typeof fetch;

  await store.loadSessions();

  const sessions = useWritingRuntimeStore.getState().sessions;
  assert.deepEqual(requested.sort(), ["/api/v2/runs?page=1&page_size=50", "/api/v2/sessions?page=1&page_size=50"]);
  assert.deepEqual(sessions.map((s) => [s.id, s.source]), [
    ["run_abc123", "governed"],
    ["run_old456", "governed"],
    ["trace_legacy1", "legacy"],
  ]);
  const live = sessions[0];
  assert.equal(live.traceId, "run_abc123");
  assert.equal(live.status, "running");
  assert.equal(live.title, "governed 最新一次");
  // total spans both sources so "load more" keeps working
  assert.equal(useWritingRuntimeStore.getState().sessionsTotal, 4);
  assert.equal(useWritingRuntimeStore.getState().sessionsLoaded, true);
});

test("an in-memory session linked by startWriting wins over the run list row", async () => {
  useWritingRuntimeStore.setState({ sessions: [], sessionsLoaded: false });
  const live: WritingSession = {
    id: "local-1",
    title: "正在写的会话",
    messages: [],
    traceId: "run_abc123",
    conversationId: "run_abc123",
    status: "running",
    style: "yinyue",
    mode: "auto",
    createdAt: Date.now(),
    folderId: null,
    archived: false,
    awaitInputAt: null,
    kbEnabled: true,
  };
  useWritingRuntimeStore.setState({ sessions: [live] });
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const path = typeof input === "string" ? input : input.toString();
    if (path.startsWith("/api/v2/runs")) return jsonResponse(GOVERNED_RUNS);
    return jsonResponse(LEGACY_SESSIONS);
  }) as typeof fetch;

  await useWritingRuntimeStore.getState().loadSessions();

  const sessions = useWritingRuntimeStore.getState().sessions;
  assert.equal(sessions.filter((s) => s.traceId === "run_abc123").length, 1);
  const kept = sessions.find((s) => s.traceId === "run_abc123");
  assert.equal(kept?.id, "local-1"); // in-memory object survived
  assert.equal(kept?.source, undefined); // not replaced by the list row
  assert.equal(sessions[0]?.id, "local-1"); // local-only entries stay on top
});

test("switchSession on a governed session loads its run instead of the legacy detail", async () => {
  useWritingRuntimeStore.setState({ sessions: [], sessionsLoaded: false });
  let runLoads = 0;
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const path = typeof input === "string" ? input : input.toString();
    if (path.startsWith("/api/v2/runs/run_old456")) {
      runLoads += 1;
      return jsonResponse({ success: true, data: { run_id: "run_old456", status: "completed", budget: {}, permissions: [] } });
    }
    if (path.startsWith("/api/v2/runs")) return jsonResponse(GOVERNED_RUNS);
    if (path.startsWith("/api/v2/sessions/run_old456")) {
      throw new Error("legacy session detail must not be called for governed sessions");
    }
    return jsonResponse({ success: true, data: { sessions: [], total: 0 } });
  }) as typeof fetch;

  await useWritingRuntimeStore.getState().loadSessions();
  useWritingRuntimeStore.getState().switchSession("run_old456");

  assert.equal(useWritingRuntimeStore.getState().activeSessionId, "run_old456");
  assert.equal(useWritingRuntimeStore.getState().activeRunId, "run_old456");
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(runLoads, 1);
  assert.equal(useWritingRuntimeStore.getState().run?.run_id, "run_old456");
});
