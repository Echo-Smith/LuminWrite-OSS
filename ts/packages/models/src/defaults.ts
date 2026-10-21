import type { ModelSlot, RegistryEntrySpec } from "@luminwrite/contracts";

import { fauxAllowed } from "./catalog.js";

export interface RegistryRecord {
  ref: string;
  kind: "model" | "tier";
  spec: RegistryEntrySpec;
}

export const MODEL_SLOTS: readonly ModelSlot[] = [
  "writer.main",
  "role.researcher",
  "role.reviewer",
  "role.fact_check",
  "role.citation_audit",
  "research.plan",
  "kb.embed",
  "prefs.summarize",
];

export function isModelSlot(value: string): value is ModelSlot {
  return (MODEL_SLOTS as readonly string[]).includes(value);
}

/** 内置档位。`test/faux` 只在测试或 `LW_ALLOW_FAUX=1` 时出现。 */
export function defaultRegistryRecords(env: NodeJS.ProcessEnv = process.env): RegistryRecord[] {
  const fast = ["openai/gpt-4.1-nano", "deepseek/deepseek-flash"];
  if (fauxAllowed(env)) fast.unshift("test/faux");
  return [
    {
      ref: "tier/fast",
      kind: "tier",
      spec: { kind: "tier", label: "快", members: fast },
    },
    {
      ref: "tier/balanced",
      kind: "tier",
      spec: {
        kind: "tier",
        label: "均衡",
        members: ["openai/gpt-4.1-mini", "deepseek/deepseek-v4-pro"],
      },
    },
    {
      ref: "tier/strong",
      kind: "tier",
      spec: { kind: "tier", label: "强", members: ["openai/gpt-4.1"] },
    },
  ];
}

/** `kb.embed` 不绑聊天模型。嵌入槽等所有者指向真正的嵌入条目。 */
export function defaultSlotBindings(): Array<{ slot: ModelSlot; targetRef: string }> {
  return [
    { slot: "writer.main", targetRef: "tier/balanced" },
    { slot: "role.researcher", targetRef: "tier/balanced" },
    { slot: "role.reviewer", targetRef: "tier/fast" },
    { slot: "role.fact_check", targetRef: "tier/strong" },
    { slot: "role.citation_audit", targetRef: "tier/strong" },
    { slot: "research.plan", targetRef: "tier/balanced" },
    { slot: "prefs.summarize", targetRef: "tier/fast" },
  ];
}
