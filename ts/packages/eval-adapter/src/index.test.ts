import { analyzePlain } from "@luminwrite/plainspeak";
import { fauxResearchPlan } from "@luminwrite/research";
import { describe, expect, it } from "vitest";

import { readResearchPlansForEval, runDeterministicSmoke, smokeSamples } from "./index.js";

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
});
