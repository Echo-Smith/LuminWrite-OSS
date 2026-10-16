import { plainspeakFloorTerms } from "@luminwrite/plainspeak";

export type StyleRule =
  | { kind: "taboo"; terms: string[]; severity: "block" | "warn" }
  | { kind: "prefer_term"; from: string[]; to: string }
  | { kind: "sentence_length"; p50?: number; p90Max?: number }
  | { kind: "paragraph"; maxChars?: number; leadWithConclusion?: boolean }
  | { kind: "pattern"; regex: string; message: string; severity: "block" | "warn" }
  | { kind: "habit"; phrase: string; maxPer1k?: number };

export type StyleOrigin = "base" | "genre" | "voice" | "personal" | "document" | "instruction";

export interface StyleLayer {
  origin: StyleOrigin;
  ref: string;
  rules: StyleRule[];
  prose?: string;
}

export interface PlainspeakFloor {
  hollowTerms: readonly string[];
  maxHollowSentenceRatio: number;
  maxHollowScore: number;
}

export interface ComposedStyle {
  effectiveRules: StyleRule[];
  promptBlock: string;
  conflicts: Array<{ rule: StyleRule; overriddenBy: StyleOrigin; reason: string }>;
}

export function defaultPlainspeakFloor(): PlainspeakFloor {
  return {
    hollowTerms: plainspeakFloorTerms(),
    maxHollowSentenceRatio: 0.2,
    maxHollowScore: 40,
  };
}

export const plainProfessional: StyleLayer = {
  origin: "voice",
  ref: "pkg:luminwrite/plain-professional@1.0.0",
  prose: "用具体的主语、数字和动作。先给结论，再给依据。不写空话，不靠排比撑篇幅。",
  rules: [
    { kind: "sentence_length", p90Max: 80 },
    { kind: "paragraph", leadWithConclusion: true, maxChars: 400 },
  ],
};

function ruleKey(rule: StyleRule): string {
  if (rule.kind === "prefer_term") return `prefer_term:${rule.to}`;
  if (rule.kind === "habit") return `habit:${rule.phrase}`;
  return rule.kind;
}

/** 后层覆盖前层。说人话地板最后套上，不能被放宽。 */
export function composeStyle(
  layers: StyleLayer[],
  floor: PlainspeakFloor = defaultPlainspeakFloor(),
): ComposedStyle {
  const conflicts: ComposedStyle["conflicts"] = [];
  const rules: StyleRule[] = [];
  for (const layer of layers) {
    for (const rule of layer.rules) {
      if (rule.kind === "taboo") {
        const weakened = rule.terms.filter((term) => floor.hollowTerms.includes(term));
        if (weakened.length > 0 && rule.severity !== "block") {
          conflicts.push({ rule, overriddenBy: "base", reason: "说人话地板不可放宽" });
          continue;
        }
      }
      const key = ruleKey(rule);
      const previous = rules.findIndex((item) => ruleKey(item) === key);
      if (previous >= 0) {
        const previousRule = rules[previous];
        if (previousRule)
          conflicts.push({ rule: previousRule, overriddenBy: layer.origin, reason: "后层覆盖" });
        rules.splice(previous, 1);
      }
      rules.push(rule);
    }
  }
  rules.push({ kind: "taboo", terms: [...floor.hollowTerms], severity: "block" });
  const promptBlock = [
    ...layers.map((layer) => layer.prose).filter((prose): prose is string => Boolean(prose)),
    "不要使用空话。修辞可以跟着风格走，但赋能、抓手、闭环这类说法一律不用。",
  ].join("\n");
  return { effectiveRules: rules, promptBlock, conflicts };
}
