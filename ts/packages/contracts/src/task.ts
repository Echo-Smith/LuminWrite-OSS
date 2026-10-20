import { z } from "zod";

import { AnchorSchema } from "./edit-op.js";

/** 与 A2A v1.0 任务状态对齐。不用 auth-required。 */
export const ApiTaskStateSchema = z.enum([
  "submitted",
  "working",
  "input_required",
  "completed",
  "failed",
  "canceled",
  "rejected",
]);
export type ApiTaskState = z.infer<typeof ApiTaskStateSchema>;

export const ApiTaskPhaseSchema = z.enum([
  "planning",
  "researching",
  "outlining",
  "drafting",
  "reviewing",
  "revising",
]);
export type ApiTaskPhase = z.infer<typeof ApiTaskPhaseSchema>;

export const TaskSourcesSchema = z.object({
  kb: z.boolean().optional(),
  rss: z.boolean().optional(),
  /** 是否允许引用作者自己的旧文。默认关。 */
  library: z.boolean().optional(),
  web: z.boolean().optional(),
  urls: z.array(z.url()).max(20).optional(),
});

export const CreateTaskBodySchema = z.object({
  kind: z.enum(["write", "revise"]),
  documentId: z.string().min(1).optional(),
  brief: z
    .object({
      topic: z.string().min(1).max(500),
      audience: z.string().max(200).optional(),
      genre: z.enum(["report", "article", "brief", "commentary"]).optional(),
      lengthChars: z.number().int().min(300).max(30000).optional(),
      mustCover: z.array(z.string().min(1).max(200)).max(20).optional(),
    })
    .optional(),
  instruction: z.string().max(4000).optional(),
  anchors: z.array(AnchorSchema).max(20).optional(),
  styleRef: z.string().min(1).max(200).optional(),
  /** `tier/balanced` 或具体模型。受密钥 `allowed_tiers` 约束。 */
  model: z.string().min(1).max(200).optional(),
  sources: TaskSourcesSchema.optional(),
  review: z.object({ outlineApproval: z.boolean().optional() }).optional(),
  applyMode: z.enum(["auto", "proposal"]).optional(),
  callbackUrl: z.url().optional(),
});
export type CreateTaskBody = z.infer<typeof CreateTaskBodySchema>;

export interface NormalizedTaskSources {
  kb: boolean;
  rss: boolean;
  library: boolean;
  web: boolean;
  urls: string[];
}

export interface NormalizedCreateTask {
  kind: "write" | "revise";
  documentId?: string;
  brief?: CreateTaskBody["brief"];
  instruction?: string;
  anchors?: CreateTaskBody["anchors"];
  styleRef?: string;
  model?: string;
  sources: NormalizedTaskSources;
  review: { outlineApproval: boolean };
  applyMode?: "auto" | "proposal";
  callbackUrl?: string;
  answer?: string;
}

export function normalizeCreateTask(body: CreateTaskBody): NormalizedCreateTask {
  return {
    kind: body.kind,
    ...(body.documentId ? { documentId: body.documentId } : {}),
    ...(body.brief ? { brief: body.brief } : {}),
    ...(body.instruction ? { instruction: body.instruction } : {}),
    ...(body.anchors ? { anchors: body.anchors } : {}),
    ...(body.styleRef ? { styleRef: body.styleRef } : {}),
    ...(body.model ? { model: body.model } : {}),
    sources: {
      kb: body.sources?.kb ?? true,
      rss: body.sources?.rss ?? true,
      library: body.sources?.library ?? false,
      web: body.sources?.web ?? false,
      urls: body.sources?.urls ?? [],
    },
    review: { outlineApproval: body.review?.outlineApproval ?? false },
    ...(body.applyMode ? { applyMode: body.applyMode } : {}),
    ...(body.callbackUrl ? { callbackUrl: body.callbackUrl } : {}),
  };
}

export const ApiTaskNeedsInputSchema = z.object({
  kind: z.enum(["outline_approval", "clarification"]),
  prompt: z.string(),
  payload: z.record(z.string(), z.unknown()).optional(),
});
export type ApiTaskNeedsInput = z.infer<typeof ApiTaskNeedsInputSchema>;

export const ApiTaskErrorSchema = z.object({
  code: z.string(),
  message: z.string(),
  retryable: z.boolean(),
});
export type ApiTaskErrorBody = z.infer<typeof ApiTaskErrorSchema>;

export const ApiTaskSchema = z.object({
  id: z.string(),
  state: ApiTaskStateSchema,
  phase: ApiTaskPhaseSchema.optional(),
  documentId: z.string().optional(),
  resultVersionId: z.string().optional(),
  proposalId: z.string().optional(),
  needsInput: ApiTaskNeedsInputSchema.optional(),
  usage: z.object({
    inputTokens: z.number(),
    outputTokens: z.number(),
    costUsd: z.number(),
  }),
  error: ApiTaskErrorSchema.optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
});
export type ApiTask = z.infer<typeof ApiTaskSchema>;

export const TaskMessageBodySchema = z.object({
  message: z.string().min(1).max(4000),
});
export type TaskMessageBody = z.infer<typeof TaskMessageBodySchema>;

export function taskInstruction(input: NormalizedCreateTask, answer?: string): string {
  const base = input.instruction?.trim() || input.brief?.topic?.trim() || "";
  const extra = answer?.trim() ?? "";
  const joined = extra ? `${base}\n${extra}`.trim() : base;
  if (joined) return joined.slice(0, 4000);
  return input.kind === "write" ? "写一篇稿" : "";
}
