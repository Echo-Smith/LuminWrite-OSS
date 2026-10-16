import { z } from "zod";

import { AnchorSchema } from "./edit-op.js";

export const TurnKindSchema = z.enum(["outline", "draft", "revise", "research", "review"]);
export type TurnKind = z.infer<typeof TurnKindSchema>;

export const TurnStatusSchema = z.enum([
  "queued",
  "running",
  "validating",
  "proposed",
  "applied",
  "conflicted",
  "failed",
  "cancelled",
]);
export type TurnStatus = z.infer<typeof TurnStatusSchema>;

export const ActorSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("user"), userId: z.string().min(1) }),
  z.object({
    type: z.literal("api_client"),
    userId: z.string().min(1),
    keyId: z.string().min(1),
  }),
  z.object({ type: z.literal("system") }),
]);
export type Actor = z.infer<typeof ActorSchema>;

export const TurnRequestSchema = z.object({
  documentId: z.string().min(1),
  kind: TurnKindSchema,
  instruction: z.string().min(1).max(4000),
  selection: z.array(AnchorSchema).optional(),
  scope: z.enum(["selection", "blocks", "document"]),
  delivery: z.enum(["followup", "steer"]),
  applyMode: z.enum(["auto", "proposal"]),
  idempotencyKey: z.string().min(1).max(200),
  actor: ActorSchema,
});
export type TurnRequest = z.infer<typeof TurnRequestSchema>;

export interface RunEvent {
  seq: number;
  documentId: string;
  turnId?: string;
  type: string;
  payload: unknown;
}
