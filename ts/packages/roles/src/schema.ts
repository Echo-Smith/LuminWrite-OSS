import type { ModelSlot } from "@luminwrite/contracts";
import { Type } from "typebox";

/** 角色只认用途槽。换模型改槽绑定，不改角色文件。 */
export const ModelSlotSchema = Type.Union([
  Type.Literal("writer.main"),
  Type.Literal("role.researcher"),
  Type.Literal("role.reviewer"),
  Type.Literal("role.fact_check"),
  Type.Literal("role.citation_audit"),
  Type.Literal("research.plan"),
  Type.Literal("kb.embed"),
  Type.Literal("prefs.summarize"),
]);

export const RoleFrontmatter = Type.Object(
  {
    id: Type.String({ minLength: 1, maxLength: 80 }),
    name: Type.String({ minLength: 1 }),
    version: Type.Integer({ minimum: 1 }),
    description: Type.String({ minLength: 1 }),
    output: Type.Union([
      Type.Literal("findings"),
      Type.Literal("proposal"),
      Type.Literal("findings+proposal"),
    ]),
    tools: Type.Object(
      {
        allow: Type.Array(Type.String({ minLength: 1 })),
        deny: Type.Optional(Type.Array(Type.String({ minLength: 1 }))),
      },
      { additionalProperties: false },
    ),
    scope: Type.Object(
      {
        maxBlocks: Type.Integer({ minimum: 0 }),
        includeNeighbors: Type.Integer({ minimum: 0 }),
        includeSources: Type.Boolean(),
      },
      { additionalProperties: false },
    ),
    model: Type.Object(
      {
        slot: ModelSlotSchema,
        prefer: Type.Optional(Type.Array(Type.String({ minLength: 1 }))),
        temperature: Type.Optional(Type.Number()),
      },
      { additionalProperties: false },
    ),
    budget: Type.Object(
      {
        maxSteps: Type.Integer({ minimum: 0 }),
        maxInputTokens: Type.Integer({ minimum: 0 }),
        maxOutputTokens: Type.Integer({ minimum: 0 }),
        maxCostUsd: Type.Number({ minimum: 0 }),
        timeoutS: Type.Integer({ minimum: 0 }),
      },
      { additionalProperties: false },
    ),
  },
  { additionalProperties: false },
);

export type RoleOrigin = "builtin" | "pack" | "user";

export type RoleOutput = "findings" | "proposal" | "findings+proposal";

export interface RoleBudget {
  maxSteps: number;
  maxInputTokens: number;
  maxOutputTokens: number;
  maxCostUsd: number;
  timeoutS: number;
}

export interface RoleDef {
  id: string;
  name: string;
  version: number;
  description: string;
  output: RoleOutput;
  tools: { allow: string[]; deny?: string[] };
  scope: { maxBlocks: number; includeNeighbors: number; includeSources: boolean };
  model: { slot: ModelSlot; prefer?: string[]; temperature?: number };
  budget: RoleBudget;
  prompt: string;
  origin: RoleOrigin;
}
