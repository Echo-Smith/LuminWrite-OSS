import { describe, expect, it } from "vitest";

import { EditOpSchema, TurnRequestSchema } from "./index.js";

describe("contracts", () => {
  it("接受一条块锚点替换", () => {
    const parsed = EditOpSchema.parse({
      op: "replace_text",
      blockId: "blk_abcdefghij1234",
      contentHash: `sha256:${"ab".repeat(32)}`,
      expectedOldText: "原句。",
      newText: "新句。",
      reason: "缩短",
    });
    expect(parsed.op).toBe("replace_text");
  });

  it("拒绝空 reason", () => {
    const result = EditOpSchema.safeParse({
      op: "delete_block",
      blockId: "blk_abcdefghij1234",
      contentHash: `sha256:${"ab".repeat(32)}`,
      expectedOldText: "原句。",
      reason: "  ",
    });
    expect(result.success).toBe(false);
  });

  it("回合请求带幂等键", () => {
    const parsed = TurnRequestSchema.parse({
      documentId: "doc_1",
      kind: "revise",
      instruction: "把第二段写短",
      scope: "document",
      delivery: "followup",
      applyMode: "auto",
      idempotencyKey: "k1",
      actor: { type: "user", userId: "user_1" },
    });
    expect(parsed.kind).toBe("revise");
  });
});
