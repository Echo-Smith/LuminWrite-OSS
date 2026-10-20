import { analyzePlain } from "@luminwrite/plainspeak";
import { fauxResearchPlan } from "@luminwrite/research";
import { describe, expect, it } from "vitest";

import {
  readResearchPlansForEval,
  runDeterministicSmoke,
  runWriteReviseRollback,
  smokeSamples,
} from "./index.js";

describe("评测冒烟", () => {
  it("中性负对照在说人话轴上排最后", () => {
    const report = runDeterministicSmoke();
    expect(report.pass).toBe(true);
    expect(report.ranking.at(-1)?.id).toBe("negative");
    const negative = smokeSamples.find((sample) => sample.role === "negative");
    expect(analyzePlain(negative?.text ?? "").pass).toBe(false);
  });

  it("能读回 research_plans 里的计划", () => {
    const plan = fauxResearchPlan("查显卡时租");
    const stored = readResearchPlansForEval([
      {
        id: "rpl_1",
        turnId: "trn_1",
        sessionId: "ses_1",
        plan,
        gaps: ["q1: 尚未有通过校验的证据"],
        createdAt: "2026-10-20T00:00:00.000Z",
      },
    ]);
    expect(stored[0]?.plan.questions[0]?.id).toBe("q1");
    expect(stored[0]?.gaps).toEqual(["q1: 尚未有通过校验的证据"]);
  });

  it("写改回滚只调用任务 API 和回滚", async () => {
    const calls: string[] = [];
    const result = await runWriteReviseRollback({
      async submitTask(body, key) {
        calls.push(`submit ${body.kind} ${key}`);
        if (body.kind === "write") {
          return { id: "tsk_w", state: "completed", documentId: "doc_1", resultVersionId: "ver_2" };
        }
        return { id: "tsk_r", state: "completed", documentId: "doc_1", resultVersionId: "ver_3" };
      },
      async getTask(id) {
        calls.push(`get ${id}`);
        return id === "tsk_w"
          ? { id, state: "completed", documentId: "doc_1", resultVersionId: "ver_2" }
          : { id, state: "completed", documentId: "doc_1", resultVersionId: "ver_3" };
      },
      async restoreDocument(id, versionId) {
        calls.push(`restore ${id} ${versionId}`);
        return { ok: true, versionId: "ver_4" };
      },
    });
    expect(result).toEqual({
      documentId: "doc_1",
      wroteVersionId: "ver_2",
      revisedVersionId: "ver_3",
    });
    expect(calls).toEqual([
      "submit write eval-write",
      "get tsk_w",
      "submit revise eval-revise",
      "get tsk_r",
      "restore doc_1 ver_2",
    ]);
  });
});
