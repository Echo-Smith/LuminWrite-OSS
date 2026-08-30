import assert from "node:assert/strict";
import test from "node:test";

import { initialWritingRuntimeProjection, projectWritingEvent } from "../src/stores/writing-runtime-store.ts";
import type { WritingEvent } from "../src/lib/writing-runtime-types.ts";

function event(sequence: number, type: WritingEvent["type"], payload: Record<string, unknown>): WritingEvent {
  return { protocol: "lumin-writing.v2", type, run_id: "run_1", sequence, timestamp: "2026-08-29T00:00:00Z", status: "running", payload };
}

test("provisional deltas remain ephemeral until a committed version event", () => {
  const streamed = projectWritingEvent(initialWritingRuntimeProjection, event(1, "writing.document.delta", {
    document_id: "doc_1", block_id: "blk_intro", delta: "临时文字", lifecycle: "provisional",
  }));
  assert.equal(streamed.provisionalDeltas.blk_intro, "临时文字");
  assert.equal(streamed.committedVersionId, null);

  const committed = projectWritingEvent(streamed, event(2, "writing.document.committed", {
    document_id: "doc_1", version_id: "ver_2", content_hash: "sha256:test", quality_state: "candidate_draft", lifecycle: "committed",
  }));
  assert.deepEqual(committed.provisionalDeltas, {});
  assert.equal(committed.committedVersionId, "ver_2");
});

test("duplicate and out-of-order events cannot rewind the projection", () => {
  const current = projectWritingEvent(initialWritingRuntimeProjection, event(3, "writing.node.status", { node_id: "draft", attempt: 1, status: "completed" }));
  const stale = projectWritingEvent(current, event(2, "writing.node.status", { node_id: "draft", attempt: 1, status: "running" }));
  assert.strictEqual(stale, current);
  assert.equal(stale.nodeStatuses.draft, "completed");
});

test("runtime projection contains no layout controls", () => {
  const projected = projectWritingEvent(initialWritingRuntimeProjection, event(1, "writing.run.status", { from: "planned", to: "running" }));
  for (const forbidden of ["globalSidebar", "detailPanel", "detailTab", "conversationPanel"]) {
    assert.equal(forbidden in projected, false);
  }
});
