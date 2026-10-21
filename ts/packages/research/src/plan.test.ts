import { describe, expect, it } from "vitest";

import { acceptPlan, fauxResearchPlan, gapsFor, readStoredPlan } from "./plan.js";

describe("检索计划", () => {
  it("接受模型写的计划，重复的问题 id 不落库", () => {
    const plan = fauxResearchPlan("查显卡时租");
    const accepted = acceptPlan(plan);
    expect(accepted.ok).toBe(true);
    if (!accepted.ok) return;
    expect(accepted.plan.thesisDraft).toBe("查显卡时租");
    expect(accepted.gaps).toEqual(["q1: 尚未有通过校验的证据"]);
    const duplicate = acceptPlan({
      ...plan,
      questions: [plan.questions[0], { ...plan.questions[0], text: "另一句" }],
    });
    expect(duplicate.ok).toBe(false);
  });

  it("web 和 academic 只留在计划里，关键问题记成缺口", () => {
    const accepted = acceptPlan({
      thesisDraft: "只查公开网页",
      questions: [
        {
          id: "q-web",
          text: "最新价格",
          critical: true,
          needs: ["recent"],
          plan: [{ channel: "web", query: "A10 时租", why: "想看网页" }],
        },
      ],
      outOfScope: [],
    });
    expect(accepted.ok).toBe(true);
    if (!accepted.ok) return;
    expect(accepted.gaps).toEqual(["q-web: M1 不检索 web 或 academic"]);
    expect(gapsFor(accepted.plan, new Set(["q-web"]))).toEqual([]);
  });

  it("读回 research_plans 的 jsonb", () => {
    const plan = fauxResearchPlan("查时租");
    const stored = readStoredPlan({
      id: "rpl_1",
      turnId: "trn_1",
      sessionId: "ses_1",
      plan,
      gaps: ["q1: 尚未有通过校验的证据"],
      createdAt: "2026-10-20T00:00:00.000Z",
    });
    expect(stored.plan.questions[0]?.id).toBe("q1");
    expect(stored.gaps).toHaveLength(1);
  });
});
