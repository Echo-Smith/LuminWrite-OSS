import assert from "node:assert/strict";
import test from "node:test";

import { resolveConversationId } from "../src/lib/conversation-session.ts";

test("follow-up writing keeps the first trace as the stable conversation key", () => {
  assert.equal(
    resolveConversationId(undefined, { conversationId: "trace-first", traceId: "trace-current" }),
    "trace-first",
  );
});

test("an explicit session key is never silently replaced", () => {
  assert.equal(
    resolveConversationId("conversation-explicit", { conversationId: "trace-first", traceId: "trace-current" }),
    "conversation-explicit",
  );
});
