import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import { composeStyle, defaultPlainspeakFloor, plainProfessional } from "./index.js";

describe("风格分层", () => {
  it("后层覆盖前层，地板上的空话不能改成警告", () => {
    const composed = composeStyle([
      plainProfessional,
      {
        origin: "instruction",
        ref: "turn",
        prose: "这篇文章写给财务负责人。",
        rules: [
          { kind: "taboo", terms: ["赋能"], severity: "warn" },
          { kind: "sentence_length", p90Max: 40 },
        ],
      },
    ]);
    const lengths = composed.effectiveRules.filter((rule) => rule.kind === "sentence_length");
    expect(lengths).toEqual([{ kind: "sentence_length", p90Max: 40 }]);
    const floor = composed.effectiveRules.filter(
      (rule) => rule.kind === "taboo" && rule.severity === "block",
    );
    expect(floor.some((rule) => rule.kind === "taboo" && rule.terms.includes("赋能"))).toBe(true);
    expect(composed.conflicts.some((item) => item.reason === "说人话地板不可放宽")).toBe(true);
    expect(composed.promptBlock).toContain("财务负责人");
    expect(defaultPlainspeakFloor().hollowTerms).toContain("赋能");
  });

  it("默认文风包和清单一致", () => {
    const manifest = JSON.parse(
      readFileSync(
        new URL("../../../styles/voice/plain-professional/manifest.json", import.meta.url),
        "utf8",
      ),
    ) as { ref: string; prose: string };
    expect(manifest.ref).toBe(plainProfessional.ref);
    expect(manifest.prose).toBe(plainProfessional.prose);
  });
});
