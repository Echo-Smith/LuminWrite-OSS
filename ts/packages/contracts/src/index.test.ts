import { describe, expect, it } from "vitest";

import {
  applyDossierOps,
  CreateTaskBodySchema,
  DossierOpSchema,
  dossierForDerived,
  EditOpSchema,
  emptyDossier,
  normalizeCreateTask,
  TurnRequestSchema,
} from "./index.js";

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

  it("档案引用可以带 evidenceId，旧引用不强制", () => {
    const parsed = DossierOpSchema.parse({
      op: "add_citation",
      title: "时租价目",
      evidenceId: "evd_abc",
    });
    expect(parsed.op).toBe("add_citation");
    const next = applyDossierOps(emptyDossier(), [
      { op: "add_citation", title: "时租价目", locator: "2026-10", evidenceId: "evd_abc" },
      { op: "add_citation", title: "旧引用", locator: "" },
    ]);
    expect(next.citations).toEqual([
      { title: "时租价目", locator: "2026-10", evidenceId: "evd_abc" },
      { title: "旧引用", locator: "" },
    ]);
    expect(dossierForDerived(next, true).citations[0]?.evidenceId).toBe("evd_abc");
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

  it("任务体用 model，旧文引用默认关闭", () => {
    const parsed = normalizeCreateTask(
      CreateTaskBodySchema.parse({
        kind: "write",
        brief: { topic: "推理成本" },
        model: "tier/balanced",
      }),
    );
    expect(parsed.model).toBe("tier/balanced");
    expect(parsed.sources.library).toBe(false);
    expect(parsed.sources.rss).toBe(true);
    expect(parsed.review.outlineApproval).toBe(false);
    expect("tier" in parsed).toBe(false);
  });
});
