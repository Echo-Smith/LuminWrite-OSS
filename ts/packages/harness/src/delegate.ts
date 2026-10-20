import type { JsonValue } from "@earendil-works/chord";
import type { Context } from "@earendil-works/chord/context";
import { type Message, Type } from "@earendil-works/pi-ai";
import {
  type ConversationId,
  configure,
  defineTool,
  type Harness,
  type ToolExecutionApi,
  type ToolRegistration,
} from "@earendil-works/pi-durable";
import { ActorSchema, type TurnRequest, type TurnStatus } from "@luminwrite/contracts";
import type { PricedModel } from "@luminwrite/models";
import { allowedToolNames, overBudget, type RoleDef } from "@luminwrite/roles";

import type { PlanBlock } from "./plan.js";
import type { WritingStore } from "./store.js";

type RunStatus = "succeeded" | "failed" | "cancelled" | "budget_exceeded";

interface DelegateFrame {
  allowed: Set<string>;
  blockIds: string[];
  steps: number;
  started: number;
  budget: RoleDef["budget"];
  rejection?: "out_of_scope" | "budget_exceeded" | "tool_denied";
}

export interface DelegateBinding {
  tool: ToolRegistration;
  charge(conversationId: ConversationId, tool: string): string | undefined;
  frameFor(conversationId: ConversationId): DelegateFrame | undefined;
}

function detailText(detail: unknown): string {
  if (typeof detail === "string") return detail;
  if (detail && typeof detail === "object" && "message" in detail) {
    const message = (detail as { message?: unknown }).message;
    if (typeof message === "string") return message;
  }
  return "";
}

function readableBlocks(
  blocks: readonly PlanBlock[],
  requested: readonly string[],
  neighbors: number,
) {
  const indexes = new Set<number>();
  for (const id of requested) {
    const index = blocks.findIndex((block) => block.blockId === id);
    if (index < 0) continue;
    const start = Math.max(0, index - neighbors);
    const end = Math.min(blocks.length - 1, index + neighbors);
    for (let cursor = start; cursor <= end; cursor += 1) indexes.add(cursor);
  }
  return [...indexes]
    .sort((left, right) => left - right)
    .flatMap((index) => {
      const block = blocks[index];
      return block ? [block] : [];
    });
}

function assistantTokens(messages: readonly Message[]): { input: number; output: number } {
  let input = 0;
  let output = 0;
  for (const message of messages) {
    if (message.role !== "assistant") continue;
    input += message.usage.input;
    output += message.usage.output;
  }
  return { input, output };
}

export function bindDelegate(deps: {
  store: WritingStore;
  roles: RoleDef[];
  resolveModel(role: RoleDef, actor: TurnRequest["actor"]): Promise<PricedModel | undefined>;
  usesFaux(model: PricedModel): boolean;
  appendFaux(): void;
  harness(): Harness;
  tools(): readonly ToolRegistration[];
}): DelegateBinding {
  const frames = new Map<string, DelegateFrame>();

  function frameFor(conversationId: ConversationId): DelegateFrame | undefined {
    return frames.get(String(conversationId));
  }

  function charge(conversationId: ConversationId, tool: string): string | undefined {
    const frame = frameFor(conversationId);
    if (!frame) return undefined;
    if (!frame.allowed.has(tool)) {
      frame.rejection = "tool_denied";
      return "工具不在允许列表";
    }
    frame.steps += 1;
    if (
      overBudget(
        {
          steps: frame.steps,
          inputTokens: 0,
          outputTokens: 0,
          costUsd: 0,
          elapsedMs: Date.now() - frame.started,
        },
        frame.budget,
      )
    ) {
      frame.rejection = "budget_exceeded";
      return "budget_exceeded";
    }
    return undefined;
  }

  const tool = defineTool({
    name: "delegate",
    description:
      "把划定的块交给一个编辑部角色。选角色、问题和块由你决定。服务端只限制工具、范围和预算。",
    parameters: Type.Object({
      agent: Type.String(),
      question: Type.String(),
      blockIds: Type.Array(Type.String()),
      sessionId: Type.String(),
      turnId: Type.String(),
      documentId: Type.String(),
      applyMode: Type.Union([Type.Literal("auto"), Type.Literal("proposal")]),
      actorJson: Type.String(),
    }),
    replay: "safe",
    async execute(args, api, context) {
      const cached = await api.memo("result", context);
      if (cached !== undefined) {
        return { output: [{ type: "text", text: JSON.stringify(cached) }] };
      }
      const parent = frameFor(api.conversationId);
      if (parent) {
        const denied = charge(api.conversationId, "delegate");
        if (denied) return { isError: true, output: [{ type: "text", text: denied }] };
      }
      const outcome = await runDelegate(args, api, context);
      const saved = await api.memo(
        "result",
        JSON.parse(JSON.stringify(outcome.body)) as JsonValue,
        context,
      );
      return {
        ...(outcome.isError ? { isError: true } : {}),
        output: [{ type: "text", text: JSON.stringify(saved) }],
      };
    },
  });

  async function runDelegate(
    args: {
      agent: string;
      question: string;
      blockIds: string[];
      sessionId: string;
      turnId: string;
      documentId: string;
      applyMode: "auto" | "proposal";
      actorJson: string;
    },
    api: ToolExecutionApi,
    context: Context,
  ): Promise<{ isError: boolean; body: JsonValue }> {
    const role = deps.roles.find((item) => item.id === args.agent);
    let actorJson: unknown;
    try {
      actorJson = JSON.parse(args.actorJson) as unknown;
    } catch {
      actorJson = undefined;
    }
    const actor = ActorSchema.safeParse(actorJson);
    const taskId = String(api.taskId);

    async function finish(input: {
      runStatus: RunStatus;
      turnStatus?: TurnStatus;
      error?: string;
      output: Record<string, unknown>;
      slot: string;
      modelRef: string | null;
      conversationId: string | null;
    }): Promise<{ isError: boolean; body: JsonValue }> {
      if (input.turnStatus) {
        await deps.store.markTurn(args.turnId, {
          status: input.turnStatus,
          ...(input.error ? { error: input.error } : {}),
        });
      }
      const saved = await deps.store.saveSubagentRun({
        turnId: args.turnId,
        sessionId: args.sessionId,
        piTaskId: taskId,
        piConversationId: input.conversationId,
        agentRef: args.agent,
        slot: input.slot,
        modelRef: input.modelRef,
        input: { question: args.question, blockIds: args.blockIds },
        output: input.output,
        status: input.runStatus,
      });
      await deps.store.appendEvent(args.documentId, {
        turnId: args.turnId,
        type: "delegate.result",
        payload: { agent: args.agent, status: input.runStatus, runId: saved.id, ...input.output },
      });
      const body = { ...input.output, runId: saved.id, status: input.runStatus };
      return {
        isError: input.runStatus !== "succeeded",
        body: JSON.parse(JSON.stringify(body)) as JsonValue,
      };
    }

    if (!role || !actor.success) {
      return finish({
        runStatus: "failed",
        turnStatus: "failed",
        error: role ? "修订格式不合法" : "找不到角色",
        output: { code: role ? "bad_actor" : "unknown_role" },
        slot: role?.model.slot ?? "",
        modelRef: null,
        conversationId: null,
      });
    }

    const blocks = await deps.store.readBlocks(args.documentId);
    const known = new Set<string>(blocks.map((block) => block.blockId));
    const inRange =
      args.blockIds.length > 0 &&
      args.blockIds.length <= role.scope.maxBlocks &&
      args.blockIds.every((id) => known.has(id));
    if (!inRange) {
      return finish({
        runStatus: "failed",
        turnStatus: "failed",
        error: "out_of_scope",
        output: { code: "out_of_scope" },
        slot: role.model.slot,
        modelRef: null,
        conversationId: null,
      });
    }

    const model = await deps.resolveModel(role, actor.data);
    if (!model) {
      return finish({
        runStatus: "failed",
        turnStatus: "failed",
        error: "没有可用模型",
        output: { code: "no_model" },
        slot: role.model.slot,
        modelRef: null,
        conversationId: null,
      });
    }
    if (
      overBudget(
        { steps: 1, inputTokens: 0, outputTokens: 0, costUsd: 0, elapsedMs: 0 },
        role.budget,
      )
    ) {
      return finish({
        runStatus: "budget_exceeded",
        turnStatus: "failed",
        error: "budget_exceeded",
        output: { code: "budget_exceeded" },
        slot: role.model.slot,
        modelRef: model.ref,
        conversationId: null,
      });
    }

    const allowed = new Set(allowedToolNames(role));
    const remove = deps.tools().filter((item) => !allowed.has(item.name));
    const childId = await api.commit(async (tx) => {
      const existing = (await tx.scanConversations({ ownerTaskId: api.taskId }, 1)).items[0];
      if (existing) return existing.id;
      const created = await tx.createConversation({
        ownership: { kind: "task", taskId: api.taskId },
      });
      await configure(tx, created.id, {
        model: { provider: model.piProviderId, modelId: model.piModelId },
        instructions: role.prompt,
        ...(remove.length > 0 ? { tools: { remove } } : {}),
      });
      return created.id;
    }, context);

    const frame: DelegateFrame = {
      allowed,
      blockIds: args.blockIds,
      steps: 1,
      started: Date.now(),
      budget: role.budget,
    };
    frames.set(String(childId), frame);
    try {
      const handle = await api.conversation(childId, context);
      if (!handle) {
        return finish({
          runStatus: "failed",
          turnStatus: "failed",
          error: "子会话没有建起来",
          output: { code: "no_conversation" },
          slot: role.model.slot,
          modelRef: model.ref,
          conversationId: String(childId),
        });
      }
      if (deps.usesFaux(model)) deps.appendFaux();
      const writable = blocks.filter((block) => args.blockIds.includes(block.blockId));
      const readable = readableBlocks(blocks, args.blockIds, role.scope.includeNeighbors);
      const prompt = [
        role.prompt,
        "只改 writable 里的块。readable 只供对照。",
        `<delegate>${JSON.stringify({
          question: args.question,
          output: role.output,
          blockIds: args.blockIds,
          writable,
          readable,
          documentId: args.documentId,
          sessionId: args.sessionId,
          turnId: args.turnId,
          applyMode: args.applyMode,
          actor: actor.data,
        })}</delegate>`,
      ].join("\n");
      const settled = await (
        await handle.submit(
          { type: "input", content: prompt, requestId: `delegate:${taskId}` },
          context,
        )
      ).wait(context);

      if (frame.rejection === "out_of_scope") {
        return finish({
          runStatus: "failed",
          turnStatus: "failed",
          error: "out_of_scope",
          output: { code: "out_of_scope" },
          slot: role.model.slot,
          modelRef: model.ref,
          conversationId: String(childId),
        });
      }
      if (frame.rejection === "tool_denied") {
        return finish({
          runStatus: "failed",
          turnStatus: "failed",
          error: "工具不在允许列表",
          output: { code: "tool_denied" },
          slot: role.model.slot,
          modelRef: model.ref,
          conversationId: String(childId),
        });
      }
      if (frame.rejection === "budget_exceeded") {
        return finish({
          runStatus: "budget_exceeded",
          turnStatus: "failed",
          error: "budget_exceeded",
          output: { code: "budget_exceeded" },
          slot: role.model.slot,
          modelRef: model.ref,
          conversationId: String(childId),
        });
      }
      if (settled.status !== "done") {
        const reason = `${settled.reason ?? ""} ${detailText(settled.detail)}`.trim();
        return finish({
          runStatus: "failed",
          turnStatus: "failed",
          error: reason || "子代理没有答完",
          output: { code: "unanswered" },
          slot: role.model.slot,
          modelRef: model.ref,
          conversationId: String(childId),
        });
      }

      const view = await (await deps.harness().conversation(childId, context))?.context(context);
      const tokens = assistantTokens(view?.messages ?? []);
      let turn = await deps.store.getTurn(args.turnId);
      if (
        !turn?.resultVersionId &&
        overBudget(
          {
            steps: frame.steps,
            inputTokens: tokens.input,
            outputTokens: tokens.output,
            costUsd: 0,
            elapsedMs: Date.now() - frame.started,
          },
          role.budget,
        )
      ) {
        return finish({
          runStatus: "budget_exceeded",
          turnStatus: "failed",
          error: "budget_exceeded",
          output: { code: "budget_exceeded" },
          slot: role.model.slot,
          modelRef: model.ref,
          conversationId: String(childId),
        });
      }
      if (turn && (turn.status === "running" || turn.status === "queued")) {
        await deps.store.markTurn(args.turnId, { status: "applied" });
        turn = await deps.store.getTurn(args.turnId);
      }
      const runStatus: RunStatus =
        turn?.status === "cancelled"
          ? "cancelled"
          : turn?.status === "failed" || turn?.status === "conflicted"
            ? "failed"
            : "succeeded";
      return finish({
        runStatus,
        output: { code: runStatus === "succeeded" ? "ok" : (turn?.error ?? "failed") },
        slot: role.model.slot,
        modelRef: model.ref,
        conversationId: String(childId),
      });
    } finally {
      frames.delete(String(childId));
    }
  }

  return { tool, charge, frameFor };
}
