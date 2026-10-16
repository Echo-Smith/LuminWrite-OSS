import type { JsonValue } from "@earendil-works/chord";
import { BACKGROUND_CONTEXT, type Context } from "@earendil-works/chord/context";
import { type Message, type TranscriptContext, Type } from "@earendil-works/pi-ai";
import { createModels } from "@earendil-works/pi-ai/models";
import {
  fauxAssistantMessage,
  fauxProvider,
  fauxToolCall,
} from "@earendil-works/pi-ai/providers/faux";
import {
  type Conversation,
  type ConversationId,
  createRegistry,
  defineDoc,
  defineExtension,
  defineTool,
  Harness,
} from "@earendil-works/pi-durable";
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node";
import {
  ActorSchema,
  asBlockId,
  type EditOp,
  EditOpSchema,
  type RunEvent,
  type TurnRequest,
  type TurnStatus,
} from "@luminwrite/contracts";
import type { UsageSink } from "@luminwrite/edition";

import { type PlanBlock, planOps, targetIndex } from "./plan.js";
import type { HarnessChange, HarnessPort } from "./port.js";
import { screenOps } from "./screen.js";
import type { WritingStore } from "./store.js";

const ctx: Context = BACKGROUND_CONTEXT;

const conversationsDoc = defineDoc({
  kind: "lw.conversations",
  version: 1,
  scope: "session",
  initial: () => ({ byKey: {} as Record<string, number> }),
});

const terminal = new Set<TurnStatus>(["applied", "proposed", "failed", "conflicted", "cancelled"]);

function parseTag(text: string, tag: string): string {
  const match = new RegExp(`<${tag}>([\\s\\S]*?)</${tag}>`).exec(text);
  return match?.[1] ?? "";
}

function latestUserText(messages: readonly Message[]): string {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (!message || message.role !== "user") continue;
    if (typeof message.content === "string") return message.content;
    return message.content.map((part) => (part.type === "text" ? part.text : "")).join("");
  }
  return "";
}

function toolRanAfterLatestUser(messages: readonly Message[]): boolean {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (!message) continue;
    if (message.role === "user") return false;
    if (message.role === "toolResult") return true;
  }
  return false;
}

function promptFor(
  turn: TurnRequest,
  turnId: string,
  blocks: PlanBlock[],
  scopeIds: string[],
): string {
  const payload = {
    documentId: turn.documentId,
    turnId,
    kind: turn.kind,
    instruction: turn.instruction,
    applyMode: turn.applyMode,
    actor: turn.actor,
    selection: turn.selection ?? [],
    scopeIds,
  };
  return [
    "只通过 propose_edits 提交修订。服务端会校验块哈希。",
    `<turn>${JSON.stringify(payload)}</turn>`,
    `<blocks>${JSON.stringify(blocks)}</blocks>`,
  ].join("\n");
}

export interface TurnResult {
  turnId: string;
  status: TurnStatus;
  submissionId: string;
  versionId?: string;
}

export interface WritingRuntime extends HarnessPort {
  submitTurn(req: TurnRequest): Promise<TurnResult>;
  events(documentId: string, afterSeq?: number): Promise<RunEvent[]>;
}

export async function createWritingRuntime(options: {
  sqlitePath: string;
  store: WritingStore;
  planner?: typeof planOps;
  usage?: UsageSink;
}): Promise<WritingRuntime> {
  const planner = options.planner ?? planOps;
  const storage = await openNodeSqliteStorage(options.sqlitePath);
  const faux = fauxProvider();
  const models = createModels();
  models.setProvider(faux.provider);
  const respond = (context: TranscriptContext) => {
    faux.appendResponses([respond]);
    if (toolRanAfterLatestUser(context.messages)) return fauxAssistantMessage("已处理这一回合。");
    const text = latestUserText(context.messages);
    const turn = JSON.parse(parseTag(text, "turn") || "{}") as {
      documentId?: string;
      turnId?: string;
      kind?: TurnRequest["kind"];
      instruction?: string;
      applyMode?: "auto" | "proposal";
      actor?: TurnRequest["actor"];
      selection?: Array<{ blockId: string }>;
      scopeIds?: string[];
    };
    const blocks = JSON.parse(parseTag(text, "blocks") || "[]") as PlanBlock[];
    if (!turn.kind || !turn.documentId || !turn.turnId || !turn.applyMode || !turn.actor) {
      return fauxAssistantMessage("回合信息不完整。");
    }
    const planned = planner({
      kind: turn.kind,
      instruction: turn.instruction ?? "",
      blocks,
      selection: turn.selection,
    });
    if (!planned.ok) return fauxAssistantMessage(planned.reason);
    return fauxAssistantMessage(
      [
        fauxToolCall("propose_edits", {
          documentId: turn.documentId,
          turnId: turn.turnId,
          applyMode: turn.applyMode,
          summary: planned.summary,
          actorJson: JSON.stringify(turn.actor),
          scopeJson: JSON.stringify(turn.scopeIds ?? []),
          opsJson: JSON.stringify(planned.ops),
        }),
      ],
      { stopReason: "toolUse" },
    );
  };
  faux.appendResponses([respond]);

  const readBlocks = defineTool({
    name: "read_blocks",
    description: "读取文档当前块的 id、哈希和文本。",
    parameters: Type.Object({ documentId: Type.String() }),
    replay: "safe",
    async execute(args) {
      const blocks = await options.store.readBlocks(args.documentId);
      return { output: [{ type: "text", text: JSON.stringify(blocks) }] };
    },
  });

  const proposeEdits = defineTool({
    name: "propose_edits",
    description: "提交块锚点修订。哈希和说人话由服务端校验。",
    parameters: Type.Object({
      documentId: Type.String(),
      turnId: Type.String(),
      applyMode: Type.Union([Type.Literal("auto"), Type.Literal("proposal")]),
      summary: Type.String(),
      actorJson: Type.String(),
      scopeJson: Type.String(),
      opsJson: Type.String(),
    }),
    replay: "safe",
    async execute(args, api, context) {
      const parsedOps = EditOpSchema.array().safeParse(JSON.parse(args.opsJson));
      const parsedActor = ActorSchema.safeParse(JSON.parse(args.actorJson));
      if (!parsedOps.success || !parsedActor.success) {
        await options.store.markTurn(args.turnId, { status: "failed", error: "修订格式不合法" });
        return { isError: true, output: [{ type: "text", text: "修订格式不合法" }] };
      }
      const ops = parsedOps.data as EditOp[];
      const report = screenOps(ops);
      if (!report.pass) {
        await options.store.markTurn(args.turnId, {
          status: "failed",
          error: "没有通过说人话检查",
        });
        return { isError: true, output: [{ type: "text", text: "没有通过说人话检查" }] };
      }
      const cached = await api.memo("result", context);
      if (cached !== undefined) return { output: [{ type: "text", text: JSON.stringify(cached) }] };
      const scope = (JSON.parse(args.scopeJson) as string[]).map((id) => asBlockId(id));
      const outcome = await options.store.propose({
        documentId: args.documentId,
        turnId: args.turnId,
        ops,
        piTaskId: String(api.taskId),
        applyMode: args.applyMode,
        actor: parsedActor.data,
        summary: args.summary,
        ...(scope.length > 0 ? { scope } : {}),
      });
      const saved = await api.memo(
        "result",
        JSON.parse(JSON.stringify(outcome)) as JsonValue,
        context,
      );
      if (!outcome.ok)
        return { isError: true, output: [{ type: "text", text: JSON.stringify(outcome.errors) }] };
      return { output: [{ type: "text", text: JSON.stringify(saved) }] };
    },
  });

  const registry = createRegistry();
  registry.install(defineExtension({ name: "lw-writing", tools: [readBlocks, proposeEdits] }));
  const harness = await Harness.open(storage, { models, registry }, ctx);
  harness.resume();
  const seenEntries = new Set<string>();

  async function conversationFor(documentId: string): Promise<Conversation> {
    const snap = await harness.snapshot(conversationsDoc, ctx);
    const existing = snap?.byKey[documentId];
    if (typeof existing === "number") {
      const found = await harness.conversation(existing as ConversationId, ctx);
      if (found) return found;
    }
    const created = await harness.createConversation(
      {
        ownership: { kind: "ownerless" },
        agent: { model: { provider: "faux", modelId: "faux-1" } },
      },
      ctx,
    );
    await harness.commit(async (tx) => {
      const doc = await tx.doc(conversationsDoc);
      doc.byKey = { ...doc.byKey, [documentId]: created.id };
    }, ctx);
    return created;
  }

  async function translate(
    conversation: Conversation,
    documentId: string,
    turnId: string,
  ): Promise<void> {
    const handle = await conversation.watch(ctx);
    try {
      for (const entry of handle.value.entries) {
        const key = `${documentId}:${entry.id}`;
        if (seenEntries.has(key)) continue;
        seenEntries.add(key);
        await options.store.appendEvent(documentId, {
          turnId,
          type: entry.kind,
          payload: { entryId: entry.id },
        });
      }
    } finally {
      await handle.stop();
    }
  }

  const runtime: WritingRuntime = {
    async submit(conversationKey, input) {
      const conversation = await conversationFor(conversationKey);
      const submission = await conversation.submit(
        {
          type: "input",
          content: input.content,
          requestId: input.requestId,
          whenBusy: input.whenBusy,
        },
        ctx,
      );
      return { submissionId: String(submission.id) };
    },
    async abort(conversationKey) {
      const conversation = await conversationFor(conversationKey);
      await conversation.abort(ctx);
    },
    async resume() {
      harness.resume();
    },
    watch(conversationKey) {
      return watchConversation(conversationKey);
    },
    async close() {
      await harness.close(ctx);
    },
    events(documentId, afterSeq) {
      return options.store.eventsAfter(documentId, afterSeq);
    },
    async submitTurn(req) {
      const begun = await options.store.beginTurn(req);
      const existing = await options.store.getTurn(begun.turnId);
      if (!begun.created && existing && terminal.has(existing.status)) {
        return {
          turnId: begun.turnId,
          status: existing.status,
          submissionId: existing.piSubmissionId ?? "",
          ...(existing.resultVersionId ? { versionId: existing.resultVersionId } : {}),
        };
      }
      await options.store.markTurn(begun.turnId, { status: "running" });
      const blocks = await options.store.readBlocks(req.documentId);
      const index = targetIndex(req.instruction, blocks, req.selection);
      const scoped = req.kind === "revise" ? blocks[index]?.blockId : undefined;
      const conversation = await conversationFor(req.documentId);
      await options.store.appendEvent(req.documentId, {
        turnId: begun.turnId,
        type: "turn.started",
        payload: { kind: req.kind },
      });
      const submission = await conversation.submit(
        {
          type: "input",
          content: promptFor(req, begun.turnId, blocks, scoped ? [scoped] : []),
          requestId: req.idempotencyKey,
          whenBusy: req.delivery === "steer" ? "steer" : "followUp",
        },
        ctx,
      );
      await options.store.markTurn(begun.turnId, {
        piConversationId: String(conversation.id),
        piSubmissionId: String(submission.id),
      });
      await submission.wait(ctx);
      await translate(conversation, req.documentId, begun.turnId);
      let turn = await options.store.getTurn(begun.turnId);
      if (turn && (turn.status === "running" || turn.status === "queued")) {
        await options.store.markTurn(begun.turnId, { status: "failed", error: "尚未实现" });
        turn = await options.store.getTurn(begun.turnId);
      }
      await options.usage?.record({
        requestId: req.idempotencyKey,
        model: "faux-1",
        inputTokens: 0,
        outputTokens: 0,
        costMicros: 0,
      });
      return {
        turnId: begun.turnId,
        status: turn?.status ?? "failed",
        submissionId: String(submission.id),
        ...(turn?.resultVersionId ? { versionId: turn.resultVersionId } : {}),
      };
    },
  };

  async function* watchConversation(conversationKey: string): AsyncGenerator<HarnessChange> {
    const conversation = await conversationFor(conversationKey);
    const handle = await conversation.watch(ctx);
    const queue: HarnessChange[] = [toChange(conversationKey, handle.value.entries)];
    let pending: (() => void) | undefined;
    let stopped = false;
    handle.start(async (value) => {
      queue.push(toChange(conversationKey, value.entries));
      pending?.();
      pending = undefined;
    });
    try {
      while (!stopped) {
        if (queue.length === 0) {
          await new Promise<void>((resolve) => {
            pending = resolve;
          });
        }
        if (stopped) return;
        const next = queue.shift();
        if (next) yield next;
      }
    } finally {
      stopped = true;
      pending?.();
      await handle.stop();
    }
  }

  return runtime;
}

function toChange(conversationKey: string, entries: readonly { kind: string }[]): HarnessChange {
  const last = entries.at(-1);
  return {
    conversationKey,
    entryCount: entries.length,
    ...(last ? { lastKind: last.kind } : {}),
  };
}
